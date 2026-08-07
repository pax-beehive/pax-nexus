// Page-level DOM tests for the Modernist audit trail (/governance/audit,
// design phase 5 §2.1). Covers behaviors the rewrite must not lose: label
// enrichment failing open to the raw ID, the free-text action/target_id
// filters, and the expand-once getAuditEvent fetch — plus the new Seg
// actor_kind filter and the five-column row grid.

import { describe, expect, it } from "vitest";
import { screen, within } from "@testing-library/react";
import type { Mock } from "vitest";
import {
  apiErrorResponse,
  callsTo,
  jsonResponse,
  makeMe,
  makeMember,
  renderApp,
  setupDomTest,
  type FetchHandler,
  type RecordedCall,
} from "./helpers";
import type { AuditEvent } from "../src/api/types";

setupDomTest();

function makeAuditEvent(overrides: Partial<AuditEvent> = {}): AuditEvent {
  return {
    audit_event_id: 1,
    actor_kind: "human",
    actor_membership_id: "mbr_01",
    action: "agent.create",
    target_kind: "agent",
    target_id: "agent-1",
    occurred_at: "2026-08-01T12:00:00Z",
    ...overrides,
  };
}

const SELF = makeMember({ membership_id: "mbr_01", email: "alice@example.com" });

/** List-endpoint calls only, excluding the per-row detail GET. */
function listCalls(fetchMock: Mock): RecordedCall[] {
  return callsTo(fetchMock, "/v1/admin/audit-events").filter(
    (c) => !/\/v1\/admin\/audit-events\/\d+/.test(c.path),
  );
}

/** getAuditEvent detail calls only (path carries the numeric id). */
function detailCalls(fetchMock: Mock): RecordedCall[] {
  return callsTo(fetchMock, "/v1/admin/audit-events").filter((c) =>
    /\/v1\/admin\/audit-events\/\d+/.test(c.path),
  );
}

function baseFetch(events: AuditEvent[]): FetchHandler {
  return (path, init) => {
    if (path.startsWith("/v1/admin/members")) return jsonResponse({ members: [SELF] });
    if (path.startsWith("/v1/admin/agents")) return jsonResponse({ agents: [] });
    if (/\/v1\/admin\/audit-events\/\d+/.test(path)) {
      const id = Number(path.split("/").pop());
      const event = events.find((e) => e.audit_event_id === id);
      if (!event) return apiErrorResponse(404, "not_found", "no such audit event");
      return jsonResponse({ audit_event: event });
    }
    if (path.startsWith("/v1/admin/audit-events")) {
      return jsonResponse({ audit_events: events });
    }
    throw new Error(`unexpected fetch: ${init.method ?? "GET"} ${path}`);
  };
}

describe("AdminAuditPage: header and row shape", () => {
  it("renders the Modernist kicker, heading and description", async () => {
    await renderApp({
      route: "/governance/audit",
      me: makeMe(),
      fetch: baseFetch([makeAuditEvent()]),
    });

    await screen.findByRole("heading", { name: "发生过的一切，未经编辑" });
    expect(screen.getByText("Governance · 审计流水")).not.toBeNull();
    expect(
      screen.getByText(/只追加。名字是我们替你查出来的方便/),
    ).not.toBeNull();
  });

  it("resolves the actor name with the raw membership_id trailing in parentheses", async () => {
    await renderApp({
      route: "/governance/audit",
      me: makeMe(),
      fetch: baseFetch([makeAuditEvent({ actor_membership_id: "mbr_01" })]),
    });

    const row = await screen.findByRole("button", { name: /agent\.create/ });
    expect(within(row).getByText("alice@example.com")).not.toBeNull();
    expect(within(row).getByText("(mbr_01)")).not.toBeNull();
  });
});

describe("AdminAuditPage: actor_kind Seg filter", () => {
  it("全部 / 人 / Agent / 系统 each send the right actor_kind (or none)", async () => {
    const { fetchMock, user } = await renderApp({
      route: "/governance/audit",
      me: makeMe(),
      fetch: baseFetch([makeAuditEvent()]),
    });

    await screen.findByRole("heading", { name: "发生过的一切，未经编辑" });
    const group = screen.getByRole("group", { name: "Filter by actor_kind" });

    // Initial mount: 全部 selected, no actor_kind param at all.
    const initialCalls = listCalls(fetchMock);
    expect(initialCalls.length).toBeGreaterThan(0);
    expect(initialCalls[initialCalls.length - 1].path).not.toContain("actor_kind");

    await user.click(within(group).getByRole("button", { name: "人" }));
    expect(listCalls(fetchMock).at(-1)?.path).toContain("actor_kind=human");

    await user.click(within(group).getByRole("button", { name: "Agent" }));
    expect(listCalls(fetchMock).at(-1)?.path).toContain("actor_kind=agent");

    await user.click(within(group).getByRole("button", { name: "系统" }));
    expect(listCalls(fetchMock).at(-1)?.path).toContain("actor_kind=system");

    await user.click(within(group).getByRole("button", { name: "全部" }));
    expect(listCalls(fetchMock).at(-1)?.path).not.toContain("actor_kind");
  });
});

describe("AdminAuditPage: label resolution fails open", () => {
  it("still renders the row with the raw id when /v1/admin/members and /v1/admin/agents 403", async () => {
    const fetch: FetchHandler = (path, init) => {
      if (path.startsWith("/v1/admin/members")) {
        return apiErrorResponse(403, "forbidden", "no access");
      }
      if (path.startsWith("/v1/admin/agents")) {
        return apiErrorResponse(403, "forbidden", "no access");
      }
      if (path.startsWith("/v1/admin/audit-events")) {
        return jsonResponse({
          audit_events: [makeAuditEvent({ actor_membership_id: "mbr_deleted_42" })],
        });
      }
      throw new Error(`unexpected fetch: ${init.method ?? "GET"} ${path}`);
    };

    await renderApp({ route: "/governance/audit", me: makeMe(), fetch });

    await screen.findByText("agent.create");
    expect(screen.getByText("mbr_deleted_42")).not.toBeNull();
  });
});

describe("AdminAuditPage: expand fetches getAuditEvent at most once", () => {
  it("open, collapse, reopen only issues one GET", async () => {
    const event = makeAuditEvent({ audit_event_id: 7, action: "identity.agent.retired" });
    const { fetchMock, user } = await renderApp({
      route: "/governance/audit",
      me: makeMe(),
      fetch: baseFetch([event]),
    });

    const row = await screen.findByRole("button", { name: /identity\.agent\.retired/ });

    await user.click(row); // open
    await screen.findByText("audit_event_id:");
    expect(detailCalls(fetchMock)).toHaveLength(1);

    await user.click(row); // collapse
    expect(screen.queryByText("audit_event_id:")).toBeNull();

    await user.click(row); // reopen
    await screen.findByText("audit_event_id:");
    expect(detailCalls(fetchMock)).toHaveLength(1);
  });
});

describe("AdminAuditPage: free-text filters", () => {
  it("applies action and target_id filters on Apply filters click", async () => {
    const { fetchMock, user } = await renderApp({
      route: "/governance/audit",
      me: makeMe(),
      fetch: baseFetch([makeAuditEvent()]),
    });

    await screen.findByRole("heading", { name: "发生过的一切，未经编辑" });
    await user.type(
      screen.getByPlaceholderText("action (e.g. agent.create)"),
      "agent.retire",
    );
    await user.type(screen.getByPlaceholderText("target_id"), "agent-9");
    await user.click(screen.getByRole("button", { name: "Apply filters" }));

    const last = listCalls(fetchMock).at(-1);
    expect(last?.path).toContain("action=agent.retire");
    expect(last?.path).toContain("target_id=agent-9");
  });
});

// C1: a variant of this page's list-load failure was previously covered by
// zero tests -- deleting AdminAuditPage.tsx:142-148 entirely left the full
// suite green. Pins the retryable error and that Retry re-issues the list
// request.
describe("AdminAuditPage: list failure renders a retryable error", () => {
  it("failed list load shows Retry; clicking it re-issues the list request", async () => {
    let calls = 0;
    const fetch: FetchHandler = (path, init) => {
      if (path.startsWith("/v1/admin/members")) return jsonResponse({ members: [SELF] });
      if (path.startsWith("/v1/admin/agents")) return jsonResponse({ agents: [] });
      if (path.startsWith("/v1/admin/audit-events")) {
        calls += 1;
        if (calls === 1) return apiErrorResponse(500, "internal_error", "boom");
        return jsonResponse({ audit_events: [makeAuditEvent({ action: "agent.create" })] });
      }
      throw new Error(`unexpected fetch: ${init.method ?? "GET"} ${path}`);
    };

    const { fetchMock, user } = await renderApp({ route: "/governance/audit", me: makeMe(), fetch });

    await screen.findByText("Failed to load the list.");
    expect(listCalls(fetchMock)).toHaveLength(1);

    await user.click(screen.getByRole("button", { name: "Retry" }));
    await screen.findByText("agent.create");
    expect(listCalls(fetchMock)).toHaveLength(2);
  });
});

// C1: AuditRow's per-row detail fetch failure had zero coverage too. A
// failure surfaces via the shared error toast (useErrorHandler), and
// AuditRow.tsx:90-95 resets `fetchedRef` so collapsing and re-expanding the
// row retries the GET rather than being stuck fetched-but-empty forever.
describe("AdminAuditPage: row detail failure resets fetchedRef, letting re-expand retry", () => {
  it("failed getAuditEvent surfaces an error; collapse + reopen re-fetches", async () => {
    const event = makeAuditEvent({ audit_event_id: 9, action: "agent.suspend" });
    let detailGets = 0;
    const fetch: FetchHandler = (path, init) => {
      if (path.startsWith("/v1/admin/members")) return jsonResponse({ members: [SELF] });
      if (path.startsWith("/v1/admin/agents")) return jsonResponse({ agents: [] });
      if (/\/v1\/admin\/audit-events\/\d+/.test(path)) {
        detailGets += 1;
        if (detailGets === 1) return apiErrorResponse(500, "internal_error", "boom");
        return jsonResponse({ audit_event: event });
      }
      if (path.startsWith("/v1/admin/audit-events")) {
        return jsonResponse({ audit_events: [event] });
      }
      throw new Error(`unexpected fetch: ${init.method ?? "GET"} ${path}`);
    };

    const { user } = await renderApp({ route: "/governance/audit", me: makeMe(), fetch });

    const row = await screen.findByRole("button", { name: /agent\.suspend/ });
    await user.click(row); // open -- GET fails
    await screen.findByText("Server error; try again later");
    expect(detailGets).toBe(1);

    await user.click(row); // collapse
    await user.click(row); // reopen -- fetchedRef must have been reset
    await screen.findByText("audit_event_id:");
    expect(detailGets).toBe(2);
  });
});
