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
