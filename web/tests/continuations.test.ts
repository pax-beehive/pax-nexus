import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  clearPendingInvitation,
  isInternalPath,
  peekPendingInvitation,
  savePendingInvitation,
  saveReturnUrl,
  takeReturnUrl,
} from "../src/lib/continuations";

function makeStorage(): Storage {
  const map = new Map<string, string>();
  return {
    get length() {
      return map.size;
    },
    clear: () => map.clear(),
    getItem: (k: string) => map.get(k) ?? null,
    key: (i: number) => [...map.keys()][i] ?? null,
    removeItem: (k: string) => void map.delete(k),
    setItem: (k: string, v: string) => void map.set(k, v),
  };
}

beforeEach(() => {
  vi.stubGlobal("sessionStorage", makeStorage());
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("isInternalPath", () => {
  it.each(["/agents", "/admin/members?status=active", "/join", "/"])(
    "accepts internal path %s",
    (p) => {
      expect(isInternalPath(p)).toBe(true);
    },
  );

  it.each([
    "https://evil.example/phish",
    "http://evil.example",
    "//evil.example/path",
    "javascript:alert(1)",
    "/\\evil.example",
    "agents",
    "",
  ])("rejects external or malformed path %s", (p) => {
    expect(isInternalPath(p)).toBe(false);
  });
});

describe("return_url continuation", () => {
  it("stores valid internal paths and reads them once", () => {
    saveReturnUrl("/agents");
    expect(takeReturnUrl()).toBe("/agents");
    expect(takeReturnUrl()).toBeUndefined(); // read-once-then-delete
  });

  it("ignores non-internal paths at save time", () => {
    saveReturnUrl("https://evil.example");
    expect(takeReturnUrl()).toBeUndefined();
  });

  it("re-validates stored values at read time", () => {
    sessionStorage.setItem("return_url", "//evil.example");
    expect(takeReturnUrl()).toBeUndefined();
  });
});

describe("pending_invitation continuation", () => {
  it("uses a channel separate from return_url", () => {
    saveReturnUrl("/agents");
    savePendingInvitation("tm_invite_inv_01.secret");
    expect(peekPendingInvitation()).toBe("tm_invite_inv_01.secret");
    expect(takeReturnUrl()).toBe("/agents");
    expect(peekPendingInvitation()).toBe("tm_invite_inv_01.secret");
    clearPendingInvitation();
    expect(peekPendingInvitation()).toBeUndefined();
  });
});
