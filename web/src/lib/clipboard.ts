/**
 * Copy text to the clipboard. The async Clipboard API requires a secure
 * context (HTTPS or localhost); plain-HTTP deployments fall back to a hidden
 * textarea + execCommand("copy"), and callers may degrade further to a
 * manual-copy prompt when this returns false. Secrets never touch storage
 * in any tier.
 */
export async function copyTextToClipboard(text: string): Promise<boolean> {
  if (navigator.clipboard?.writeText) {
    try {
      await navigator.clipboard.writeText(text);
      return true;
    } catch {
      // Permission denied or similar: fall through to the legacy path.
    }
  }
  if (typeof document.execCommand !== "function") return false;
  const area = document.createElement("textarea");
  area.value = text;
  area.setAttribute("readonly", "");
  area.style.position = "fixed";
  area.style.top = "0";
  area.style.opacity = "0";
  document.body.appendChild(area);
  area.select();
  try {
    return document.execCommand("copy");
  } catch {
    return false;
  } finally {
    area.remove();
  }
}
