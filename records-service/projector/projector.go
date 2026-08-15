// Package projector keeps the read model up to date by replaying the event
// stream.
//
// It is the second of the three consumers of the stream, and the least
// privileged: it writes nothing that is not derivable from events, so the read
// model is disposable. Delete the summary table and the projector rebuilds it
// from sequence zero with no coordination, no downtime window, and no data
// migration -- which is the operational reason to keep the events rather than
// the state.
//
// Progress is a checkpoint, not an acknowledgment, and the batch is saved in the
// same transaction as the checkpoint. A projector that crashes between the two
// cannot exist. A projector that repeats a batch it already saved is ignored,
// because the checkpoint is compared before anything is written.
package projector

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/edgentx/code-examples/records-service/recordsrequest"
)

// Projector catches a read model up to a store.
type Projector struct {
	repo  recordsrequest.Repository
	model recordsrequest.Projection
	batch int
	log   *slog.Logger
}

// New wires a projector. A batch size of zero uses a sensible default.
func New(repo recordsrequest.Repository, model recordsrequest.Projection, batch int,
	log *slog.Logger) *Projector {
	if batch <= 0 {
		batch = 200
	}
	if log == nil {
		log = slog.Default()
	}
	return &Projector{repo: repo, model: model, batch: batch, log: log}
}

// CatchUp applies every event the read model has not seen and returns how many
// it applied.
//
// The summaries are rendered by rehydrating each request the batch touched
// rather than by folding the events a second time. One request may appear in a
// batch several times and is rendered once, at the state the batch leaves it in.
func (p *Projector) CatchUp(ctx context.Context) (int, error) {
	applied := 0
	for {
		from, err := p.model.Checkpoint(ctx)
		if err != nil {
			return applied, err
		}
		batch, err := p.repo.Stream(ctx, from, p.batch)
		if err != nil {
			return applied, fmt.Errorf("read the stream after %d: %w", from, err)
		}
		if len(batch) == 0 {
			return applied, nil
		}

		touched := make([]string, 0, len(batch))
		seen := make(map[string]bool, len(batch))
		for _, stored := range batch {
			if !seen[stored.RequestID] {
				seen[stored.RequestID] = true
				touched = append(touched, stored.RequestID)
			}
		}

		summaries := make([]recordsrequest.Summary, 0, len(touched))
		for _, requestID := range touched {
			request, err := p.repo.Load(ctx, requestID)
			if err != nil {
				return applied, fmt.Errorf("rehydrate %s: %w", requestID, err)
			}
			summaries = append(summaries, recordsrequest.SummaryOf(request))
		}

		through := batch[len(batch)-1].Sequence
		if err := p.model.Save(ctx, summaries, through); err != nil {
			return applied, err
		}
		applied += len(batch)

		if len(batch) < p.batch {
			return applied, nil
		}
	}
}

// Run catches up on a fixed interval until the context is canceled. A failed
// pass is logged and retried on the next tick rather than ending the projector:
// the events are durable and the checkpoint did not move, so nothing is lost by
// waiting.
func (p *Projector) Run(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := p.CatchUp(ctx); err != nil && ctx.Err() == nil {
				p.log.Error("projection pass failed", "error", err)
			}
		}
	}
}
