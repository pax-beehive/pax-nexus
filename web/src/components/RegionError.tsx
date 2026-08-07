import { noticeForError } from "../lib/statusMessage";
import { Button } from "./Button";

/**
 * A region's failure, with its way out.
 *
 * `message` overrides the text derived from `error`, for the callers that
 * know something the error object does not ("Could not load the team's
 * people") or that only track a boolean. Passing it without an `error` is the
 * normal shape for those; the tone then defaults to `bad`.
 *
 * `role="alert"` lives here rather than at the call sites: every region error
 * is an unrequested state change the reader needs told about, and having it
 * on some copies and not others was the reason the hand-rolled versions
 * diverged in the first place.
 */
export function RegionError({
  error,
  onRetry,
  message,
}: {
  error?: unknown;
  onRetry?: () => void;
  message?: string;
}) {
  // A caller that passes only a message has already classified the failure
  // itself and has no error object to derive a tone from, so it gets `bad`.
  const derived = error === undefined ? undefined : noticeForError(error);
  const kind = derived?.kind ?? "bad";
  const text = message ?? derived?.message ?? "Something went wrong; try again";
  return (
    <div className={`note ${kind === "ok" ? "" : kind}`} role="alert">
      <div className="row">
        <span>{text}</span>
        {onRetry && (
          <Button size="sm" onClick={onRetry}>
            Retry
          </Button>
        )}
      </div>
    </div>
  );
}
