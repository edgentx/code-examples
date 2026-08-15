/**
 * Everything the console shows about where a request stands is derived here,
 * from two server-supplied fields and nothing else:
 *
 *   status          -- the one lifecycle field on the aggregate
 *   allowed_actions -- what the authorization model says this caller may do
 *
 * There is no `isClosed` in component state, no `isBusy` flag set beside a
 * fetch, and no role check in the console. Those are the two habits that put a
 * screen into a state the record was never in: a second field that describes the
 * same thing eventually disagrees with the first, and a rule copied into the
 * client eventually disagrees with the server. Deriving both from one place is
 * how the screen stays a rendering of the record rather than a second opinion
 * about it.
 */

import type { RequestView } from './api';

/** The lifecycle states the service can report. */
export type Status =
  | 'open'
  | 'acknowledged'
  | 'release_pending'
  | 'release_failed'
  | 'fulfilled'
  | 'denied';

/** How a status is presented, and what it implies for the controls. */
export interface StatusPresentation {
  /** The label an operator reads. */
  label: string;
  /** A sentence saying what the state means, used as the badge's description. */
  meaning: string;
  /**
   * The visual tone. It is never the only carrier of meaning: every badge shows
   * its label as text, so the state is legible without color vision.
   */
  tone: 'neutral' | 'active' | 'waiting' | 'attention' | 'done';
  /** True once the request is answered and accepts nothing further. */
  closed: boolean;
  /** True while a release is out for delivery and the outcome is not known. */
  awaitingDelivery: boolean;
  /** True when a release was withdrawn and the officer has to decide again. */
  compensated: boolean;
}

const PRESENTATION: Record<Status, StatusPresentation> = {
  open: {
    label: 'Open',
    meaning: 'Received. The statutory receipt notice has not gone out yet.',
    tone: 'neutral',
    closed: false,
    awaitingDelivery: false,
    compensated: false,
  },
  acknowledged: {
    label: 'Acknowledged',
    meaning: 'The requester has the receipt notice. The request is being worked.',
    tone: 'active',
    closed: false,
    awaitingDelivery: false,
    compensated: false,
  },
  release_pending: {
    label: 'Release pending',
    meaning: 'Records were released and the package is out for delivery.',
    tone: 'waiting',
    closed: false,
    awaitingDelivery: true,
    compensated: false,
  },
  release_failed: {
    label: 'Release withdrawn',
    meaning: 'The package could not be delivered, so the release was withdrawn.',
    tone: 'attention',
    closed: false,
    awaitingDelivery: false,
    compensated: true,
  },
  fulfilled: {
    label: 'Fulfilled',
    meaning: 'The release package reached the requester. The request is closed.',
    tone: 'done',
    closed: true,
    awaitingDelivery: false,
    compensated: false,
  },
  denied: {
    label: 'Denied',
    meaning: 'Records were withheld under a cited exemption. The request is closed.',
    tone: 'done',
    closed: true,
    awaitingDelivery: false,
    compensated: false,
  },
};

/**
 * present maps a status to how it is shown. An unrecognized status is presented
 * as itself rather than guessed at: a console that renders a state it does not
 * know as "open" is telling the operator something that is not true.
 */
export function present(status: string): StatusPresentation {
  const known = PRESENTATION[status as Status];
  if (known) {
    return known;
  }
  return {
    label: status,
    meaning: 'This console does not recognize this state. Check the service version.',
    tone: 'attention',
    closed: false,
    awaitingDelivery: false,
    compensated: false,
  };
}

/** The commands the console can send. */
export type ActionName = 'acknowledge' | 'assign_reviewer' | 'release' | 'deny';

/** One control the panel may offer. */
export interface Action {
  name: ActionName;
  /** The label on the control, and the accessible name of its icon button. */
  label: string;
  /** The path segment the command is sent to. */
  path: string;
  /** The icon, drawn as text so the console ships no image assets. */
  icon: string;
}

const ACTIONS: Record<ActionName, Action> = {
  acknowledge: { name: 'acknowledge', label: 'Send receipt notice', path: 'acknowledgment', icon: '✉' },
  assign_reviewer: { name: 'assign_reviewer', label: 'Assign records officer', path: 'reviewer', icon: '👤' },
  release: { name: 'release', label: 'Release records', path: 'release', icon: '📄' },
  deny: { name: 'deny', label: 'Deny under exemption', path: 'denial', icon: '⛔' },
};

/**
 * availableActions is the whole of the console's control logic.
 *
 * An action is offered when the authorization model permits it *and* the
 * request's status can accept it. Both halves are needed and neither is
 * guessed: the first comes from the server's decision, the second from the one
 * status field. A control that is not offered is absent rather than disabled,
 * because a disabled control an operator can never enable is a question they
 * cannot answer.
 */
export function availableActions(view: RequestView): Action[] {
  const state = present(view.status);
  if (state.closed || state.awaitingDelivery) {
    // Nothing can be decided about a closed request, and nothing may be decided
    // about one whose release outcome is not known yet.
    return [];
  }

  const permitted = new Set(view.allowed_actions);
  const offered: Action[] = [];

  if (view.status === 'open' && permitted.has('acknowledge')) {
    offered.push(ACTIONS.acknowledge);
  }
  if (view.status !== 'open') {
    if (permitted.has('assign_reviewer')) {
      offered.push(ACTIONS.assign_reviewer);
    }
    // Records cannot be released without an accountable officer, which is a
    // domain rule the service would enforce anyway. Reflecting it here means
    // the operator is not offered a button that is certain to be refused.
    if (permitted.has('release') && view.reviewer !== '') {
      offered.push(ACTIONS.release);
    }
    if (permitted.has('deny') && view.reviewer !== '') {
      offered.push(ACTIONS.deny);
    }
  }
  return offered;
}

/**
 * whyNoActions explains an empty control set, so a screen with no buttons is
 * never a screen with no explanation.
 */
export function whyNoActions(view: RequestView): string {
  const state = present(view.status);
  if (state.closed) {
    return 'This request is closed. Its history is still readable.';
  }
  if (state.awaitingDelivery) {
    return 'A release is out for delivery. The request can be worked again once the outcome is known.';
  }
  if (!view.allowed_actions.includes('release') && !view.allowed_actions.includes('deny')) {
    return 'You have read access to this request. Deciding it is a records officer action.';
  }
  if (view.reviewer === '') {
    return 'Assign a records officer before releasing or denying records.';
  }
  return 'There is nothing to do on this request right now.';
}

/** The due date, rendered for reading rather than for sorting. */
export function formatDate(iso: string): string {
  const at = new Date(iso);
  if (Number.isNaN(at.getTime())) {
    return 'unknown';
  }
  return at.toLocaleDateString(undefined, { year: 'numeric', month: 'short', day: 'numeric' });
}
