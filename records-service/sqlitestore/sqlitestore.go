// Package sqlitestore is the second driven adapter: the same event store port,
// backed by SQLite through database/sql. The driver is modernc.org/sqlite, a
// pure-Go translation of SQLite, so the example needs no cgo, no toolchain
// beyond Go, and no service running anywhere -- which is why the contract suite
// can be run against a real relational store in continuous integration on an
// empty machine.
//
// Everything SQL-shaped is confined to this file. The domain does not know that
// a stream is rows in a table, that a version conflict is a uniqueness
// violation, or that "one transaction" is a BEGIN and a COMMIT.
package sqlitestore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/edgentx/code-examples/records-service/recordsrequest"

	_ "modernc.org/sqlite" // registers the "sqlite" database/sql driver
)

// schema is applied by Open.
//
// Three constraints carry the guarantees, and they are constraints rather than
// application checks on purpose. UNIQUE (request_id, version) is the optimistic
// lock: two writers who both read version 3 cannot both write version 4,
// whatever the application does. The idempotency key is a primary key, so a
// command cannot be applied twice even if the pre-check is skipped. The outbox
// row's primary key references the event's sequence, so an entry cannot exist
// for an event that was rolled back.
const schema = `
CREATE TABLE IF NOT EXISTS events (
	sequence        INTEGER PRIMARY KEY AUTOINCREMENT,
	request_id      TEXT    NOT NULL,
	version         INTEGER NOT NULL,
	name            TEXT    NOT NULL,
	payload         BLOB    NOT NULL,
	traceparent     TEXT    NOT NULL,
	idempotency_key TEXT    NOT NULL,
	recorded_at     INTEGER NOT NULL,
	UNIQUE (request_id, version)
);
CREATE INDEX IF NOT EXISTS events_by_stream ON events (request_id, version);

CREATE TABLE IF NOT EXISTS outbox (
	sequence   INTEGER PRIMARY KEY REFERENCES events (sequence),
	dispatched INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS outbox_pending ON outbox (dispatched, sequence);

CREATE TABLE IF NOT EXISTS idempotency (
	idempotency_key TEXT    PRIMARY KEY,
	request_id      TEXT    NOT NULL,
	version         INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS request_summary (
	request_id     TEXT    PRIMARY KEY,
	requester      TEXT    NOT NULL,
	description    TEXT    NOT NULL,
	status         TEXT    NOT NULL,
	reviewer       TEXT    NOT NULL,
	submitted_at   INTEGER NOT NULL,
	due_at         INTEGER NOT NULL,
	released_pages INTEGER NOT NULL,
	exemption      TEXT    NOT NULL,
	package_id     TEXT    NOT NULL,
	failure_cause  TEXT    NOT NULL,
	version        INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS projection_checkpoint (
	name     TEXT    PRIMARY KEY,
	sequence INTEGER NOT NULL
);
`

// summaryProjection names the read model's checkpoint row. A second projection
// would take a second name and catch up independently.
const summaryProjection = "request_summary"

// eventColumns is the read shape of an event, written once so every query
// scans the same way.
const eventColumns = `sequence, request_id, version, name, payload, traceparent,
	idempotency_key, recorded_at`

// Store is a SQLite-backed event store with an outbox and a read model.
type Store struct {
	db *sql.DB
}

// Open connects to a SQLite database and applies the schema. Pass a file path,
// or ":memory:" for a throwaway database that lives only as long as the process.
func Open(ctx context.Context, dsn string) (*Store, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// SQLite permits a single writer. Holding one connection keeps a ":memory:"
	// database from becoming several independent databases, removes "database is
	// locked" as a source of flakiness, and makes concurrent appends queue for
	// the writer rather than fail -- which is what the interleaved-writers case
	// in the contract suite exercises.
	db.SetMaxOpenConns(1)
	if _, err := db.ExecContext(ctx, schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	return &Store{db: db}, nil
}

// Close releases the database handle.
func (s *Store) Close() error { return s.db.Close() }

// Load rebuilds a request by replaying its stream in version order.
func (s *Store) Load(ctx context.Context, requestID string) (*recordsrequest.Request, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+eventColumns+` FROM events WHERE request_id = ? ORDER BY version`, requestID)
	if err != nil {
		return nil, fmt.Errorf("select stream: %w", err)
	}
	stream, err := scanEvents(rows)
	if err != nil {
		return nil, err
	}
	if len(stream) == 0 {
		return nil, recordsrequest.ErrNotFound
	}

	history := make([]recordsrequest.Event, 0, len(stream))
	for _, stored := range stream {
		event, err := stored.Event()
		if err != nil {
			return nil, err
		}
		history = append(history, event)
	}
	return recordsrequest.FromHistory(history)
}

// Recorded reports the result of an earlier append under an idempotency key.
func (s *Store) Recorded(ctx context.Context, key string) (recordsrequest.Result, bool, error) {
	result, err := recordedIn(ctx, s.db, key)
	if errors.Is(err, sql.ErrNoRows) {
		return recordsrequest.Result{}, false, nil
	}
	if err != nil {
		return recordsrequest.Result{}, false, err
	}
	return result, true, nil
}

// Append writes the events, their outbox entries, and the idempotency record in
// one transaction. Nothing is published here and nothing is queued in memory: an
// outbox entry is a row committed with the event it belongs to, so if the
// process dies one instruction after COMMIT, the announcement is still on disk
// waiting for the relay.
func (s *Store) Append(ctx context.Context, write recordsrequest.Append) (recordsrequest.Result, error) {
	if write.IdempotencyKey == "" {
		return recordsrequest.Result{}, recordsrequest.ErrNoIdempotencyKey
	}
	if len(write.Events) == 0 {
		return recordsrequest.Result{}, recordsrequest.ErrNoChanges
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return recordsrequest.Result{}, fmt.Errorf("begin transaction: %w", err)
	}
	// Rollback after a successful Commit is a no-op, so this needs no flag: any
	// path out of this function that is not the COMMIT leaves nothing behind.
	defer tx.Rollback() //nolint:errcheck // the deferred rollback's error is not actionable

	recorded, err := recordedIn(ctx, tx, write.IdempotencyKey)
	switch {
	case err == nil:
		return recorded, nil
	case !errors.Is(err, sql.ErrNoRows):
		return recordsrequest.Result{}, err
	}

	var current int
	if err := tx.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(version), 0) FROM events WHERE request_id = ?`,
		write.RequestID).Scan(&current); err != nil {
		return recordsrequest.Result{}, fmt.Errorf("read current version: %w", err)
	}
	if current != write.ExpectedVersion {
		return recordsrequest.Result{}, recordsrequest.ErrVersionConflict
	}

	version := current
	for _, event := range write.Events {
		payload, err := recordsrequest.EncodeEvent(event)
		if err != nil {
			return recordsrequest.Result{}, err
		}
		version++
		result, err := tx.ExecContext(ctx, `
			INSERT INTO events
				(request_id, version, name, payload, traceparent, idempotency_key, recorded_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)`,
			write.RequestID, version, event.EventName(), payload,
			write.TraceParent, write.IdempotencyKey, event.OccurredAt().UTC().UnixNano())
		if err != nil {
			if isUniqueViolation(err) {
				// Another writer took this version between the read above and
				// this insert. The constraint is the lock; the check was only
				// the fast path.
				return recordsrequest.Result{}, recordsrequest.ErrVersionConflict
			}
			return recordsrequest.Result{}, fmt.Errorf("append event: %w", err)
		}
		sequence, err := result.LastInsertId()
		if err != nil {
			return recordsrequest.Result{}, fmt.Errorf("append event: %w", err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO outbox (sequence) VALUES (?)`, sequence); err != nil {
			return recordsrequest.Result{}, fmt.Errorf("queue outbox entry: %w", err)
		}
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO idempotency (idempotency_key, request_id, version) VALUES (?, ?, ?)`,
		write.IdempotencyKey, write.RequestID, version); err != nil {
		if isUniqueViolation(err) {
			return recordsrequest.Result{}, recordsrequest.ErrVersionConflict
		}
		return recordsrequest.Result{}, fmt.Errorf("record idempotency key: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return recordsrequest.Result{}, fmt.Errorf("commit: %w", err)
	}
	return recordsrequest.Result{RequestID: write.RequestID, Version: version}, nil
}

// Stream reads committed events after a sequence, in commit order.
func (s *Store) Stream(ctx context.Context, afterSequence int64,
	limit int) ([]recordsrequest.Stored, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+eventColumns+`
		FROM events
		WHERE sequence > ?
		ORDER BY sequence
		LIMIT ?`, afterSequence, limit)
	if err != nil {
		return nil, fmt.Errorf("select stream: %w", err)
	}
	return scanEvents(rows)
}

// PendingOutbox returns committed events that have not been published.
func (s *Store) PendingOutbox(ctx context.Context, limit int) ([]recordsrequest.Stored, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+withPrefix(eventColumns, "events")+`
		FROM outbox JOIN events ON events.sequence = outbox.sequence
		WHERE outbox.dispatched = 0
		ORDER BY outbox.sequence
		LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("select pending outbox: %w", err)
	}
	return scanEvents(rows)
}

// MarkDispatched records that an event's message was published.
func (s *Store) MarkDispatched(ctx context.Context, sequence int64) error {
	if _, err := s.db.ExecContext(ctx,
		`UPDATE outbox SET dispatched = 1 WHERE sequence = ?`, sequence); err != nil {
		return fmt.Errorf("mark dispatched: %w", err)
	}
	return nil
}

// Checkpoint is the sequence the read model has consumed through.
func (s *Store) Checkpoint(ctx context.Context) (int64, error) {
	var sequence int64
	err := s.db.QueryRowContext(ctx,
		`SELECT sequence FROM projection_checkpoint WHERE name = ?`, summaryProjection).
		Scan(&sequence)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read projection checkpoint: %w", err)
	}
	return sequence, nil
}

// Save writes summaries and advances the checkpoint in one transaction.
func (s *Store) Save(ctx context.Context, summaries []recordsrequest.Summary,
	throughSequence int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // the deferred rollback's error is not actionable

	var current int64
	err = tx.QueryRowContext(ctx,
		`SELECT sequence FROM projection_checkpoint WHERE name = ?`, summaryProjection).
		Scan(&current)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("read projection checkpoint: %w", err)
	}
	if throughSequence <= current {
		// Already applied. Repeating a batch is normal for a projector that
		// crashed after writing and before recording its progress.
		return nil
	}

	for _, summary := range summaries {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO request_summary
				(request_id, requester, description, status, reviewer, submitted_at,
				 due_at, released_pages, exemption, package_id, failure_cause, version)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (request_id) DO UPDATE SET
				requester = excluded.requester,
				description = excluded.description,
				status = excluded.status,
				reviewer = excluded.reviewer,
				submitted_at = excluded.submitted_at,
				due_at = excluded.due_at,
				released_pages = excluded.released_pages,
				exemption = excluded.exemption,
				package_id = excluded.package_id,
				failure_cause = excluded.failure_cause,
				version = excluded.version`,
			summary.RequestID, summary.Requester, summary.Description, string(summary.Status),
			summary.Reviewer, summary.SubmittedAt.UTC().UnixNano(), summary.DueAt.UTC().UnixNano(),
			summary.ReleasedPages, summary.Exemption, summary.PackageID, summary.FailureCause,
			summary.Version); err != nil {
			return fmt.Errorf("write summary %s: %w", summary.RequestID, err)
		}
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO projection_checkpoint (name, sequence) VALUES (?, ?)
		ON CONFLICT (name) DO UPDATE SET sequence = excluded.sequence`,
		summaryProjection, throughSequence); err != nil {
		return fmt.Errorf("advance projection checkpoint: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// Summaries returns the read model in submission order. The ORDER BY is part of
// the port's contract, not a convenience: the in-memory twin sorts the same way,
// so a caller cannot come to depend on an accident of one store.
func (s *Store) Summaries(ctx context.Context) ([]recordsrequest.Summary, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT request_id, requester, description, status, reviewer, submitted_at,
		       due_at, released_pages, exemption, package_id, failure_cause, version
		FROM request_summary
		ORDER BY submitted_at, request_id`)
	if err != nil {
		return nil, fmt.Errorf("select summaries: %w", err)
	}
	defer rows.Close()

	summaries := make([]recordsrequest.Summary, 0)
	for rows.Next() {
		var (
			summary            recordsrequest.Summary
			status             string
			submittedAt, dueAt int64
		)
		if err := rows.Scan(&summary.RequestID, &summary.Requester, &summary.Description,
			&status, &summary.Reviewer, &submittedAt, &dueAt, &summary.ReleasedPages,
			&summary.Exemption, &summary.PackageID, &summary.FailureCause,
			&summary.Version); err != nil {
			return nil, fmt.Errorf("scan summary: %w", err)
		}
		summary.Status = recordsrequest.Status(status)
		summary.SubmittedAt = time.Unix(0, submittedAt).UTC()
		summary.DueAt = time.Unix(0, dueAt).UTC()
		summaries = append(summaries, summary)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("select summaries: %w", err)
	}
	return summaries, nil
}

// scanEvents maps rows onto stored events and closes the rows.
func scanEvents(rows *sql.Rows) ([]recordsrequest.Stored, error) {
	defer rows.Close()

	stream := make([]recordsrequest.Stored, 0)
	for rows.Next() {
		var (
			stored     recordsrequest.Stored
			recordedAt int64
		)
		if err := rows.Scan(&stored.Sequence, &stored.RequestID, &stored.Version,
			&stored.Name, &stored.Payload, &stored.Metadata.TraceParent,
			&stored.Metadata.IdempotencyKey, &recordedAt); err != nil {
			return nil, fmt.Errorf("scan event: %w", err)
		}
		stored.RecordedAt = time.Unix(0, recordedAt).UTC()
		stream = append(stream, stored)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read events: %w", err)
	}
	return stream, nil
}

// querier is satisfied by both *sql.DB and *sql.Tx, so the idempotency lookup is
// written once and used both outside and inside the transaction.
type querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// recordedIn reads the result an idempotency key already produced. It returns
// sql.ErrNoRows for an unused key so callers can branch without a second query.
func recordedIn(ctx context.Context, q querier, key string) (recordsrequest.Result, error) {
	result := recordsrequest.Result{Replayed: true}
	err := q.QueryRowContext(ctx,
		`SELECT request_id, version FROM idempotency WHERE idempotency_key = ?`, key).
		Scan(&result.RequestID, &result.Version)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return recordsrequest.Result{}, err
		}
		return recordsrequest.Result{}, fmt.Errorf("read idempotency key: %w", err)
	}
	return result, nil
}

// isUniqueViolation reports whether the driver refused a write because a
// uniqueness constraint would have been broken. The check is on the message
// because the constraint, not its error code, is the thing being relied on: what
// matters is that the database refused the second writer, and it did.
func isUniqueViolation(err error) bool {
	return strings.Contains(err.Error(), "UNIQUE constraint failed")
}

// withPrefix qualifies a column list with a table name, for the join in
// PendingOutbox where both tables have a sequence column.
func withPrefix(columns, table string) string {
	parts := strings.Split(columns, ",")
	for i, part := range parts {
		parts[i] = table + "." + strings.TrimSpace(part)
	}
	return strings.Join(parts, ", ")
}
