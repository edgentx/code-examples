# Accessibility as a merge gate (Playwright + axe-core)

**Requirement this addresses:** Section 508 and WCAG 2.1 AA conformance, enforced as a build gate rather than attested in documentation.

A small agency-style form and the test that will not let it regress. Every pull request runs an
axe-core scan of the page in a real browser, repeats that scan under the color scheme and motion
preferences a reader may have set, and then makes the measurements a scanner cannot: the keyboard
traversal, the focus indicator at every stop, reflow at 320 CSS pixels, and text at 200%. A
violation fails the job, and a failed job blocks the merge. Conformance is therefore a property of
the repository rather than a paragraph in a VPAT that ages out between releases.

## What it demonstrates

- **The gate is the build.** `npm test` starts a static server, loads the page in Chromium, runs
  axe against the WCAG 2.0/2.1 A and AA tag set, and fails on any violation. There is no separate
  audit step and nothing to remember to run. In CI it is the **`Accessibility gate`** job in
  [`.github/workflows/ci.yml`](../.github/workflows/ci.yml).
- **A readable failure.** The report names the rule, its impact, the failing selector, the actual
  markup, and the Deque page that explains the fix. A gate that fails with an unreadable dump gets
  muted rather than fixed, so the formatting is part of the design (see the output below).
- **Proof the gate bites.** Two fixtures, one for each kind of check.
  `tests/fixtures/violations.html` is the same form with four planted defects, and a committed,
  passing test asserts that axe reports `label`, `image-alt`, `color-contrast`, and `heading-order`
  on it. `tests/fixtures/presentation-defects.html` carries three defects the scanner is blind to:
  a focus outline suppressed with `outline: none`, a 900px banner that forces sideways scrolling at
  320 CSS pixels, and a row that cannot wrap, which carries its control off the screen at a 200%
  root font size. One test asserts axe reports nothing at all on that file, which is the argument
  for writing the other checks by hand; three more assert that each of those checks reports its
  defect by name. All of them stay green in CI while proving the gate is really running: a scan or
  a check that quietly stopped measuring would pass every other test in the file and fail only
  these.
- **What a scanner cannot see.** Automated tooling catches roughly a third of WCAG issues. The
  other tests are the manual review methodology written down as assertions, and they are the same
  checks the VPAT records:
  - **A 25-stop keyboard traversal.** Tab from the top of the document, recording every stop, to a
    bound of 25 so that a focus trap fails the gate in a second rather than hanging it. The order
    has to match the order the page is read.
  - **A visible focus indicator at every stop.** Reachability is not usability. The same walk reads
    the computed `outline-style` and `outline-width` where focus landed, so an outline that
    somebody removed without replacing it is a failed build rather than a finding in a report.
  - **Reflow at 320 CSS pixels.** WCAG 1.4.10, measured as `document.scrollingElement.scrollWidth`
    against `window.innerWidth` in a 320px viewport. Scrolling down is expected; scrolling down and
    across as well is the failure.
  - **A 200% root font size.** WCAG 1.4.4, applied by doubling the root font size rather than by
    shrinking the window, which is a different criterion. Every labeled control has to stay
    rendered and stay inside the horizontal bounds of the viewport.
  - **A failed submission moves focus to the error summary** rather than merely announcing it.
- **The error state is scanned too.** Pages are usually audited empty and happy. The state a user
  actually gets stuck in is the one after a failed submission, so the gate loads that state and
  scans it as well.
- **The preferences a reader arrives with are scanned too.** The scan runs again under
  `prefers-color-scheme: dark` and again under `prefers-reduced-motion: reduce`. A dark palette is a
  second set of color pairs, and a ratio that holds on white says nothing about the pair that
  replaces it. The reduced-motion branch is a second branch of the stylesheet, and a branch nothing
  loads is a branch nothing checks. Both re-scans were confirmed against a deliberate regression
  planted inside one preference block: the first-load scan stays green and only the matching
  re-scan turns red.
- **An accessible page worth copying.** Skip link, landmark structure, labeled controls,
  `fieldset`/`legend` for the delivery choice, a required-field pattern where the visible hint and
  the visible error are the same nodes named in `aria-describedby`, a focusable `role="alert"`
  error summary that links to each failing control, a `<caption>`ed table with `scope` on every
  header cell held in a named, focusable scroll region so the one thing that cannot reflow is still
  operable from the keyboard, contrast chosen for 4.5:1 and up in both palettes, and one visible
  focus style defined once.

## Layout

| File | Contents |
| --- | --- |
| `public/index.html` | The page under test: an agency records-request form and its client-side validation. |
| `public/styles.css` | Color tokens chosen for contrast in both palettes, the visible focus style, the visually-hidden helper. |
| `tests/accessibility.spec.ts` | The gate: axe scans, the keyboard traversal, reflow and text resize, and the fixture proofs. |
| `tests/fixtures/violations.html` | The same form with four deliberate, commented defects the scanner catches. |
| `tests/fixtures/presentation-defects.html` | The same form with three defects the scanner cannot see. The control sample for the written checks. |
| `playwright.config.ts` | Chromium only, no retries, `list` reporter, and the static `webServer`. |
| `package.json` | Exactly pinned dependencies and `npm test`. |

## Run it

```bash
cd accessibility-gated-ci
npm install
npx playwright install --with-deps chromium
npm test
```

Green looks like this. The `list` reporter prints each test as it finishes, so the order is the
order they completed in, not the order they are written:

```
Running 14 tests using 12 workers

  ✓   1 [chromium] › tests/accessibility.spec.ts:381:7 › what an automated scan cannot see › the page reflows into 320 CSS pixels (248ms)
  ✓   6 [chromium] › tests/accessibility.spec.ts:393:7 › what an automated scan cannot see › nothing is lost when the root font size is doubled (232ms)
  ✓   7 [chromium] › tests/accessibility.spec.ts:342:7 › what an automated scan cannot see › the keyboard reaches every control in the order the page is read (266ms)
  ✓   4 [chromium] › tests/accessibility.spec.ts:430:7 › proof the gate bites › the focus check reports the suppressed outline (287ms)
  ✓  13 [chromium] › tests/accessibility.spec.ts:438:7 › proof the gate bites › the reflow check reports the fixed-width banner (81ms)
  ✓   8 [chromium] › tests/accessibility.spec.ts:351:7 › what an automated scan cannot see › every keyboard stop shows where the keyboard is (269ms)
  ✓  14 [chromium] › tests/accessibility.spec.ts:447:7 › proof the gate bites › the resize check reports the control carried off screen (146ms)
  ✓   2 [chromium] › tests/accessibility.spec.ts:420:7 › proof the gate bites › the scanner reports nothing at all on the presentation fixture (564ms)
  ✓   5 [chromium] › tests/accessibility.spec.ts:406:7 › proof the gate bites › the planted defects in the fixture are all detected (554ms)
  ✓   9 [chromium] › tests/accessibility.spec.ts:298:7 › WCAG 2.1 AA gate › the request form has no violations on first load (608ms)
  ✓  10 [chromium] › tests/accessibility.spec.ts:319:7 › WCAG 2.1 AA gate › the page scans clean under a dark color scheme (625ms)
  ✓  12 [chromium] › tests/accessibility.spec.ts:330:7 › WCAG 2.1 AA gate › the page scans clean when the reader prefers reduced motion (627ms)
  ✓   3 [chromium] › tests/accessibility.spec.ts:306:7 › WCAG 2.1 AA gate › the error state has no violations either (798ms)
  ✓  11 [chromium] › tests/accessibility.spec.ts:364:7 › what an automated scan cannot see › a failed submission moves focus to the error summary (1.1s)

  14 passed (2.1s)
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
  1) [chromium] › tests/accessibility.spec.ts:298:7 › WCAG 2.1 AA gate › the request form has no violations on first load

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

The written checks are held to the same standard, because a failure nobody can read is a check
somebody deletes. Adding `.button:focus-visible { outline: none }` to `public/styles.css` produces
this, again verbatim:

```
  1) [chromium] › tests/accessibility.spec.ts:351:7 › what an automated scan cannot see › every keyboard stop shows where the keyboard is

    Error: 1 keyboard stop(s) with no visible focus indicator:
      1. #submit-request - outline-style: none, outline-width: 3px

    expect(received).toEqual(expected) // deep equality

    - Expected  - 1
    + Received  + 3

    - Array []
    + Array [
    +   "#submit-request",
    + ]
```

The reported width is 3px because the rule that suppressed the outline left the width behind, which
is exactly why the check reads both properties rather than either one.

## Notes

The page, the form fields, the phone number, and the response times are synthetic. Nothing here is
drawn from a real agency system.

**Scope of the claim.** Automated scanning catches roughly a third of WCAG success criteria. It
proves the mechanical things (a missing label, an unlabeled image, a contrast ratio, a skipped
heading) and it proves them on every commit, which manual testing cannot. It does not prove that
the alternative text is *accurate*, that the reading order makes sense, or that the form is usable
with a screen reader. This gate is the floor, not the ceiling: it makes regressions impossible to
merge and leaves the human review to spend its time where a machine is useless.

**The table is the exception, and it is written down as one.** 1.4.10 exempts content that needs
two dimensions for its meaning, and a three-column data table is the standard example. The sideways
scrolling is confined to a wrapper so that the document itself still reflows into 320 CSS pixels,
and the wrapper is a named, focusable region so the scrolling is not mouse-only. Both halves are
asserted: the region appears in the expected tab order, and it has to paint a focus outline like
every other stop. Deleting the `tabindex` fails two tests.

**What the written checks do not prove.** The focus check reads `outline-style` and `outline-width`,
so a page that draws its indicator with a box-shadow ring or a swapped border would need the
matching property asserted instead. It is a floor in another sense too: Chromium supplies a default
ring when a stylesheet says nothing at all, so what the check really catches is an indicator
somebody suppressed, which is the version of this defect that ships. The resize check measures
whether a control is still rendered and still on screen, not whether the text inside it is still
legible, and the reflow check measures the document rather than each region within it.

**Tag set.** The gate runs `wcag2a`, `wcag2aa`, `wcag21a`, `wcag21aa`, plus axe's `best-practice`
bundle. The best-practice rules are included because axe files some structural checks there rather
than mapping them to one success criterion, a skipped heading level being the obvious one, and
those are cheap to satisfy now and expensive to retrofit.

**Retries are zero on purpose.** An accessibility violation is deterministic. Giving the gate a
flake budget is giving it a way to hide a real failure.

**No lockfile is committed.** Versions are pinned exactly in `package.json` and CI runs
`npm install`, so the run is reproducible without carrying a large generated file in a repository
whose point is to be read.

The static server is rooted at this directory rather than `public/` so that the same server also
serves `tests/fixtures/violations.html`. Nothing on the page loads from a CDN or any other
external host; the one image in the fixture is an inline data URI.
