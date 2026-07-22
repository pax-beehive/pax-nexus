// Shared helpers for page-level DOM smoke tests (tests/*.dom.test.tsx).
//
// Pattern: render the real <App /> against a stubbed global fetch. Auth
// state is never injected — it is driven by the GET /v1/me response exactly
// as in production, so route guards, the continuation redirect, and page
// data loads all run through the real composition. Call setupDomTest() once
// per file to get cleanup, global unstubbing, and browser-state reset.
//
//   const fetchMock = stubFetch((path, init) => {
//     if (path === "/v1/me") return jsonResponse(makeMe());
//     if (path.startsWith("/v1/me/agents")) return jsonResponse({ agents: [] });
//     throw new Error(`unexpected fetch: ${path}`);
//   });
//   render(<App />);
//   await screen.findByText("My Agents");

import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, expect, vi, type Mock } from "vitest";
import App from "../src/App";
import type {
  AgentProfile,
  CredentialMetadata,
  EnrollmentMetadata,
  HumanMe,
  Invitation,
  Member,
} from "../src/api/types";

// ---- fetch stubbing (same Response-based style as client.test.ts) ----

export function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

/** Structured API error body (doc section 9): stable code + diagnostic. */
export function apiErrorResponse(status: number, code: string, message: string): Response {
  return jsonResponse({ code, message }, status);
}

export type FetchHandler = (path: string, init: RequestInit) => Response | Promise<Response>;

/** Install a fetch stub; unmatched requests must throw inside the handler. */
export function stubFetch(handler: FetchHandler): Mock {
  const fn = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) =>
    handler(String(input), init ?? {}),
  );
  vi.stubGlobal("fetch", fn);
  return fn;
}

export interface RecordedCall {
  path: string;
  init: RequestInit;
  headers: Headers;
}

/** Filter recorded fetch calls by path prefix and (optionally) method. */
export function callsTo(fetchMock: Mock, pathPrefix: string, method?: string): RecordedCall[] {
  return fetchMock.mock.calls
    .map(([input, init]) => ({
      path: String(input),
      init: (init ?? {}) as RequestInit,
    }))
    .filter(
      (call) =>
        call.path.startsWith(pathPrefix) &&
        (!method || (call.init.method ?? "GET").toUpperCase() === method),
    )
    .map((call) => ({ ...call, headers: new Headers(call.init.headers) }));
}

// ---- browser state ----

/**
 * Per-test reset: cookies the app needs (CSRF double-submit), cleared
 * storage, and a known URL. renderApp calls this implicitly.
 */
export function resetBrowserState(): void {
  sessionStorage.clear();
  localStorage.clear();
  document.cookie = "tm_csrf=test-csrf";
  window.history.pushState({}, "", "/");
}

/** Register once per dom test file. */
export function setupDomTest(): void {
  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
    resetBrowserState();
  });
}

// ---- app rendering ----

export interface RenderAppOptions {
  /** Initial URL (path + optional hash), e.g. "/join#invite=...". */
  route?: string;
  /**
   * GET /v1/me shorthand: a HumanMe (or a function returning one, so tests
   * can flip auth state mid-flow) answers 200; null answers 401. When
   * omitted entirely, /v1/me is delegated to `fetch` like any other path.
   */
  me?: HumanMe | null | (() => HumanMe | null);
  /** Every non-/v1/me request goes here; throw on unexpected paths. */
  fetch: FetchHandler;
  /** sessionStorage entries seeded after the reset, before first render. */
  sessionStorage?: Record<string, string>;
}

/**
 * Render the full portal at `route` and wait for the boot GET /v1/me to
 * settle (the global "加载中…" placeholder disappears). Returns the fetch
 * mock for request assertions and a user-event instance for interactions.
 */
export async function renderApp(options: RenderAppOptions): Promise<{
  fetchMock: Mock;
  user: ReturnType<typeof userEvent.setup>;
  unmount: () => void;
}> {
  resetBrowserState();
  document.cookie = "tm_csrf=test-csrf";
  window.history.pushState({}, "", options.route ?? "/");
  for (const [key, value] of Object.entries(options.sessionStorage ?? {})) {
    sessionStorage.setItem(key, value);
  }
  const fetchMock = stubFetch((path, init) => {
    if (path === "/v1/me" && options.me !== undefined) {
      const me = typeof options.me === "function" ? options.me() : options.me;
      if (me) return jsonResponse(me);
      return apiErrorResponse(401, "unauthorized", "no session");
    }
    return options.fetch(path, init);
  });
  const view = render(<App />);
  await waitFor(() => expect(screen.queryAllByText("加载中…")).toHaveLength(0));
  return { fetchMock, user: userEvent.setup(), unmount: () => view.unmount() };
}

// ---- fixture factories ----

export function makeMe(overrides: Partial<HumanMe> = {}): HumanMe {
  return {
    user_id: "usr_01",
    email: "alice@example.com",
    email_verified: true,
    membership_id: "mbr_01",
    role: "owner",
    membership_status: "active",
    capabilities: [],
    ...overrides,
  };
}

/** An authenticated user without any Membership (join/bootstrap state). */
export function makeNoMembershipMe(overrides: Partial<HumanMe> = {}): HumanMe {
  return makeMe({
    membership_id: undefined,
    role: undefined,
    membership_status: undefined,
    ...overrides,
  });
}

export function makeMember(overrides: Partial<Member> = {}): Member {
  return {
    membership_id: "mbr_02",
    user_id: "usr_02",
    email: "bob@example.com",
    email_verified: true,
    display_name: "Bob",
    role: "member",
    status: "active",
    joined_at: "2026-07-01T00:00:00Z",
    updated_at: "2026-07-01T00:00:00Z",
    resource_version: 3,
    ...overrides,
  };
}

export function makeAgent(overrides: Partial<AgentProfile> = {}): AgentProfile {
  return {
    agent_id: "agent-1",
    display_name: "Alice Codex",
    description: "test agent",
    agent_type: "codex",
    status: "active",
    directory_visible: true,
    created_at: "2026-07-01T00:00:00Z",
    updated_at: "2026-07-01T00:00:00Z",
    resource_version: 7,
    owner_membership_id: "mbr_01",
    owner_user_id: "usr_01",
    ...overrides,
  };
}

export function makeInvitation(overrides: Partial<Invitation> = {}): Invitation {
  return {
    invitation_id: "inv_01",
    target_email: "bob@example.com",
    role: "member",
    status: "pending",
    created_at: "2026-07-01T00:00:00Z",
    expires_at: "2026-07-02T00:00:00Z",
    ...overrides,
  };
}

export function makeEnrollment(overrides: Partial<EnrollmentMetadata> = {}): EnrollmentMetadata {
  return {
    enrollment_id: "enr_01",
    agent_id: "agent-1",
    credential_label: "Alice MacBook",
    permissions: ["observe", "search"],
    status: "pending",
    created_at: "2026-07-01T00:00:00Z",
    expires_at: "2026-07-02T00:00:00Z",
    ...overrides,
  };
}

export function makeCredential(overrides: Partial<CredentialMetadata> = {}): CredentialMetadata {
  return {
    credential_id: "cred_01",
    agent_id: "agent-1",
    label: "Alice MacBook",
    permissions: ["observe", "search"],
    created_at: "2026-07-01T00:00:00Z",
    expires_at: "2027-07-01T00:00:00Z",
    last_used_at: "2026-07-20T00:00:00Z",
    ...overrides,
  };
}
