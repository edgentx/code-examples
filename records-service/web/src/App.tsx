import { useCallback, useEffect, useRef, useState } from 'react';

import { ApiError, command, listRequests, readRequest, type RequestView } from './api';
import { DetailPanel } from './components/DetailPanel';
import { IconButton } from './components/IconButton';
import { SkeletonDetail, SkeletonRows } from './components/Skeleton';
import {
  availableActions,
  formatDate,
  present,
  whyNoActions,
  type Action,
  type ActionName,
} from './derive';

/**
 * The operators the console can act as.
 *
 * In a deployment there is no picker: the authorization sidecar in front of the
 * service stamps the identity header and the browser cannot. It is here so the
 * example can be operated without an identity provider, and because switching
 * between a clerk and a records officer shows the controls changing with the
 * authorization model rather than with anything this console decided.
 */
const OPERATORS = [
  { id: 'r.okafor', description: 'records officer, midtown office' },
  { id: 'c.hall', description: 'intake clerk, midtown office' },
];

/** How long to wait before looking again while a release is out for delivery. */
const DELIVERY_POLL_MS = 700;

export function App() {
  const [operator, setOperator] = useState(OPERATORS[0].id);
  const [rows, setRows] = useState<RequestView[] | null>(null);
  const [refreshing, setRefreshing] = useState(false);
  const [listError, setListError] = useState<string | null>(null);
  const [openID, setOpenID] = useState<string | null>(null);
  const [detail, setDetail] = useState<RequestView | null>(null);
  const [detailError, setDetailError] = useState<string | null>(null);
  const [conflict, setConflict] = useState<number | null>(null);
  const [fieldError, setFieldError] = useState<{ action: ActionName; message: string } | null>(null);
  const [sending, setSending] = useState<ActionName | null>(null);
  const [announcement, setAnnouncement] = useState('');

  const conflictNotice = useRef<HTMLDivElement>(null);

  const loadList = useCallback(async (who: string) => {
    // A refresh keeps the rows that are already on screen and marks the table
    // busy. Blanking them would replace real content with placeholders, and it
    // would destroy the control the operator's focus is standing on.
    setRefreshing(true);
    setListError(null);
    try {
      const loaded = await listRequests(who);
      setRows(loaded);
      // The announcement is what a screen reader user gets in place of the
      // skeleton, so it says what arrived rather than that something did.
      setAnnouncement(
        loaded.length === 1 ? '1 request loaded.' : `${loaded.length} requests loaded.`,
      );
    } catch (error) {
      setRows((current) => current ?? []);
      setListError(describe(error));
    } finally {
      setRefreshing(false);
    }
  }, []);

  const loadDetail = useCallback(async (who: string, id: string) => {
    setDetailError(null);
    try {
      setDetail(await readRequest(who, id));
    } catch (error) {
      setDetail(null);
      setDetailError(describe(error));
    }
  }, []);

  useEffect(() => {
    void loadList(operator);
  }, [loadList, operator]);

  useEffect(() => {
    if (openID === null) {
      setDetail(null);
      setConflict(null);
      setFieldError(null);
      return;
    }
    setDetail(null);
    void loadDetail(operator, openID);
  }, [loadDetail, openID, operator]);

  // While a release is out for delivery, the outcome arrives from another
  // service rather than from anything the operator does here. The console looks
  // again, and it decides to do so from the one status field rather than from a
  // flag somebody remembered to set.
  useEffect(() => {
    if (!detail || !present(detail.status).awaitingDelivery || openID === null) {
      return;
    }
    const timer = window.setTimeout(() => {
      void loadDetail(operator, openID);
      void loadList(operator);
    }, DELIVERY_POLL_MS);
    return () => window.clearTimeout(timer);
  }, [detail, loadDetail, loadList, openID, operator]);

  // A conflict is the one message the operator must not miss, so focus moves to
  // it. `role="alert"` announces it; moving focus is what puts the keyboard user
  // at the control that resolves it.
  useEffect(() => {
    if (conflict !== null) {
      conflictNotice.current?.focus();
    }
  }, [conflict]);

  async function send(action: Action, body?: unknown) {
    if (!detail) {
      return;
    }
    setSending(action.name);
    setFieldError(null);
    try {
      const updated = await command(operator, detail.id, action.path, detail.version, body);
      setDetail(updated);
      setConflict(null);
      setAnnouncement(
        `${action.label} accepted. ${detail.id} is now ${present(updated.status).label.toLowerCase()}.`,
      );
      void loadList(operator);
    } catch (error) {
      if (error instanceof ApiError && error.isConflict) {
        // Nothing was written. The operator is told what happened and given the
        // version to start again from, rather than having their decision
        // silently applied over somebody else's.
        setConflict(error.currentVersion ?? null);
        setAnnouncement('This request changed while you were working on it. Nothing was saved.');
      } else {
        setFieldError({ action: action.name, message: describe(error) });
        setAnnouncement(`${action.label} was refused. ${describe(error)}`);
      }
    } finally {
      setSending(null);
    }
  }

  async function reviewCurrent() {
    if (openID === null) {
      return;
    }
    setConflict(null);
    setDetail(null);
    await loadDetail(operator, openID);
    await loadList(operator);
    setAnnouncement('Reloaded. Review the current version and decide again.');
  }

  const loading = rows === null;
  const busy = loading || refreshing;

  return (
    <>
      <a className="skip-link" href="#requests">Skip to the request list</a>

      <header className="masthead">
        <div className="masthead__identity">
          <h1>Public records console</h1>
          <p className="masthead__office">Midtown records office</p>
        </div>
        <div className="masthead__controls">
          <div className="field field--inline">
            <label htmlFor="operator">Signed in as</label>
            <select
              id="operator"
              value={operator}
              onChange={(event) => {
                // A different operator sees a different list, so the rows on
                // screen are not a stale view of the same data -- they are the
                // wrong data, and the skeleton is the right thing to show.
                setOpenID(null);
                setRows(null);
                setOperator(event.target.value);
              }}
            >
              {OPERATORS.map((who) => (
                <option key={who.id} value={who.id}>
                  {who.id} — {who.description}
                </option>
              ))}
            </select>
          </div>
          <IconButton icon="⟳" label="Reload the request list" onClick={() => void loadList(operator)} />
        </div>
      </header>

      <main id="requests">
        <h2>Requests</h2>
        <p className="section-note">
          Every control below is offered because the service said this operator may use it.
        </p>

        {listError && (
          <p className="notice notice--error" role="alert">
            {listError}
          </p>
        )}

        <table className="requests" aria-busy={busy}>
          <caption className="sr-only">
            Public records requests filed with the midtown office, oldest first.
          </caption>
          <thead>
            <tr>
              <th scope="col">Request</th>
              <th scope="col">Requester</th>
              <th scope="col">Status</th>
              <th scope="col">Response due</th>
              <th scope="col">Details</th>
            </tr>
          </thead>
          <tbody>
            {loading && <SkeletonRows />}
            {!loading && rows.length === 0 && !listError && (
              <tr>
                <td colSpan={5}>No requests are visible to this operator.</td>
              </tr>
            )}
            {!loading &&
              rows.map((row) => {
                const state = present(row.status);
                return (
                  <tr key={row.id} className={row.id === openID ? 'requests__row--open' : undefined}>
                    <th scope="row">{row.id}</th>
                    <td>{row.requester}</td>
                    <td>
                      <span className={`badge badge--${state.tone}`}>
                        <span className="badge__dot" aria-hidden="true" />
                        {state.label}
                      </span>
                    </td>
                    <td>{formatDate(row.due_at)}</td>
                    <td>
                      <IconButton
                        icon="▸"
                        label={`Open details for ${row.id}`}
                        onClick={() => setOpenID(row.id)}
                      />
                    </td>
                  </tr>
                );
              })}
          </tbody>
        </table>
      </main>

      {/* One polite live region for everything that happens without the operator
          asking: data arriving, a command accepted, a command refused. */}
      <p className="sr-only" role="status" aria-live="polite">
        {announcement}
      </p>

      {openID !== null && (
        <DetailPanel
          title={openID}
          subtitle={detail ? present(detail.status).meaning : 'Loading the request.'}
          onClose={() => setOpenID(null)}
        >
          {detailError && (
            <p className="notice notice--error" role="alert">
              {detailError}
            </p>
          )}
          {!detail && !detailError && <SkeletonDetail />}
          {detail && (
            <>
              {conflict !== null && (
                <div
                  className="notice notice--conflict"
                  role="alert"
                  tabIndex={-1}
                  ref={conflictNotice}
                >
                  <h3>This record changed</h3>
                  <p>
                    Somebody else changed this request while it was open here
                    {conflict !== null ? `; it is now at version ${conflict}` : ''}. Nothing you
                    entered was saved. Review the current version and decide again.
                  </p>
                  <IconButton
                    icon="⟳"
                    label="Reload and review the current version"
                    tone="primary"
                    onClick={() => void reviewCurrent()}
                  />
                </div>
              )}

              <dl className="detail">
                <dt>Requester</dt>
                <dd>{detail.requester}</dd>
                <dt>Records described</dt>
                <dd>{detail.description}</dd>
                <dt>Status</dt>
                <dd>{present(detail.status).label}</dd>
                <dt>Records officer</dt>
                <dd>{detail.reviewer || 'not assigned'}</dd>
                <dt>Response due</dt>
                <dd>{formatDate(detail.due_at)}</dd>
                <dt>Version</dt>
                <dd>{detail.version}</dd>
                {detail.released_pages > 0 && (
                  <>
                    <dt>Pages released</dt>
                    <dd>{detail.released_pages}</dd>
                  </>
                )}
                {detail.package_id && (
                  <>
                    <dt>Delivered package</dt>
                    <dd>{detail.package_id}</dd>
                  </>
                )}
                {detail.exemption && (
                  <>
                    <dt>Exemption cited</dt>
                    <dd>{detail.exemption}</dd>
                  </>
                )}
                {detail.failure_cause && (
                  <>
                    <dt>Why the release was withdrawn</dt>
                    <dd>{detail.failure_cause}</dd>
                  </>
                )}
              </dl>

              <Decisions
                view={detail}
                busy={sending}
                fieldError={fieldError}
                onSend={(action, body) => void send(action, body)}
              />
            </>
          )}
        </DetailPanel>
      )}
    </>
  );
}

/** The controls for one request, and the reason when there are none. */
function Decisions({
  view,
  busy,
  fieldError,
  onSend,
}: {
  view: RequestView;
  busy: ActionName | null;
  fieldError: { action: ActionName; message: string } | null;
  onSend: (action: Action, body?: unknown) => void;
}) {
  const actions = availableActions(view);

  return (
    <section aria-labelledby="decisions-heading" className="decisions">
      <h3 id="decisions-heading">Decide this request</h3>
      {actions.length === 0 ? (
        <p className="section-note">{whyNoActions(view)}</p>
      ) : (
        actions.map((action) => (
          <ActionForm
            key={action.name}
            action={action}
            busy={busy === action.name}
            error={fieldError?.action === action.name ? fieldError.message : null}
            onSend={onSend}
          />
        ))
      )}
    </section>
  );
}

/** One command, with its input if it needs one and its error if it was refused. */
function ActionForm({
  action,
  busy,
  error,
  onSend,
}: {
  action: Action;
  busy: boolean;
  error: string | null;
  onSend: (action: Action, body?: unknown) => void;
}) {
  const [value, setValue] = useState(action.name === 'release' ? '0' : '');
  const inputID = `field-${action.name}`;
  const errorID = `${inputID}-error`;
  const hintID = `${inputID}-hint`;

  const field = FIELDS[action.name];

  return (
    <form
      className="action"
      onSubmit={(event) => {
        event.preventDefault();
        onSend(action, field ? field.body(value) : undefined);
      }}
    >
      {field && (
        <div className="field">
          <label htmlFor={inputID}>{field.label}</label>
          <input
            id={inputID}
            name={action.name}
            type={field.type}
            min={field.type === 'number' ? 0 : undefined}
            value={value}
            onChange={(event) => setValue(event.target.value)}
            // The error is attached to the field it is about, so a screen
            // reader reads the reason when focus reaches the input rather than
            // leaving it stranded as loose text near the form.
            aria-invalid={error ? true : undefined}
            aria-describedby={error ? `${hintID} ${errorID}` : hintID}
          />
          <p className="hint" id={hintID}>
            {field.hint}
          </p>
          {error && (
            <p className="field-error" id={errorID} role="alert">
              {error}
            </p>
          )}
        </div>
      )}
      {!field && error && (
        <p className="field-error" role="alert">
          {error}
        </p>
      )}
      <IconButton
        icon={action.icon}
        label={action.label}
        tone={action.name === 'deny' ? 'danger' : 'primary'}
        type="submit"
        disabled={busy}
      />
    </form>
  );
}

/** The input each command needs, if any. */
const FIELDS: Partial<
  Record<ActionName, { label: string; hint: string; type: string; body: (value: string) => unknown }>
> = {
  assign_reviewer: {
    label: 'Records officer',
    hint: 'Who is accountable for the response.',
    type: 'text',
    body: (value) => ({ reviewer: value }),
  },
  release: {
    label: 'Pages released',
    hint: 'The page count going out with the release package.',
    type: 'number',
    body: (value) => ({ released_pages: Number(value) }),
  },
  deny: {
    label: 'Exemption cited',
    hint: 'The exemption the agency would defend on appeal.',
    type: 'text',
    body: (value) => ({ exemption: value }),
  },
};

/** describe turns any failure into a sentence an operator can act on. */
function describe(error: unknown): string {
  if (error instanceof ApiError) {
    return error.problem.detail ? `${error.problem.title}. ${error.problem.detail}` : error.problem.title;
  }
  if (error instanceof Error) {
    return `The console could not reach the service. ${error.message}`;
  }
  return 'The console could not reach the service.';
}
