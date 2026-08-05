// 占位实现：只固定 props 契约，让 AppShell 在 Task 8 之前就能通过类型检查。
// Task 8 用完整实现整体替换本文件。

import type { HumanMe } from "../api/types";
import type { NavSection } from "../app/navModel";

export function CommandPalette({
  open,
  onOpenChange,
}: {
  me: HumanMe;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  sections: NavSection[];
}) {
  if (!open) return null;
  return (
    <div
      className="dialog-backdrop"
      onClick={() => onOpenChange(false)}
      role="dialog"
      aria-modal="true"
      aria-label="Command palette"
    />
  );
}
