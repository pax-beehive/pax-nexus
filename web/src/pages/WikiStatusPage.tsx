import { useEffect, useState } from "react";
import { useLocation, useNavigate } from "react-router-dom";
import type { HumanMe } from "../api/types";
import {
  getWikiIngestionStatus,
  getWikiSettings,
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
import { ConfirmDialog } from "../components/ConfirmDialog";
import { formatTime } from "../lib/format";
import {
  codePointLength,
  composeInstructions,
  decomposeInstructions,
  INSTRUCTION_PRESETS,
  INSTRUCTIONS_LIMIT,
} from "../lib/instructionPresets";
import { isAbortError, usePolling } from "../lib/usePolling";
import { useErrorHandler } from "../lib/useErrorHandler";

// Presets shown as language chips; "" is "Follow source evidence". Loading a
// language outside this list (i.e. previously set to a free-form value)
// falls back to the "Custom…" chip with the value in a text input.
const LANGUAGE_PRESETS = ["", "简体中文", "English"];
const CUSTOM_LANGUAGE = "custom";
const LANGUAGE_CHIP_VALUES = [...LANGUAGE_PRESETS, CUSTOM_LANGUAGE];

function languageChipLabel(value: string): string {
  if (value === "") return "Follow source evidence";
  if (value === CUSTOM_LANGUAGE) return "Custom…";
  return value;
}

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

  // Generation settings: loaded once (not polled — the editor owns the
  // value while the user works on it, and settings don't change underneath
  // them from elsewhere).
  const [language, setLanguage] = useState("");
  const [customLanguage, setCustomLanguage] = useState("");
  const [selectedPresets, setSelectedPresets] = useState<string[]>([]);
  const [instructions, setInstructions] = useState("");
  const [settingsBusy, setSettingsBusy] = useState(false);
  const [settingsMessage, setSettingsMessage] = useState("");

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
    const decomposed = decomposeInstructions(settings.custom_instructions);
    setSelectedPresets(decomposed.selectedIds);
    setInstructions(decomposed.additional);
    if (LANGUAGE_PRESETS.includes(settings.language)) {
      setLanguage(settings.language);
      setCustomLanguage("");
    } else {
      setLanguage(CUSTOM_LANGUAGE);
      setCustomLanguage(settings.language);
    }
  };

  // Preserve INSTRUCTION_PRESETS array order in state: re-derive via filter
  // rather than tracking toggle (push) order.
  const togglePreset = (id: string) => {
    setSelectedPresets((current) => {
      const next = current.includes(id)
        ? current.filter((existing) => existing !== id)
        : [...current, id];
      return INSTRUCTION_PRESETS.filter((preset) => next.includes(preset.id)).map(
        (preset) => preset.id,
      );
    });
  };

  const composedInstructions = composeInstructions(selectedPresets, instructions);
  const instructionsRemaining = INSTRUCTIONS_LIMIT - codePointLength(composedInstructions);

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
        custom_instructions: composedInstructions,
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

      <section className="card wiki-generation" aria-label="Wiki generation settings">
        <div className="wiki-ingestion-copy">
          <span className="wiki-eyebrow">Generation</span>
          <strong>Language & instructions</strong>
          <span className="muted small">
            Applies to future generation runs only. Use Rebuild to switch the whole wiki.
          </span>
        </div>
        <div className="wiki-generation-fields">
          <label id="wiki-language-chips-label">Language</label>
          <div
            className="wiki-preset-chips"
            role="group"
            aria-labelledby="wiki-language-chips-label"
          >
            {LANGUAGE_CHIP_VALUES.map((value) => {
              const selected = language === value;
              return (
                <button
                  key={value === "" ? "default" : value}
                  type="button"
                  className={selected ? "chip selected" : "chip"}
                  aria-pressed={selected}
                  disabled={settingsBusy}
                  onClick={() => setLanguage(value)}
                >
                  {languageChipLabel(value)}
                </button>
              );
            })}
          </div>
          {language === CUSTOM_LANGUAGE && (
            <>
              <label htmlFor="wiki-generation-custom-language">Custom language</label>
              <input
                id="wiki-generation-custom-language"
                value={customLanguage}
                placeholder="e.g. 日本語"
                disabled={settingsBusy}
                onChange={(event) => setCustomLanguage(event.target.value)}
              />
            </>
          )}
          <label id="wiki-style-chips-label">Style presets</label>
          <div
            className="wiki-preset-chips"
            role="group"
            aria-labelledby="wiki-style-chips-label"
          >
            {INSTRUCTION_PRESETS.map((preset) => {
              const selected = selectedPresets.includes(preset.id);
              return (
                <button
                  key={preset.id}
                  type="button"
                  className={selected ? "chip selected" : "chip"}
                  aria-pressed={selected}
                  disabled={settingsBusy}
                  onClick={() => togglePreset(preset.id)}
                >
                  {preset.label}
                </button>
              );
            })}
          </div>
          <label htmlFor="wiki-generation-instructions">Additional instructions</label>
          <textarea
            id="wiki-generation-instructions"
            placeholder="Stacks with the selected presets above."
            value={instructions}
            disabled={settingsBusy}
            onChange={(event) => setInstructions(event.target.value)}
          />
          <span className="muted small">{instructionsRemaining} characters left</span>
          <button
            className="btn primary"
            type="button"
            disabled={settingsBusy || instructionsRemaining < 0}
            onClick={() => void saveSettings()}
          >
            {settingsBusy ? "Saving…" : "Save generation settings"}
          </button>
        </div>
        {settingsMessage && <p className="wiki-ingestion-message">{settingsMessage}</p>}
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
