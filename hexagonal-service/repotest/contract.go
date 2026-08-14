// Package repotest holds the executable contract for permit.Repository: one
// exported suite that any implementation of the port must pass.
//
// This is the load-bearing part of the example. An interface only says what the
// method names are; it cannot say that registering a duplicate number fails, or
// that a stale update writes nothing, or that two stores return renewal
// candidates in the same order. Those are the promises callers actually build
// on, so they are written down once, here, and every adapter runs them. The
// in-memory twin is then trustworthy as a test double for exactly one reason:
// it satisfies the same contract as the store that runs in production.
//
// The suite lives in a normal (non-_test) file so adapters in other packages can
// import it, which is the same arrangement the standard library uses for
// testing/fstest and net/http/httptest.
package repotest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/edgentx/code-examples/hexagonal-service/permit"
)

// day0 is the fixture epoch. Every fixture date is expressed relative to it so
// the assertions do not depend on when the suite runs.
var day0 = time.Date(2026, time.April, 6, 0, 0, 0, 0, time.UTC)

// RunRepositoryContract exercises the whole permit.Repository port against an
// implementation. newRepo is called once per case and must return an empty
// register; it takes *testing.T so an adapter can register its own cleanup.
func RunRepositoryContract(t *testing.T, newRepo func(t *testing.T) permit.Repository) {
	t.Helper()

	tests := []struct {
		name string
		run  func(t *testing.T, repo permit.Repository)
	}{
		{"register round-trips every field and stamps the first version", registerRoundTrips},
		{"register rejects a number already on the register", registerRejectsDuplicate},
		{"a rejected registration does not overwrite the stored permit", duplicateLeavesStoredPermit},
		{"an unknown number is not found", unknownNumberIsNotFound},
		{"update at the current version advances it", updateAdvancesVersion},
		{"update at a stale version is rejected and writes nothing", staleUpdateIsRejected},
		{"update of an unknown number is not found", updateOfUnknownNumber},
		{"expiring permits come back soonest first", expiringIsOrdered},
		{"suspended permits are not renewal candidates", expiringSkipsSuspended},
		{"nothing due is an empty result, not an error", expiringEmptyIsNotAnError},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			test.run(t, newRepo(t))
		})
	}
}

func registerRoundTrips(t *testing.T, repo permit.Repository) {
	ctx := context.Background()
	applied := issue(t, "BP-2026-00417", "Ridgeline Builders", permit.KindBuilding,
		"1400 Canal Street", day0)

	registered, err := repo.Register(ctx, applied)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if registered.Version != 1 {
		t.Errorf("Version after Register = %d, want 1", registered.Version)
	}

	loaded, err := repo.ByNumber(ctx, applied.Number)
	if err != nil {
		t.Fatalf("ByNumber: %v", err)
	}
	assertSame(t, loaded, registered)
}

func registerRejectsDuplicate(t *testing.T, repo permit.Repository) {
	ctx := context.Background()
	first := issue(t, "BP-2026-00417", "Ridgeline Builders", permit.KindBuilding,
		"1400 Canal Street", day0)
	if _, err := repo.Register(ctx, first); err != nil {
		t.Fatalf("Register: %v", err)
	}

	second := issue(t, "BP-2026-00417", "Harbor Mechanical", permit.KindPlumbing,
		"22 Quarry Road", day0)
	_, err := repo.Register(ctx, second)

	if !errors.Is(err, permit.ErrDuplicateNumber) {
		t.Fatalf("Register duplicate error = %v, want %v", err, permit.ErrDuplicateNumber)
	}
}

func duplicateLeavesStoredPermit(t *testing.T, repo permit.Repository) {
	ctx := context.Background()
	first := issue(t, "BP-2026-00417", "Ridgeline Builders", permit.KindBuilding,
		"1400 Canal Street", day0)
	registered, err := repo.Register(ctx, first)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	second := issue(t, "BP-2026-00417", "Harbor Mechanical", permit.KindPlumbing,
		"22 Quarry Road", day0)
	if _, err := repo.Register(ctx, second); !errors.Is(err, permit.ErrDuplicateNumber) {
		t.Fatalf("Register duplicate error = %v, want %v", err, permit.ErrDuplicateNumber)
	}

	loaded, err := repo.ByNumber(ctx, first.Number)
	if err != nil {
		t.Fatalf("ByNumber: %v", err)
	}
	assertSame(t, loaded, registered)
}

func unknownNumberIsNotFound(t *testing.T, repo permit.Repository) {
	_, err := repo.ByNumber(context.Background(), "BP-2026-99999")

	if !errors.Is(err, permit.ErrNotFound) {
		t.Fatalf("ByNumber error = %v, want %v", err, permit.ErrNotFound)
	}
}

func updateAdvancesVersion(t *testing.T, repo permit.Repository) {
	ctx := context.Background()
	registered := register(t, repo, issue(t, "EP-2026-00218", "Harbor Mechanical",
		permit.KindElectrical, "22 Quarry Road", day0))

	suspended, err := registered.Suspend()
	if err != nil {
		t.Fatalf("Suspend: %v", err)
	}
	updated, err := repo.Update(ctx, suspended)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Version != registered.Version+1 {
		t.Errorf("Version after Update = %d, want %d", updated.Version, registered.Version+1)
	}

	loaded, err := repo.ByNumber(ctx, registered.Number)
	if err != nil {
		t.Fatalf("ByNumber: %v", err)
	}
	assertSame(t, loaded, updated)
}

// staleUpdateIsRejected is the lost-update case: two officers read the same
// permit, both act on it, and the second write must lose rather than quietly
// discard the first.
func staleUpdateIsRejected(t *testing.T, repo permit.Repository) {
	ctx := context.Background()
	registered := register(t, repo, issue(t, "EP-2026-00218", "Harbor Mechanical",
		permit.KindElectrical, "22 Quarry Road", day0))

	suspended, err := registered.Suspend()
	if err != nil {
		t.Fatalf("Suspend: %v", err)
	}
	winner, err := repo.Update(ctx, suspended)
	if err != nil {
		t.Fatalf("first Update: %v", err)
	}

	// The second officer still holds the copy read before the suspension.
	stale := registered
	stale.Site = "22 Quarry Road, Bay 3"
	_, err = repo.Update(ctx, stale)

	if !errors.Is(err, permit.ErrVersionConflict) {
		t.Fatalf("stale Update error = %v, want %v", err, permit.ErrVersionConflict)
	}
	loaded, err := repo.ByNumber(ctx, registered.Number)
	if err != nil {
		t.Fatalf("ByNumber: %v", err)
	}
	assertSame(t, loaded, winner)
}

func updateOfUnknownNumber(t *testing.T, repo permit.Repository) {
	absent := issue(t, "BP-2026-99999", "Ridgeline Builders", permit.KindBuilding,
		"1400 Canal Street", day0)
	absent.Version = 1

	_, err := repo.Update(context.Background(), absent)

	if !errors.Is(err, permit.ErrNotFound) {
		t.Fatalf("Update error = %v, want %v", err, permit.ErrNotFound)
	}
}

func expiringIsOrdered(t *testing.T, repo permit.Repository) {
	ctx := context.Background()
	// Two permits share an issue date, so their expiry dates tie and the
	// contract's tiebreak by number is what decides the order.
	register(t, repo, issue(t, "PP-2026-00590", "Harbor Mechanical", permit.KindPlumbing,
		"9 Foundry Lane", day0.AddDate(0, 0, 20)))
	register(t, repo, issue(t, "BP-2026-00417", "Ridgeline Builders", permit.KindBuilding,
		"1400 Canal Street", day0))
	register(t, repo, issue(t, "EP-2026-00218", "Harbor Mechanical", permit.KindElectrical,
		"22 Quarry Road", day0))

	due, err := repo.ExpiringBefore(ctx, day0.AddDate(1, 0, 10))
	if err != nil {
		t.Fatalf("ExpiringBefore: %v", err)
	}

	want := []string{"BP-2026-00417", "EP-2026-00218"}
	assertNumbers(t, due, want)
}

func expiringSkipsSuspended(t *testing.T, repo permit.Repository) {
	ctx := context.Background()
	registered := register(t, repo, issue(t, "BP-2026-00417", "Ridgeline Builders",
		permit.KindBuilding, "1400 Canal Street", day0))
	register(t, repo, issue(t, "EP-2026-00218", "Harbor Mechanical",
		permit.KindElectrical, "22 Quarry Road", day0))

	suspended, err := registered.Suspend()
	if err != nil {
		t.Fatalf("Suspend: %v", err)
	}
	if _, err := repo.Update(ctx, suspended); err != nil {
		t.Fatalf("Update: %v", err)
	}

	due, err := repo.ExpiringBefore(ctx, day0.AddDate(1, 0, 10))
	if err != nil {
		t.Fatalf("ExpiringBefore: %v", err)
	}

	assertNumbers(t, due, []string{"EP-2026-00218"})
}

func expiringEmptyIsNotAnError(t *testing.T, repo permit.Repository) {
	register(t, repo, issue(t, "BP-2026-00417", "Ridgeline Builders", permit.KindBuilding,
		"1400 Canal Street", day0))

	due, err := repo.ExpiringBefore(context.Background(), day0.AddDate(0, 1, 0))

	if err != nil {
		t.Fatalf("ExpiringBefore: %v", err)
	}
	assertNumbers(t, due, nil)
}

// issue builds a valid permit through the domain constructor.
func issue(t *testing.T, number, holder string, kind permit.Kind, site string,
	issuedOn time.Time) permit.Permit {
	t.Helper()
	p, err := permit.Issue(number, holder, kind, site, issuedOn)
	if err != nil {
		t.Fatalf("permit.Issue(%s): %v", number, err)
	}
	return p
}

// register stores a permit and fails the test if the store rejects it.
func register(t *testing.T, repo permit.Repository, p permit.Permit) permit.Permit {
	t.Helper()
	registered, err := repo.Register(context.Background(), p)
	if err != nil {
		t.Fatalf("Register(%s): %v", p.Number, err)
	}
	return registered
}

// assertSame compares two permits field by field. Times are compared with Equal
// rather than == so an adapter that reconstructs a time.Time from stored
// components is not failed over its monotonic clock reading or location.
func assertSame(t *testing.T, got, want permit.Permit) {
	t.Helper()
	if got.Number != want.Number || got.Holder != want.Holder || got.Kind != want.Kind ||
		got.Site != want.Site || got.Status != want.Status || got.Version != want.Version {
		t.Errorf("permit = %+v, want %+v", got, want)
	}
	if !got.IssuedOn.Equal(want.IssuedOn) {
		t.Errorf("IssuedOn = %s, want %s", got.IssuedOn, want.IssuedOn)
	}
	if !got.ExpiresOn.Equal(want.ExpiresOn) {
		t.Errorf("ExpiresOn = %s, want %s", got.ExpiresOn, want.ExpiresOn)
	}
}

// assertNumbers checks a result list against the expected permit numbers in
// order.
func assertNumbers(t *testing.T, got []permit.Permit, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d permit(s) %v, want %d %v", len(got), numbers(got), len(want), want)
	}
	for i := range want {
		if got[i].Number != want[i] {
			t.Fatalf("permit numbers = %v, want %v", numbers(got), want)
		}
	}
}

// numbers extracts permit numbers for readable failure messages.
func numbers(permits []permit.Permit) []string {
	out := make([]string, len(permits))
	for i, p := range permits {
		out[i] = p.Number
	}
	return out
}
