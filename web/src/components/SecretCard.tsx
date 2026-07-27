import type { ReactNode } from "react";
import { useToast } from "./Toasts";
import { Countdown } from "./Countdown";
import { copyTextToClipboard } from "../lib/clipboard";

async function copyText(text: string, what: string, toast: (k: "ok" | "warn", m: string) => void) {
  if (await copyTextToClipboard(text)) {
    toast("ok", `${what} copied`);
    return;
  }
  // Clipboard unavailable (permissions or non-secure context): fall back to
  // a manual-copy prompt. The secret still never hits storage.
  window.prompt("Copy manually:", text);
}

/**
 * One-time secret display (invitation token, enrollment token). The value
 * lives only in this component tree — never persisted, never logged.
 */
export function SecretCard({
  title,
  value,
  valueLabel,
  expiresAt,
  note,
  extraActions,
  onClose,
}: {
  title: string;
  value: string;
  valueLabel: string;
  expiresAt?: string;
  note: string;
  extraActions?: ReactNode;
  onClose: () => void;
}) {
  const toast = useToast();
  return (
    <div className="secret-card">
      <div className="row between">
        <strong>{title}</strong>
        {expiresAt && (
          <span className="small">
            Expires in <Countdown to={expiresAt} />
          </span>
        )}
      </div>
      <div className="secret-val">{value}</div>
      <div className="row wrap">
        <button className="btn sm primary" onClick={() => void copyText(value, valueLabel, toast)}>
          Copy {valueLabel}
        </button>
        {extraActions}
        <button className="btn sm ghost" onClick={onClose}>
          I've saved it, close
        </button>
      </div>
      <div className="note warn small" style={{ marginBottom: 0 }}>
        {note}
      </div>
    </div>
  );
}
