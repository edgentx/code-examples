import { test, expect, type Page } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';

/**
 * The conformance target, stated as axe tags rather than prose. Section 508 (as refreshed in
 * 2017) incorporates WCAG 2.0 level AA by reference; agencies increasingly buy against WCAG 2.1
 * AA, so the gate asserts the union of both. Adding a tag here tightens the gate for every page.
 */
const WCAG_AA = ['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa'];

/**
 * What the gate actually runs. axe files a handful of structural checks as `best-practice` rather
 * than mapping them to one success criterion - a skipped heading level is the obvious one, and a
 * skipped heading level is exactly what a screen reader user notices first. They are cheap to
 * satisfy and expensive to retrofit, so they are inside the gate rather than on a wish list.
 */
const GATE_TAGS = [...WCAG_AA, 'best-practice'];

/** The page under test, and the deliberately broken copies that prove the gate bites. */
const PAGE = '/public/index.html';
const FIXTURE = '/tests/fixtures/violations.html';
const PRESENTATION_FIXTURE = '/tests/fixtures/presentation-defects.html';

/** The rule ids planted in the fixture, one per class of defect a reviewer would recognize. */
const PLANTED_RULES = ['label', 'image-alt', 'color-contrast', 'heading-order'];

/**
 * The keyboard path through the page, in order. Anything that can be reached with a mouse must be
 * reachable with Tab, in an order that matches the visual one, and every stop must show where the
 * keyboard is.
 */
const EXPECTED_TAB_ORDER = [
  'a[href=#main-content]', // skip link
  'a[href=#main-content]', // primary navigation
  'a[href=#deadlines]',
  'a[href=#help]',
  '#full-name',
  '#email',
  '#case-number',
  '#records',
  '#delivery-email', // a radio group is one tab stop
  '#fee-waiver',
  '#submit-request',
  '#response-times', // the table scrolls sideways, so the keyboard has to be able to scroll it
];

/**
 * The traversal is bounded so that a focus trap fails the gate in a second rather than hanging it.
 * Twenty-five stops is comfortably more than this page has and small enough to stay honest.
 */
const MAX_TAB_STOPS = 25;

/**
 * WCAG 1.4.10 states the reflow target as a 320 CSS pixel viewport, which is the same thing as a
 * 1280px page zoomed to 400%. Content has to reflow into it in one dimension: scrolling down is
 * expected, scrolling down *and* across is the failure.
 */
const REFLOW_VIEWPORT = { width: 320, height: 640 };

/**
 * WCAG 1.4.4 asks for text at 200% of its normal size with no loss of content or function. Setting
 * the root font size is the honest way to test it: `page.setViewportSize` shrinks the window, which
 * is a different criterion, and browser page zoom scales everything including the pixel grid.
 */
const ENLARGED_ROOT_FONT = '200%';

/** The subset of an axe result this file needs. Declared locally to keep the toolchain small. */
type Violation = {
  id: string;
  impact?: string | null;
  help: string;
  description: string;
  helpUrl: string;
  nodes: Array<{ target: unknown[]; html: string; failureSummary?: string }>;
};

/** One stop on the keyboard path: where focus landed, and the outline the browser computed there. */
type FocusStop = { at: string; outlineStyle: string; outlineWidth: string };

/** A control that stopped being usable once the text was enlarged. */
type LostControl = {
  control: string;
  label: string;
  reason: string;
  left: number;
  right: number;
  viewport: number;
};

/** What the page measured at 320 CSS pixels: the content width against the window it has to fit. */
type ReflowMeasurement = { scrollWidth: number; innerWidth: number };

/**
 * Render violations as something a reviewer can act on without opening a browser: the rule id,
 * how bad it is, the selector that failed, the offending markup, and the page that explains the
 * fix. A gate that fails with an unreadable dump gets muted rather than fixed.
 */
function formatViolations(violations: Violation[]): string {
  if (violations.length === 0) {
    return 'no violations';
  }

  const blocks = violations.map((violation, index) => {
    const nodes = violation.nodes.map((node) => {
      const selector = node.target.map((part) => String(part)).join(' ');
      const markup = node.html.replace(/\s+/g, ' ').trim();
      return [
        `     selector: ${selector}`,
        `     markup:   ${markup.length > 160 ? `${markup.slice(0, 160)}...` : markup}`,
      ].join('\n');
    });

    return [
      `  ${index + 1}. ${violation.id} (${violation.impact ?? 'unknown impact'}) - ${violation.help}`,
      `     why:      ${violation.description}`,
      `     fix:      ${violation.helpUrl}`,
      ...nodes,
    ].join('\n');
  });

  return [
    `${violations.length} accessibility violation(s): ${violations.map((v) => v.id).join(', ')}`,
    ...blocks,
  ].join('\n');
}

/** Scan a URL against the conformance target and hand back the violations. */
async function scan(page: Page, url: string): Promise<Violation[]> {
  await page.goto(url);
  const results = await new AxeBuilder({ page }).withTags(GATE_TAGS).analyze();
  return results.violations as unknown as Violation[];
}

/**
 * Press Tab from the top of the document and record every stop: where focus landed, and the
 * outline the browser computed for it while it was there. The walk ends when focus leaves the page
 * - Chromium hands it to the browser chrome, which reads here as `<document>` - or at
 * MAX_TAB_STOPS, whichever comes first.
 *
 * One walk answers both questions the page has to answer. Reachability is the order of the stops;
 * visibility is the outline recorded at each of them. Asking them separately would mean walking
 * the page twice and letting the two walks drift apart.
 */
async function walkTabStops(page: Page, url: string): Promise<FocusStop[]> {
  await page.goto(url);
  const stops: FocusStop[] = [];

  for (let step = 0; step < MAX_TAB_STOPS; step += 1) {
    await page.keyboard.press('Tab');
    const stop = await page.evaluate(() => {
      const active = document.activeElement as HTMLElement | null;
      if (!active || active === document.body) {
        return { at: '<document>', outlineStyle: 'none', outlineWidth: '0px' };
      }

      const at = active.id
        ? `#${active.id}`
        : `${active.tagName.toLowerCase()}[href=${active.getAttribute('href') ?? ''}]`;
      const computed = getComputedStyle(active);

      return { at, outlineStyle: computed.outlineStyle, outlineWidth: computed.outlineWidth };
    });

    if (stop.at === '<document>') {
      break;
    }
    stops.push(stop);
  }

  return stops;
}

/**
 * Whether the browser is going to paint anything at this stop. `outline: none` is how the
 * indicator usually disappears and a zero width is the other way, so both are checked.
 *
 * This is a floor rather than a full 2.4.7 audit. Chromium supplies a default ring when a
 * stylesheet says nothing at all, so what the check catches is an outline somebody deliberately
 * suppressed, which is the version of this defect that actually ships. A page that draws its
 * indicator some other way - a box-shadow ring, a swapped border - would need the matching
 * property asserted here. This one defines a single outline once, in styles.css, so one assertion
 * covers every stop.
 */
function hasVisibleOutline(stop: FocusStop): boolean {
  return stop.outlineStyle !== 'none' && Number.parseFloat(stop.outlineWidth) > 0;
}

/** Name the stops that show nothing, and say what was computed there instead. */
function formatStops(stops: FocusStop[]): string {
  if (stops.length === 0) {
    return 'every keyboard stop has a visible focus indicator';
  }

  return [
    `${stops.length} keyboard stop(s) with no visible focus indicator:`,
    ...stops.map(
      (stop, index) =>
        `  ${index + 1}. ${stop.at} - outline-style: ${stop.outlineStyle}, outline-width: ${stop.outlineWidth}`,
    ),
  ].join('\n');
}

/**
 * Load a page into a 320 CSS pixel viewport and measure what it does with it. A page that reflowed
 * has a content width equal to the window; anything wider is a horizontal scrollbar, and a
 * horizontal scrollbar on top of the vertical one is the two-dimensional scrolling 1.4.10 forbids.
 */
async function measureReflow(page: Page, url: string): Promise<ReflowMeasurement> {
  await page.setViewportSize(REFLOW_VIEWPORT);
  await page.goto(url);

  return page.evaluate(() => {
    const scroller = document.scrollingElement ?? document.documentElement;
    return { scrollWidth: scroller.scrollWidth, innerWidth: window.innerWidth };
  });
}

/**
 * Enlarge the root font size and report every labeled control that did not survive it: one that
 * stopped being rendered, or one that was carried outside the horizontal bounds of the viewport
 * where no amount of scrolling down will bring it back. Returns the root size the browser actually
 * applied as well, so a test cannot pass because the enlargement silently failed to take.
 */
async function controlsLostAtRootFontSize(
  page: Page,
  url: string,
  rootFontSize: string,
): Promise<{ rootFontSize: string; lost: LostControl[] }> {
  await page.goto(url);
  await page.addStyleTag({ content: `html { font-size: ${rootFontSize}; }` });

  return page.evaluate(() => {
    const viewport = window.innerWidth;
    const lost: LostControl[] = [];
    const controls = Array.from(
      document.querySelectorAll<
        HTMLInputElement | HTMLSelectElement | HTMLTextAreaElement | HTMLButtonElement
      >('input:not([type="hidden"]), select, textarea, button'),
    );

    for (const control of controls) {
      const labelled = control.labels?.[0]?.textContent ?? control.textContent ?? '';
      const label = labelled.replace(/\s+/g, ' ').trim();
      if (label === '') {
        continue; // An unlabeled control is a different failure, and axe already reports it.
      }

      const computed = getComputedStyle(control);
      const box = control.getBoundingClientRect();
      const unrendered =
        computed.display === 'none' ||
        computed.visibility === 'hidden' ||
        box.width === 0 ||
        box.height === 0;
      // Half a pixel of tolerance: sub-pixel layout should not be reported as lost content.
      const offscreen = box.left < -0.5 || box.right > viewport + 0.5;

      if (!unrendered && !offscreen) {
        continue;
      }

      lost.push({
        control: control.id ? `#${control.id}` : control.tagName.toLowerCase(),
        label,
        reason: unrendered ? 'not rendered at all' : 'carried outside the viewport',
        left: Math.round(box.left),
        right: Math.round(box.right),
        viewport,
      });
    }

    return {
      rootFontSize: getComputedStyle(document.documentElement).fontSize,
      lost,
    };
  });
}

/** Name what was lost, where it went, and which control the reader can no longer reach. */
function formatLostControls(lost: LostControl[]): string {
  if (lost.length === 0) {
    return 'every labeled control is still on screen';
  }

  return [
    `${lost.length} control(s) lost at a ${ENLARGED_ROOT_FONT} root font size:`,
    ...lost.map((control, index) =>
      [
        `  ${index + 1}. ${control.control} - ${control.reason}`,
        `     label:    ${control.label}`,
        `     bounds:   ${control.left}px to ${control.right}px, in a ${control.viewport}px viewport`,
      ].join('\n'),
    ),
  ].join('\n');
}

test.describe('WCAG 2.1 AA gate', () => {
  test('the request form has no violations on first load', async ({ page }) => {
    // To watch the gate fail by hand, make exactly this one-line edit and rerun `npm test`:
    //     const violations = await scan(page, FIXTURE);
    const violations = await scan(page, PAGE);

    expect(violations.map((violation) => violation.id), formatViolations(violations)).toEqual([]);
  });

  test('the error state has no violations either', async ({ page }) => {
    // Pages are usually audited in their empty, happy state. The state a user actually gets stuck
    // in is the one after a failed submission, so it is scanned too.
    await page.goto(PAGE);
    await page.locator('#submit-request').click();
    await expect(page.locator('#error-summary')).toBeVisible();

    const results = await new AxeBuilder({ page }).withTags(GATE_TAGS).analyze();
    const violations = results.violations as unknown as Violation[];

    expect(violations.map((violation) => violation.id), formatViolations(violations)).toEqual([]);
  });

  test('the page scans clean under a dark color scheme', async ({ page }) => {
    // styles.css carries a second palette behind prefers-color-scheme, and a contrast ratio
    // measured on white says nothing about the pair that replaces it. axe reads whichever palette
    // the browser actually applied, so emulating the preference is the whole of the test: without
    // it, half the colors this page can render are never measured by anything.
    await page.emulateMedia({ colorScheme: 'dark' });
    const violations = await scan(page, PAGE);

    expect(violations.map((violation) => violation.id), formatViolations(violations)).toEqual([]);
  });

  test('the page scans clean when the reader prefers reduced motion', async ({ page }) => {
    // The preference selects a different branch of the stylesheet, and a branch nothing loads is a
    // branch nothing checks. The scan is cheap, and it is the only place a preference-specific
    // override that happened to break a color pair would ever be caught.
    await page.emulateMedia({ reducedMotion: 'reduce' });
    const violations = await scan(page, PAGE);

    expect(violations.map((violation) => violation.id), formatViolations(violations)).toEqual([]);
  });
});

test.describe('what an automated scan cannot see', () => {
  test('the keyboard reaches every control in the order the page is read', async ({ page }) => {
    // axe can tell that a control is focusable. It cannot tell whether the order a keyboard user
    // walks through actually matches the order they read, or whether the submit button is
    // reachable at all without a mouse. That is a human judgement encoded as an assertion.
    const stops = await walkTabStops(page, PAGE);

    expect(stops.map((stop) => stop.at)).toEqual(EXPECTED_TAB_ORDER);
  });

  test('every keyboard stop shows where the keyboard is', async ({ page }) => {
    // Reachability is not usability. A control that takes focus while painting nothing leaves a
    // sighted keyboard user pressing Tab and guessing, and no scanner reports it: the markup is
    // perfect and the accessible name is correct. The only way to know is to walk the page and
    // read the outline the browser computed at each stop, which is what the 2.4.7 review does by
    // hand and what this does on every commit.
    const stops = await walkTabStops(page, PAGE);
    const unmarked = stops.filter((stop) => !hasVisibleOutline(stop));

    expect(stops, 'the walk recorded no stops at all').toHaveLength(EXPECTED_TAB_ORDER.length);
    expect(unmarked.map((stop) => stop.at), formatStops(unmarked)).toEqual([]);
  });

  test('a failed submission moves focus to the error summary', async ({ page }) => {
    // Announcing an error is not enough: a keyboard or screen reader user left at the bottom of
    // the form has to hunt for what went wrong. Focus must move to the summary.
    await page.goto(PAGE);
    await page.locator('#submit-request').click();

    const summary = page.locator('#error-summary');
    await expect(summary).toBeVisible();
    await expect(summary).toBeFocused();

    // One entry per unmet requirement, each linking to the control that failed.
    await expect(summary.locator('li a')).toHaveCount(4);
    await summary.locator('li a').first().click();
    await expect(page.locator('#full-name')).toBeFocused();
    await expect(page.locator('#full-name')).toHaveAttribute('aria-invalid', 'true');
  });

  test('the page reflows into 320 CSS pixels', async ({ page }) => {
    // 1.4.10. At 320 CSS pixels, which is this page at 400% zoom, the content has to come down to
    // one column. Scrolling down is expected; having to scroll across as well means every line of
    // text needs a second gesture to finish reading, which is what the criterion is about.
    const { scrollWidth, innerWidth } = await measureReflow(page, PAGE);

    expect(
      scrollWidth,
      `content is ${scrollWidth}px wide in a ${innerWidth}px viewport, so the page scrolls in two directions`,
    ).toEqual(innerWidth);
  });

  test('nothing is lost when the root font size is doubled', async ({ page }) => {
    // 1.4.4. Doubling the root font size is what a reader who has set a larger default type size
    // gets, and it is the point where fixed widths and rows that cannot wrap push a control off
    // the side of the screen. The control is still in the DOM and still labeled, so the scan is
    // still clean; it is simply somewhere the reader cannot get to.
    const { rootFontSize, lost } = await controlsLostAtRootFontSize(page, PAGE, ENLARGED_ROOT_FONT);

    expect(rootFontSize, 'the root font size was not actually enlarged').toEqual('32px');
    expect(lost, formatLostControls(lost)).toEqual([]);
  });
});

test.describe('proof the gate bites', () => {
  test('the planted defects in the fixture are all detected', async ({ page }) => {
    // This is the control sample. It asserts the scan reports violations rather than that it
    // reports none, so it stays green in CI while still failing loudly if the gate ever stops
    // working - an empty result set from a misconfigured AxeBuilder would pass every other test
    // in this file and fail only this one.
    const violations = await scan(page, FIXTURE);
    const detected = violations.map((violation) => violation.id);

    expect(detected.length, 'the broken fixture must not scan clean').toBeGreaterThan(0);
    for (const rule of PLANTED_RULES) {
      expect(detected, `expected axe to report "${rule}" on the broken fixture`).toContain(rule);
    }
  });

  test('the scanner reports nothing at all on the presentation fixture', async ({ page }) => {
    // The second fixture is the argument for the three checks below it, stated as a test. Every
    // defect planted in that file is invisible to axe: the markup is valid, the controls are
    // labeled, the headings are in order and the contrast is fine. If this scan ever reports
    // something, the fixture has drifted into testing the scanner instead of the checks.
    const violations = await scan(page, PRESENTATION_FIXTURE);

    expect(violations.map((violation) => violation.id), formatViolations(violations)).toEqual([]);
  });

  test('the focus check reports the suppressed outline', async ({ page }) => {
    // Planted defect 1: `#case-number:focus { outline: none }` with no replacement.
    const stops = await walkTabStops(page, PRESENTATION_FIXTURE);
    const unmarked = stops.filter((stop) => !hasVisibleOutline(stop));

    expect(unmarked.map((stop) => stop.at)).toEqual(['#case-number']);
  });

  test('the reflow check reports the fixed-width banner', async ({ page }) => {
    // Planted defect 2: a paragraph 900px wide, in a viewport 320px wide.
    const { scrollWidth, innerWidth } = await measureReflow(page, PRESENTATION_FIXTURE);

    expect(scrollWidth, 'the fixed-width banner must force a horizontal scrollbar').toBeGreaterThan(
      innerWidth,
    );
  });

  test('the resize check reports the control carried off screen', async ({ page }) => {
    // Planted defect 3: a row that cannot wrap, holding a control sized in rem. Both halves are
    // asserted, because a check that reports the control at every size would report it here too
    // and prove nothing: the fixture is whole at the default size and broken at twice it.
    const normal = await controlsLostAtRootFontSize(page, PRESENTATION_FIXTURE, '100%');
    expect(normal.lost, 'the fixture is meant to look fine until the text is enlarged').toEqual([]);

    const enlarged = await controlsLostAtRootFontSize(
      page,
      PRESENTATION_FIXTURE,
      ENLARGED_ROOT_FONT,
    );

    expect(enlarged.lost.map((control) => control.control)).toEqual(['#fee-reference']);
  });
});
