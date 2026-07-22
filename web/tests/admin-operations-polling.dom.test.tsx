// Page-level polling tests for operations doc section 12 item 18 (poll while
// visible, pause while hidden, exactly one fresh cycle on wake) and the
// section 4.3 teardown contract (unmount aborts the in-flight request and
// stops the timer). These run under fake timers from the very first render:
// the 15s interval must be created inside the fake clock to be drivable.

import { afterEach, beforeAll, describe, expect, it, vi } from "vitest";
import { act, render, screen } from "@testing-library/react";
import App from "../src/App";
import {
  callsTo,
  jsonResponse,
  resetBrowserState,
  setupDomTest,
  stubFetch,
  type FetchHandler,
} from "./helpers";
import { makeSummary, operationsFetch, opsMe } from "./operationsFixtures";

setupDomTest();

beforeAll(() => {
  // Direct act() calls outside RTL wrappers need the flag set explicitly.
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
});

afterEach(() => {
  vi.useRealTimers();
  visibility = "visible";
  // Drop the own property so the jsdom prototype getter applies again.
  delete (document as unknown as Record<string, unknown>).visibilityState;
});

let visibility: "visible" | "hidden" = "visible";

function setVisibility(state: "visible" | "hidden"): void {
  visibility = state;
  act(() => {
    document.dispatchEvent(new Event("visibilitychange"));
  });
}

function boot(handler?: FetchHandler) {
  vi.useFakeTimers();
  resetBrowserState();
  window.history.pushState({}, "", "/admin/operations");
  Object.defineProperty(document, "visibilityState", {
    configurable: true,
    get: () => visibility,
  });
  const fetchMock = stubFetch((path, init) => {
    if (path === "/v1/me") return jsonResponse(opsMe());
    return (handler ?? operationsFetch())(path, init);
  });
  const view = render(<App />);
  return { fetchMock, view };
}

/**
 * Flush boot and the first poll cycle without crossing the 15s interval.
 * The page is settled once the storage snapshot (the last region to render
 * its data) is on screen.
 */
async function settle(): Promise<void> {
  for (let i = 0; i < 40; i++) {
    await act(async () => {
      await vi.advanceTimersByTimeAsync(5);
    });
    if (screen.queryByText("database physical")) return;
  }
  throw new Error("operations page did not settle under fake timers");
}

describe("section 12 item 18: polling pauses while hidden and resumes once", () => {
  it("polls summary and events every 15s only while the page is visible", async () => {
    const { fetchMock } = boot();
    await settle();

    const summaryCalls = () => callsTo(fetchMock, "/v1/admin/operations/summary");
    const eventCalls = () => callsTo(fetchMock, "/v1/admin/operations/events");
    const storageCalls = () => callsTo(fetchMock, "/v1/admin/operations/storage");
    expect(summaryCalls()).toHaveLength(1);
    expect(eventCalls()).toHaveLength(1);
    expect(storageCalls()).toHaveLength(1);

    // One interval later: summary and first-page events refresh; storage
    // current is not polled (doc section 4.3).
    await act(async () => {
      await vi.advanceTimersByTimeAsync(15_000);
    });
    expect(summaryCalls()).toHaveLength(2);
    expect(eventCalls()).toHaveLength(2);
    expect(storageCalls()).toHaveLength(1);

    // Hidden: interval ticks are skipped entirely, no catch-up burst.
    setVisibility("hidden");
    await act(async () => {
      await vi.advanceTimersByTimeAsync(60_000);
    });
    expect(summaryCalls()).toHaveLength(2);
    expect(eventCalls()).toHaveLength(2);

    // Visible again: exactly one fresh cycle per polled region.
    setVisibility("visible");
    expect(summaryCalls()).toHaveLength(3);
    expect(eventCalls()).toHaveLength(3);

    // The regular cadence continues afterwards.
    await act(async () => {
      await vi.advanceTimersByTimeAsync(15_000);
    });
    expect(summaryCalls()).toHaveLength(4);
    expect(eventCalls()).toHaveLength(4);
  });
});

describe("section 4.3: teardown aborts the in-flight request", () => {
  it("unmount stops the timer and aborts the hanging poll request", async () => {
    const signals: AbortSignal[] = [];
    let hang = false;
    const { fetchMock, view } = boot((path, init) => {
      if (path.startsWith("/v1/admin/operations/summary")) {
        signals.push(init.signal as AbortSignal);
        if (hang) return new Promise<Response>(() => {});
        return jsonResponse(makeSummary());
      }
      return operationsFetch()(path, init);
    });
    await settle();
    expect(signals).toHaveLength(1);

    // Start a second cycle that never resolves; the poller keeps it in flight.
    hang = true;
    await act(async () => {
      await vi.advanceTimersByTimeAsync(15_000);
    });
    expect(signals).toHaveLength(2);
    expect(signals[1].aborted).toBe(false);

    act(() => view.unmount());
    expect(signals[1].aborted).toBe(true);

    // No further cycles fire after teardown.
    await act(async () => {
      await vi.advanceTimersByTimeAsync(60_000);
    });
    expect(callsTo(fetchMock, "/v1/admin/operations/summary")).toHaveLength(2);
  });
});
