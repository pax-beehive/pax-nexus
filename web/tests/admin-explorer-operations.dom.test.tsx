import { describe, expect, it } from "vitest";
import { screen, within } from "@testing-library/react";
import { callsTo, jsonResponse, makeMe, renderApp, setupDomTest } from "./helpers";
import {
  eventsPage,
  makeEvent,
  makeSnapshot,
  makeSummary,
} from "./operationsFixtures";

setupDomTest();

function drawer(): HTMLElement {
  return document.querySelector(".drawer") as HTMLElement;
}

function operationsResponse(path: string): Response {
  if (path.startsWith("/v1/admin/operations/summary")) return jsonResponse(makeSummary());
  if (path.startsWith("/v1/admin/operations/storage")) {
    return jsonResponse({ storage: makeSnapshot() });
  }
  if (path.startsWith("/v1/admin/agents")) return jsonResponse({ agents: [] });
  throw new Error(`unexpected fetch: ${path}`);
}

describe("Owner diagnostics from Operations", () => {
  it("opens extraction and capsule safe projections from event detail references", async () => {
    const { fetchMock, user } = await renderApp({
      route: "/governance/pipeline",
      me: makeMe({ capabilities: ["view.operations", "view.team-memory"] }),
      fetch: (path) => {
        if (path.startsWith("/v1/admin/operations/events")) {
          return jsonResponse(eventsPage([
            makeEvent({
              operation_event_id: 1,
              operation_kind: "extraction.run",
              detail_kind: "extraction_run",
              detail_id: "run-1",
            }),
            makeEvent({
              operation_event_id: 2,
              attempt_id: "channel-2",
              operation_kind: "channel.send",
              detail_kind: "channel_envelope",
              detail_id: "envelope-1",
            }),
          ]));
        }
        if (path === "/v1/admin/diagnostics/extractions/run-1") {
          return jsonResponse({
            run: {
              run_id: "run-1", user_id: "user", agent_id: "codex",
              session_id: "session", from_sequence: 1, to_sequence: 2,
              model: "extractor", prompt_version: "v1", status: "completed",
              input_tokens: 30, output_tokens: 8, error_code: "operation_failed",
              created_at: "2026-07-26T12:00:00Z", completed_at: "2026-07-26T12:00:01Z",
            },
            source_events: [{
              event_id: "event-1", user_id: "user", agent_id: "codex",
              session_id: "session", sequence: 1, type: "message",
              content: "Safe source content", visibility: "",
              occurred_at: "2026-07-26T12:00:00Z", captured_at: "2026-07-26T12:00:00Z",
            }],
            candidates: [{
              candidate_id: "candidate-1", action: "create", kind: "fact",
              subject: "Release status", body: "Ready", origin_agent_id: "codex",
              evidence_event_ids: ["event-1"], admission_status: "rejected",
              rejection_reason: "missing_evidence", created_at: "2026-07-26T12:00:00Z",
            }],
            resulting_notes: [{
              note_id: "note-1", kind: "fact", subject: "Result note", state: "active",
              origin_agent_id: "codex", audience_agent_ids: [], revision: 1,
              created_at: "2026-07-26T12:00:00Z", updated_at: "2026-07-26T12:00:00Z",
              soft_expires_at: "2026-07-27T12:00:00Z",
              hard_expires_at: "2026-08-02T12:00:00Z",
            }],
          });
        }
        if (path === "/v1/admin/diagnostics/channels/envelope-1") {
          return jsonResponse({
            envelope_id: "envelope-1", from_agent_id: "codex", to_agent_id: "claude",
            status: "pending", payload_status: "decoded", created_at: "2026-07-26T12:00:00Z",
            accepted_at: "2026-07-26T12:00:01Z", archived_at: "2026-07-26T12:00:02Z",
            capsule: {
              capsule_id: "capsule-1", source_session_id: "session-source",
              source_agent: "codex", title: "Release capsule",
              content: "Safe capsule content", keyword: "release",
              status: "ready", truncated: false,
            },
          });
        }
        return operationsResponse(path);
      },
    });

    await user.click(await screen.findByRole("button", { name: "Inspect extraction" }));
    await within(drawer()).findByText("Safe source content");
    within(drawer()).getByText("missing_evidence");
    within(drawer()).getByText("operation_failed");
    within(drawer()).getByRole("link", { name: "Result note" });
    await user.click(within(drawer()).getByRole("button", { name: "Close" }));

    await user.click(screen.getByRole("button", { name: "Inspect capsule" }));
    await within(drawer()).findByText("Safe capsule content");
    within(drawer()).getByText("capsule: capsule-1");
    within(drawer()).getByText("session: session-source");
    within(drawer()).getByText("truncated: no");
    expect(callsTo(fetchMock, "/v1/admin/diagnostics/extractions/run-1")).toHaveLength(1);
    expect(callsTo(fetchMock, "/v1/admin/diagnostics/channels/envelope-1")).toHaveLength(1);
    expect(document.body.textContent).not.toContain("secret-key");
  });
});
