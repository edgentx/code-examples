package permit

import (
	"context"
	"time"
)

// Repository is the driven port. It is declared here, by the domain, in the
// domain's own words: a register is something you can add a permit to, replace
// a permit in, look a permit up in by its number, and ask for the permits whose
// term is about to run out. There is no row, no column, no transaction and no
// query language in this interface, which is what lets the same domain run
// against a map, a relational store, or a records service without change.
//
// The behavior every implementation owes the domain is not written down in this
// file alone -- prose is not enforceable. It is written as an executable
// contract in ../repotest, which both adapters run.
type Repository interface {
	// Register adds a permit that is not on the register yet. It stamps the
	// first version and returns the stored permit. It returns
	// ErrDuplicateNumber if the number is already taken.
	Register(ctx context.Context, p Permit) (Permit, error)

	// Update replaces a permit that is already on the register. The supplied
	// Version must match the stored one; otherwise the update is rejected with
	// ErrVersionConflict and nothing is written. On success the stored version
	// advances by one and the stored permit is returned. It returns ErrNotFound
	// if the number is not on the register.
	Update(ctx context.Context, p Permit) (Permit, error)

	// ByNumber returns one permit, or ErrNotFound.
	ByNumber(ctx context.Context, number string) (Permit, error)

	// ExpiringBefore returns the active permits whose term ends before the
	// cutoff, soonest first, ties broken by number so that two adapters return
	// the same list in the same order. Suspended permits are excluded: they do
	// not authorize work, so they are not renewal candidates. Nothing due is an
	// empty result, not an error.
	ExpiringBefore(ctx context.Context, cutoff time.Time) ([]Permit, error)
}
