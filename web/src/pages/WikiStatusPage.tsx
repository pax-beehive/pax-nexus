import { useEffect, useState } from "react";
import { useLocation, useNavigate } from "react-router-dom";
import type { HumanMe } from "../api/types";
import { getWikiIngestionStatus, type WikiIngestionStatus } from "../api/wiki";
import { beginAction, injectWikiSession, rebuildWiki, setWikiAutoInject } from "../api/actions";
import { ConfirmDialog } from "../components/ConfirmDialog";
import { formatTime } from "../lib/format";
import { isAbortError, usePolling } from "../lib/usePolling";
import { useErrorHandler } from "../lib/useErrorHandler";

export function WikiStatusPage({ me }: { me: HumanMe }) {
  const navigate = useNavigate();
  const location = useLocation();
  const handleError = useErrorHandler();
  const [status, setStatus] = useState<WikiIngestionStatus>();
  const [statusError, setStatusError] = useState(false);
  const [autoInject, setAutoInject] = useState(false);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [sessionID, setSessionID] = useState(
    () => new URLSearchParams(window.location.search).get("session") ?? "",
  );
  const [message, setMessage] = useState("");
  const [rebuildOpen, setRebuildOpen] = useState(false);

  // Legacy deep links: /wiki?page=<slug> used to render the wiki inline
  // here. Forward the whole query string so revision links keep working.
  const legacyPage = new URLSearchParams(location.search).get("page");
  useEffect(() => {
    if (legacyPage) {
      navigate({ pathname: "/wiki/browse", search: location.search }, { replace: true });
    }
  }, [legacyPage, location.search, navigate]);

  usePolling(
    async (signal) => {
      try {
        const loaded = await getWikiIngestionStatus(signal);
        setStatus(loaded);
        setAutoInject(loaded.auto_inject);
        setStatusError(false);
      } catch (error) {
        if (isAbortError(error)) return;
        setStatusError(true);
      } finally {
        setLoading(false);
      }
    },
    5000,
    [],
  );

  const toggleAutoInject = async () => {
    setBusy(true);
    setMessage("");
    try {
      const updated = await setWikiAutoInject(!autoInject);
      setAutoInject(updated.auto_inject);
      setMessage(
        updated.auto_inject
          ? "Auto inject is on. New Session Lake evidence will be organized into the wiki."
          : "Auto inject is off.",
      );
    } catch (error) {
      handleError(error);
    } finally {
      setBusy(false);
    }
  };

  const injectFixedSession = async () => {
    const fixedSessionID = sessionID.trim();
    if (!fixedSessionID) return;
    setBusy(true);
    setMessage("");
    try {
      const result = await injectWikiSession(fixedSessionID, beginAction());
      setMessage(
        `Injected ${result.processed_streams} stream${result.processed_streams === 1 ? "" : "s"} from ${fixedSessionID}.`,
      );
    } catch (error) {
      handleError(error);
    } finally {
      setBusy(false);
    }
  };

  const confirmRebuild = async () => {
    setBusy(true);
    setMessage("");
    try {
      const updated = await rebuildWiki(beginAction());
      setAutoInject(updated.auto_inject);
      setRebuildOpen(false);
      setMessage("Wiki cleared. Rebuilding from Session Lake…");
    } catch (error) {
      handleError(error);
    } finally {
      setBusy(false);
    }
  };

  const progressAvailable = !statusError && status?.pending_sessions !== undefined;

  return (
    <div className="wiki">
      <header className="wiki-header">
        <div>
          <span className="wiki-eyebrow">Grounded team knowledge</span>
          <h1>Wiki</h1>
          <p className="muted">Ingestion status and extraction progress for the team wiki.</p>
        </div>
        <button className="btn primary" type="button" onClick={() => navigate("/wiki/browse")}>
          Open Wiki
        </button>
      </header>

      <section className="card wiki-progress" aria-label="Extraction progress">
        <div className="wiki-ingestion-copy">
          <span className="wiki-eyebrow">Extraction</span>
          <strong>Progress</strong>
        </div>
        {progressAvailable ? (
          <div className="wiki-progress-stats">
            <div>
              <span className="muted small">Pending sessions</span>
              <strong className="wiki-progress-figure">{status?.pending_sessions}</strong>
            </div>
            <div>
              <span className="muted small">Last processed</span>
              <strong className="wiki-progress-figure">
                {status?.last_processed_at ? formatTime(status.last_processed_at) : "Never"}
              </strong>
            </div>
          </div>
        ) : (
          <p className="muted small">Progress is unavailable.</p>
        )}
      </section>

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
          onClick={() => void toggleAutoInject()}
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
            onChange={(event) => setSessionID(event.target.value)}
          />
          <button
            className="btn primary"
            type="button"
            disabled={busy || sessionID.trim() === ""}
            onClick={() => void injectFixedSession()}
          >
            {busy ? "Injecting…" : "Inject session"}
          </button>
        </div>
        {me.role === "owner" && (
          <div className="wiki-reset">
            <div>
              <strong>Start over with current Session Lake evidence</strong>
              <span className="muted small">
                Clears PageWiki-derived data and rebuilds it with the currently configured organizer.
              </span>
            </div>
            <button
              className="btn danger"
              type="button"
              disabled={busy}
              onClick={() => setRebuildOpen(true)}
            >
              Reset & rebuild
            </button>
          </div>
        )}
        {message && <p className="wiki-ingestion-message">{message}</p>}
      </section>

      {rebuildOpen && (
        <ConfirmDialog
          title="Reset and rebuild Wiki"
          consequences={[
            "All PageWiki pages, revisions, links, citations, and maintenance runs will be deleted.",
            "PageWiki ingestion cursors will reset and every Session Lake stream will be processed again.",
            "Session Lake events and Team Notes are preserved.",
            "An LLM-backed rebuild may make paid provider calls.",
          ]}
          confirmLabel="Confirm reset & rebuild"
          busy={busy}
          onConfirm={() => void confirmRebuild()}
          onClose={() => setRebuildOpen(false)}
        />
      )}
    </div>
  );
}
