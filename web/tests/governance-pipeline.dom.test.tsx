// Page-level DOM tests for the Pipeline health shell redraw (Modernist
// Portal phase 5 task 5, design spec §2.3): the six-cell PipelineMetrics bar
// and the block-level error isolation the redraw must not break. The three
// region hooks in pages/operations/hooks.ts are untouched by this task --
// these tests exercise the existing region-isolation contract through the
// new shell, they don't re-test the hooks themselves (that coverage lives in
// admin-operations-regions.dom.test.tsx and stays as-is).

import { describe, expect, it } from "vitest";
import { screen, within } from "@testing-library/react";
import { apiErrorResponse, jsonResponse, setupDomTest } from "./helpers";
import {
  eventsPage,
  eventsTable,
  makeEvent,
  makeSummary,
  pipelineMetric,
  renderOperationsPage,
} from "./operationsFixtures";

setupDomTest();

describe("Pipeline metrics: six cells read from the correct fields", () => {
  it("六格各自取自正确的字段", async () => {
    // Six mutually distinct numbers -- a swapped field mapping must fail at
    // least one of these assertions. `errors` is a seventh, also distinct,
    // number: it doesn't get its own cell, but it must show up in "失败"'s
    // subtitle (see the assertion below).
    const summary = makeSummary({
      errors: 8,
      extraction: {
        runs: 20,
        completed: 15,
        quarantined: 2,
        failed: 3,
        admitted_revisions: 10,
        unextracted_events: 4,
        oldest_unextracted_at: "2026-07-22T11:50:00Z",
      },
      latency: { sample_count: 40, p50_ms: 50, p95_ms: 90 },
      recalls: {
        requests: 30,
        succeeded: 24,
        with_evidence: 18,
        empty: 6,
        memory_hits: 41,
        team_notes_delivered: 21,
        memory_search_requests: 20,
        memory_get_requests: 4,
        team_note_recall_requests: 6,
        evidence_hits: 23,
        hint_hits: 11,
        reference_hits: 7,
      },
    });

    await renderOperationsPage({ summary: () => jsonResponse(summary) });

    expect(pipelineMetric("Held for review").value).toBe("2");
    expect(pipelineMetric("Failed").value).toBe("3");
    expect(pipelineMetric("Waiting").value).toBe("4");
    expect(pipelineMetric("Typical delay").value).toBe("50 ms");
    expect(pipelineMetric("Worst case").value).toBe("90 ms");
    expect(pipelineMetric("Recalls refused").value).toBe("6");
    // 排队中's subtitle is the relative time from oldest_unextracted_at as of
    // generated_at (GEN_AT = "2026-07-22T12:00:01Z" from operationsFixtures),
    // not from oldest_unextracted_at to wall-clock now. 11:50:00 to 12:00:01
    // is 601s, which floors to 10 minutes -- swapping the time source for
    // Date.now() would make this assertion fail (or flake).
    expect(pipelineMetric("Waiting").sub).toBe("Oldest unextracted event: 10m ago");
    // summary.errors (a broader, cross-operation error count than the
    // headline extraction.failed number) has no cell of its own -- it must
    // still show up in "失败"'s subtitle, not silently vanish. Not pinning
    // the exact wording (it may be edited later), just that the field's
    // value actually reaches the DOM.
    expect(pipelineMetric("Failed").sub).toContain("8");
  });
});

describe("Pipeline shell: block-level error isolation survives the redraw", () => {
  it("summary 区块失败时，另两个区块仍渲染", async () => {
    await renderOperationsPage({
      summary: () => apiErrorResponse(500, "internal_error", "boom"),
      events: () => jsonResponse(eventsPage([makeEvent()])),
    });

    // The six-cell bar doesn't render at all when summary errors.
    expect(document.querySelector(".gv-metrics")).toBeNull();
    screen.getByText("Server error; try again later");
    // Events and storage are unaffected.
    within(eventsTable()).getByText("Memory Search");
    screen.getByText("database physical");
  });

  it("events 区块失败时，另两个区块仍渲染", async () => {
    await renderOperationsPage({
      events: () => apiErrorResponse(500, "internal_error", "boom"),
    });

    // Summary and storage are unaffected; the events region shows its own
    // retryable error instead.
    expect(pipelineMetric("Held for review").value).toBe("1");
    screen.getByText("database physical");
    screen.getByText("Server error; try again later");
  });

  it("storage 区块失败时，另两个区块仍渲染", async () => {
    await renderOperationsPage({
      storage: () => apiErrorResponse(500, "internal_error", "boom"),
      events: () => jsonResponse(eventsPage([makeEvent()])),
    });

    // Summary and events are unaffected; storage shows its own retryable
    // error instead.
    expect(pipelineMetric("Held for review").value).toBe("1");
    within(eventsTable()).getByText("Memory Search");
    screen.getByText("Server error; try again later");
  });
});

// The Operations page's window selector had no coverage at all before this,
// and it hand-rolled its own seg with a hardcoded ["1h","24h","7d"] instead of
// sharing the presets. It now renders the shared Seg from the same
// timeWindowOptions() the Overview page uses (issue #86).
describe("Pipeline time window selector", () => {
  it("offers every window when the deployment keeps a week", async () => {
    await renderOperationsPage({ summary: () => jsonResponse(makeSummary()) });

    for (const label of ["1h", "24h", "7d"]) {
      const button = screen.getByRole("button", { name: label }) as HTMLButtonElement;
      expect(button.disabled).toBe(false);
      expect(button.getAttribute("title")).toBeNull();
    }
    expect(screen.queryByText(/^Retained for /)).toBeNull();
  });

  it("disables 7d and names the ceiling on a 24h-retention deployment", async () => {
    await renderOperationsPage({
      summary: () => jsonResponse(makeSummary({ event_retention_seconds: 24 * 60 * 60 })),
    });

    const sevenDay = screen.getByRole("button", { name: "7d" }) as HTMLButtonElement;
    expect(sevenDay.disabled).toBe(true);
    expect(sevenDay.getAttribute("title")).toBe("This deployment keeps 24h of events");
    // Visible, not hover-only -- see the same assertion on the Overview page.
    screen.getByText("Retained for 24h");
    expect((screen.getByRole("button", { name: "24h" }) as HTMLButtonElement).disabled).toBe(false);
  });
});
