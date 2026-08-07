// Pure presentation helpers for the Operations console. Every enum is
// forward-compatible (operations doc section 5): known values get friendly
// labels, unknown values render as the raw safe code and never throw.

import type {
  OperationKind,
  OperationOutcome,
  OperationsStorageSnapshot,
  StorageComponent,
} from "../api/types";

export type TimeWindowPreset = "1h" | "24h" | "7d";

export const TIME_WINDOW_PRESETS: TimeWindowPreset[] = ["1h", "24h", "7d"];

const PRESET_MS: Record<TimeWindowPreset, number> = {
  "1h": 60 * 60 * 1000,
  "24h": 24 * 60 * 60 * 1000,
  "7d": 7 * 24 * 60 * 60 * 1000,
};

/**
 * "7d" / "36h" / "90m" — whole units only, largest that divides evenly.
 *
 * Days only from two days up: a 24h retention is configured as `24h`, and the
 * window preset the user is looking at is labelled `24h`, so rendering it as
 * "1d" would answer in a vocabulary that appears nowhere else on the screen.
 */
export function formatRetention(seconds: number): string {
  if (seconds % 86_400 === 0 && seconds >= 2 * 86_400) return `${seconds / 86_400}d`;
  if (seconds % 3_600 === 0) return `${seconds / 3_600}h`;
  if (seconds % 60 === 0) return `${seconds / 60}m`;
  return `${seconds}s`;
}

export interface TimeWindowOption {
  value: TimeWindowPreset;
  label: string;
  disabled: boolean;
  title?: string;
}

/**
 * The window presets, with any window longer than the deployment's event
 * retention disabled and labelled with the actual ceiling.
 *
 * `retentionSeconds` is undefined when no response has landed yet, or when the
 * backend predates the field. Unknown means "offer everything": guessing a
 * ceiling would hide windows that do work, which is worse than the status quo
 * of a request that fails. A window equal to retention is offered -- the
 * backend rejects strictly greater (issue #86).
 *
 * A non-positive or non-finite value is treated as unknown for the same
 * reason. The backend floor is 24h so it should never arrive, but taking it
 * literally would disable every window and label the reason "0s", leaving the
 * page with no working option at all -- a strictly worse failure than the one
 * this function exists to prevent.
 */
export function timeWindowOptions(retentionSeconds?: number): TimeWindowOption[] {
  const known =
    retentionSeconds !== undefined && Number.isFinite(retentionSeconds) && retentionSeconds > 0;
  return TIME_WINDOW_PRESETS.map((preset) => {
    const overRetention = known && PRESET_MS[preset] > (retentionSeconds as number) * 1000;
    return {
      value: preset,
      label: preset,
      disabled: overRetention,
      title: overRetention
        ? `This deployment keeps ${formatRetention(retentionSeconds as number)} of events`
        : undefined,
    };
  });
}

/** Sliding UTC window for a preset, emitted as RFC3339 (operations doc 4.1). */
export function timeWindow(
  preset: TimeWindowPreset,
  now: Date = new Date(),
): { from: string; to: string } {
  return {
    from: new Date(now.getTime() - PRESET_MS[preset]).toISOString(),
    to: now.toISOString(),
  };
}

export const OPERATION_KINDS: OperationKind[] = [
  "observation.observe",
  "memory.search",
  "memory.get",
  "team_note.recall",
  "extraction.run",
  "channel.send",
  "channel.accept",
  "channel.archive",
  "system.retention",
];

export const OPERATION_OUTCOMES: OperationOutcome[] = [
  "succeeded",
  "rejected",
  "failed",
  "timed_out",
  "cancelled",
];

// UI labels from operations doc section 8.
const KIND_LABELS: Record<OperationKind, string> = {
  "observation.observe": "Observation",
  "memory.search": "Memory Search",
  "memory.get": "Memory Get",
  "team_note.recall": "Team Note Recall",
  "extraction.run": "Extraction",
  "channel.send": "Capsule Send",
  "channel.accept": "Capsule Accept",
  "channel.archive": "Capsule Archive",
  "system.retention": "Retention",
};

export function operationKindLabel(kind: string): string {
  return KIND_LABELS[kind as OperationKind] ?? kind;
}

export type OutcomeTone = "ok" | "warn" | "bad" | "muted";

export function operationOutcomeTone(outcome: string): OutcomeTone {
  switch (outcome) {
    case "succeeded":
      return "ok";
    case "rejected":
      return "warn";
    case "failed":
    case "timed_out":
      return "bad";
    default:
      return "muted";
  }
}

/**
 * Badge class per outcome tone, shared by the operations pages.
 *
 * `bad` deliberately does NOT reuse `b-retired`: that class also marks a
 * retired agent, which is a terminal-but-normal state and must stay neutral,
 * whereas a failed or timed-out operation must read as needing attention.
 * They shared a class once, which silently painted `failed` the same grey as
 * `succeeded`. `b-failed` is accent-styled in components.css's legacy compat
 * block alongside b-suspended / b-pending / b-risk-* / b-approval-*.
 */
export const TONE_BADGE: Record<OutcomeTone, string> = {
  ok: "b-active",
  warn: "b-suspended",
  bad: "b-failed",
  muted: "b-expired",
};

const RECALL_KINDS = new Set<string>(["memory.search", "memory.get", "team_note.recall"]);

/**
 * An empty successful recall is still `succeeded` (operations doc section 7)
 * and must render differently from a failed one.
 */
export function isEmptyRecall(event: {
  operation_kind: string;
  outcome: string;
  result_items: number;
}): boolean {
  return event.outcome === "succeeded" && event.result_items === 0 && RECALL_KINDS.has(event.operation_kind);
}

/**
 * Only `recall_observation` details with a positive-integer id may open the
 * recall drawer; channel/extraction details have no endpoint (doc section 8).
 */
export function recallObservationId(event: {
  detail_kind?: string;
  detail_id?: string;
}): number | undefined {
  if (event.detail_kind !== "recall_observation" || event.detail_id === undefined) {
    return undefined;
  }
  if (!/^[1-9]\d*$/.test(event.detail_id)) return undefined;
  const id = Number(event.detail_id);
  return Number.isSafeInteger(id) ? id : undefined;
}

export const KNOWN_STORAGE_SCHEMA_VERSION = 1;

// Known storage components (operations doc section 10); unknown components
// fall back to the raw code.
const COMPONENT_LABELS: Record<string, string> = {
  session_lake: "Session Lake",
  extraction: "Extraction",
  team_memory: "Team Memory",
  recall_diagnostics: "Recall Diagnostics",
  capsule_channel: "Capsule Channel",
  identity_audit: "Identity & Audit",
  operations: "Operations",
};

export function storageComponentLabel(component: string): string {
  return COMPONENT_LABELS[component] ?? component;
}

export const STORAGE_STALE_MS = 2 * 60 * 60 * 1000;

/**
 * Snapshots older than two hours are flagged stale, never declared a
 * database failure -- deployments may tune the interval (doc section 10).
 */
export function isSnapshotStale(capturedAt: string, now: number = Date.now()): boolean {
  const captured = new Date(capturedAt).getTime();
  if (Number.isNaN(captured)) return false;
  return now - captured > STORAGE_STALE_MS;
}

export interface ComponentAvailability {
  logical: boolean;
  physical: boolean;
}

/**
 * In a partial snapshot a failed component may still appear with zeroed
 * counts/bytes; the warning codes name which measurement is unavailable so
 * the UI never shows those zeros as a healthy empty database (doc 10/12).
 */
export function componentAvailability(
  snapshot: OperationsStorageSnapshot,
  component: StorageComponent,
): ComponentAvailability {
  if (snapshot.status === "complete") return { logical: true, physical: true };
  const codes = snapshot.warning_codes ?? [];
  return {
    logical: !codes.includes(`${component.component}_logical_unavailable`),
    physical: !codes.includes(`${component.component}_relation_size_unavailable`),
  };
}
