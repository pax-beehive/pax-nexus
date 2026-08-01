// Status filter tab group for the enrollment/credential lists. Styled by the
// parent-scoped `.tabs button` CSS, so these stay plain <button> elements.

export function Tabs({
  label,
  options,
  value,
  onChange,
}: {
  /** Accessible group name, e.g. "enrollment status". */
  label: string;
  options: readonly string[];
  value: string;
  onChange: (v: string) => void;
}) {
  return (
    <div className="tabs" role="group" aria-label={label}>
      {options.map((o) => (
        <button
          key={o}
          className={o === value ? "on" : ""}
          aria-pressed={o === value}
          onClick={() => onChange(o)}
        >
          {o}
        </button>
      ))}
    </div>
  );
}
