import { describe, expect, it } from "vitest";
import { ApiError } from "../src/api/client";
import { noticeForError } from "../src/lib/statusMessage";

describe("noticeForError", () => {
  it("explains the last active Owner invariant without treating it as stale data", () => {
    expect(noticeForError(new ApiError(409, "conflict", "last_active_owner"))).toEqual({
      kind: "bad",
      message: "You must promote another active Owner first",
    });
  });

  it("keeps stale resource versions refreshable", () => {
    expect(noticeForError(new ApiError(409, "conflict", "resource_version_conflict"))).toEqual({
      kind: "warn",
      message: "The data was modified by someone else; refresh and try again",
    });
  });

  it("surfaces the Retry-After hint on 429", () => {
    expect(noticeForError(new ApiError(429, "slow down", undefined, 30))).toEqual({
      kind: "warn",
      message: "Too many requests; try again in 30 seconds",
    });
  });

  it("falls back to the generic 429 message without Retry-After", () => {
    expect(noticeForError(new ApiError(429, "slow down"))).toEqual({
      kind: "warn",
      message: "Too many requests; try again later",
    });
  });

  it("maps 503 storage_not_available to the storage-specific empty state (operations doc 11)", () => {
    expect(noticeForError(new ApiError(503, "unavailable", "storage_not_available"))).toEqual({
      kind: "warn",
      message: "Storage statistics are temporarily unavailable; try again later",
    });
  });

  it("keeps a generic 503 distinct from storage_not_available", () => {
    expect(noticeForError(new ApiError(503, "unavailable"))).toEqual({
      kind: "bad",
      message: "Service temporarily unavailable; try again later",
    });
  });
});
