import { useState } from "react";

export const THEMES = ["beige", "dark", "arcade"] as const;
export type Theme = (typeof THEMES)[number];

export const THEME_LABELS: Record<Theme, string> = {
  beige: "Beige",
  dark: "Dark",
  arcade: "Arcade",
};

const STORAGE_KEY = "portal-theme";

function isTheme(value: string | null): value is Theme {
  return value !== null && (THEMES as readonly string[]).includes(value);
}

/** The default beige theme is bare :root, so only non-default themes set the attribute. */
export function applyTheme(theme: Theme): void {
  if (theme === "beige") {
    delete document.documentElement.dataset.theme;
  } else {
    document.documentElement.dataset.theme = theme;
  }
}

export function getStoredTheme(): Theme {
  try {
    const stored = localStorage.getItem(STORAGE_KEY);
    return isTheme(stored) ? stored : "beige";
  } catch {
    return "beige";
  }
}

export function setStoredTheme(theme: Theme): void {
  try {
    localStorage.setItem(STORAGE_KEY, theme);
  } catch {
    // Private browsing or disabled storage: theme still applies for the session.
  }
  applyTheme(theme);
}

export function useTheme(): [Theme, (theme: Theme) => void] {
  const [theme, setTheme] = useState<Theme>(getStoredTheme);
  const change = (next: Theme) => {
    setStoredTheme(next);
    setTheme(next);
  };
  return [theme, change];
}
