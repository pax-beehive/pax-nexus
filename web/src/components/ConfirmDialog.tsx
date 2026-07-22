import { Modal } from "./Modal";

/**
 * Destructive-action confirmation. Cascade consequences are spelled out in
 * the dialog body; terminal actions get the danger-styled confirm button.
 */
export function ConfirmDialog({
  title,
  consequences,
  confirmLabel,
  busy,
  onConfirm,
  onClose,
}: {
  title: string;
  consequences: string[];
  confirmLabel: string;
  busy?: boolean;
  onConfirm: () => void;
  onClose: () => void;
}) {
  return (
    <Modal title={title} onClose={onClose}>
      <div className="note bad">
        <ul style={{ margin: "2px 0 2px 18px", padding: 0 }}>
          {consequences.map((c) => (
            <li key={c}>{c}</li>
          ))}
        </ul>
      </div>
      <div className="row" style={{ justifyContent: "flex-end" }}>
        <button className="btn ghost" onClick={onClose} disabled={busy}>
          取消
        </button>
        <button className="btn danger" onClick={onConfirm} disabled={busy}>
          {busy ? "执行中…" : confirmLabel}
        </button>
      </div>
    </Modal>
  );
}
