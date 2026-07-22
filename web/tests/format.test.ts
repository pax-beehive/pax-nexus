import { describe, expect, it } from "vitest";
import { formatBytes } from "../src/lib/format";

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
