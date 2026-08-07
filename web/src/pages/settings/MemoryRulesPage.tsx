import { useEffect, useState } from "react";
import { useLocation, useNavigate } from "react-router-dom";
import type { HumanMe } from "../../api/types";
import {
  getWikiIngestionStatus,
  getWikiSettings,
  type WikiGenerationSettings,
  type WikiIngestionStatus,
} from "../../api/wiki";
import {
  beginAction,
  injectWikiSession,
  rebuildWiki,
  setWikiAutoInject,
  updateWikiSettings,
} from "../../api/actions";
import { Button } from "../../components/Button";
import { PageHeader } from "../../components/PageHeader";
import { isAbortError, usePolling } from "../../lib/usePolling";
import { useErrorHandler } from "../../lib/useErrorHandler";
import { WikiProgressCard } from "../wiki-status/WikiProgressCard";
import { WikiIngestionCard } from "../wiki-status/WikiIngestionCard";
import { CUSTOM_LANGUAGE, WikiGenerationCard } from "../wiki-status/WikiGenerationCard";
import { WikiRebuildDialog } from "../wiki-status/WikiRebuildDialog";

// Presets shown in the language select; "" is "Follow source evidence".
// Loading a language outside this list (i.e. previously set to a free-form
// value) falls back to the "Custom…" option with the value in a text input.
const LANGUAGE_PRESETS = ["", "简体中文", "English"];

/**
 * /settings/memory — how memory gets written: ingestion status, extraction
 * progress, and generation settings. Split from the former WikiStatusPage
 * (phase 6 task 1); LLM token usage moved out to ModelUsagePage.
 */
export function MemoryRulesPage({ me }: { me: HumanMe }) {
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

  // Stray deep link, not the /wiki legacy chain: resolveLegacy() already
  // rewrites /wiki(/browse)?page=<slug> to /apps/wiki/<slug> at the router
  // layer (legacyRoutes.ts), so this component never mounts for that case.
  // What this guards is someone landing on /settings/memory?page=<slug>
  // directly — a bookmark or shared link built against this URL rather than
  // the old /wiki one. Forward the whole query string so revision links
  // keep working.
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
    // Close before awaiting; the endpoint answers 202 quickly, and rebuild
    // progress is reported by the polled ingestion status.
    closeRebuild();
    setMessage("Reset & rebuild triggered. Waiting for the server to clear the wiki…");
    try {
      await rebuildWiki(beginAction(), since);
      setMessage("Reset & rebuild queued. The wiki will be cleared and rebuilt in the background.");
    } catch (error) {
      setMessage("");
      handleError(error);
    } finally {
      setBusy(false);
    }
  };

  return (
    <>
      <PageHeader
        kicker="Settings · memory rules"
        title="记忆是怎么写出来的"
        lede={
          <p className="muted flush memory-rules-copy">
            这些规则只对以后的运行生效。要让它们作用于已有内容，就重建——它会重读证据、重写每一页。
          </p>
        }
        actions={
          <Button variant="primary" type="button" onClick={() => navigate("/wiki/browse")}>
            Open Wiki
          </Button>
        }
      />

      <WikiProgressCard status={status} statusError={statusError} />

      <WikiIngestionCard
        autoInject={autoInject}
        loading={loading}
        busy={busy}
        sessionID={sessionID}
        message={message}
        isOwner={me.role === "owner"}
        rebuildState={status?.rebuild_state}
        rebuildError={status?.rebuild_error}
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

      {rebuildOpen && (
        <WikiRebuildDialog
          busy={busy}
          rebuildSince={rebuildSince}
          onRebuildSinceChange={setRebuildSince}
          onConfirm={confirmRebuild}
          onClose={closeRebuild}
        />
      )}
    </>
  );
}
