import { describe, expect, it } from "vitest";
import { ApiError } from "../src/api/client";
import { noticeForError } from "../src/lib/statusMessage";

describe("noticeForError", () => {
  it("explains the last active Owner invariant without treating it as stale data", () => {
    expect(noticeForError(new ApiError(409, "conflict", "last_active_owner"))).toEqual({
      kind: "bad",
      message: "必须先提升另一位 active Owner",
    });
  });

  it("keeps stale resource versions refreshable", () => {
    expect(noticeForError(new ApiError(409, "conflict", "resource_version_conflict"))).toEqual({
      kind: "warn",
      message: "数据已被他人修改，请刷新后重试",
    });
  });

  it("surfaces the Retry-After hint on 429", () => {
    expect(noticeForError(new ApiError(429, "slow down", undefined, 30))).toEqual({
      kind: "warn",
      message: "请求过于频繁，请 30 秒后重试",
    });
  });

  it("falls back to the generic 429 message without Retry-After", () => {
    expect(noticeForError(new ApiError(429, "slow down"))).toEqual({
      kind: "warn",
      message: "请求过于频繁，请稍后重试",
    });
  });

  it("maps 503 storage_not_available to the storage-specific empty state (operations doc 11)", () => {
    expect(noticeForError(new ApiError(503, "unavailable", "storage_not_available"))).toEqual({
      kind: "warn",
      message: "Storage 统计暂不可用，请稍后重试",
    });
  });

  it("keeps a generic 503 distinct from storage_not_available", () => {
    expect(noticeForError(new ApiError(503, "unavailable"))).toEqual({
      kind: "bad",
      message: "服务暂时不可用，请稍后重试",
    });
  });
});
