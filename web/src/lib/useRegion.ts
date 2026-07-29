// Shared region skeleton for the polling pages (Operations console, Team
// Pulse): each region owns its loading/ready/error state, retries by bumping
// an epoch, and a failed refresh keeps the last good data marked possibly
// stale instead of clearing the region (operations doc section 11).

import { useCallback, useRef, useState } from "react";
import { usePolling } from "./usePolling";

export interface PolledRegionState<T> {
  data?: T;
  status: "loading" | "ready" | "error";
  error?: unknown;
}

/**
 * Polls `fetch` every `intervalMs`, storing the result as region data. On
 * error the auth handler runs and the region keeps its last good data: it
 * stays "ready" when data was already shown, otherwise it flips to "error".
 * `prev` lets a fetcher inspect the region state from before this cycle.
 */
export function usePolledRegion<T>(
  fetch: (signal: AbortSignal, prev: PolledRegionState<T>) => Promise<T>,
  intervalMs: number,
  deps: readonly unknown[],
  onAuthError: (err: unknown) => void,
): PolledRegionState<T> & { retry: () => void } {
  const [state, setState] = useState<PolledRegionState<T>>({ status: "loading" });
  const stateRef = useRef(state);
  stateRef.current = state;
  const [epoch, setEpoch] = useState(0);

  usePolling(
    async (signal) => {
      const data = await fetch(signal, stateRef.current);
      setState({ status: "ready", data });
    },
    intervalMs,
    [...deps, epoch],
    useCallback(
      (err: unknown) => {
        onAuthError(err);
        // Keep the last good data and mark it possibly stale (doc 11).
        setState((prev) => ({
          ...prev,
          status: prev.status === "ready" ? "ready" : "error",
          error: err,
        }));
      },
      [onAuthError],
    ),
  );

  return { ...state, retry: () => setEpoch((e) => e + 1) };
}
