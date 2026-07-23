// Shared modal dialog. Exposes dialog semantics (role="dialog", aria-modal,
// accessible name from its title) and manages focus: focus moves into the
// dialog on open, Tab stays trapped inside, Escape closes, and closing
// restores focus to the trigger element. The inner error boundary keeps a
// crashing form from taking the page down with it.

import { useEffect, useId, useRef, type ReactNode } from "react";
import { ErrorBoundary } from "./ErrorBoundary";

const FOCUSABLE = 'button, [href], input, select, textarea, [tabindex]:not([tabindex="-1"])';

/** Tabbable controls inside the dialog; jsdom has no layout, so only disabled controls are excluded. */
function focusableIn(root: HTMLElement): HTMLElement[] {
  return Array.from(root.querySelectorAll<HTMLElement>(FOCUSABLE)).filter(
    (el) => !el.hasAttribute("disabled"),
  );
}

export function Modal({
  title,
  onClose,
  children,
}: {
  title: string;
  onClose: () => void;
  children: ReactNode;
}) {
  const titleId = useId();
  const dialogRef = useRef<HTMLDivElement>(null);
  // Parents pass inline closures whose identity changes every render; the
  // focus effect must run once per mount, so it goes through a ref.
  const onCloseRef = useRef(onClose);
  onCloseRef.current = onClose;

  useEffect(() => {
    const trigger = document.activeElement;
    const dialog = dialogRef.current;
    if (dialog) {
      (focusableIn(dialog)[0] ?? dialog).focus();
    }
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        e.stopPropagation();
        onCloseRef.current();
        return;
      }
      if (e.key !== "Tab" || !dialog) return;
      const items = focusableIn(dialog);
      if (items.length === 0) {
        e.preventDefault();
        return;
      }
      const first = items[0];
      const last = items[items.length - 1];
      const outside = !dialog.contains(document.activeElement);
      if (e.shiftKey && (document.activeElement === first || outside)) {
        e.preventDefault();
        last.focus();
      } else if (!e.shiftKey && (document.activeElement === last || outside)) {
        e.preventDefault();
        first.focus();
      }
    };
    document.addEventListener("keydown", onKeyDown, true);
    return () => {
      document.removeEventListener("keydown", onKeyDown, true);
      // Restore focus to the trigger, unless the flow already removed it
      // from the document (e.g. a successful submit navigated away).
      if (trigger instanceof HTMLElement && trigger.isConnected) trigger.focus();
    };
  }, []);

  return (
    <div
      className="modal-backdrop"
      onClick={(e) => {
        if (e.target === e.currentTarget) onClose();
      }}
    >
      <div
        className="modal"
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        tabIndex={-1}
        ref={dialogRef}
      >
        <h2 id={titleId}>{title}</h2>
        <ErrorBoundary region="modal" escapeLabel="关闭" onEscape={onClose}>
          {children}
        </ErrorBoundary>
      </div>
    </div>
  );
}
