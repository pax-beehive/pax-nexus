import { Button } from "../../components/Button";

export interface WikiIngestionCardProps {
  autoInject: boolean;
  loading: boolean;
  busy: boolean;
  sessionID: string;
  message: string;
  isOwner: boolean;
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
  onSessionIDChange,
  onToggleAutoInject,
  onInjectFixedSession,
  onOpenRebuild,
}: WikiIngestionCardProps) {
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
          <Button variant="danger" type="button" disabled={busy} onClick={onOpenRebuild}>
            Reset & rebuild
          </Button>
        </div>
      )}
      {message && <p className="wiki-ingestion-message">{message}</p>}
    </section>
  );
}
