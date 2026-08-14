# Gherkin-driven testing (Go + godog)

**Agency ask this answers:** "Show me that the acceptance criteria in the contract are the tests that run, so 'done' means the same thing to both of us."

Permit inspection scheduling, written as acceptance criteria first. The rules a contractor argues
about — how much notice each priority owes, which days the office is closed, how many inspections a
district can staff in a day, and which single refusal a request that breaks several rules at once is
given — live in `features/*.feature` in the language the division uses. The Go code underneath is
what makes them true. `go test ./...` runs the criteria, so a criterion that stops holding breaks
the build.

## What it demonstrates

- **The criteria are the tests.** There is no separate document that says what the system should do
  and no separate command to check it. `features/inspection_notice.feature` is both the statement a
  program manager signs off on and the thing continuous integration executes. A criterion with no
  passing scenario is not delivered, and there is no place for the two to disagree.
- **`go test` is the whole runner.** `TestFeatures` builds a `godog.TestSuite` with
  `Options{Format: "pretty", Paths: []string{"features"}, TestingT: t}`. `TestingT` routes every
  failing step through the `*testing.T`, so the features fail like any other Go test, in the same
  command, with the same output. `Strict: true` fails the run on a step nobody implemented — without
  it a criterion can sit in the file for months being counted as delivered while never having
  executed once.
- **Boundaries in the Examples table.** Standard work owes three business days of notice, expedited
  one, an emergency none. Each scenario outline states the rule once and the table carries the rows
  that matter: exactly at the threshold, one business day inside it, and the crossings where a
  weekend or an observed holiday makes the calendar arithmetic non-obvious.
- **Refusal paths are first-class.** Twelve of the twenty scenarios are refusals, each naming the
  reason the contractor is read. `Reason` values in the domain are the exact phrases the feature
  files quote, so renaming a rule fails the criteria until they are renegotiated with the people who
  agreed to them.
- **Precedence is a stated rule, not an accident of ordering.** A request routinely breaks several
  rules at once — a suspended permit asking for a holiday it gave no notice for — and gets exactly
  one answer. Which one is a real decision: it is the difference between telling somebody to fix
  their permit and telling them to try another day. So it is pinned, in `daily_capacity.feature` for
  the collision a scheduler fields every week, and case by case in `TestRefusalPrecedence` for the
  rest.
- **Step definitions are a translation layer and nothing else.** Each one parses the Gherkin, calls
  the domain, and asserts. No rule is implemented in a step, so the steps cannot drift from the
  behavior the application ships. Setup steps book real requests through `Book` rather than writing
  to the scheduler's counters, because a step that writes state directly can arrange a situation the
  domain would never allow, and the scenario then proves something about a world that cannot happen.
- **Two layers, on purpose.** Gherkin carries the criteria somebody outside engineering agreed to.
  Ordinary table-driven Go tests carry the internals nobody should have to read in Gherkin: business
  day arithmetic at both ends of an interval, clock time within a day, refused requests consuming no
  capacity, the booking key covering both district and date.
- **Scenarios do not inherit state.** `ScenarioContext.Before` rebuilds the scenario struct before
  every scenario. Delete that one hook and five scenarios fail immediately: every capacity scenario
  books onto 2026-06-18 in a district that staffs two appointments a day, so the second one finds the
  day already committed by the first. Scenarios that leak are no longer independent statements about
  the system — the file becomes a script that only means anything read top to bottom.

## Layout

| File | Contents |
| --- | --- |
| `features/inspection_notice.feature` | Notice owed by priority, the calendar, and the rules that outrank them. |
| `features/daily_capacity.feature` | Routine appointments, the emergency reserve, and notice-before-capacity. |
| `features_test.go` | godog wiring, the per-scenario reset, and the step definitions. |
| `scheduler.go` | The rules and the order they are applied in. |
| `calendar.go` | Business days, observed holidays, and notice arithmetic. |
| `reasons.go` | One named reason per rule, worded exactly as the feature files quote them. |
| `scheduler_test.go` | Refusal precedence, capacity accounting, and the reserve slot. |
| `calendar_test.go` | Weekday, weekend, holiday, and interval boundaries. |

## Run it

```bash
cd gherkin-driven-testing
go test ./... -v
```

The features run inside `TestFeatures`. The godog summary should read `20 scenarios (20 passed)` and
`105 steps (105 passed)`, and because `TestingT` is set each scenario also appears as a Go subtest:

```
--- PASS: TestFeatures/An_emergency_takes_the_reserve_appointment_on_a_full_day (0.00s)
```

which means a single criterion can be selected the way any subtest is:

```bash
go test . -v -run 'TestFeatures/An_emergency_takes_the_reserve_appointment_on_a_full_day'
```

Go 1.24 or newer. The only dependency is [godog](https://github.com/cucumber/godog) v0.16, resolved
by `go.mod`; there is no vendor directory and no separate godog binary to install.

## How a criterion gets built

1. **The criterion is written in the feature file, before the code.** In the words of the person who
   has to sign it off — the priority, the day, the answer, and the reason.
2. **Run it and watch it fail.** A criterion that has never failed has never been shown to test
   anything. `Strict: true` makes an unimplemented step a failure rather than a note.
3. **Write the step definition, thin.** Parse, call, assert. If a step starts making a decision, that
   decision belongs in the domain, where the shipped application will also use it.
4. **Make it pass in the domain, and unit test the internals there.** The Gherkin proves the rule the
   agency agreed to; the Go tests hold the arithmetic and the edge cases underneath it.
5. **A criterion with no passing scenario is not delivered.** That is the whole contract, and it is
   why the criteria and the test suite have to be one artifact rather than two that are periodically
   reconciled.

## Notes

Every permit number, district, and date here is synthetic. The notice periods, the daily capacity,
and the observed holiday are invented for the example; a real division's numbers come from its own
published schedule, which is exactly the kind of thing that belongs in a feature file where the
division can read it.

The domain is deliberately a separate package from the steps, and the scheduler holds no persistence
code. See [`../event-sourced-aggregate`](../event-sourced-aggregate) for the same separation drawn
around a case record whose history is the system of record.
