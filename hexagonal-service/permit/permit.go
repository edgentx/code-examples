// Package permit is the inside of the hexagon: the permit register's entity,
// its invariants, and the ports it needs the outside world to satisfy. It
// imports nothing but the standard library, and in particular it imports no
// database driver. That is the property the example exists to demonstrate --
// the rules an agency has to defend are not welded to a vendor's storage engine.
package permit

import (
	"strings"
	"time"
)

// termYears is how long a permit runs from the day it is issued. The expiry is
// computed on the calendar rather than by adding a fixed number of hours, so a
// permit issued the day before a leap day still runs a full year.
const termYears = 1

// expiry returns the last day a term that began on the given day authorizes work.
func expiry(issuedOn time.Time) time.Time {
	return issuedOn.UTC().AddDate(termYears, 0, 0)
}

// Kind is the class of work a permit authorizes.
type Kind string

const (
	// KindBuilding covers structural work.
	KindBuilding Kind = "building"
	// KindElectrical covers wiring and service equipment.
	KindElectrical Kind = "electrical"
	// KindPlumbing covers supply and waste lines.
	KindPlumbing Kind = "plumbing"
)

// Valid reports whether the kind is one the register recognizes.
func (k Kind) Valid() bool {
	switch k {
	case KindBuilding, KindElectrical, KindPlumbing:
		return true
	default:
		return false
	}
}

// Status is where a permit stands on the register.
type Status string

const (
	// StatusActive means the permit authorizes work today.
	StatusActive Status = "active"
	// StatusSuspended means an inspector has stopped work under this permit.
	StatusSuspended Status = "suspended"
)

// Permit is a single entry on the register. Its fields are exported because an
// adapter has to be able to write every one of them to a row and read them
// back; the invariants are enforced at the doors -- Issue, Suspend, Reinstate,
// Renew -- rather than by hiding the fields.
type Permit struct {
	// Number is the register identifier and the natural key.
	Number string
	// Holder is the contractor or owner accountable for the work.
	Holder string
	// Kind is the class of work authorized.
	Kind Kind
	// Site is the address the permit is drawn against.
	Site string
	// Status is the current standing on the register.
	Status Status
	// IssuedOn is the day the current term began.
	IssuedOn time.Time
	// ExpiresOn is the last day the permit authorizes work.
	ExpiresOn time.Time
	// Version is the optimistic-concurrency counter. It is owned by the
	// repository, never by the caller: a repository stamps the next value and
	// rejects an update that carries a stale one.
	Version int
}

// Issue mints a new permit for the register. The returned permit has Version
// zero because nothing has stored it yet.
func Issue(number, holder string, kind Kind, site string, issuedOn time.Time) (Permit, error) {
	if strings.TrimSpace(number) == "" ||
		strings.TrimSpace(holder) == "" ||
		strings.TrimSpace(site) == "" {
		return Permit{}, ErrMissingField
	}
	if !kind.Valid() {
		return Permit{}, ErrUnknownKind
	}
	if issuedOn.IsZero() {
		return Permit{}, ErrMissingField
	}
	return Permit{
		Number:    number,
		Holder:    holder,
		Kind:      kind,
		Site:      site,
		Status:    StatusActive,
		IssuedOn:  issuedOn.UTC(),
		ExpiresOn: expiry(issuedOn),
	}, nil
}

// Suspend stops work under an active permit. It returns a new value rather than
// mutating the receiver, so a rejected transition cannot leave a half-changed
// permit behind for the caller to store.
func (p Permit) Suspend() (Permit, error) {
	if p.Status != StatusActive {
		return Permit{}, ErrNotActive
	}
	p.Status = StatusSuspended
	return p, nil
}

// Reinstate returns a suspended permit to active standing.
func (p Permit) Reinstate() (Permit, error) {
	if p.Status != StatusSuspended {
		return Permit{}, ErrNotSuspended
	}
	p.Status = StatusActive
	return p, nil
}

// Renew starts a fresh term. A suspended permit cannot be renewed: the register
// must not carry an expiry date that implies work is authorized when it is not.
func (p Permit) Renew(on time.Time) (Permit, error) {
	if p.Status != StatusActive {
		return Permit{}, ErrNotActive
	}
	if on.IsZero() {
		return Permit{}, ErrMissingField
	}
	p.IssuedOn = on.UTC()
	p.ExpiresOn = expiry(on)
	return p, nil
}

// Expired reports whether the permit's term has run out as of the given moment.
func (p Permit) Expired(at time.Time) bool { return !at.Before(p.ExpiresOn) }
