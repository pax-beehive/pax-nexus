// Interval polling with real cancellation (operations doc section 4.3):
// every cycle aborts the previous in-flight request so polls never overlap,
// cycles are skipped while the document is hidden, waking up starts exactly
// one fresh cycle, and an AbortError from a cancelled request is a normal
// outcome that is never surfaced as an error.

import { useEffect, useRef } from "react";

export function isAbortError(err: unknown): boolean {
  return err instanceof Error && err.name === "AbortError";
}

export interface PollerOptions {
  intervalMs: number;
  run: (signal: AbortSignal) => Promise<unknown>;
  isVisible: () => boolean;
  onError?: (err: unknown) => void;
}

export interface Poller {
  /** Starts an immediate cycle, then one per interval. Idempotent. */
  start: () => void;
  /** Stops the timer and aborts any in-flight request. */
  stop: () => void;
  /** Runs one cycle now, cancelling the previous in-flight request. */
  refresh: () => void;
}

export function createPoller(options: PollerOptions): Poller {
  let timer: ReturnType<typeof setInterval> | undefined;
  let controller: AbortController | undefined;
  let running = false;

  const tick = () => {
    if (!running || !options.isVisible()) return;
    controller?.abort();
    const current = new AbortController();
    controller = current;
    void options
      .run(current.signal)
      .catch((err: unknown) => {
        if (!isAbortError(err)) options.onError?.(err);
      })
      .finally(() => {
        if (controller === current) controller = undefined;
      });
  };

  return {
    start() {
      if (running) return;
      running = true;
      tick();
      timer = setInterval(tick, options.intervalMs);
    },
    stop() {
      running = false;
      if (timer !== undefined) clearInterval(timer);
      timer = undefined;
      controller?.abort();
      controller = undefined;
    },
    refresh() {
      tick();
    },
  };
}

/**
 * Polls `run` every `intervalMs` while the document is visible. Changing
 * `deps` tears down the old poller (aborting its in-flight request) and
 * starts a fresh one with an immediate cycle.
 */
export function usePolling(
  run: (signal: AbortSignal) => Promise<unknown>,
  intervalMs: number,
  deps: readonly unknown[],
  onError?: (err: unknown) => void,
): void {
  const runRef = useRef(run);
  runRef.current = run;
  const errorRef = useRef(onError);
  errorRef.current = onError;

  useEffect(() => {
    const poller = createPoller({
      intervalMs,
      run: (signal) => runRef.current(signal),
      isVisible: () => document.visibilityState === "visible",
      onError: (err) => errorRef.current?.(err),
    });
    const onVisibility = () => {
      // Waking up starts exactly one fresh cycle instead of waiting for the
      // next interval boundary (operations doc section 4.3).
      if (document.visibilityState === "visible") poller.refresh();
    };
    document.addEventListener("visibilitychange", onVisibility);
    poller.start();
    return () => {
      document.removeEventListener("visibilitychange", onVisibility);
      poller.stop();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [...deps, intervalMs]);
}
