/** 单选预设切换。与 .tabs 不同：.seg 用于「同一数据的不同取值」。 */
export function Seg<T extends string>({
  label,
  options,
  value,
  onChange,
}: {
  label: string;
  options: { value: T; label: string; title?: string }[];
  value: T;
  onChange: (value: T) => void;
}) {
  return (
    <div className="seg" role="group" aria-label={label}>
      {options.map((option) => (
        <button
          key={option.value}
          type="button"
          className={option.value === value ? "on" : ""}
          aria-pressed={option.value === value}
          onClick={() => onChange(option.value)}
          title={option.title}
        >
          {option.label}
        </button>
      ))}
    </div>
  );
}
