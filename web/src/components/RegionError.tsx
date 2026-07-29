import { noticeForError } from "../lib/statusMessage";

export function RegionError({ error, onRetry }: { error: unknown; onRetry?: () => void }) {
  const notice = noticeForError(error);
  return (
    <div className={`note ${notice.kind === "ok" ? "" : notice.kind}`}>
      {notice.message}
      {onRetry && (
        <button className="btn sm" style={{ marginLeft: 10 }} onClick={onRetry}>
          Retry
        </button>
      )}
    </div>
  );
}
