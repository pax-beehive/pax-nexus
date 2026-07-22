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
});
