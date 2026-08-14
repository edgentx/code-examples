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

/** The page under test, and the deliberately broken copy that proves the gate bites. */
const PAGE = '/public/index.html';
const FIXTURE = '/tests/fixtures/violations.html';

/** The rule ids planted in the fixture, one per class of defect a reviewer would recognize. */
const PLANTED_RULES = ['label', 'image-alt', 'color-contrast', 'heading-order'];

/**
 * The keyboard path through the page, in order. Anything that can be reached with a mouse must be
 * reachable with Tab, in an order that matches the visual one, and the sequence must end on the
 * control that submits the form.
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
];

/** The subset of an axe result this file needs. Declared locally to keep the toolchain small. */
type Violation = {
  id: string;
  impact?: string | null;
  help: string;
  description: string;
  helpUrl: string;
  nodes: Array<{ target: unknown[]; html: string; failureSummary?: string }>;
};

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
 * Press Tab repeatedly from the top of the document, recording where focus lands, and stop once
 * it reaches the submit button. Returns the path taken so the test can assert on the whole order,
 * not merely on arrival.
 */
async function tabPath(page: Page, stopAtId: string, maxSteps = 30): Promise<string[]> {
  const path: string[] = [];

  for (let step = 0; step < maxSteps; step += 1) {
    await page.keyboard.press('Tab');
    const landed = await page.evaluate(() => {
      const active = document.activeElement as HTMLElement | null;
      if (!active || active === document.body) {
        return '<document>';
      }
      if (active.id) {
        return `#${active.id}`;
      }
      return `${active.tagName.toLowerCase()}[href=${active.getAttribute('href') ?? ''}]`;
    });

    path.push(landed);
    if (landed === `#${stopAtId}`) {
      break;
    }
  }

  return path;
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
});

test.describe('what an automated scan cannot see', () => {
  test('the keyboard reaches every control and ends on submit', async ({ page }) => {
    // axe can tell that a control is focusable. It cannot tell whether the order a keyboard user
    // walks through actually matches the order they read, or whether the submit button is
    // reachable at all without a mouse. That is a human judgement encoded as an assertion.
    await page.goto(PAGE);
    const path = await tabPath(page, 'submit-request');

    expect(path).toEqual(EXPECTED_TAB_ORDER);
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
});
