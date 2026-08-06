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
    // least one of these assertions.
    const summary = makeSummary({
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

    expect(pipelineMetric("扣下待查").value).toBe("2");
    expect(pipelineMetric("失败").value).toBe("3");
    expect(pipelineMetric("排队中").value).toBe("4");
    expect(pipelineMetric("典型延迟").value).toBe("50 ms");
    expect(pipelineMetric("最坏情况").value).toBe("90 ms");
    expect(pipelineMetric("空手而归").value).toBe("6");
    // 排队中's subtitle is the relative time from oldest_unextracted_at as of
    // generated_at (GEN_AT = "2026-07-22T12:00:01Z" from operationsFixtures),
    // not from oldest_unextracted_at to wall-clock now. 11:50:00 to 12:00:01
    // is 601s, which floors to 10 minutes -- swapping the time source for
    // Date.now() would make this assertion fail (or flake).
    expect(pipelineMetric("排队中").sub).toBe("最老的未抽取事件：10 分钟前");
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
    expect(pipelineMetric("扣下待查").value).toBe("1");
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
    expect(pipelineMetric("扣下待查").value).toBe("1");
    within(eventsTable()).getByText("Memory Search");
    screen.getByText("Server error; try again later");
  });
});
