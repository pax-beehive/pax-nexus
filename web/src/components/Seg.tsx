/** 单选预设切换。与 .tabs 不同：.seg 用于「同一数据的不同取值」。 */
export function Seg<T extends string>({
  label,
  options,
  value,
  onChange,
}: {
  label: string;
  options: { value: T; label: string; title?: string; disabled?: boolean }[];
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
          // A disabled option stays visible rather than vanishing: the reason
          // it cannot be picked is a property of the deployment, and hiding it
          // would leave no place to say so. `title` carries that reason.
          disabled={option.disabled}
          onClick={() => onChange(option.value)}
          title={option.title}
        >
          {option.label}
        </button>
      ))}
    </div>
  );
}
