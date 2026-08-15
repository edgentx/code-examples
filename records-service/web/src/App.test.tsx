import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { App } from './App';
import type { RequestView } from './api';

/**
 * The console's behavior against a stubbed service. What is asserted here is the
 * part a browser test cannot see cheaply: that the skeleton gives way to data
 * and says so out loud, that the slide-out panel takes focus and gives it back,
 * that a stale edit produces a visible conflict rather than a silent overwrite,
 * and that a refused command explains itself on the field it was about.
 */

function view(overrides: Partial<RequestView> = {}): RequestView {
  return {
    id: 'PRR-2026-0041',
    requester: 'M. Alvarez',
    description: 'Inspection reports for the Fifth Street bridge, 2025',
    status: 'acknowledged',
    reviewer: 'records.officer.7',
    submitted_at: '2026-03-02T09:00:00Z',
    due_at: '2026-03-12T09:00:00Z',
    released_pages: 0,
    exemption: '',
    package_id: '',
    failure_cause: '',
    version: 3,
    allowed_actions: ['read', 'acknowledge', 'assign_reviewer', 'release', 'deny'],
    ...overrides,
  };
}

/** A JSON response, as the service sends it. */
function ok(body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { 'Content-Type': 'application/json' },
  });
}

/** An RFC 7807 problem, as the service sends it. */
function problem(status: number, code: string, extra: Record<string, unknown> = {}): Response {
  return new Response(
    JSON.stringify({
      type: `/problems/${code}`,
      title: code === 'version_conflict' ? 'The record changed while you were working on it' : 'Refused',
      status,
      code,
      ...extra,
    }),
    { status, headers: { 'Content-Type': 'application/problem+json' } },
  );
}

let fetchMock: ReturnType<typeof vi.fn>;

beforeEach(() => {
  fetchMock = vi.fn();
  vi.stubGlobal('fetch', fetchMock);
});

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

/** The default service: one request, readable and decidable. */
function serveOneRequest(detail: RequestView = view()) {
  fetchMock.mockImplementation((input: RequestInfo | URL) => {
    const url = String(input);
    if (url.endsWith('/api/requests')) {
      return Promise.resolve(ok([detail]));
    }
    return Promise.resolve(ok(detail));
  });
}

describe('loading', () => {
  it('shows a skeleton in place of the table, then announces what arrived', async () => {
    let release: (value: Response) => void = () => undefined;
    fetchMock.mockImplementation(
      () => new Promise<Response>((resolve) => {
        release = resolve;
      }),
    );

    render(<App />);

    // Never a blank region and never a bare spinner: the table is present and
    // marked busy while its rows are on the way.
    const table = screen.getByRole('table', { name: /public records requests/i });
    expect(table).toHaveAttribute('aria-busy', 'true');

    release(ok([view()]));

    await waitFor(() => expect(table).toHaveAttribute('aria-busy', 'false'));
    // The live region is what a screen reader user gets instead of the skeleton.
    expect(screen.getByRole('status')).toHaveTextContent('1 request loaded.');
    expect(screen.getByRole('rowheader', { name: 'PRR-2026-0041' })).toBeInTheDocument();
  });

  it('reports a failure to reach the service instead of showing an empty list', async () => {
    fetchMock.mockResolvedValue(problem(500, 'internal_error'));

    render(<App />);

    expect(await screen.findByRole('alert')).toHaveTextContent(/refused/i);
  });
});

describe('the slide-out panel', () => {
  it('takes focus when it opens and gives it back when it closes', async () => {
    const user = userEvent.setup();
    serveOneRequest();
    render(<App />);

    const open = await screen.findByRole('button', { name: 'Open details for PRR-2026-0041' });
    await user.click(open);

    const panel = await screen.findByRole('dialog', { name: 'PRR-2026-0041' });
    // Focus lands on the panel's own heading, so the first thing announced is
    // what opened rather than whatever control happened to be first.
    await waitFor(() =>
      expect(within(panel).getByRole('heading', { name: 'PRR-2026-0041' })).toHaveFocus(),
    );

    await user.keyboard('{Escape}');

    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
    // The operator is put back where they were, not at the top of the document.
    expect(open).toHaveFocus();
  });

  it('offers the controls the server permitted and no others', async () => {
    const user = userEvent.setup();
    serveOneRequest(view({ status: 'open', allowed_actions: ['read', 'acknowledge'] }));
    render(<App />);

    await user.click(await screen.findByRole('button', { name: /Open details/ }));
    const panel = await screen.findByRole('dialog');

    expect(within(panel).getByRole('button', { name: 'Send receipt notice' })).toBeInTheDocument();
    expect(within(panel).queryByRole('button', { name: 'Release records' })).not.toBeInTheDocument();
    expect(within(panel).queryByRole('button', { name: 'Deny under exemption' })).not.toBeInTheDocument();
  });
});

describe('an edit against a record that has moved on', () => {
  it('shows the conflict, names the current version, and saves nothing', async () => {
    const user = userEvent.setup();
    const stored = view();
    fetchMock.mockImplementation((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (init?.method === 'POST') {
        return Promise.resolve(problem(409, 'version_conflict', { current_version: 5 }));
      }
      if (url.endsWith('/api/requests')) {
        return Promise.resolve(ok([stored]));
      }
      return Promise.resolve(ok(stored));
    });

    render(<App />);
    await user.click(await screen.findByRole('button', { name: /Open details/ }));
    const panel = await screen.findByRole('dialog');

    await user.type(within(panel).getByLabelText('Records officer'), 'records.officer.9');
    await user.click(within(panel).getByRole('button', { name: 'Assign records officer' }));

    const notice = await screen.findByRole('alert');
    expect(notice).toHaveTextContent(/This record changed/i);
    // The version to start again from is on the screen, so the refusal is
    // actionable rather than a dead end.
    expect(notice).toHaveTextContent('version 5');
    // Focus moves to the notice: an announcement a keyboard user has to go
    // looking for is an announcement they will miss.
    await waitFor(() => expect(notice).toHaveFocus());
    expect(within(notice).getByRole('button', { name: /Reload and review/ })).toBeInTheDocument();

    // Nothing on the record changed, and the console is not pretending it did.
    expect(within(panel).getByText('records.officer.7')).toBeInTheDocument();
    expect(screen.getByRole('status')).toHaveTextContent(/Nothing was saved/i);
  });

  it('clears the conflict once the operator reloads and reviews', async () => {
    const user = userEvent.setup();
    let conflictNext = true;
    fetchMock.mockImplementation((input: RequestInfo | URL, init?: RequestInit) => {
      if (init?.method === 'POST') {
        if (conflictNext) {
          conflictNext = false;
          return Promise.resolve(problem(409, 'version_conflict', { current_version: 5 }));
        }
        return Promise.resolve(ok(view({ version: 6, reviewer: 'records.officer.9' })));
      }
      const body = view({ version: 5, reviewer: 'records.officer.4' });
      return Promise.resolve(ok(String(input).endsWith('/api/requests') ? [body] : body));
    });

    render(<App />);
    await user.click(await screen.findByRole('button', { name: /Open details/ }));
    const panel = await screen.findByRole('dialog');
    await user.type(within(panel).getByLabelText('Records officer'), 'records.officer.9');
    await user.click(within(panel).getByRole('button', { name: 'Assign records officer' }));

    await user.click(await screen.findByRole('button', { name: /Reload and review/ }));

    await waitFor(() => expect(screen.queryByText(/This record changed/i)).not.toBeInTheDocument());
    // The operator now sees what the other officer did, which is what they were
    // asked to review.
    expect(await within(panel).findByText('records.officer.4')).toBeInTheDocument();
  });
});

describe('a command the domain refuses', () => {
  it('explains itself on the field it was about', async () => {
    const user = userEvent.setup();
    fetchMock.mockImplementation((input: RequestInfo | URL, init?: RequestInit) => {
      if (init?.method === 'POST') {
        return Promise.resolve(
          problem(422, 'rule_violated', { detail: 'reviewer is already assigned to this request' }),
        );
      }
      const body = view();
      return Promise.resolve(ok(String(input).endsWith('/api/requests') ? [body] : body));
    });

    render(<App />);
    await user.click(await screen.findByRole('button', { name: /Open details/ }));
    const panel = await screen.findByRole('dialog');

    const field = within(panel).getByLabelText('Records officer');
    await user.type(field, 'records.officer.7');
    await user.click(within(panel).getByRole('button', { name: 'Assign records officer' }));

    await waitFor(() => expect(field).toHaveAttribute('aria-invalid', 'true'));
    // The message is attached to the input, so a screen reader reads the reason
    // when focus reaches the field rather than leaving it as loose text nearby.
    expect(field).toHaveAccessibleDescription(/already assigned/i);
    // A rule violation is not a conflict, and must not be shown as one.
    expect(screen.queryByText(/This record changed/i)).not.toBeInTheDocument();
  });
});
