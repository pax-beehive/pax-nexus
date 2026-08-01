import { noticeForError } from "../lib/statusMessage";
import { Button } from "./Button";

export function RegionError({ error, onRetry }: { error: unknown; onRetry?: () => void }) {
  const notice = noticeForError(error);
  return (
    <div className={`note ${notice.kind === "ok" ? "" : notice.kind}`}>
      <div className="row">
        <span>{notice.message}</span>
        {onRetry && (
          <Button size="sm" onClick={onRetry}>
            Retry
          </Button>
        )}
      </div>
    </div>
  );
}
