import { useEffect, useState } from "react";
import { useLocation, useNavigate } from "react-router-dom";
import type { HumanMe } from "../api/types";
import {
  getLLMUsage,
  getWikiIngestionStatus,
  getWikiSettings,
  type LLMUsageRow,
  type WikiGenerationSettings,
  type WikiIngestionStatus,
} from "../api/wiki";
import {
  beginAction,
  injectWikiSession,
  rebuildWiki,
  setWikiAutoInject,
  updateWikiSettings,
} from "../api/actions";
import { Button } from "../components/Button";
import { isAbortError, usePolling } from "../lib/usePolling";
import { useErrorHandler } from "../lib/useErrorHandler";
import { WikiProgressCard } from "./wiki-status/WikiProgressCard";
import { WikiIngestionCard } from "./wiki-status/WikiIngestionCard";
import { CUSTOM_LANGUAGE, WikiGenerationCard } from "./wiki-status/WikiGenerationCard";
import { WikiLLMUsageCard } from "./wiki-status/WikiLLMUsageCard";
import { WikiRebuildDialog } from "./wiki-status/WikiRebuildDialog";

// Presets shown in the language select; "" is "Follow source evidence".
// Loading a language outside this list (i.e. previously set to a free-form
// value) falls back to the "Custom…" option with the value in a text input.
const LANGUAGE_PRESETS = ["", "简体中文", "English"];

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
  // Calendar cutoff for Reset & rebuild (YYYY-MM-DD); empty = full history.
  const [rebuildSince, setRebuildSince] = useState("");

  // Generation settings: loaded once (not polled — the editor owns the
  // value while the user works on it, and settings don't change underneath
  // them from elsewhere).
  const [language, setLanguage] = useState("");
  const [customLanguage, setCustomLanguage] = useState("");
  const [instructions, setInstructions] = useState("");
  const [settingsBusy, setSettingsBusy] = useState(false);
  const [settingsMessage, setSettingsMessage] = useState("");

  // LLM token usage card: reloaded whenever the window selector changes.
  const [usageDays, setUsageDays] = useState(7);
  const [usage, setUsage] = useState<LLMUsageRow[]>([]);
  const [usageError, setUsageError] = useState(false);

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

  const applySettings = (settings: WikiGenerationSettings) => {
    setInstructions(settings.custom_instructions);
    if (LANGUAGE_PRESETS.includes(settings.language)) {
      setLanguage(settings.language);
      setCustomLanguage("");
    } else {
      setLanguage(CUSTOM_LANGUAGE);
      setCustomLanguage(settings.language);
    }
  };

  useEffect(() => {
    const controller = new AbortController();
    getWikiSettings(controller.signal)
      .then(applySettings)
      .catch((error: unknown) => {
        if (!isAbortError(error)) handleError(error);
      });
    return () => controller.abort();
  }, [handleError]);

  useEffect(() => {
    const controller = new AbortController();
    getLLMUsage(usageDays, controller.signal)
      .then((result) => {
        setUsage(result.rows);
        setUsageError(false);
      })
      .catch((error: unknown) => {
        if (isAbortError(error)) return;
        setUsageError(true);
      });
    return () => controller.abort();
  }, [usageDays]);

  const saveSettings = async () => {
    setSettingsBusy(true);
    setSettingsMessage("");
    try {
      const effectiveLanguage = language === CUSTOM_LANGUAGE ? customLanguage.trim() : language;
      const updated = await updateWikiSettings({
        language: effectiveLanguage,
        custom_instructions: instructions.trim(),
      });
      applySettings(updated);
      setSettingsMessage(
        "Generation settings saved. They apply to future runs only; use Rebuild to regenerate existing pages.",
      );
    } catch (error) {
      handleError(error);
    } finally {
      setSettingsBusy(false);
    }
  };

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

  const closeRebuild = () => {
    setRebuildOpen(false);
    setRebuildSince("");
  };

  const confirmRebuild = async () => {
    const since = rebuildSince
      ? new Date(`${rebuildSince}T00:00:00`).toISOString()
      : undefined;
    setBusy(true);
    // Close before awaiting: the rebuild endpoint queues behind any
    // in-flight injection sweep and can take minutes to answer. The dialog
    // must not hold the page hostage while it waits; busy keeps the
    // ingestion-card controls disabled until the server confirms.
    closeRebuild();
    setMessage("Reset & rebuild triggered. Waiting for the server to clear the wiki…");
    try {
      const updated = await rebuildWiki(beginAction(), since);
      setAutoInject(updated.auto_inject);
      setMessage("Wiki cleared. Rebuilding from Session Lake…");
    } catch (error) {
      setMessage("");
      handleError(error);
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="wiki">
      <header className="wiki-header">
        <div>
          <span className="wiki-eyebrow">Grounded team knowledge</span>
          <h1>Wiki</h1>
          <p className="muted">Ingestion status and extraction progress for the team wiki.</p>
        </div>
        <Button variant="primary" type="button" onClick={() => navigate("/wiki/browse")}>
          Open Wiki
        </Button>
      </header>

      <WikiProgressCard status={status} statusError={statusError} />

      <WikiIngestionCard
        autoInject={autoInject}
        loading={loading}
        busy={busy}
        sessionID={sessionID}
        message={message}
        isOwner={me.role === "owner"}
        onSessionIDChange={setSessionID}
        onToggleAutoInject={toggleAutoInject}
        onInjectFixedSession={injectFixedSession}
        onOpenRebuild={() => setRebuildOpen(true)}
      />

      <WikiGenerationCard
        language={language}
        customLanguage={customLanguage}
        instructions={instructions}
        settingsBusy={settingsBusy}
        settingsMessage={settingsMessage}
        onLanguageChange={setLanguage}
        onCustomLanguageChange={setCustomLanguage}
        onInstructionsChange={setInstructions}
        onSave={saveSettings}
      />

      <WikiLLMUsageCard
        usageDays={usageDays}
        usage={usage}
        usageError={usageError}
        onUsageDaysChange={setUsageDays}
      />

      {rebuildOpen && (
        <WikiRebuildDialog
          busy={busy}
          rebuildSince={rebuildSince}
          onRebuildSinceChange={setRebuildSince}
          onConfirm={confirmRebuild}
          onClose={closeRebuild}
        />
      )}
    </div>
  );
}
