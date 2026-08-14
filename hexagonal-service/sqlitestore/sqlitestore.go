// Package sqlitestore is the second driven adapter: the same permit.Repository
// port, backed by SQLite through database/sql. The driver is
// modernc.org/sqlite, a pure-Go translation of SQLite, so the example needs no
// cgo, no toolchain beyond Go, and no service running anywhere -- which is why
// the contract suite can be run against a real relational store in continuous
// integration on an empty machine.
//
// Everything SQL-shaped is confined to this file. The domain does not know that
// a version conflict is an UPDATE that matched no rows, and it does not need to:
// the adapter's job is to translate between the register's vocabulary and the
// store's.
package sqlitestore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/edgentx/code-examples/hexagonal-service/permit"

	_ "modernc.org/sqlite" // registers the "sqlite" database/sql driver
)

// schema is applied by Open. Times are stored as UTC Unix nanoseconds so that
// ordering by expiry is an integer comparison; storing formatted timestamps
// would sort wrongly whenever fractional seconds are present.
const schema = `
CREATE TABLE IF NOT EXISTS permits (
	number     TEXT    PRIMARY KEY,
	holder     TEXT    NOT NULL,
	kind       TEXT    NOT NULL,
	site       TEXT    NOT NULL,
	status     TEXT    NOT NULL,
	issued_on  INTEGER NOT NULL,
	expires_on INTEGER NOT NULL,
	version    INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS permits_expiry ON permits (status, expires_on, number);
`

const columns = `number, holder, kind, site, status, issued_on, expires_on, version`

// Store is a SQLite-backed permit register.
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
	// database from becoming several independent databases and removes
	// "database is locked" as a source of flakiness.
	db.SetMaxOpenConns(1)
	if _, err := db.ExecContext(ctx, schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	return &Store{db: db}, nil
}

// Close releases the database handle.
func (s *Store) Close() error { return s.db.Close() }

// Register inserts a permit that is not on the register yet. ON CONFLICT DO
// NOTHING makes the uniqueness check and the insert one statement, so two
// concurrent applications for the same number cannot both succeed; the loser
// affects no rows and is told the number is taken.
func (s *Store) Register(ctx context.Context, p permit.Permit) (permit.Permit, error) {
	p.Version = 1
	result, err := s.db.ExecContext(ctx, `
		INSERT INTO permits (`+columns+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (number) DO NOTHING`,
		p.Number, p.Holder, string(p.Kind), p.Site, string(p.Status),
		p.IssuedOn.UTC().UnixNano(), p.ExpiresOn.UTC().UnixNano(), p.Version)
	if err != nil {
		return permit.Permit{}, fmt.Errorf("insert permit: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return permit.Permit{}, fmt.Errorf("insert permit: %w", err)
	}
	if affected == 0 {
		return permit.Permit{}, permit.ErrDuplicateNumber
	}
	return p, nil
}

// Update writes a permit back only if the stored version still matches the one
// the caller read. The WHERE clause carries the version, so the check and the
// write are the same statement and no transaction is needed to make them atomic.
func (s *Store) Update(ctx context.Context, p permit.Permit) (permit.Permit, error) {
	next := p.Version + 1
	result, err := s.db.ExecContext(ctx, `
		UPDATE permits
		SET holder = ?, kind = ?, site = ?, status = ?,
		    issued_on = ?, expires_on = ?, version = ?
		WHERE number = ? AND version = ?`,
		p.Holder, string(p.Kind), p.Site, string(p.Status),
		p.IssuedOn.UTC().UnixNano(), p.ExpiresOn.UTC().UnixNano(), next,
		p.Number, p.Version)
	if err != nil {
		return permit.Permit{}, fmt.Errorf("update permit: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return permit.Permit{}, fmt.Errorf("update permit: %w", err)
	}
	if affected == 0 {
		// No row matched. Two different domain outcomes look identical here, so
		// ask which one it was: an absent permit is ErrNotFound, a present one
		// means somebody else wrote first.
		if _, err := s.ByNumber(ctx, p.Number); err != nil {
			return permit.Permit{}, err
		}
		return permit.Permit{}, permit.ErrVersionConflict
	}
	p.Version = next
	return p, nil
}

// ByNumber returns one permit.
func (s *Store) ByNumber(ctx context.Context, number string) (permit.Permit, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+columns+` FROM permits WHERE number = ?`, number)
	stored, err := scan(row)
	if errors.Is(err, sql.ErrNoRows) {
		return permit.Permit{}, permit.ErrNotFound
	}
	if err != nil {
		return permit.Permit{}, fmt.Errorf("select permit: %w", err)
	}
	return stored, nil
}

// ExpiringBefore returns active permits expiring before the cutoff. The ORDER BY
// is part of the port's contract, not a convenience: the in-memory twin sorts
// the same way, so a caller cannot come to depend on an accident of one store.
func (s *Store) ExpiringBefore(ctx context.Context, cutoff time.Time) ([]permit.Permit, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+columns+`
		FROM permits
		WHERE status = ? AND expires_on < ?
		ORDER BY expires_on, number`,
		string(permit.StatusActive), cutoff.UTC().UnixNano())
	if err != nil {
		return nil, fmt.Errorf("select expiring permits: %w", err)
	}
	defer rows.Close()

	due := make([]permit.Permit, 0)
	for rows.Next() {
		stored, err := scan(rows)
		if err != nil {
			return nil, fmt.Errorf("scan expiring permit: %w", err)
		}
		due = append(due, stored)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("select expiring permits: %w", err)
	}
	return due, nil
}

// scanner is satisfied by both *sql.Row and *sql.Rows, so one row-mapping
// function serves the single-row and multi-row queries.
type scanner interface {
	Scan(dest ...any) error
}

// scan maps one database row onto the domain type.
func scan(src scanner) (permit.Permit, error) {
	var (
		stored             permit.Permit
		kind, status       string
		issuedOn, expireOn int64
	)
	if err := src.Scan(&stored.Number, &stored.Holder, &kind, &stored.Site, &status,
		&issuedOn, &expireOn, &stored.Version); err != nil {
		return permit.Permit{}, err
	}
	stored.Kind = permit.Kind(kind)
	stored.Status = permit.Status(status)
	stored.IssuedOn = time.Unix(0, issuedOn).UTC()
	stored.ExpiresOn = time.Unix(0, expireOn).UTC()
	return stored, nil
}
