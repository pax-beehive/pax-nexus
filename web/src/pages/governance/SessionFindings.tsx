// Findings row-cards for /governance/sessions (design phase 5 §2.2). Replaces
// the original table view: severity tag, humanized title + summary + raw
// kind/evidence ref line, agent/session IDs, and a "看这些调用" action that
// jumps to the tool-calls view carrying this finding's session_id.
import type { SessionAuditFinding } from "../../api/types";
import { Button } from "../../components/Button";
import { RegionError } from "../../components/RegionError";
import { Tag } from "../../components/Tag";

const FINDING_KIND_LABELS: Record<string, string> = {
  high_risk_unapproved: "High-risk unapproved",
  denied_tool_executed: "Denied tool executed",
  visibility_unknown: "Visibility unknown",
  attribution_missing: "Attribution missing",
};

export function findingKindLabel(kind: string): string {
  return FINDING_KIND_LABELS[kind] ?? kind;
}

/** Severity and risk level share a vocabulary; only high/critical get accent. */
function isAccentSeverity(severity: string): boolean {
  return severity === "high" || severity === "critical";
}

/** IDs stay copyable via the title tooltip while the row keeps its width. */
function TruncatedId({ value }: { value: string }) {
  return (
    <code className="session-audit-truncate" title={value}>
      {value}
    </code>
  );
}

export interface SessionFindingsListState {
  items: SessionAuditFinding[];
  loading: boolean;
  error: unknown;
  reload: () => void;
}

export function SessionFindingsList({
  state,
  onInspectToolCalls,
}: {
  state: SessionFindingsListState;
  onInspectToolCalls: (sessionId: string) => void;
}) {
  if (state.loading) {
    return (
      <div className="card">
        <p className="muted small">Loading…</p>
      </div>
    );
  }
  if (state.error) {
    return (
      <div className="card">
        <RegionError error={state.error} onRetry={state.reload} />
      </div>
    );
  }
  if (state.items.length === 0) {
    return (
      <div className="card">
        <p className="muted small">No matching findings.</p>
      </div>
    );
  }
  return (
    <div className="card">
      {state.items.map((f) => (
        <div className="gv-finding" key={f.finding_id}>
          <Tag tone={isAccentSeverity(f.severity) ? "attention" : "neutral"}>{f.severity}</Tag>
          <div>
            <div className="gv-finding-title">{findingKindLabel(f.kind)}</div>
            <div className="gv-finding-summary">{f.summary}</div>
            <div className="gv-finding-ref">
              {f.kind} · evidence{" "}
              {f.evidence_event_ids.length === 0 ? "—" : f.evidence_event_ids.join(", ")}
            </div>
          </div>
          <div className="small">
            <TruncatedId value={f.agent_id} /> <span className="faint">/</span>{" "}
            <TruncatedId value={f.session_id} />
          </div>
          <Button size="sm" onClick={() => onInspectToolCalls(f.session_id)}>
            See the calls
          </Button>
        </div>
      ))}
    </div>
  );
}
