import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createPoller, isAbortError } from "../src/lib/usePolling";

// Polling contract from operations doc section 4.3: non-overlapping cycles,
// real AbortController cancellation, paused while hidden, exactly one fresh
// cycle when the page becomes visible again.

describe("isAbortError", () => {
  it("recognizes DOMException and plain Error aborts", () => {
    expect(isAbortError(new DOMException("cancelled", "AbortError"))).toBe(true);
    const err = new Error("cancelled");
    err.name = "AbortError";
    expect(isAbortError(err)).toBe(true);
  });

  it("rejects other errors", () => {
    expect(isAbortError(new Error("boom"))).toBe(false);
    expect(isAbortError("AbortError")).toBe(false);
    expect(isAbortError(undefined)).toBe(false);
  });
});

describe("createPoller", () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it("runs one cycle immediately on start, then one per interval", async () => {
    const run = vi.fn().mockResolvedValue(undefined);
    const poller = createPoller({ intervalMs: 1000, run, isVisible: () => true });
    poller.start();
    expect(run).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(3000);
    expect(run).toHaveBeenCalledTimes(4);
    poller.stop();
  });

  it("start is idempotent and never doubles the timer", async () => {
    const run = vi.fn().mockResolvedValue(undefined);
    const poller = createPoller({ intervalMs: 1000, run, isVisible: () => true });
    poller.start();
    poller.start();
    expect(run).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(1000);
    expect(run).toHaveBeenCalledTimes(2);
    poller.stop();
  });

  it("aborts the previous in-flight request before the next cycle (no overlap)", async () => {
    const signals: AbortSignal[] = [];
    const run = vi.fn((signal: AbortSignal) => {
      signals.push(signal);
      return new Promise<void>(() => {});
    });
    const poller = createPoller({ intervalMs: 1000, run, isVisible: () => true });
    poller.start();
    await vi.advanceTimersByTimeAsync(1000);
    expect(run).toHaveBeenCalledTimes(2);
    expect(signals[0].aborted).toBe(true);
    expect(signals[1].aborted).toBe(false);
    poller.stop();
  });

  it("swallows AbortError from cancelled requests without reporting", async () => {
    const onError = vi.fn();
    const run = vi.fn().mockRejectedValue(new DOMException("cancelled", "AbortError"));
    const poller = createPoller({ intervalMs: 1000, run, isVisible: () => true, onError });
    poller.start();
    await vi.advanceTimersByTimeAsync(0);
    expect(onError).not.toHaveBeenCalled();
    poller.stop();
  });

  it("reports non-abort errors to onError", async () => {
    const onError = vi.fn();
    const failure = new Error("boom");
    const run = vi.fn().mockRejectedValue(failure);
    const poller = createPoller({ intervalMs: 1000, run, isVisible: () => true, onError });
    poller.start();
    await vi.advanceTimersByTimeAsync(0);
    expect(onError).toHaveBeenCalledWith(failure);
    poller.stop();
  });

  it("skips cycles while hidden and runs none of them later", async () => {
    const run = vi.fn().mockResolvedValue(undefined);
    let visible = false;
    const poller = createPoller({ intervalMs: 1000, run, isVisible: () => visible });
    poller.start();
    await vi.advanceTimersByTimeAsync(5000);
    expect(run).not.toHaveBeenCalled();
    poller.stop();
  });

  it("starts exactly one fresh cycle when the page wakes up", async () => {
    const run = vi.fn().mockResolvedValue(undefined);
    let visible = false;
    const poller = createPoller({ intervalMs: 1000, run, isVisible: () => visible });
    poller.start();
    await vi.advanceTimersByTimeAsync(2000);
    visible = true;
    poller.refresh();
    expect(run).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(1000);
    expect(run).toHaveBeenCalledTimes(2);
    poller.stop();
  });

  it("refresh cancels the in-flight request before starting the new one", async () => {
    const signals: AbortSignal[] = [];
    const run = vi.fn((signal: AbortSignal) => {
      signals.push(signal);
      return new Promise<void>(() => {});
    });
    const poller = createPoller({ intervalMs: 60_000, run, isVisible: () => true });
    poller.start();
    poller.refresh();
    expect(run).toHaveBeenCalledTimes(2);
    expect(signals[0].aborted).toBe(true);
    poller.stop();
  });

  it("stop aborts the in-flight request and stops the timer", async () => {
    const signals: AbortSignal[] = [];
    const run = vi.fn((signal: AbortSignal) => {
      signals.push(signal);
      return new Promise<void>(() => {});
    });
    const poller = createPoller({ intervalMs: 1000, run, isVisible: () => true });
    poller.start();
    poller.stop();
    expect(signals[0].aborted).toBe(true);
    await vi.advanceTimersByTimeAsync(5000);
    expect(run).toHaveBeenCalledTimes(1);
  });
});
