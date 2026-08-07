import { formatRetention, timeWindowOptions, type TimeWindowPreset } from "../lib/operations";
import { Seg } from "./Seg";

/**
 * 时间窗口选择器。Overview 与 Pipeline 两屏各有一个，之前是两份互相抄的
 * 代码（其中一份还手写了 `.seg` 并把预设写死成 `["1h","24h","7d"]`）。
 *
 * 保留期是可见文字，不只是 `title`：issue #86 对阶段 2b 那个兜底 tooltip 的
 * 主要意见就是「触屏上根本看不到」，只把原因换成真实数值、仍旧藏在 tooltip
 * 里，等于换了个说法犯同一个毛病。禁用态给出「哪个不能选」，这行字给出
 * 「为什么」，两者都不依赖悬停。
 *
 * 保留期未知（首个响应未到，或后端尚无此字段）时全部可选，这行字也不出现
 * ——没有可说的数值时保持沉默，好过写一句放之四海而皆准的废话。
 */
export function TimeWindowPicker({
  label,
  value,
  onChange,
  retentionSeconds,
}: {
  label: string;
  value: TimeWindowPreset;
  onChange: (value: TimeWindowPreset) => void;
  retentionSeconds?: number;
}) {
  const options = timeWindowOptions(retentionSeconds);
  const limited = options.some((option) => option.disabled);

  return (
    <div className="row">
      <Seg label={label} options={options} value={value} onChange={onChange} />
      {limited && retentionSeconds !== undefined && (
        <span className="small muted">
          Retained for {formatRetention(retentionSeconds)}
        </span>
      )}
    </div>
  );
}
