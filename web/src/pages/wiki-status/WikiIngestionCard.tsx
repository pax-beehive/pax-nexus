import { Button } from "../../components/Button";
import type { WikiRebuildState } from "../../api/wiki";

export interface WikiIngestionCardProps {
  autoInject: boolean;
  loading: boolean;
  busy: boolean;
  sessionID: string;
  message: string;
  isOwner: boolean;
  rebuildState?: WikiRebuildState;
  rebuildError?: string;
  onSessionIDChange: (value: string) => void;
  onToggleAutoInject: () => Promise<void>;
  onInjectFixedSession: () => Promise<void>;
  onOpenRebuild: () => void;
}

export function WikiIngestionCard({
  autoInject,
  loading,
  busy,
  sessionID,
  message,
  isOwner,
  rebuildState,
  rebuildError,
  onSessionIDChange,
  onToggleAutoInject,
  onInjectFixedSession,
  onOpenRebuild,
}: WikiIngestionCardProps) {
  const rebuildActive = rebuildState === "queued" || rebuildState === "running";
  return (
    <section className="card wiki-ingestion" aria-label="Wiki ingestion controls">
      <div className="wiki-ingestion-copy">
        <span className="wiki-eyebrow">Session Lake</span>
        <strong>Automatic Wiki injection</strong>
        <span className="muted small">
          Uses an independent PageWiki cursor; Team Note extraction is unaffected.
        </span>
      </div>
      <button
        className={autoInject ? "wiki-switch active" : "wiki-switch"}
        type="button"
        role="switch"
        aria-checked={autoInject}
        disabled={loading || busy}
        onClick={() => void onToggleAutoInject()}
      >
        <span aria-hidden="true" />
        {autoInject ? "On" : "Off"}
      </button>
      <div className="wiki-fixed-session">
        <label htmlFor="wiki-session-id">Fixed session ID</label>
        <input
          id="wiki-session-id"
          value={sessionID}
          placeholder="e.g. 019fa46f-…"
          onChange={(event) => onSessionIDChange(event.target.value)}
        />
        <Button
          variant="primary"
          type="button"
          disabled={busy || sessionID.trim() === ""}
          onClick={() => void onInjectFixedSession()}
        >
          {busy ? "Injecting…" : "Inject session"}
        </Button>
      </div>
      {isOwner && (
        <div className="wiki-reset">
          <div>
            <strong>Start over with current Session Lake evidence</strong>
            <span className="muted small">
              Clears PageWiki-derived data and rebuilds it with the currently configured organizer.
            </span>
          </div>
          {rebuildActive && (
            <span className="muted small" role="status">
              {rebuildState === "queued" ? "Rebuild queued…" : "Rebuild in progress…"}
            </span>
          )}
          {rebuildState === "failed" && rebuildError && (
            <span className="muted small" role="alert">
              Rebuild failed: {rebuildError}
            </span>
          )}
          <Button
            variant="danger"
            type="button"
            disabled={busy || rebuildActive}
            onClick={onOpenRebuild}
          >
            Reset & rebuild
          </Button>
        </div>
      )}
      {message && <p className="wiki-ingestion-message">{message}</p>}
    </section>
  );
}
