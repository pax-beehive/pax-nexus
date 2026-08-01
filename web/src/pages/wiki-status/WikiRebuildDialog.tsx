import { ConfirmDialog } from "../../components/ConfirmDialog";

export interface WikiRebuildDialogProps {
  busy: boolean;
  rebuildSince: string;
  onRebuildSinceChange: (value: string) => void;
  onConfirm: () => Promise<void>;
  onClose: () => void;
}

export function WikiRebuildDialog({
  busy,
  rebuildSince,
  onRebuildSinceChange,
  onConfirm,
  onClose,
}: WikiRebuildDialogProps) {
  return (
    <ConfirmDialog
      title="Reset and rebuild Wiki"
      consequences={[
        "All PageWiki pages, revisions, links, citations, and maintenance runs will be deleted.",
        rebuildSince
          ? `Only sessions with activity on or after ${rebuildSince} will be replayed; older sessions will be skipped until a wider rebuild.`
          : "PageWiki ingestion cursors will reset and every Session Lake stream will be processed again.",
        "Session Lake events and Team Notes are preserved.",
        "An LLM-backed rebuild may make paid provider calls.",
      ]}
      confirmLabel="Confirm reset & rebuild"
      busy={busy}
      onConfirm={() => void onConfirm()}
      onClose={onClose}
    >
      <div className="wiki-rebuild-since">
        <label htmlFor="wiki-rebuild-since">Replay sessions since (optional)</label>
        <input
          id="wiki-rebuild-since"
          type="date"
          value={rebuildSince}
          max={new Intl.DateTimeFormat("en-CA").format(new Date())}
          onChange={(event) => onRebuildSinceChange(event.target.value)}
          disabled={busy}
        />
        <span className="muted small">
          Leave empty to replay the full Session Lake history.
        </span>
      </div>
    </ConfirmDialog>
  );
}
