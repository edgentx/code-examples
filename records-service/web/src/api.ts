/**
 * The API client. It is the only file that knows about HTTP, and it holds no
 * rules: it sends the version the operator was shown, attaches an idempotency
 * key so a retry is harmless, and turns an RFC 7807 problem document into a
 * typed error the console can act on.
 */

/** One records request, exactly as the service renders it. */
export interface RequestView {
  id: string;
  requester: string;
  description: string;
  status: string;
  reviewer: string;
  submitted_at: string;
  due_at: string;
  released_pages: number;
  exemption: string;
  package_id: string;
  failure_cause: string;
  /** The version to send back with an edit. This is the whole of the console's optimistic locking. */
  version: number;
  /**
   * What this caller may do, decided by the authorization model on the server.
   * The console renders controls from this list and holds no rule of its own.
   */
  allowed_actions: string[];
}

/** An RFC 7807 problem document, plus the two fields this API adds. */
export interface Problem {
  type: string;
  title: string;
  status: number;
  detail?: string;
  code: string;
  current_version?: number;
}

/**
 * ApiError carries the problem document rather than a message string, so a
 * caller branches on `code` and can show `title` to the operator. A conflict
 * additionally carries the version to start again from.
 */
export class ApiError extends Error {
  readonly problem: Problem;

  constructor(problem: Problem) {
    super(problem.title);
    this.name = 'ApiError';
    this.problem = problem;
  }

  get code(): string {
    return this.problem.code;
  }

  /** True when the record moved while the operator was working on it. */
  get isConflict(): boolean {
    return this.problem.code === 'version_conflict';
  }

  /** The version the operator should review, when the service supplied one. */
  get currentVersion(): number | undefined {
    return this.problem.current_version;
  }
}

/**
 * Identity travels as a header. In a deployment an authorization sidecar stamps
 * it and a browser cannot set it; here the console sends it so the example can
 * be operated without standing up an identity provider, and so that switching
 * operators shows the controls changing with the authorization model rather
 * than with anything the console decided.
 */
const USER_HEADER = 'X-User-Id';

/** A fresh idempotency key per command, so a retry of that command is the same command. */
function idempotencyKey(): string {
  if (typeof crypto !== 'undefined' && 'randomUUID' in crypto) {
    return crypto.randomUUID();
  }
  return `key-${Date.now()}-${Math.random().toString(16).slice(2)}`;
}

async function refuse(response: Response): Promise<never> {
  let problem: Problem = {
    type: '/problems/unreadable',
    title: `The service answered ${response.status}`,
    status: response.status,
    code: 'unreadable_response',
  };
  try {
    problem = (await response.json()) as Problem;
  } catch {
    // The body was not a problem document. The status line is all there is.
  }
  throw new ApiError(problem);
}

/** List the requests this operator may read. */
export async function listRequests(operator: string): Promise<RequestView[]> {
  const response = await fetch('/api/requests', {
    headers: { [USER_HEADER]: operator },
  });
  if (!response.ok) {
    return refuse(response);
  }
  return (await response.json()) as RequestView[];
}

/** Read one request. */
export async function readRequest(operator: string, id: string): Promise<RequestView> {
  const response = await fetch(`/api/requests/${encodeURIComponent(id)}`, {
    headers: { [USER_HEADER]: operator },
  });
  if (!response.ok) {
    return refuse(response);
  }
  return (await response.json()) as RequestView;
}

/**
 * Send one command.
 *
 * `version` is the version the operator was looking at when they decided. It
 * goes out as If-Match, so a decision made against a screen that has since gone
 * stale is refused with 409 instead of overwriting whatever arrived in the
 * meantime.
 */
export async function command(
  operator: string,
  id: string,
  path: string,
  version: number,
  body?: unknown,
): Promise<RequestView> {
  const response = await fetch(`/api/requests/${encodeURIComponent(id)}/${path}`, {
    method: 'POST',
    headers: {
      [USER_HEADER]: operator,
      'Idempotency-Key': idempotencyKey(),
      'If-Match': `"${version}"`,
      'Content-Type': 'application/json',
    },
    body: JSON.stringify(body ?? {}),
  });
  if (!response.ok) {
    return refuse(response);
  }
  return (await response.json()) as RequestView;
}
