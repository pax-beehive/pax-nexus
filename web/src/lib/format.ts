export function formatTime(iso: string | undefined): string {
  if (!iso) return "—";
  const time = new Date(iso).getTime();
  if (Number.isNaN(time)) return iso;
  return new Date(iso).toLocaleString("en-US", { hour12: false });
}

/**
 * 单复数：计数文案里 1 台机器不该写成 "1 machines"。`one`/`many` 收的是整个
 * 名词短语而不只是名词，所以 "person has" / "people have" 这类连动词一起变的
 * 情况也能表达。
 */
export function plural(count: number, one: string, many: string): string {
  return `${count} ${count === 1 ? one : many}`;
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
 * "—" when either timestamp is missing or unparseable. `iso` after
 * `referenceIso` (a future timestamp) clamps to "刚刚" rather than going
 * negative. Uses `Math.floor`, not `Math.round`, on every bucket boundary:
 * this page is about being honest about staleness, and rounding up would
 * report an item as older than it actually is (90 minutes would round to
 * "2 小时前" instead of the true "1 小时前").
 */
export function formatRelativeFrom(iso: string | undefined, referenceIso: string): string {
  if (!iso) return "—";
  const from = new Date(iso).getTime();
  const reference = new Date(referenceIso).getTime();
  if (Number.isNaN(from) || Number.isNaN(reference)) return "—";
  const diffMs = Math.max(0, reference - from);
  const seconds = Math.floor(diffMs / 1_000);
  if (seconds < 60) return `${seconds}s ago`;
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes}m ago`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours}h ago`;
  const days = Math.floor(hours / 24);
  return `${days}d ago`;
}
