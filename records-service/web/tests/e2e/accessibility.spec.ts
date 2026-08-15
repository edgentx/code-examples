import AxeBuilder from '@axe-core/playwright';
import { expect, test, type Page } from '@playwright/test';

import { assignMeanwhile, fileRequest, openConsole, openPanel, REVIEWER } from './office';

/**
 * The conformance target, stated as axe tags rather than prose. Section 508 (as
 * refreshed in 2017) incorporates WCAG 2.0 level AA by reference; agencies
 * increasingly buy against WCAG 2.1 AA, so the gate asserts the union of both.
 *
 * `best-practice` is inside the gate rather than on a wish list. axe files a
 * handful of structural checks there rather than mapping them to one success
 * criterion -- a skipped heading level is the obvious one, and a skipped heading
 * level is exactly what a screen reader user notices first.
 */
const GATE_TAGS = ['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa', 'best-practice'];

/** The subset of an axe result this file needs. Declared locally to keep the toolchain small. */
type Violation = {
  id: string;
  impact?: string | null;
  help: string;
  description: string;
  helpUrl: string;
  nodes: Array<{ target: unknown[]; html: string }>;
};

/**
 * Render violations as something a reviewer can act on without opening a
 * browser: the rule id, how bad it is, the selector that failed, the offending
 * markup, and the page that explains the fix. A gate that fails with an
 * unreadable dump gets muted rather than fixed.
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

/** Scan whatever is on the screen right now. */
async function scan(page: Page): Promise<Violation[]> {
  const results = await new AxeBuilder({ page }).withTags(GATE_TAGS).analyze();
  return results.violations as unknown as Violation[];
}

/** Assert the current state is clean, reporting anything that is not. */
async function expectClean(page: Page): Promise<void> {
  const violations = await scan(page);
  expect(violations.map((violation) => violation.id), formatViolations(violations)).toEqual([]);
}

/**
 * Four states, not one. A page is usually audited in its empty, happy, first
 * render; the states an operator actually gets stuck in are a panel they have to
 * escape from, a form that refused them, and a conflict they have to resolve.
 */
test.describe('Section 508 and WCAG 2.1 AA gate', () => {
  test('the request list is clean on first load', async ({ page, request }) => {
    await fileRequest(request, 'Inspection reports for the Fifth Street bridge, 2025');
    await openConsole(page);

    await expectClean(page);
  });

  test('the open detail panel is clean', async ({ page, request }) => {
    const { id } = await fileRequest(request, 'Correspondence about the Harbor Road speed study');
    await openConsole(page);
    await openPanel(page, id);

    await expectClean(page);
  });

  test('a refused command is clean, and its message is attached to its field', async ({
    page,
    request,
  }) => {
    const { id } = await fileRequest(request, 'Permit file for 1400 Canal Street');
    await openConsole(page);
    await openPanel(page, id);

    // Reassigning the officer who is already assigned is refused by the domain,
    // which is the error state an operator reaches without trying to.
    const field = page.getByRole('textbox', { name: 'Records officer' });
    await field.fill('records.officer.7');
    await page.getByRole('button', { name: 'Assign records officer' }).click();

    await expect(field).toHaveAttribute('aria-invalid', 'true');
    await expect(page.getByRole('alert')).toBeVisible();
    await expectClean(page);
  });

  test('the conflict state is clean and names the version to review', async ({ page, request }) => {
    const { id, version } = await fileRequest(request, 'Overtime totals for the streets division');
    await openConsole(page);
    await openPanel(page, id);

    // Another officer changes the record while this screen is open.
    await assignMeanwhile(request, id, version, 'records.officer.4');

    await page.getByRole('textbox', { name: 'Records officer' }).fill('records.officer.9');
    await page.getByRole('button', { name: 'Assign records officer' }).click();

    const notice = page.getByRole('alert');
    await expect(notice).toContainText('This record changed');
    await expect(notice).toContainText('version 4');
    await expectClean(page);
  });

  test('the console is readable in dark mode', async ({ page, request }) => {
    // The contrast checks are against computed colors, so a palette that only
    // passes in one theme fails here.
    await page.emulateMedia({ colorScheme: 'dark' });
    const { id } = await fileRequest(request, 'Inspection reports for the Canal Street viaduct');
    await openConsole(page);
    await openPanel(page, id);

    await expectClean(page);
  });
});

test.describe('what an automated scan cannot see', () => {
  test('the panel returns focus to the control that opened it', async ({ page, request }) => {
    const { id } = await fileRequest(request, 'Minutes of the zoning board, March 2026');
    await openConsole(page);

    const opener = page.getByRole('button', { name: `Open details for ${id}` });
    await opener.click();
    await expect(page.getByRole('dialog')).toBeVisible();

    await page.keyboard.press('Escape');

    await expect(page.getByRole('dialog')).toHaveCount(0);
    // An operator left at the top of the document after closing a dialog has to
    // find their place again; axe cannot tell that happened.
    await expect(opener).toBeFocused();
  });

  test('the controls follow the server decision rather than a rule in the console', async ({
    page,
    request,
  }) => {
    const { id } = await fileRequest(request, 'Purchase orders for the parks division, 2025');
    await openConsole(page);
    await openPanel(page, id);
    await expect(page.getByRole('button', { name: 'Release records' })).toBeVisible();

    // The same request, seen by a clerk. Nothing in the console changed; the
    // authorization model answered differently.
    await page.keyboard.press('Escape');
    await page.getByLabel('Signed in as').selectOption('c.hall');
    await expect(page.getByRole('table')).toHaveAttribute('aria-busy', 'false');
    await openPanel(page, id);

    await expect(page.getByRole('button', { name: 'Release records' })).toHaveCount(0);
    await expect(page.getByRole('button', { name: 'Deny under exemption' })).toHaveCount(0);
    await expect(page.getByText(/records officer action/i)).toBeVisible();
    void REVIEWER;
  });
});
