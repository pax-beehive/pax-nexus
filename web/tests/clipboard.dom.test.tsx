// copyTextToClipboard tiers (non-secure-context fallback): native Clipboard
// API, hidden textarea + execCommand, then false so callers can prompt.

import { afterEach, describe, expect, it, vi } from "vitest";
import { copyTextToClipboard } from "../src/lib/clipboard";

function stubClipboard(writeText?: (text: string) => Promise<void>) {
  Object.defineProperty(navigator, "clipboard", {
    value: writeText ? { writeText } : undefined,
    configurable: true,
  });
}

function stubExecCommand(result: boolean | Error) {
  Object.defineProperty(document, "execCommand", {
    value: vi.fn(() => {
      if (result instanceof Error) throw result;
      return result;
    }),
    configurable: true,
  });
}

afterEach(() => {
  delete (navigator as { clipboard?: unknown }).clipboard;
  delete (document as { execCommand?: unknown }).execCommand;
});

describe("copyTextToClipboard", () => {
  it("uses the native Clipboard API when available", async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    stubClipboard(writeText);
    stubExecCommand(true);

    await expect(copyTextToClipboard("secret")).resolves.toBe(true);
    expect(writeText).toHaveBeenCalledWith("secret");
    expect(document.execCommand).not.toHaveBeenCalled();
  });

  it("falls back to execCommand when the Clipboard API is absent", async () => {
    stubClipboard(undefined);
    stubExecCommand(true);

    await expect(copyTextToClipboard("secret")).resolves.toBe(true);
    expect(document.execCommand).toHaveBeenCalledWith("copy");
    // The temporary textarea must not linger (and the secret with it).
    expect(document.body.querySelector("textarea")).toBeNull();
  });

  it("falls back to execCommand when the Clipboard API rejects", async () => {
    stubClipboard(vi.fn().mockRejectedValue(new Error("denied")));
    stubExecCommand(true);

    await expect(copyTextToClipboard("secret")).resolves.toBe(true);
    expect(document.execCommand).toHaveBeenCalledWith("copy");
  });

  it("returns false when every tier is unavailable", async () => {
    stubClipboard(undefined);
    stubExecCommand(new Error("not supported"));

    await expect(copyTextToClipboard("secret")).resolves.toBe(false);
    expect(document.body.querySelector("textarea")).toBeNull();
  });
});
