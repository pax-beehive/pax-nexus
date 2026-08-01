import type { ReactNode } from "react";
import { Button } from "./Button";
import { Modal } from "./Modal";

/**
 * Destructive-action confirmation. Cascade consequences are spelled out in
 * the dialog body; terminal actions get the danger-styled confirm button.
 * Optional children render between the consequences and the action row.
 */
export function ConfirmDialog({
  title,
  consequences,
  confirmLabel,
  busy,
  onConfirm,
  onClose,
  children,
}: {
  title: string;
  consequences: string[];
  confirmLabel: string;
  busy?: boolean;
  onConfirm: () => void;
  onClose: () => void;
  children?: ReactNode;
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
      {children}
      <div className="row" style={{ justifyContent: "flex-end" }}>
        <Button variant="ghost" onClick={onClose} disabled={busy}>
          Cancel
        </Button>
        <Button variant="danger" onClick={onConfirm} disabled={busy}>
          {busy ? "Processing…" : confirmLabel}
        </Button>
      </div>
    </Modal>
  );
}
