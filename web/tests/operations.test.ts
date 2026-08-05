import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";
import {
  componentAvailability,
  isEmptyRecall,
  isSnapshotStale,
  KNOWN_STORAGE_SCHEMA_VERSION,
  operationKindLabel,
  operationOutcomeTone,
  recallObservationId,
  storageComponentLabel,
  STORAGE_STALE_MS,
  timeWindow,
  TONE_BADGE,
} from "../src/lib/operations";
import type { OperationsStorageSnapshot, StorageComponent } from "../src/api/types";

describe("timeWindow (operations doc 4.1)", () => {
  const now = new Date("2026-07-22T12:00:00.000Z");

  it.each([
    ["1h", 60 * 60 * 1000],
    ["24h", 24 * 60 * 60 * 1000],
    ["7d", 7 * 24 * 60 * 60 * 1000],
  ] as const)("%s spans exactly the preset duration", (preset, ms) => {
    const { from, to } = timeWindow(preset, now);
    expect(to).toBe("2026-07-22T12:00:00.000Z");
    expect(new Date(to).getTime() - new Date(from).getTime()).toBe(ms);
  });

  it("emits RFC3339 UTC strings with from earlier than to", () => {
    const { from, to } = timeWindow("24h", now);
    expect(from).toBe("2026-07-21T12:00:00.000Z");
    expect(new Date(from).getTime()).toBeLessThan(new Date(to).getTime());
  });
});

describe("operationKindLabel (forward-compatible enum, doc 5/8)", () => {
  it("labels every known kind", () => {
    expect(operationKindLabel("observation.observe")).toBe("Observation");
    expect(operationKindLabel("memory.search")).toBe("Memory Search");
    expect(operationKindLabel("memory.get")).toBe("Memory Get");
    expect(operationKindLabel("team_note.recall")).toBe("Team Note Recall");
    expect(operationKindLabel("extraction.run")).toBe("Extraction");
    expect(operationKindLabel("channel.send")).toBe("Capsule Send");
    expect(operationKindLabel("channel.accept")).toBe("Capsule Accept");
    expect(operationKindLabel("channel.archive")).toBe("Capsule Archive");
    expect(operationKindLabel("system.retention")).toBe("Retention");
  });

  it("falls back to the raw code for unknown kinds", () => {
    expect(operationKindLabel("future.operation")).toBe("future.operation");
  });
});

describe("operationOutcomeTone", () => {
  it("maps known outcomes", () => {
    expect(operationOutcomeTone("succeeded")).toBe("ok");
    expect(operationOutcomeTone("rejected")).toBe("warn");
    expect(operationOutcomeTone("failed")).toBe("bad");
    expect(operationOutcomeTone("timed_out")).toBe("bad");
    expect(operationOutcomeTone("cancelled")).toBe("muted");
  });

  it("treats unknown outcomes as muted instead of throwing", () => {
    expect(operationOutcomeTone("exploded")).toBe("muted");
  });
});

// A failed operation must be visibly distinguishable from a successful one.
// Asserting the two class strings differ is not enough — `bad` used to map to
// `b-retired`, a *different* string that the stylesheet nonetheless leaves at
// the neutral `.badge` default, so `failed` and `succeeded` rendered as the
// same grey. These assertions therefore read components.css: the `bad` class
// must appear in the accent selector list and the `ok` class must not.
describe("TONE_BADGE paints failure differently from success", () => {
  const components = readFileSync(
    new URL("../src/styles/components.css", import.meta.url),
    "utf8",
  ).replace(/\/\*[\s\S]*?\*\//g, ""); // comments out of the way before parsing

  /**
   * Every `.b-*` selector the stylesheet paints with the accent background.
   * Anything absent falls through to the neutral `.badge` default.
   */
  const accentSelectors = new Set<string>();
  for (const rule of components.split("}")) {
    const [selector, body] = rule.split("{");
    if (body === undefined) continue;
    if (!body.includes("background: var(--color-accent-100)")) continue;
    for (const part of selector.split(",")) {
      const name = part.trim();
      if (name.startsWith(".b-")) accentSelectors.add(name);
    }
  }

  it("gives the bad tone a class the stylesheet paints with the accent", () => {
    expect(accentSelectors.size).toBeGreaterThan(0);
    expect(accentSelectors.has(`.${TONE_BADGE.bad}`)).toBe(true);
  });

  it("leaves the ok tone at the neutral default", () => {
    expect(accentSelectors.has(`.${TONE_BADGE.ok}`)).toBe(false);
  });

  it("does not reuse the retired-agent class, which must stay neutral", () => {
    // `retired` is a terminal-but-normal agent state; painting it as
    // attention would be wrong, so `bad` needs its own class.
    expect(TONE_BADGE.bad).not.toBe("b-retired");
    expect(accentSelectors.has(".b-retired")).toBe(false);
  });

  it("keeps every tone on a distinct class", () => {
    const classes = Object.values(TONE_BADGE);
    expect(new Set(classes).size).toBe(classes.length);
  });
});

describe("isEmptyRecall (doc 7/12: empty success differs from failure)", () => {
  it("flags zero-result successful recall kinds", () => {
    expect(
      isEmptyRecall({ operation_kind: "memory.search", outcome: "succeeded", result_items: 0 }),
    ).toBe(true);
    expect(
      isEmptyRecall({ operation_kind: "team_note.recall", outcome: "succeeded", result_items: 0 }),
    ).toBe(true);
  });

  it("does not flag failed recalls or non-recall kinds", () => {
    expect(
      isEmptyRecall({ operation_kind: "memory.search", outcome: "failed", result_items: 0 }),
    ).toBe(false);
    expect(
      isEmptyRecall({ operation_kind: "observation.observe", outcome: "succeeded", result_items: 0 }),
    ).toBe(false);
    expect(
      isEmptyRecall({ operation_kind: "memory.search", outcome: "succeeded", result_items: 3 }),
    ).toBe(false);
  });
});

describe("recallObservationId (doc 8: only recall_observation opens the drawer)", () => {
  it("parses a positive integer detail id", () => {
    expect(
      recallObservationId({ detail_kind: "recall_observation", detail_id: "41" }),
    ).toBe(41);
  });

  it("rejects channel and extraction details", () => {
    expect(recallObservationId({ detail_kind: "channel_envelope", detail_id: "41" })).toBeUndefined();
    expect(recallObservationId({ detail_kind: "extraction_run", detail_id: "41" })).toBeUndefined();
  });

  it("rejects missing or malformed ids", () => {
    expect(recallObservationId({ detail_kind: "recall_observation" })).toBeUndefined();
    expect(recallObservationId({ detail_kind: "recall_observation", detail_id: "0" })).toBeUndefined();
    expect(recallObservationId({ detail_kind: "recall_observation", detail_id: "-3" })).toBeUndefined();
    expect(recallObservationId({ detail_kind: "recall_observation", detail_id: "4.5" })).toBeUndefined();
    expect(recallObservationId({ detail_kind: "recall_observation", detail_id: "abc" })).toBeUndefined();
    expect(recallObservationId({})).toBeUndefined();
  });
});

describe("storage helpers (doc 10)", () => {
  const component = (over: Partial<StorageComponent>): StorageComponent => ({
    component: "recall_diagnostics",
    counts: {},
    logical_bytes: 0,
    physical_bytes: 0,
    ...over,
  });

  const snapshot = (over: Partial<OperationsStorageSnapshot>): OperationsStorageSnapshot => ({
    snapshot_id: 1,
    schema_version: KNOWN_STORAGE_SCHEMA_VERSION,
    captured_at: "2026-07-22T12:00:00Z",
    status: "complete",
    warning_codes: [],
    database_physical_bytes: 0,
    other_physical_bytes: 0,
    components: [],
    ...over,
  });

  it("labels known components and falls back to the raw name", () => {
    expect(storageComponentLabel("session_lake")).toBe("Session Lake");
    expect(storageComponentLabel("identity_audit")).toBe("Identity & Audit");
    expect(storageComponentLabel("future_component")).toBe("future_component");
  });

  it("flags snapshots older than two hours as stale, never younger ones", () => {
    const now = new Date("2026-07-22T12:00:00Z").getTime();
    expect(
      isSnapshotStale(new Date(now - STORAGE_STALE_MS - 1000).toISOString(), now),
    ).toBe(true);
    expect(isSnapshotStale(new Date(now - 60 * 60 * 1000).toISOString(), now)).toBe(false);
  });

  it("treats an unparseable captured_at as not stale", () => {
    expect(isSnapshotStale("not-a-date")).toBe(false);
  });

  it("complete snapshots report every component measurement available", () => {
    const avail = componentAvailability(snapshot({ status: "complete" }), component({}));
    expect(avail).toEqual({ logical: true, physical: true });
  });

  it("partial snapshots mark warned measurements unavailable so zeros are not shown as healthy", () => {
    const snap = snapshot({
      status: "partial",
      warning_codes: [
        "recall_diagnostics_logical_unavailable",
        "session_lake_relation_size_unavailable",
        "unknown_future_warning",
      ],
    });
    expect(componentAvailability(snap, component({}))).toEqual({ logical: false, physical: true });
    expect(componentAvailability(snap, component({ component: "session_lake" }))).toEqual({
      logical: true,
      physical: false,
    });
    expect(componentAvailability(snap, component({ component: "team_memory" }))).toEqual({
      logical: true,
      physical: true,
    });
  });
});
