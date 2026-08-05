import { describe, expect, it } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import { callsTo, jsonResponse, makeMe, renderApp, setupDomTest } from "./helpers";

setupDomTest();

const NOW = "2026-07-26T12:00:00Z";

function noteSummary(noteId = "note-1") {
  return {
    note_id: noteId,
    kind: "blocker",
    subject: "Release approval",
    state: "active",
    origin_agent_id: "codex",
    audience_agent_ids: [],
    revision: 1,
    created_at: NOW,
    updated_at: NOW,
    soft_expires_at: "2026-07-27T12:00:00Z",
    hard_expires_at: "2026-08-02T12:00:00Z",
  };
}

function noteDetail() {
  return {
    note: {
      summary: noteSummary(),
      body: "Waiting for Todd's approval.",
      origin_user_id: "user-1",
      origin_session_id: "session-source",
      related_subjects: ["release owner"],
    },
    related_notes: [],
    revisions: [
      {
        revision: 1,
        candidate_id: "candidate-1",
        operation: "create",
        body: "Waiting for Todd's approval.",
        related_subjects: [],
        created_at: NOW,
        candidate: {
          candidate_id: "candidate-1",
          action: "create",
          kind: "blocker",
          subject: "Release approval candidate",
          body: "Waiting for Todd's approval.",
          origin_agent_id: "codex",
          evidence_event_ids: ["event-1"],
          admission_status: "admitted",
          created_at: NOW,
          resulting_note_id: "note-1",
        },
        extraction: {
          run_id: "run-1",
          user_id: "user-1",
          agent_id: "codex",
          session_id: "session-source",
          from_sequence: 7,
          to_sequence: 7,
          model: "extractor",
          prompt_version: "prompt-v1",
          status: "completed",
          input_tokens: 32,
          output_tokens: 9,
          created_at: NOW,
        },
        evidence: [
          {
            event_id: "event-1",
            user_id: "user-1",
            agent_id: "codex",
            session_id: "session-source",
            sequence: 7,
            type: "message",
            content: "The release is waiting for Todd's approval.",
            visibility: "",
            occurred_at: NOW,
            captured_at: NOW,
          },
        ],
        deliveries: [
          {
            recipient_user_id: "user-2",
            recipient_agent_id: "claude",
            recipient_session_id: "session-consumer",
            delivered_at: NOW,
            context_tokens: 12,
          },
        ],
      },
    ],
    recall_observations: [
      {
        observation_id: 41,
        recipient_agent_id: "claude",
        recipient_session_id: "session-consumer",
        occurred_at: NOW,
        disposition: "evidence",
        delivered: true,
        rejection_reasons: [],
        budget_drop_reasons: [],
        hard_gate_failures: [],
      },
    ],
  };
}

describe("Team Memory Explorer access and chain", () => {
  it("gates navigation and data fetch on view.team-memory", async () => {
    // No view.team-memory capability: RequireCapability bounces to
    // landingPath(me), which is /overview because view.operations is
    // present (navModel.ts). Operations' own endpoints are stubbed only so
    // that landing page can settle cleanly; the point of this case is what
    // it must NOT fetch.
    const denied = await renderApp({
      route: "/governance/memory",
      me: makeMe({ role: "admin", capabilities: ["view.operations"] }),
      fetch: (path) => {
        if (path.startsWith("/v1/admin/operations/agents")) {
          return jsonResponse({
            agents: [],
            from_time: NOW,
            to_time: NOW,
            generated_at: NOW,
          });
        }
        if (path.startsWith("/v1/admin/operations/events")) {
          return jsonResponse({ events: [], generated_at: NOW });
        }
        throw new Error(`unexpected fetch: ${path}`);
      },
    });
    await screen.findByRole("heading", { name: "Team Pulse" });
    expect(document.querySelector('nav a[href="/governance/memory"]')).toBeNull();
    expect(callsTo(denied.fetchMock, "/v1/admin/team-notes")).toHaveLength(0);
    denied.unmount();

    await renderApp({
      route: "/governance/memory",
      me: makeMe({ capabilities: ["view.operations", "view.team-memory"] }),
      fetch: (path) => {
        if (path.startsWith("/v1/admin/team-notes")) {
          return jsonResponse({ notes: [noteSummary()] });
        }
        throw new Error(`unexpected fetch: ${path}`);
      },
    });
    await screen.findByRole("heading", { name: "Team Memory Explorer" });
    expect(document.querySelector('nav a[href="/governance/memory"]')).not.toBeNull();
    screen.getByRole("link", { name: "Release approval" });
  });

  it("opens a note and renders source, extraction, revision, delivery and recall", async () => {
    const { user } = await renderApp({
      route: "/governance/memory",
      me: makeMe({ capabilities: ["view.operations", "view.team-memory"] }),
      fetch: (path) => {
        if (path === "/v1/admin/team-notes/note-1") return jsonResponse(noteDetail());
        if (path.startsWith("/v1/admin/team-notes")) {
          return jsonResponse({ notes: [noteSummary()] });
        }
        throw new Error(`unexpected fetch: ${path}`);
      },
    });

    await user.click(await screen.findByRole("link", { name: "Release approval" }));
    await screen.findByRole("heading", { name: "Release approval" });
    const chain = screen.getByLabelText("Team Note diagnostic chain");
    within(chain).getByText("Source events");
    within(chain).getByText("Candidate");
    within(chain).getByText("Extraction");
    within(chain).getByText("Deliveries");
    within(chain).getByText("Recall");
    screen.getByText("The release is waiting for Todd's approval.");
    screen.getByText("Release approval candidate");
    screen.getByText("run-1");
    screen.getByText("session-consumer");
    screen.getByText("evidence");
  });

  it("renders recall decisions when a backend omits empty reason arrays", async () => {
    const detail = noteDetail();
    detail.recall_observations = [
      {
        observation_id: 41,
        recipient_agent_id: "claude",
        recipient_session_id: "session-consumer",
        occurred_at: NOW,
        disposition: "evidence",
        delivered: true,
        rejection_reasons: null,
        budget_drop_reasons: null,
        hard_gate_failures: null,
      },
    ] as unknown as ReturnType<typeof noteDetail>["recall_observations"];
    await renderApp({
      route: "/governance/memory/note-1",
      me: makeMe({ capabilities: ["view.operations", "view.team-memory"] }),
      fetch: (path) => {
        if (path === "/v1/admin/team-notes/note-1") return jsonResponse(detail);
        throw new Error(`unexpected fetch: ${path}`);
      },
    });

    await screen.findByRole("heading", { name: "Release approval" });
    expect(screen.queryByText("This section failed to render")).toBeNull();
    screen.getByText("session-consumer");
    expect(screen.getAllByText("None").length).toBeGreaterThan(0);
  });

  it("filters resolved notes and ignores stale pagination results", async () => {
    let resolveOldPage!: (response: Response) => void;
    const oldPage = new Promise<Response>((resolve) => {
      resolveOldPage = resolve;
    });
    const { user, fetchMock } = await renderApp({
      route: "/governance/memory",
      me: makeMe({ capabilities: ["view.operations", "view.team-memory"] }),
      fetch: (path) => {
        if (path.includes("cursor=old-page")) return oldPage;
        if (path.includes("state=resolved")) {
          return jsonResponse({
            notes: [{ ...noteSummary("note-resolved"), subject: "Resolved decision", state: "resolved" }],
          });
        }
        if (path.startsWith("/v1/admin/team-notes")) {
          return jsonResponse({ notes: [noteSummary()], next_cursor: "old-page" });
        }
        throw new Error(`unexpected fetch: ${path}`);
      },
    });

    await screen.findByRole("link", { name: "Release approval" });
    await user.click(screen.getByRole("button", { name: "Load more" }));
    await user.click(screen.getByRole("button", { name: "resolved" }));
    await screen.findByRole("link", { name: "Resolved decision" });
    resolveOldPage(jsonResponse({
      notes: [{ ...noteSummary("note-stale"), subject: "Stale active note" }],
    }));

    await waitFor(() => {
      expect(screen.queryByText("Stale active note")).toBeNull();
    });
    expect(callsTo(fetchMock, "/v1/admin/team-notes").some(
      (call) => call.path.includes("state=resolved"),
    )).toBe(true);
  });
});
