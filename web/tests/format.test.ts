import { describe, expect, it } from "vitest";
import { formatBytes, formatRelativeFrom } from "../src/lib/format";

describe("formatBytes (operations doc section 13, IEC units)", () => {
  it("keeps zero as 0 B instead of an empty-state dash", () => {
    expect(formatBytes(0)).toBe("0 B");
  });

  it("formats sub-KiB values without a unit fraction", () => {
    expect(formatBytes(512)).toBe("512 B");
  });

  it("uses binary IEC units", () => {
    expect(formatBytes(1024)).toBe("1 KiB");
    expect(formatBytes(1536)).toBe("1.5 KiB");
    expect(formatBytes(104857600)).toBe("100 MiB");
    expect(formatBytes(12582912)).toBe("12 MiB");
    expect(formatBytes(1073741824)).toBe("1 GiB");
  });

  it("rounds to one decimal", () => {
    expect(formatBytes(513313)).toBe("501.3 KiB");
  });

  it("renders missing or invalid input as a dash", () => {
    expect(formatBytes(undefined)).toBe("—");
    expect(formatBytes(Number.NaN)).toBe("—");
  });

  it("handles negative values with a sign", () => {
    expect(formatBytes(-2048)).toBe("-2 KiB");
  });
});

describe("formatRelativeFrom (Pipeline health 排队中 subtitle)", () => {
  const REF = "2026-07-22T12:00:00Z";

  it("under a minute reads 刚刚", () => {
    expect(formatRelativeFrom("2026-07-22T11:59:30Z", REF)).toBe("刚刚");
  });

  it("minutes bucket", () => {
    expect(formatRelativeFrom("2026-07-22T11:41:00Z", REF)).toBe("19 分钟前");
  });

  it("90 minutes floors to 1 小时前, never rounds up to 2 小时前", () => {
    expect(formatRelativeFrom("2026-07-22T10:30:00Z", REF)).toBe("1 小时前");
  });

  it("hours bucket floors instead of rounds", () => {
    // 36 hours back would round to "2 天前"; floor keeps it honest at "1 天前".
    expect(formatRelativeFrom("2026-07-21T00:00:00Z", REF)).toBe("1 天前");
    expect(formatRelativeFrom("2026-07-22T02:00:00Z", REF)).toBe("10 小时前");
  });

  it("days bucket", () => {
    expect(formatRelativeFrom("2026-07-15T12:00:00Z", REF)).toBe("7 天前");
  });

  it("a future timestamp clamps to 刚刚 instead of going negative", () => {
    expect(formatRelativeFrom("2026-07-22T12:05:00Z", REF)).toBe("刚刚");
  });

  it("renders missing or invalid input as a dash", () => {
    expect(formatRelativeFrom(undefined, REF)).toBe("—");
    expect(formatRelativeFrom("not-a-timestamp", REF)).toBe("—");
    expect(formatRelativeFrom("2026-07-22T11:00:00Z", "not-a-timestamp")).toBe("—");
  });
});
