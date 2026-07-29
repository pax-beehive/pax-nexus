// Recall detail drawer (operations doc section 9); state lives in React
// memory only. 404/410 are terminal lifecycle states and are never retried.

import { useEffect, useState } from "react";
import { apiError } from "../../api/client";
import { getRecallDiagnostic } from "../../api/queries";
import type { RecallDiagnostic } from "../../api/types";
import { formatTime } from "../../lib/format";
import { isAbortError } from "../../lib/usePolling";
import { RegionError } from "../../components/RegionError";
import { Stat } from "./SummaryCards";

type DrawerState =
  | { status: "loading" }
  | { status: "ready"; recall: RecallDiagnostic }
  | { status: "not-found" }
  | { status: "expired" }
  | { status: "error"; error: unknown };

/** Count maps render generically so unknown reason/lane codes survive (doc 5). */
function CountMap({ title, counts }: { title: string; counts: Record<string, number> }) {
  const entries = Object.entries(counts);
  return (
    <div>
      <h3>{title}</h3>
      {entries.length === 0 ? (
        <p className="faint small">None</p>
      ) : (
        <div className="chips">
          {entries.map(([key, value]) => (
            <code key={key}>
              {key}: {value}
            </code>
          ))}
        </div>
      )}
    </div>
  );
}

export function RecallDrawer({
  observationId,
  onClose,
  onAuthError,
}: {
  observationId: number;
  onClose: () => void;
  onAuthError: (err: unknown) => void;
}) {
  const [state, setState] = useState<DrawerState>({ status: "loading" });
  const [epoch, setEpoch] = useState(0);

  useEffect(() => {
    const controller = new AbortController();
    setState({ status: "loading" });
    getRecallDiagnostic(observationId, controller.signal)
      .then((recall) => setState({ status: "ready", recall }))
      .catch((err: unknown) => {
        if (isAbortError(err)) return;
        // 404/410 are terminal lifecycle states and are never retried (doc 9).
        if (apiError(err, 404)) {
          setState({ status: "not-found" });
        } else if (apiError(err, 410)) {
          setState({ status: "expired" });
        } else {
          onAuthError(err);
          setState({ status: "error", error: err });
        }
      });
    return () => controller.abort();
  }, [observationId, epoch, onAuthError]);

  return (
    <>
      <div className="drawer-backdrop" onClick={onClose} />
      <aside className="drawer">
        <div className="row between" style={{ marginBottom: 12 }}>
          <h2 style={{ margin: 0 }}>Recall #{observationId}</h2>
          <button className="btn ghost sm" onClick={onClose}>
            Close
          </button>
        </div>
        {state.status === "loading" && <p className="muted small">Loading…</p>}
        {state.status === "not-found" && (
          <div className="note warn">
            Diagnostic not found: it was never recorded, or both the Operation Event and its
            diagnostic are past retention. The event in the list remains valid.
          </div>
        )}
        {state.status === "expired" && (
          <div className="note warn">
            The diagnostic has expired or been cleaned up (diagnostic_expired); the safe event
            in the list remains valid.
          </div>
        )}
        {state.status === "error" && (
          <RegionError error={state.error} onRetry={() => setEpoch((e) => e + 1)} />
        )}
        {state.status === "ready" && <RecallView recall={state.recall} />}
      </aside>
    </>
  );
}

function RecallView({ recall }: { recall: RecallDiagnostic }) {
  return (
    <>
      <div className="stat-grid" style={{ marginBottom: 14 }}>
        <Stat
          label="occurred at"
          value={
            <span className="small" title={recall.occurred_at}>
              {formatTime(recall.occurred_at)}
            </span>
          }
        />
        <Stat label="agent" value={<code>{recall.agent_id}</code>} />
        <Stat label="session" value={<code>{recall.session_id}</code>} />
        <Stat label="duration" value={`${recall.duration_ms} ms`} />
        <Stat label="token budget" value={recall.token_budget} />
        <Stat label="planned tokens" value={recall.planned_tokens} />
        <Stat label="max items" value={recall.max_items} />
        <Stat
          label="evidence sufficient"
          value={
            <span className={`badge ${recall.evidence_sufficient ? "b-active" : "b-suspended"}`}>
              {recall.evidence_sufficient ? "yes" : "no"}
            </span>
          }
        />
      </div>
      <h3>Delivery funnel</h3>
      <div className="funnel">
        <Stat label="candidates" value={recall.candidates} />
        <span className="funnel-arrow">→</span>
        <Stat label="fusion kept" value={recall.fusion_kept} />
        <span className="funnel-arrow">→</span>
        <Stat label="planned notes" value={recall.planned_notes} />
        <span className="funnel-arrow">→</span>
        <Stat label="delivered" value={recall.delivered_items} />
      </div>
      <h3>Lanes executed</h3>
      {recall.lanes_executed.length === 0 ? (
        <p className="faint small">None</p>
      ) : (
        <div className="chips">
          {recall.lanes_executed.map((lane) => (
            <code key={lane}>{lane}</code>
          ))}
        </div>
      )}
      <h3>Reason codes</h3>
      {recall.reason_codes.length === 0 ? (
        <p className="faint small">None</p>
      ) : (
        <div className="chips">
          {recall.reason_codes.map((code) => (
            <code key={code}>{code}</code>
          ))}
        </div>
      )}
      <CountMap title="Disposition counts" counts={recall.disposition_counts} />
      <CountMap title="Rejection counts" counts={recall.rejection_counts} />
      <CountMap title="Budget drop counts" counts={recall.budget_drop_counts} />
      <CountMap title="Hard gate failure counts" counts={recall.hard_gate_failure_counts} />
    </>
  );
}
