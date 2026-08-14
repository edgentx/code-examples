# Accessibility as a merge gate (Playwright + axe-core)

**Agency ask this answers:** "Show me that Section 508 and WCAG 2.1 AA conformance is enforced by the build, not promised in a document."

A small agency-style form and the test that will not let it regress. Every pull request runs an
axe-core scan of the page in a real browser, plus keyboard assertions that a scanner cannot make.
A violation fails the job, and a failed job blocks the merge. Conformance is therefore a property
of the repository rather than a paragraph in a VPAT that ages out between releases.

## What it demonstrates

- **The gate is the build.** `npm test` starts a static server, loads the page in Chromium, runs
  axe against the WCAG 2.0/2.1 A and AA tag set, and fails on any violation. There is no separate
  audit step and nothing to remember to run. In CI it is the **`Accessibility gate`** job in
  [`.github/workflows/ci.yml`](../.github/workflows/ci.yml).
- **A readable failure.** The report names the rule, its impact, the failing selector, the actual
  markup, and the Deque page that explains the fix. A gate that fails with an unreadable dump gets
  muted rather than fixed, so the formatting is part of the design (see the output below).
- **Proof the gate bites.** `tests/fixtures/violations.html` is the same form with four planted
  defects. A committed, passing test asserts that axe reports `label`, `image-alt`,
  `color-contrast`, and `heading-order` on it. That test stays green in CI while proving the
  scanner is really running: a misconfigured `AxeBuilder` returning an empty result set would pass
  every other test in the file and fail only this one.
- **What a scanner cannot see.** Automated tooling catches roughly a third of WCAG issues.
  The other tests cover the part it misses: the full Tab order through the page, ending on the
  submit control, and that a failed submission moves focus to the error summary rather than merely
  announcing it. Those are human judgements written down as assertions.
- **The error state is scanned too.** Pages are usually audited empty and happy. The state a user
  actually gets stuck in is the one after a failed submission, so the gate loads that state and
  scans it as well.
- **An accessible page worth copying.** Skip link, landmark structure, labeled controls,
  `fieldset`/`legend` for the delivery choice, a required-field pattern where the visible hint and
  the visible error are the same nodes named in `aria-describedby`, a focusable `role="alert"`
  error summary that links to each failing control, a `<caption>`ed table with `scope` on every
  header cell, contrast chosen for 4.5:1 and up, and one visible focus style defined once.

## Layout

| File | Contents |
| --- | --- |
| `public/index.html` | The page under test: an agency records-request form and its client-side validation. |
| `public/styles.css` | Color tokens chosen for contrast, the visible focus style, the visually-hidden helper. |
| `tests/accessibility.spec.ts` | The gate: axe scans, keyboard order, error-summary focus, and the fixture proof. |
| `tests/fixtures/violations.html` | The same form with four deliberate, commented defects. The control sample. |
| `playwright.config.ts` | Chromium only, no retries, `list` reporter, and the static `webServer`. |
| `package.json` | Exactly pinned dependencies and `npm test`. |

## Run it

```bash
cd accessibility-gated-ci
npm install
npx playwright install --with-deps chromium
npm test
```

Green looks like this:

```
Running 5 tests using 5 workers

  ✓  1 [chromium] › tests/accessibility.spec.ts:127:7 › WCAG 2.1 AA gate › the request form has no violations on first load (639ms)
  ✓  2 [chromium] › tests/accessibility.spec.ts:135:7 › WCAG 2.1 AA gate › the error state has no violations either (604ms)
  ✓  3 [chromium] › tests/accessibility.spec.ts:150:7 › what an automated scan cannot see › the keyboard reaches every control and ends on submit (168ms)
  ✓  4 [chromium] › tests/accessibility.spec.ts:160:7 › what an automated scan cannot see › a failed submission moves focus to the error summary (234ms)
  ✓  5 [chromium] › tests/accessibility.spec.ts:179:7 › proof the gate bites › the planted defects in the fixture are all detected (556ms)

  5 passed (1.6s)
```

To read the page in a browser: `npm run serve`, then open
`http://127.0.0.1:4173/public/index.html`.

## Watching the gate fail

The one-line edit is written in the spec, above the first test. Point the first scan at the broken
fixture instead of the real page:

```ts
// tests/accessibility.spec.ts, in "the request form has no violations on first load"
const violations = await scan(page, FIXTURE);   // instead of: await scan(page, PAGE);
```

Then `npm test`. This is the actual output, not a paraphrase:

```
  1) [chromium] › tests/accessibility.spec.ts:127:7 › WCAG 2.1 AA gate › the request form has no violations on first load

    Error: 4 accessibility violation(s): color-contrast, heading-order, image-alt, label
      1. color-contrast (serious) - Elements must meet minimum color contrast ratio thresholds
         why:      Ensure the contrast between foreground and background colors meets WCAG 2 AA minimum contrast ratio thresholds
         fix:      https://dequeuniversity.com/rules/axe/4.13/color-contrast?application=playwright
         selector: .fine-print
         markup:   <p class="fine-print"> Requests are answered in the order received. Fee waivers are decided separately. </p>
      2. heading-order (moderate) - Heading levels should only increase by one
         why:      Ensure the order of headings is semantically correct
         fix:      https://dequeuniversity.com/rules/axe/4.13/heading-order?application=playwright
         selector: h3
         markup:   <h3>What you are asking for</h3>
      3. image-alt (critical) - Images must have alternative text
         why:      Ensure <img> elements have alternative text or a role of none or presentation
         fix:      https://dequeuniversity.com/rules/axe/4.13/image-alt?application=playwright
         selector: img
         markup:   <img src="data:image/gif;base64,R0lGODlhAQABAIAAAAAAAP///yH5BAEAAAAALAAAAAABAAEAAAIBRAA7" width="48" height="48">
      4. label (critical) - Form elements must have labels
         why:      Ensure every form element has a label
         fix:      https://dequeuniversity.com/rules/axe/4.13/label?application=playwright
         selector: input
         markup:   <input type="text" name="full-name">

    expect(received).toEqual(expected) // deep equality

    - Expected  - 1
    + Received  + 6

    - Array []
    + Array [
    +   "color-contrast",
    +   "heading-order",
    +   "image-alt",
    +   "label",
    + ]

  1 failed
```

Every line a developer needs in order to fix it is in the failure itself: which rule, how serious,
which element, and where the remedy is documented.

## Notes

The page, the form fields, the phone number, and the response times are synthetic. Nothing here is
drawn from a real agency system.

**Scope of the claim.** Automated scanning catches roughly a third of WCAG success criteria. It
proves the mechanical things — a missing label, an unlabeled image, a contrast ratio, a skipped
heading — and it proves them on every commit, which manual testing cannot. It does not prove that
the alternative text is *accurate*, that the reading order makes sense, or that the form is usable
with a screen reader. This gate is the floor, not the ceiling: it makes regressions impossible to
merge and leaves the human review to spend its time where a machine is useless.

**Tag set.** The gate runs `wcag2a`, `wcag2aa`, `wcag21a`, `wcag21aa`, plus axe's `best-practice`
bundle. The best-practice rules are included because axe files some structural checks there rather
than mapping them to one success criterion — a skipped heading level being the obvious one — and
those are cheap to satisfy now and expensive to retrofit.

**Retries are zero on purpose.** An accessibility violation is deterministic. Giving the gate a
flake budget is giving it a way to hide a real failure.

**No lockfile is committed.** Versions are pinned exactly in `package.json` and CI runs
`npm install`, so the run is reproducible without carrying a large generated file in a repository
whose point is to be read.

The static server is rooted at this directory rather than `public/` so that the same server also
serves `tests/fixtures/violations.html`. Nothing on the page loads from a CDN or any other
external host; the one image in the fixture is an inline data URI.
