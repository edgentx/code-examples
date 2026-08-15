import { expect, type APIRequestContext, type Page } from '@playwright/test';

/**
 * Shared fixtures for the browser specs.
 *
 * Every spec creates the request it works on, through the same API the console
 * uses, so specs do not depend on the seeded data or on each other. The identity
 * header is what an authorization sidecar stamps in a deployment; here the spec
 * sends it, the same way the console does.
 */

export const REVIEWER = 'r.okafor';
export const CLERK = 'c.hall';

/** Headers for one command sent outside the browser. */
function headers(operator: string, version?: number): Record<string, string> {
  const sent: Record<string, string> = {
    'X-User-Id': operator,
    'Idempotency-Key': `spec-${Math.random().toString(16).slice(2)}`,
    'Content-Type': 'application/json',
  };
  if (version !== undefined) {
    sent['If-Match'] = `"${version}"`;
  }
  return sent;
}

/**
 * fileRequest creates a request and works it up to "acknowledged, assigned",
 * which is the state most of the console's controls are about. It returns the
 * identifier and the version the record is now at.
 */
export async function fileRequest(
  api: APIRequestContext,
  description: string,
): Promise<{ id: string; version: number }> {
  const filed = await api.post('/api/requests', {
    headers: headers(CLERK),
    data: { requester: 'M. Alvarez', description },
  });
  expect(filed.status(), await filed.text()).toBe(201);
  const { id } = (await filed.json()) as { id: string };

  const acknowledged = await api.post(`/api/requests/${id}/acknowledgment`, {
    headers: headers(CLERK, 1),
    data: {},
  });
  expect(acknowledged.status(), await acknowledged.text()).toBe(200);

  const assigned = await api.post(`/api/requests/${id}/reviewer`, {
    headers: headers(REVIEWER, 2),
    data: { reviewer: 'records.officer.7' },
  });
  expect(assigned.status(), await assigned.text()).toBe(200);

  return { id, version: 3 };
}

/** assignMeanwhile is the other officer, changing the record while a screen is open. */
export async function assignMeanwhile(
  api: APIRequestContext,
  id: string,
  version: number,
  reviewer: string,
): Promise<void> {
  const changed = await api.post(`/api/requests/${id}/reviewer`, {
    headers: headers(REVIEWER, version),
    data: { reviewer },
  });
  expect(changed.status(), await changed.text()).toBe(200);
}

/** openConsole loads the console and waits for the request list to arrive. */
export async function openConsole(page: Page): Promise<void> {
  await page.goto('/');
  await expect(page.getByRole('table')).toHaveAttribute('aria-busy', 'false');
}

/** openPanel opens the slide-out detail panel for one request. */
export async function openPanel(page: Page, id: string): Promise<void> {
  await page.getByRole('button', { name: `Open details for ${id}` }).click();
  await expect(page.getByRole('dialog', { name: id })).toBeVisible();
  // The panel's fields are rendered once the request has been read; waiting for
  // the decision section keeps a spec from racing the skeleton.
  await expect(page.getByRole('heading', { name: 'Decide this request' })).toBeVisible();
}
