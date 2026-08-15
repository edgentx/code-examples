import { expect, test, type Page } from '@playwright/test';

import { assignMeanwhile, fileRequest, openConsole } from './office';

/**
 * The whole flow, with no pointer at any point: list, open the panel, edit,
 * submit, hit a conflict, recover from it, and close.
 *
 * This is the spec that catches what a scanner cannot. axe can tell that a
 * control is focusable; it cannot tell whether the operator can reach it in an
 * order that matches what they read, whether the panel lets go of focus, or
 * whether a refusal leaves a keyboard user with anywhere to go. Every
 * interaction below is a key press.
 */

/** The accessible name of whatever currently has focus. */
async function focused(page: Page): Promise<string> {
  return page.evaluate(() => {
    const active = document.activeElement as HTMLElement | null;
    if (!active || active === document.body) {
      return '<document>';
    }
    const label =
      active.getAttribute('aria-label') ??
      (active.id ? document.querySelector(`label[for="${active.id}"]`)?.textContent : null) ??
      active.textContent;
    return `${active.tagName.toLowerCase()}:${(label ?? '').trim()}`;
  });
}

/**
 * tabTo presses Tab until focus lands on the named control, and fails with the
 * path it walked if it never does. The path is the useful part of the failure: a
 * control that is unreachable and a control that is reachable in the wrong order
 * are different defects.
 */
async function tabTo(page: Page, name: string, limit = 25): Promise<string[]> {
  const path: string[] = [];
  for (let step = 0; step < limit; step += 1) {
    await page.keyboard.press('Tab');
    const landed = await focused(page);
    path.push(landed);
    if (landed.includes(name)) {
      return path;
    }
  }
  throw new Error(`Tab never reached ${name}. Focus went: ${path.join(' -> ')}`);
}

test('an operator can work a request start to finish without a pointer', async ({
  page,
  request,
}) => {
  const { id, version } = await fileRequest(request, 'Inspection reports for the Ninth Street pier');
  await openConsole(page);

  // The first stop is the skip link, so a keyboard user reaches the content
  // without walking the whole masthead.
  await page.keyboard.press('Tab');
  expect(await focused(page)).toContain('Skip to the request list');

  // From there, in reading order, to the control that opens this request.
  await tabTo(page, `Open details for ${id}`);
  await page.keyboard.press('Enter');

  const panel = page.getByRole('dialog', { name: id });
  await expect(panel).toBeVisible();
  // Focus is inside the panel, on its heading, so the first thing announced is
  // what opened.
  expect(await focused(page)).toContain(id);

  // Tab forward from the heading reaches the panel's own controls.
  await tabTo(page, 'Close request details');
  await tabTo(page, 'Records officer');
  await page.keyboard.type('records.officer.9');

  // Meanwhile, another officer changes the record.
  await assignMeanwhile(request, id, version, 'records.officer.4');

  await tabTo(page, 'Assign records officer');
  await page.keyboard.press('Enter');

  // The refusal is announced and takes focus, so the operator is standing on
  // the thing that explains it rather than having to go looking.
  const notice = page.getByRole('alert');
  await expect(notice).toContainText('This record changed');
  expect(await focused(page)).toContain('This record changed');

  // The control that resolves it is the next stop.
  await tabTo(page, 'Reload and review the current version');
  await page.keyboard.press('Enter');

  await expect(page.getByText('records.officer.4')).toBeVisible();
  await expect(page.getByText('This record changed')).toHaveCount(0);

  // Decide again against the version now on the screen.
  await tabTo(page, 'Records officer');
  await page.keyboard.type('records.officer.9');
  await tabTo(page, 'Assign records officer');
  await page.keyboard.press('Enter');

  await expect(panel.getByText('records.officer.9')).toBeVisible();

  // Escape closes, and focus comes back to where it started.
  await page.keyboard.press('Escape');
  await expect(page.getByRole('dialog')).toHaveCount(0);
  expect(await focused(page)).toContain(`Open details for ${id}`);
});

test('focus cannot leave the panel while it is open', async ({ page, request }) => {
  const { id } = await fileRequest(request, 'Correspondence with the harbor commission');
  await openConsole(page);
  await tabTo(page, `Open details for ${id}`);
  await page.keyboard.press('Enter');
  await expect(page.getByRole('dialog')).toBeVisible();

  // Twenty forward tabs is more than the panel holds. If focus escaped, one of
  // them would land on the masthead or a table row behind the dialog.
  const visited: string[] = [];
  for (let step = 0; step < 20; step += 1) {
    await page.keyboard.press('Tab');
    visited.push(await focused(page));
  }
  expect(visited.some((name) => name.includes('Signed in as'))).toBe(false);
  expect(visited.some((name) => name.includes('Open details for'))).toBe(false);
  expect(visited.some((name) => name.includes('Skip to the request list'))).toBe(false);

  // And backward, which is the half that is usually left out: Shift+Tab from
  // the first control has to wrap to the last rather than leave.
  const backward: string[] = [];
  for (let step = 0; step < 20; step += 1) {
    await page.keyboard.press('Shift+Tab');
    backward.push(await focused(page));
  }
  expect(backward.some((name) => name.includes('Signed in as'))).toBe(false);
  expect(backward.some((name) => name.includes('Open details for'))).toBe(false);
});
