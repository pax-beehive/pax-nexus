export function formatTime(iso: string | undefined): string {
  if (!iso) return "—";
  const time = new Date(iso).getTime();
  if (Number.isNaN(time)) return iso;
  return new Date(iso).toLocaleString("zh-CN", { hour12: false });
}

const IEC_UNITS = ["B", "KiB", "MiB", "GiB", "TiB", "PiB"] as const;

/** IEC binary byte formatting (operations doc section 13); zero stays "0 B". */
export function formatBytes(bytes: number | undefined): string {
  if (bytes === undefined || Number.isNaN(bytes)) return "—";
  const sign = bytes < 0 ? "-" : "";
  let value = Math.abs(bytes);
  let unit = 0;
  while (value >= 1024 && unit < IEC_UNITS.length - 1) {
    value /= 1024;
    unit += 1;
  }
  const rounded = unit === 0 ? value : Math.round(value * 10) / 10;
  return `${sign}${rounded} ${IEC_UNITS[unit]}`;
}

/**
 * Relative time from `iso` as of `referenceIso` (not wall-clock `Date.now()`),
 * so it stays deterministic against a fixed report timestamp (e.g. a
 * summary's `generated_at`) instead of drifting with real time. Returns
 * "—" when either timestamp is missing or unparseable.
 */
export function formatRelativeFrom(iso: string | undefined, referenceIso: string): string {
  if (!iso) return "—";
  const from = new Date(iso).getTime();
  const reference = new Date(referenceIso).getTime();
  if (Number.isNaN(from) || Number.isNaN(reference)) return "—";
  const diffMs = Math.max(0, reference - from);
  const minutes = Math.round(diffMs / 60_000);
  if (minutes < 1) return "刚刚";
  if (minutes < 60) return `${minutes} 分钟前`;
  const hours = Math.round(minutes / 60);
  if (hours < 24) return `${hours} 小时前`;
  const days = Math.round(hours / 24);
  return `${days} 天前`;
}
