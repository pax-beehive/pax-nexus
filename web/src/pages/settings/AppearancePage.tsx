import { THEMES, THEME_LABELS, useTheme, type Theme } from "../../lib/theme";
import { PageHeader } from "../../components/PageHeader";

/**
 * Theme picker, Modernist repaint (phase 6 task 4). `lib/theme.ts` still owns
 * persistence (localStorage + document.documentElement.dataset.theme) —
 * this component only renders the control and calls setTheme.
 *
 * Each option previews its own palette rather than describing it in prose:
 * the swatch wraps a `data-theme` attribute matching that option's value, so
 * the cascade resolves --color-bg / --color-surface / --color-accent from
 * themes.css for that subtree regardless of which theme is currently active
 * on the document. That also keeps this honest about arcade, where
 * --color-accent is remapped to ink and the page background itself carries
 * the red — nothing here assumes accent is always red.
 */
export function AppearancePage() {
  const [theme, setTheme] = useTheme();

  return (
    <>
      <PageHeader
        kicker="Settings · appearance"
        title="Appearance"
        lede="Applies to your account on this device only. Nobody else on the team sees your choice."
      />

      <div className="theme-grid" role="group" aria-label="Theme">
        {THEMES.map((value) => (
          <button
            key={value}
            type="button"
            className={value === theme ? "theme-option on" : "theme-option"}
            aria-pressed={value === theme}
            onClick={() => setTheme(value as Theme)}
          >
            <span className="theme-swatch" data-theme={value === "beige" ? undefined : value} aria-hidden="true">
              <span className="theme-swatch-accent" />
              <span className="theme-swatch-surface" />
            </span>
            <span className="theme-option-label">{THEME_LABELS[value]}</span>
          </button>
        ))}
      </div>
    </>
  );
}
