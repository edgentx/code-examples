package cloudevent

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// W3C Trace Context, the part of it a message needs. A message that crosses a
// broker cannot inherit a trace from a call stack, so the correlation has to
// travel inside the message. This file is the whole of that: parse a
// traceparent, start one, continue one.
//
// The format is fixed by the specification and is not negotiable between
// services:
//
//	version "-" trace-id "-" parent-id "-" trace-flags
//	  00     -   32 hex   -    16 hex   -     2 hex
//
// A receiver that cannot parse the header must start a new trace rather than
// guess, otherwise one malformed header silently reparents every span after it.

const (
	// supportedVersion is the only version this code writes. Later versions are
	// still accepted on the way in, per the specification's forward-compatibility
	// rule, as long as the three fields this code reads are well formed.
	supportedVersion = "00"
	// invalidVersion is reserved by the specification and never valid.
	invalidVersion = "ff"
	// headerLength is the length of a version 00 traceparent.
	headerLength = 55
	// sampledFlag is the one trace flag with a defined meaning.
	sampledFlag = 0x01
)

// TraceParentHeader is the header and CloudEvents extension attribute name. It
// is lowercase in both places, which is why it is written once here.
const TraceParentHeader = "traceparent"

var (
	// ErrMalformedTraceParent is returned when a traceparent cannot be read. The
	// caller starts a new trace; it never proceeds with a partial one.
	ErrMalformedTraceParent = errors.New("malformed traceparent")
	// ErrNoTraceParent is returned when there is no traceparent to read.
	ErrNoTraceParent = errors.New("no traceparent")
)

// TraceID identifies one end-to-end operation. It is the value that has to
// survive every hop, including the hop through a broker.
type TraceID [16]byte

// SpanID identifies one unit of work inside a trace.
type SpanID [8]byte

// SpanContext is what travels: which operation this is part of, which unit of
// work is the parent of the next one, and whether the operation is sampled.
type SpanContext struct {
	TraceID TraceID
	SpanID  SpanID
	Sampled bool
}

// StartTrace begins a new trace. It is called when a request arrives with no
// usable traceparent, which is the only place a trace id may be invented.
func StartTrace() (SpanContext, error) {
	var span SpanContext
	if _, err := rand.Read(span.TraceID[:]); err != nil {
		return SpanContext{}, fmt.Errorf("generate trace id: %w", err)
	}
	if _, err := rand.Read(span.SpanID[:]); err != nil {
		return SpanContext{}, fmt.Errorf("generate span id: %w", err)
	}
	span.Sampled = true
	return span, nil
}

// Child continues the trace in a new unit of work. The trace id is carried
// unchanged -- that is what makes one incident one search -- and only the span
// id moves, so the hops stay distinguishable from each other.
func (c SpanContext) Child() (SpanContext, error) {
	if !c.Valid() {
		return StartTrace()
	}
	child := c
	if _, err := rand.Read(child.SpanID[:]); err != nil {
		return SpanContext{}, fmt.Errorf("generate span id: %w", err)
	}
	return child, nil
}

// Valid reports whether the context can be sent. All-zero identifiers are
// invalid by specification, and sending one is worse than sending none: it
// looks like a trace and correlates nothing.
func (c SpanContext) Valid() bool {
	return c.TraceID != TraceID{} && c.SpanID != SpanID{}
}

// TraceParent renders the context as a header value, or the empty string if
// there is nothing valid to render.
func (c SpanContext) TraceParent() string {
	if !c.Valid() {
		return ""
	}
	flags := byte(0)
	if c.Sampled {
		flags = sampledFlag
	}
	return fmt.Sprintf("%s-%s-%s-%s",
		supportedVersion,
		hex.EncodeToString(c.TraceID[:]),
		hex.EncodeToString(c.SpanID[:]),
		hex.EncodeToString([]byte{flags}))
}

// TraceIDString renders the trace id on its own, which is the value a log line
// carries and an operator pastes into a query.
func (c SpanContext) TraceIDString() string { return hex.EncodeToString(c.TraceID[:]) }

// SpanIDString renders the span id on its own.
func (c SpanContext) SpanIDString() string { return hex.EncodeToString(c.SpanID[:]) }

// ParseTraceParent reads a traceparent header value.
//
// A header from a later version of the specification is accepted as long as the
// version, trace id, parent id and flags it starts with are well formed, and
// anything after them is ignored. Rejecting the whole header because it carries
// a field this code does not know about would break exactly the compatibility
// the version prefix exists to provide.
func ParseTraceParent(value string) (SpanContext, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return SpanContext{}, ErrNoTraceParent
	}
	if len(value) < headerLength {
		return SpanContext{}, fmt.Errorf("%w: %d characters", ErrMalformedTraceParent, len(value))
	}

	fields := strings.Split(value[:headerLength], "-")
	if len(fields) != 4 {
		return SpanContext{}, fmt.Errorf("%w: expected 4 fields", ErrMalformedTraceParent)
	}
	version, traceField, spanField, flagField := fields[0], fields[1], fields[2], fields[3]

	if !isHex(version) || version == invalidVersion {
		return SpanContext{}, fmt.Errorf("%w: version %q", ErrMalformedTraceParent, version)
	}
	if version == supportedVersion && len(value) != headerLength {
		// Version 00 has exactly four fields. Trailing data means the sender and
		// this reader disagree about the format, which is not something to guess at.
		return SpanContext{}, fmt.Errorf("%w: trailing data after a version 00 header",
			ErrMalformedTraceParent)
	}
	if version != supportedVersion && value[headerLength] != '-' {
		return SpanContext{}, fmt.Errorf("%w: unterminated field after trace flags",
			ErrMalformedTraceParent)
	}

	var span SpanContext
	if !isHex(traceField) || !decodeInto(traceField, span.TraceID[:]) {
		return SpanContext{}, fmt.Errorf("%w: trace id %q", ErrMalformedTraceParent, traceField)
	}
	if !isHex(spanField) || !decodeInto(spanField, span.SpanID[:]) {
		return SpanContext{}, fmt.Errorf("%w: parent id %q", ErrMalformedTraceParent, spanField)
	}
	if !isHex(flagField) {
		return SpanContext{}, fmt.Errorf("%w: flags %q", ErrMalformedTraceParent, flagField)
	}
	flags, err := hex.DecodeString(flagField)
	if err != nil {
		return SpanContext{}, fmt.Errorf("%w: flags %q", ErrMalformedTraceParent, flagField)
	}
	span.Sampled = flags[0]&sampledFlag == sampledFlag

	if !span.Valid() {
		return SpanContext{}, fmt.Errorf("%w: all-zero identifier", ErrMalformedTraceParent)
	}
	return span, nil
}

// ContinueOrStart reads an inbound traceparent and continues that trace, or
// starts a fresh one if there is nothing readable to continue. It is what every
// entry point calls, so no entry point has to decide what to do about a bad
// header on its own.
func ContinueOrStart(value string) (SpanContext, error) {
	parent, err := ParseTraceParent(value)
	if err != nil {
		return StartTrace()
	}
	return parent.Child()
}

// decodeInto fills a fixed-size identifier and reports whether the input was
// the right length for it.
func decodeInto(field string, out []byte) bool {
	if len(field) != len(out)*2 {
		return false
	}
	decoded, err := hex.DecodeString(field)
	if err != nil {
		return false
	}
	copy(out, decoded)
	return true
}

// isHex reports whether every character is a lowercase hexadecimal digit. The
// specification requires lowercase, and accepting uppercase here would make two
// spellings of the same id compare unequal downstream.
func isHex(field string) bool {
	if field == "" {
		return false
	}
	for _, char := range field {
		switch {
		case char >= '0' && char <= '9':
		case char >= 'a' && char <= 'f':
		default:
			return false
		}
	}
	return true
}
