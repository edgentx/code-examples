// Package acceptance holds the step definitions for the acceptance criteria in
// ../features.
//
// The criteria are the tests. There is no separate runner and no separate
// command: `go test ./...` executes every scenario in every feature file, so a
// criterion that stops holding breaks the build the same way a unit test does.
// Everything the criteria drive is the real thing -- the real aggregate, the
// real event store, the real authorization model, the real relay, and for the
// console feature the real HTTP handler -- with only the clock, the identifier
// generator, and the package assembler replaced, because those are the three
// things whose real behavior would make an assertion depend on the weather.
package acceptance
