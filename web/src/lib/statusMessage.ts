// Central HTTP status -> UI behavior mapping (integration doc section 9).
// Stable error codes select precise recovery; the diagnostic message is never
// string-matched. A 429 surfaces the Retry-After hint parsed by the client;
// automatic retries stay forbidden.

import { ApiError } from "../api/client";

export type NoticeKind = "ok" | "warn" | "bad";

export interface Notice {
  kind: NoticeKind;
  message: string;
}

export function noticeForError(err: unknown, opts?: { conflict?: string }): Notice {
  if (err instanceof ApiError) {
    if (err.code === "last_active_owner") {
      return { kind: "bad", message: "You must promote another active Owner first" };
    }
    if (err.code === "agent_id_conflict") {
      return { kind: "warn", message: "This Agent ID already exists; choose a different ID" };
    }
    if (err.code === "idempotency_conflict") {
      return {
        kind: "bad",
        message: "This Idempotency-Key was already used for a different request; resubmit the operation with a new key",
      };
    }
    if (err.code === "invalid_state_transition") {
      return { kind: "warn", message: "This operation is not allowed in the current state; refresh and try again" };
    }
    if (err.code === "storage_not_available") {
      return { kind: "warn", message: "Storage statistics are temporarily unavailable; try again later" };
    }
    switch (err.status) {
      case 400:
        return { kind: "warn", message: "The request was malformed; check your input and try again" };
      case 401:
        return { kind: "warn", message: "Your session has expired; sign in again" };
      case 403:
        return {
          kind: "bad",
          message: "You do not have permission to perform this operation; if your role was just changed, refresh and try again",
        };
      case 404:
        return { kind: "warn", message: "The target resource does not exist or is not visible" };
      case 409:
        return { kind: "warn", message: opts?.conflict ?? "The data was modified by someone else; refresh and try again" };
      case 410:
        return {
          kind: "bad",
          message: "This credential is no longer valid (expired, revoked, or already used); issue a new one",
        };
      case 422:
        return { kind: "warn", message: "The input is invalid; correct it and try again" };
      case 429:
        return {
          kind: "warn",
          message:
            err.retryAfterSeconds !== undefined
              ? `Too many requests; try again in ${err.retryAfterSeconds} seconds`
              : "Too many requests; try again later",
        };
      case 500:
        return { kind: "bad", message: "Server error; try again later" };
      case 501:
        return {
          kind: "bad",
          message: "Human Identity is not configured; contact operations to check the installation",
        };
      case 503:
        return { kind: "bad", message: "Service temporarily unavailable; try again later" };
      default:
        return { kind: "bad", message: `Request failed (HTTP ${err.status})` };
    }
  }
  return {
    kind: "bad",
    message:
      "Network error; if you just submitted a one-time credential creation, refresh the list to confirm whether it was created before retrying",
  };
}
