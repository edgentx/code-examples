import { describe, expect, it } from 'vitest';

import type { RequestView } from './api';
import { availableActions, present, whyNoActions } from './derive';

/**
 * These are the tests for the console's whole state model. If the derivation is
 * right, there is no other place a screen can disagree with the record, because
 * there is no other place a screen decides anything.
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

describe('presenting the one status field', () => {
  // Every visual and behavioral property of a request is a function of this
  // single field. The table is the whole mapping: nothing else in the console
  // is allowed to compute "closed" or "in flight" for itself.
  const cases = [
    { status: 'open', label: 'Open', closed: false, awaiting: false, compensated: false },
    { status: 'acknowledged', label: 'Acknowledged', closed: false, awaiting: false, compensated: false },
    { status: 'release_pending', label: 'Release pending', closed: false, awaiting: true, compensated: false },
    { status: 'release_failed', label: 'Release withdrawn', closed: false, awaiting: false, compensated: true },
    { status: 'fulfilled', label: 'Fulfilled', closed: true, awaiting: false, compensated: false },
    { status: 'denied', label: 'Denied', closed: true, awaiting: false, compensated: false },
  ];

  it.each(cases)('$status is presented as $label', (expected) => {
    const state = present(expected.status);

    expect(state.label).toBe(expected.label);
    expect(state.closed).toBe(expected.closed);
    expect(state.awaitingDelivery).toBe(expected.awaiting);
    expect(state.compensated).toBe(expected.compensated);
    expect(state.meaning.length).toBeGreaterThan(0);
  });

  it('shows an unrecognized status as itself rather than guessing', () => {
    // A console that renders a state it does not know as "open" is telling the
    // operator something that is not true.
    const state = present('under_appeal');

    expect(state.label).toBe('under_appeal');
    expect(state.closed).toBe(false);
    expect(state.meaning).toMatch(/does not recognize/i);
  });
});

describe('offering only the controls the server would accept', () => {
  it('offers a records officer the decisions their relationship permits', () => {
    expect(availableActions(view()).map((action) => action.name)).toEqual([
      'assign_reviewer',
      'release',
      'deny',
    ]);
  });

  it('offers a clerk the receipt notice and nothing that decides the answer', () => {
    // This is the deny case, and it comes from the server: the clerk's
    // allowed_actions do not include release or deny, so those controls are not
    // rendered. The console holds no role check of its own.
    const clerk = view({ status: 'open', allowed_actions: ['read', 'acknowledge'] });

    expect(availableActions(clerk).map((action) => action.name)).toEqual(['acknowledge']);
  });

  it('offers a clerk nothing to decide on a request that is already acknowledged', () => {
    const clerk = view({ allowed_actions: ['read', 'acknowledge'] });

    expect(availableActions(clerk)).toEqual([]);
    expect(whyNoActions(clerk)).toMatch(/records officer action/i);
  });

  it('offers a reader nothing at all', () => {
    const requester = view({ allowed_actions: ['read'] });

    expect(availableActions(requester)).toEqual([]);
  });

  it('offers the receipt notice only while the request is open', () => {
    expect(availableActions(view({ status: 'open' })).map((a) => a.name)).toEqual(['acknowledge']);
    expect(availableActions(view({ status: 'acknowledged' })).map((a) => a.name)).not.toContain(
      'acknowledge',
    );
  });

  it('withholds release and denial until an officer is accountable', () => {
    const unassigned = view({ reviewer: '' });

    expect(availableActions(unassigned).map((action) => action.name)).toEqual(['assign_reviewer']);
    expect(whyNoActions(view({ reviewer: '', allowed_actions: ['read'] }))).toMatch(/read access/i);
  });

  it('offers nothing while a release is out for delivery, and says why', () => {
    const inFlight = view({ status: 'release_pending' });

    expect(availableActions(inFlight)).toEqual([]);
    expect(whyNoActions(inFlight)).toMatch(/out for delivery/i);
  });

  it('offers the decisions again after a release is withdrawn', () => {
    // The compensation puts the request back in front of the officer, so the
    // controls come back. That is the whole visible consequence of compensating
    // rather than closing the request in a failed state.
    const compensated = view({ status: 'release_failed', failure_cause: 'still under legal hold' });

    expect(availableActions(compensated).map((action) => action.name)).toEqual([
      'assign_reviewer',
      'release',
      'deny',
    ]);
  });

  it('offers nothing on a closed request, and says why', () => {
    for (const status of ['fulfilled', 'denied']) {
      const closed = view({ status });

      expect(availableActions(closed)).toEqual([]);
      expect(whyNoActions(closed)).toMatch(/closed/i);
    }
  });
});
