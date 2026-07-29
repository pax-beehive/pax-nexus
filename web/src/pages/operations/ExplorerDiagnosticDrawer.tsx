// Explorer diagnostic drawer: a retained extraction run or capsule envelope
// opened from an operations event or a deep link.

import { useEffect, useState } from "react";
import { apiError } from "../../api/client";
import { getChannelDiagnostic, getExtractionDiagnostic } from "../../api/queries";
import type { ChannelDiagnostic, ExtractionDiagnostic } from "../../api/types";
import { isAbortError } from "../../lib/usePolling";
import { RegionError } from "../../components/RegionError";
import { ChannelDiagnosticView } from "./ChannelDiagnosticView";
import { ExtractionDiagnosticView } from "./ExtractionDiagnosticView";
import type { ExplorerDrawerTarget } from "./explorerTarget";

type ExplorerDrawerState =
  | { status: "loading" }
  | { status: "ready"; value: ExtractionDiagnostic | ChannelDiagnostic }
  | { status: "not-found" }
  | { status: "error"; error: unknown };

export function ExplorerDiagnosticDrawer({
  target,
  onClose,
  onAuthError,
}: {
  target: ExplorerDrawerTarget;
  onClose: () => void;
  onAuthError: (err: unknown) => void;
}) {
  const [state, setState] = useState<ExplorerDrawerState>({ status: "loading" });
  const [epoch, setEpoch] = useState(0);

  useEffect(() => {
    const controller = new AbortController();
    setState({ status: "loading" });
    const request =
      target.kind === "extraction"
        ? getExtractionDiagnostic(target.id, controller.signal)
        : getChannelDiagnostic(target.id, controller.signal);
    request
      .then((value) => setState({ status: "ready", value }))
      .catch((err: unknown) => {
        if (isAbortError(err)) return;
        if (apiError(err, 404)) {
          setState({ status: "not-found" });
          return;
        }
        onAuthError(err);
        setState({ status: "error", error: err });
      });
    return () => controller.abort();
  }, [target.kind, target.id, epoch, onAuthError]);

  const title = target.kind === "extraction" ? "Extraction diagnostic" : "Capsule diagnostic";
  return (
    <>
      <div className="drawer-backdrop" onClick={onClose} />
      <aside className="drawer">
        <div className="row between" style={{ marginBottom: 12 }}>
          <div>
            <h2 style={{ margin: 0 }}>{title}</h2>
            <code className="small">{target.id}</code>
          </div>
          <button className="btn ghost sm" onClick={onClose}>Close</button>
        </div>
        {state.status === "loading" && <p className="muted small">Loading…</p>}
        {state.status === "not-found" && (
          <div className="note warn">This retained diagnostic is no longer available.</div>
        )}
        {state.status === "error" && (
          <RegionError error={state.error} onRetry={() => setEpoch((value) => value + 1)} />
        )}
        {state.status === "ready" && target.kind === "extraction" && (
          <ExtractionDiagnosticView diagnostic={state.value as ExtractionDiagnostic} />
        )}
        {state.status === "ready" && target.kind === "channel" && (
          <ChannelDiagnosticView diagnostic={state.value as ChannelDiagnostic} />
        )}
      </aside>
    </>
  );
}
