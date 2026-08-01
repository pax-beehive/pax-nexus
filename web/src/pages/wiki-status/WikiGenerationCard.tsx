import { Button } from "../../components/Button";

// Sentinel language value for the "Custom…" preset option; the page uses it
// to decide between the preset select value and the free-form input.
export const CUSTOM_LANGUAGE = "custom";
export const INSTRUCTIONS_MAX_LENGTH = 2000;

export interface WikiGenerationCardProps {
  language: string;
  customLanguage: string;
  instructions: string;
  settingsBusy: boolean;
  settingsMessage: string;
  onLanguageChange: (value: string) => void;
  onCustomLanguageChange: (value: string) => void;
  onInstructionsChange: (value: string) => void;
  onSave: () => Promise<void>;
}

export function WikiGenerationCard({
  language,
  customLanguage,
  instructions,
  settingsBusy,
  settingsMessage,
  onLanguageChange,
  onCustomLanguageChange,
  onInstructionsChange,
  onSave,
}: WikiGenerationCardProps) {
  return (
    <section className="card wiki-generation" aria-label="Wiki generation settings">
      <div className="wiki-ingestion-copy">
        <span className="wiki-eyebrow">Generation</span>
        <strong>Language & instructions</strong>
        <span className="muted small">
          Applies to future generation runs only. Use Rebuild to switch the whole wiki.
        </span>
      </div>
      <div className="wiki-generation-fields">
        <label htmlFor="wiki-generation-language">Language</label>
        <select
          id="wiki-generation-language"
          value={language}
          disabled={settingsBusy}
          onChange={(event) => onLanguageChange(event.target.value)}
        >
          <option value="">Follow source evidence</option>
          <option value="简体中文">简体中文</option>
          <option value="English">English</option>
          <option value={CUSTOM_LANGUAGE}>Custom…</option>
        </select>
        {language === CUSTOM_LANGUAGE && (
          <>
            <label htmlFor="wiki-generation-custom-language">Custom language</label>
            <input
              id="wiki-generation-custom-language"
              value={customLanguage}
              placeholder="e.g. 日本語"
              disabled={settingsBusy}
              onChange={(event) => onCustomLanguageChange(event.target.value)}
            />
          </>
        )}
        <label htmlFor="wiki-generation-instructions">Custom instructions</label>
        <textarea
          id="wiki-generation-instructions"
          value={instructions}
          maxLength={INSTRUCTIONS_MAX_LENGTH}
          disabled={settingsBusy}
          onChange={(event) => onInstructionsChange(event.target.value)}
        />
        <span className="muted small">
          {INSTRUCTIONS_MAX_LENGTH - instructions.length} characters left
        </span>
        <Button
          variant="primary"
          type="button"
          disabled={settingsBusy}
          onClick={() => void onSave()}
        >
          {settingsBusy ? "Saving…" : "Save generation settings"}
        </Button>
      </div>
      {settingsMessage && <p className="wiki-ingestion-message">{settingsMessage}</p>}
    </section>
  );
}
