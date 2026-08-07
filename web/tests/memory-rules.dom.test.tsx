// /settings/memory is the memory-rules settings page: ingestion controls,
// extraction progress, generation settings, an Open Wiki entry, and a
// legacy deep-link redirect (spec 2026-07-29-wiki-standalone-page sections
// 1-3). LLM token usage used to render here too but was split out to its
// own route at /settings/usage (ModelUsagePage, phase 6 task 1) — see
// model-usage.dom.test.tsx for that card's coverage. The two "not render"
// assertions below are the ones that actually prove the split: without
// them, rendering the same component at both routes would pass everything
// else here too.
//
// The ingestion-control cases below were ported from the retired
// web/tests/wiki.dom.test.tsx (see d874ac5~1), adapted to the /wiki route:
// that file exercised WikiPage's inline ingestion controls directly; those
// controls now live on MemoryRulesPage while browsing moved to
// WikiBrowsePage (see wiki-browse.dom.test.tsx). The retired file's
// "adds a dedicated Knowledge sidebar and renders grounded wiki context"
// case asserted the old inline wiki at /wiki and no longer applies; it is
// replaced below by a case proving the sidebar Wiki link still routes to
// this status page.

import { describe, expect, it, vi } from "vitest";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import App from "../src/App";
import {
  apiErrorResponse,
  callsTo,
  jsonResponse,
  makeMe,
  renderApp,
  resetBrowserState,
  setupDomTest,
  stubFetch,
} from "./helpers";
import { llmUsageFixture, wikiFetch } from "./wikiFixtures";

setupDomTest();

describe("memory rules page", () => {
  it("renders the progress, ingestion, and generation cards but not the LLM usage card", async () => {
    await renderApp({ route: "/settings/memory", me: makeMe(), fetch: wikiFetch });

    await screen.findByRole("region", { name: "Extraction progress" });
    await screen.findByRole("region", { name: "Wiki ingestion controls" });
    await screen.findByRole("region", { name: "Wiki generation settings" });
    expect(screen.queryByRole("region", { name: "LLM token usage" })).toBeNull();
  });

  it("shows ingestion controls, progress, and opens the full-screen wiki", async () => {
    const { user } = await renderApp({
      route: "/settings/memory",
      me: makeMe(),
      fetch: (path) => {
        if (path === "/v1/wiki/ingestion") {
          return jsonResponse({
            auto_inject: true,
            pending_sessions: 3,
            last_processed_at: "2026-07-29T08:00:00Z",
          });
        }
        return wikiFetch(path);
      },
    });

    await waitFor(() => expect(screen.getByRole("switch")).toBeTruthy());
    expect(screen.getByText("3")).toBeTruthy();
    // Portal shell (top bar) stays visible around the status page.
    expect(screen.getByRole("navigation", { name: "Sections" })).toBeTruthy();

    await user.click(screen.getByRole("button", { name: "Open Wiki" }));
    // Lands on /apps/wiki with no slug, then WikiBrowsePage auto-selects the
    // first page and rewrites the URL to /apps/wiki/:slug.
    await waitFor(() => expect(window.location.pathname).toBe("/apps/wiki/alpha"));
  });

  it("degrades to a progress-unavailable notice without blocking controls", async () => {
    await renderApp({
      route: "/settings/memory",
      me: makeMe(),
      fetch: (path) => {
        if (path === "/v1/wiki/ingestion") return jsonResponse({ auto_inject: false });
        return wikiFetch(path);
      },
    });

    await waitFor(() => expect(screen.getByText("Progress is unavailable.")).toBeTruthy());
    expect(screen.getByRole("switch")).toBeTruthy();
  });

  // The case above ("degrades to a progress-unavailable notice...") only
  // exercises the response-shape branch (pending_sessions missing from an
  // otherwise-successful response) — status stays undefined either way, so
  // it can't tell that branch apart from the actual fetch-failure branch
  // (statusError, set in the catch block of the polled ingestion-status
  // fetch). Deleting `setStatusError(true)` from that catch left every test
  // in this suite green. This case closes the gap for real: the first poll
  // succeeds with a populated status (so `status` itself is defined and
  // would render numbers), then a second poll's request fails — only
  // `statusError` flipping to true can explain the card going blank from
  // there, and the other two cards (loaded independently) must keep working
  // through it.
  it("degrades only the progress card when a later ingestion-status poll fails", async () => {
    vi.useFakeTimers();
    try {
      resetBrowserState();
      window.history.pushState({}, "", "/settings/memory");
      let ingestionCalls = 0;
      stubFetch((path) => {
        if (path === "/v1/me") return jsonResponse(makeMe());
        if (path === "/v1/wiki/ingestion") {
          ingestionCalls += 1;
          if (ingestionCalls === 1) {
            return jsonResponse({
              auto_inject: false,
              pending_sessions: 5,
              last_processed_at: "2026-07-29T08:00:00Z",
            });
          }
          return apiErrorResponse(500, "internal", "boom");
        }
        return wikiFetch(path);
      });
      render(<App />);

      // First poll succeeds: the progress card shows real numbers.
      for (let i = 0; i < 200 && ingestionCalls < 1; i += 1) {
        await act(async () => {
          await vi.advanceTimersByTimeAsync(5);
        });
      }
      await act(async () => {
        await vi.advanceTimersByTimeAsync(5);
      });
      expect(screen.getByText("5")).toBeTruthy();
      expect(screen.queryByText("Progress is unavailable.")).toBeNull();

      // Second poll (5s cadence) fails: only statusError can take the card
      // from showing "5" to unavailable, since `status` itself isn't cleared.
      await act(async () => {
        await vi.advanceTimersByTimeAsync(5_000);
      });
      expect(ingestionCalls).toBeGreaterThanOrEqual(2);
      for (let i = 0; i < 200; i += 1) {
        if (screen.queryByText("Progress is unavailable.")) break;
        await act(async () => {
          await vi.advanceTimersByTimeAsync(5);
        });
      }
      expect(screen.getByText("Progress is unavailable.")).toBeTruthy();

      // Ingestion controls and generation settings loaded independently and
      // still work.
      expect(screen.getByRole("switch")).toBeTruthy();
      expect(screen.getByRole("region", { name: "Wiki generation settings" })).toBeTruthy();
    } finally {
      vi.useRealTimers();
    }
  });

  it("redirects the bare legacy /wiki route to this settings page", async () => {
    await renderApp({ route: "/wiki", me: makeMe(), fetch: wikiFetch });

    await waitFor(() => expect(window.location.pathname).toBe("/settings/memory"));
    await screen.findByRole("region", { name: "Wiki ingestion controls" });
  });

  it("redirects legacy /wiki?page= deep links to the browse route", async () => {
    await renderApp({ route: "/wiki?page=alpha", me: makeMe(), fetch: wikiFetch });

    await waitFor(() => expect(window.location.pathname).toBe("/apps/wiki/alpha"));
    expect(window.location.search).toBe("");
    await waitFor(() => expect(screen.getByText("Alpha summary")).toBeTruthy());
  });

  // Landing directly on /settings/memory?page=<slug> never happens through
  // the top-level /wiki legacy route (LegacyRedirect resolves that one hop
  // straight to /apps/wiki/:slug — see legacy-routes.test.ts), so it isn't
  // covered by the case above. It's still this page's own responsibility:
  // this is the effect that forwards a stray ?page= query on the settings
  // route itself, in case anything (an old saved link, a stale in-app
  // navigation) lands here with one still attached.
  it("forwards a stray ?page= query on /settings/memory itself to the browse route", async () => {
    await renderApp({ route: "/settings/memory?page=alpha", me: makeMe(), fetch: wikiFetch });

    await waitFor(() => expect(window.location.pathname).toBe("/apps/wiki/alpha"));
    expect(window.location.search).toBe("");
    await waitFor(() => expect(screen.getByText("Alpha summary")).toBeTruthy());
  });

  it("routes to the status page from the portal top bar Settings link", async () => {
    const { user } = await renderApp({
      route: "/management",
      me: makeMe(),
      fetch: (path) => {
        if (path === "/v1/wiki/ingestion") return jsonResponse({ auto_inject: false });
        if (path === "/v1/wiki/settings") {
          return jsonResponse({ language: "", custom_instructions: "" });
        }
        if (path.startsWith("/v1/me/agents")) return jsonResponse({ agents: [] });
        if (path.startsWith("/v1/llm-usage")) return jsonResponse(llmUsageFixture);
        // Default makeMe() is owner, so /management renders admin+'s access
        // tree (AdminAccessTree), whose snapshot pulls all three admin list
        // legs at once. members is the spine — without it the page is the
        // tree's full error card. devices and agents are not: a missing leg
        // still renders the root level, but with "Could not be loaded" in
        // its summary tiles. All three are stubbed so this test starts from
        // a real, fully loaded page; its assertions look only at the top
        // bar, so a degraded page underneath would go unnoticed.
        if (path.startsWith("/v1/admin/members")) return jsonResponse({ members: [] });
        if (path.startsWith("/v1/admin/devices")) return jsonResponse({ devices: [] });
        if (path.startsWith("/v1/admin/agents")) return jsonResponse({ agents: [] });
        throw new Error(`unexpected fetch: ${path}`);
      },
    });

    const topbar = screen.getByRole("navigation", { name: "Sections" });
    await user.click(within(topbar).getByRole("link", { name: "Settings" }));

    await waitFor(() => expect(window.location.pathname).toBe("/settings/memory"));
    await screen.findByRole("switch");
    expect(screen.getByRole("button", { name: "Open Wiki" })).toBeTruthy();
  });
});

// -- ingestion controls: toggle, fixed-session injection, and owner-only
// reset & rebuild (ported from the retired wiki.dom.test.tsx's "Page Wiki
// portal integration" describe block) --
describe("memory rules page ingestion controls", () => {
  it("toggles auto injection and manually injects a fixed session", async () => {
    const { user, fetchMock } = await renderApp({
      route: "/settings/memory",
      me: makeMe({ role: "member" }),
      fetch: (path, init) => {
        const method = init?.method ?? "GET";
        if (path === "/v1/wiki/ingestion" && method === "PUT") {
          return jsonResponse({ auto_inject: true });
        }
        if (path === "/v1/wiki/ingestion") return jsonResponse({ auto_inject: false });
        if (path === "/v1/wiki/settings") {
          return jsonResponse({ language: "", custom_instructions: "" });
        }
        if (path === "/v1/wiki/sessions/runtime-session/inject") {
          return jsonResponse({ processed_streams: 1 });
        }
        if (path.startsWith("/v1/llm-usage")) return jsonResponse(llmUsageFixture);
        throw new Error(`unexpected fetch: ${path}`);
      },
    });

    const toggle = await screen.findByRole("switch", { name: "Off" });
    await user.click(toggle);
    await waitFor(() => expect(toggle.getAttribute("aria-checked")).toBe("true"));

    await user.type(screen.getByLabelText("Fixed session ID"), "runtime-session");
    await user.click(screen.getByRole("button", { name: "Inject session" }));
    await screen.findByText("Injected 1 stream from runtime-session.");

    expect(callsTo(fetchMock, "/v1/wiki/ingestion", "PUT")).toHaveLength(1);
    expect(callsTo(fetchMock, "/v1/wiki/sessions/runtime-session/inject", "POST")).toHaveLength(1);
  });

  it("lets an owner confirm a full Wiki rebuild without deleting Session Lake", async () => {
    const { user, fetchMock } = await renderApp({
      route: "/settings/memory",
      me: makeMe({ role: "owner" }),
      fetch: (path, init) => {
        const method = init?.method ?? "GET";
        if (path === "/v1/wiki/rebuild" && method === "POST") {
          return jsonResponse({ auto_inject: true, rebuild_state: "queued" });
        }
        if (path === "/v1/wiki/ingestion") return jsonResponse({ auto_inject: false });
        if (path === "/v1/wiki/settings") {
          return jsonResponse({ language: "", custom_instructions: "" });
        }
        if (path.startsWith("/v1/llm-usage")) return jsonResponse(llmUsageFixture);
        throw new Error(`unexpected fetch: ${path}`);
      },
    });

    await screen.findByRole("switch");
    await user.click(screen.getByRole("button", { name: "Reset & rebuild" }));
    const dialog = screen.getByRole("dialog", { name: "Reset and rebuild Wiki" });
    within(dialog).getByText("Session Lake events and Team Notes are preserved.");
    await user.click(within(dialog).getByRole("button", { name: "Confirm reset & rebuild" }));

    await screen.findByText(
      "Reset & rebuild queued. The wiki will be cleared and rebuilt in the background.",
    );
    expect(callsTo(fetchMock, "/v1/wiki/rebuild", "POST")).toHaveLength(1);
  });

  it("closes the rebuild dialog on confirm before the server responds", async () => {
    // The rebuild endpoint can wait minutes behind an in-flight injection
    // sweep; the dialog must not hold the page hostage while it does.
    let releaseRebuild: (() => void) | undefined;
    const { user } = await renderApp({
      route: "/settings/memory",
      me: makeMe({ role: "owner" }),
      fetch: (path, init) => {
        const method = init?.method ?? "GET";
        if (path === "/v1/wiki/rebuild" && method === "POST") {
          return new Promise<Response>((resolve) => {
            releaseRebuild = () =>
              resolve(jsonResponse({ auto_inject: true, rebuild_state: "queued" }));
          });
        }
        if (path === "/v1/wiki/ingestion") return jsonResponse({ auto_inject: false });
        if (path === "/v1/wiki/settings") {
          return jsonResponse({ language: "", custom_instructions: "" });
        }
        if (path.startsWith("/v1/llm-usage")) return jsonResponse(llmUsageFixture);
        throw new Error(`unexpected fetch: ${path}`);
      },
    });

    await screen.findByRole("switch");
    await user.click(screen.getByRole("button", { name: "Reset & rebuild" }));
    const dialog = screen.getByRole("dialog", { name: "Reset and rebuild Wiki" });
    await user.click(within(dialog).getByRole("button", { name: "Confirm reset & rebuild" }));

    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    screen.getByText(/Reset & rebuild triggered/);

    releaseRebuild?.();
    await screen.findByText(
      "Reset & rebuild queued. The wiki will be cleared and rebuilt in the background.",
    );
  });

  it("sends the lookback cutoff when a rebuild date is picked", async () => {
    const { user, fetchMock } = await renderApp({
      route: "/settings/memory",
      me: makeMe({ role: "owner" }),
      fetch: (path, init) => {
        const method = init?.method ?? "GET";
        if (path === "/v1/wiki/rebuild" && method === "POST") {
          return jsonResponse({ auto_inject: true });
        }
        if (path === "/v1/wiki/ingestion") return jsonResponse({ auto_inject: false });
        if (path === "/v1/wiki/settings") {
          return jsonResponse({ language: "", custom_instructions: "" });
        }
        if (path.startsWith("/v1/llm-usage")) return jsonResponse(llmUsageFixture);
        throw new Error(`unexpected fetch: ${path}`);
      },
    });

    await screen.findByRole("switch");
    await user.click(screen.getByRole("button", { name: "Reset & rebuild" }));
    const dialog = screen.getByRole("dialog", { name: "Reset and rebuild Wiki" });
    fireEvent.change(within(dialog).getByLabelText("Replay sessions since (optional)"), {
      target: { value: "2026-07-01" },
    });
    expect(
      within(dialog).getByText(/Only sessions with activity on or after 2026-07-01/),
    ).toBeTruthy();
    await user.click(within(dialog).getByRole("button", { name: "Confirm reset & rebuild" }));

    await screen.findByText(
      "Reset & rebuild queued. The wiki will be cleared and rebuilt in the background.",
    );
    const calls = callsTo(fetchMock, "/v1/wiki/rebuild", "POST");
    expect(calls).toHaveLength(1);
    expect(JSON.parse(String(calls[0].init?.body))).toEqual({
      since: new Date("2026-07-01T00:00:00").toISOString(),
    });
  });

  it("omits since when the rebuild date is left empty", async () => {
    const { user, fetchMock } = await renderApp({
      route: "/settings/memory",
      me: makeMe({ role: "owner" }),
      fetch: (path, init) => {
        const method = init?.method ?? "GET";
        if (path === "/v1/wiki/rebuild" && method === "POST") {
          return jsonResponse({ auto_inject: true });
        }
        if (path === "/v1/wiki/ingestion") return jsonResponse({ auto_inject: false });
        if (path === "/v1/wiki/settings") {
          return jsonResponse({ language: "", custom_instructions: "" });
        }
        if (path.startsWith("/v1/llm-usage")) return jsonResponse(llmUsageFixture);
        throw new Error(`unexpected fetch: ${path}`);
      },
    });

    await screen.findByRole("switch");
    await user.click(screen.getByRole("button", { name: "Reset & rebuild" }));
    const dialog = screen.getByRole("dialog", { name: "Reset and rebuild Wiki" });
    await user.click(within(dialog).getByRole("button", { name: "Confirm reset & rebuild" }));

    await screen.findByText(
      "Reset & rebuild queued. The wiki will be cleared and rebuilt in the background.",
    );
    const calls = callsTo(fetchMock, "/v1/wiki/rebuild", "POST");
    expect(calls).toHaveLength(1);
    expect(JSON.parse(String(calls[0].init?.body))).toEqual({});
  });

  it("disables Reset & rebuild and shows progress while a rebuild runs", async () => {
    await renderApp({
      route: "/settings/memory",
      me: makeMe({ role: "owner" }),
      fetch: (path) => {
        if (path === "/v1/wiki/ingestion") {
          return jsonResponse({ auto_inject: true, rebuild_state: "running" });
        }
        if (path === "/v1/wiki/settings") {
          return jsonResponse({ language: "", custom_instructions: "" });
        }
        if (path.startsWith("/v1/llm-usage")) return jsonResponse(llmUsageFixture);
        throw new Error(`unexpected fetch: ${path}`);
      },
    });

    await screen.findByText("Rebuild in progress…");
    const rebuildButton = screen.getByRole("button", { name: "Reset & rebuild" });
    expect(rebuildButton.hasAttribute("disabled")).toBe(true);
  });

  it("surfaces a failed rebuild with its error", async () => {
    await renderApp({
      route: "/settings/memory",
      me: makeMe({ role: "owner" }),
      fetch: (path) => {
        if (path === "/v1/wiki/ingestion") {
          return jsonResponse({
            auto_inject: false,
            rebuild_state: "failed",
            rebuild_error: "database unavailable",
          });
        }
        if (path === "/v1/wiki/settings") {
          return jsonResponse({ language: "", custom_instructions: "" });
        }
        if (path.startsWith("/v1/llm-usage")) return jsonResponse(llmUsageFixture);
        throw new Error(`unexpected fetch: ${path}`);
      },
    });

    await screen.findByText("Rebuild failed: database unavailable");
    const rebuildButton = screen.getByRole("button", { name: "Reset & rebuild" });
    expect(rebuildButton.hasAttribute("disabled")).toBe(false);
  });

  it("hides the destructive rebuild control from members", async () => {
    await renderApp({
      route: "/settings/memory",
      me: makeMe({ role: "member" }),
      fetch: (path) => {
        if (path === "/v1/wiki/ingestion") return jsonResponse({ auto_inject: false });
        if (path === "/v1/wiki/settings") {
          return jsonResponse({ language: "", custom_instructions: "" });
        }
        if (path.startsWith("/v1/llm-usage")) return jsonResponse(llmUsageFixture);
        throw new Error(`unexpected fetch: ${path}`);
      },
    });

    await screen.findByRole("switch");
    expect(screen.queryByRole("button", { name: "Reset & rebuild" })).toBeNull();
  });
});

// -- generation settings card (spec 2026-07-30-wiki-generation-settings) --
describe("memory rules page generation settings", () => {
  it("renders defaults when no language or instructions are configured", async () => {
    await renderApp({
      route: "/settings/memory",
      me: makeMe(),
      fetch: (path, init) => {
        const method = init?.method ?? "GET";
        if (path === "/v1/wiki/ingestion") return jsonResponse({ auto_inject: false });
        if (path === "/v1/wiki/settings" && method === "GET") {
          return jsonResponse({ language: "", custom_instructions: "" });
        }
        if (path.startsWith("/v1/llm-usage")) return jsonResponse(llmUsageFixture);
        throw new Error(`unexpected fetch: ${path}`);
      },
    });

    const languageSelect = (await screen.findByLabelText("Language")) as HTMLSelectElement;
    expect(languageSelect.value).toBe("");
    const selectedOption = within(languageSelect).getByRole("option", {
      name: "Follow source evidence",
    }) as HTMLOptionElement;
    expect(selectedOption.selected).toBe(true);
    const instructionsField = screen.getByLabelText(
      "Custom instructions",
    ) as HTMLTextAreaElement;
    expect(instructionsField.value).toBe("");
    expect(screen.queryByLabelText("Custom language")).toBeNull();
  });

  it("saves the selected language and instructions", async () => {
    const { user, fetchMock } = await renderApp({
      route: "/settings/memory",
      me: makeMe(),
      fetch: (path, init) => {
        const method = init?.method ?? "GET";
        if (path === "/v1/wiki/ingestion") return jsonResponse({ auto_inject: false });
        if (path === "/v1/wiki/settings" && method === "PUT") {
          return jsonResponse({ language: "简体中文", custom_instructions: "Keep it short." });
        }
        if (path === "/v1/wiki/settings") {
          return jsonResponse({ language: "", custom_instructions: "" });
        }
        if (path.startsWith("/v1/llm-usage")) return jsonResponse(llmUsageFixture);
        throw new Error(`unexpected fetch: ${path}`);
      },
    });

    await screen.findByLabelText("Language");
    await user.selectOptions(screen.getByLabelText("Language"), "简体中文");
    await user.type(screen.getByLabelText("Custom instructions"), "Keep it short.");
    await user.click(screen.getByRole("button", { name: "Save generation settings" }));

    await screen.findByText(
      "Generation settings saved. They apply to future runs only; use Rebuild to regenerate existing pages.",
    );
    const calls = callsTo(fetchMock, "/v1/wiki/settings", "PUT");
    expect(calls).toHaveLength(1);
    expect(JSON.parse(calls[0].init.body as string)).toEqual({
      language: "简体中文",
      custom_instructions: "Keep it short.",
    });
  });

  it("falls back to a custom-language input for a value outside the presets", async () => {
    await renderApp({
      route: "/settings/memory",
      me: makeMe(),
      fetch: (path, init) => {
        const method = init?.method ?? "GET";
        if (path === "/v1/wiki/ingestion") return jsonResponse({ auto_inject: false });
        if (path === "/v1/wiki/settings" && method === "GET") {
          return jsonResponse({ language: "日本語", custom_instructions: "" });
        }
        if (path.startsWith("/v1/llm-usage")) return jsonResponse(llmUsageFixture);
        throw new Error(`unexpected fetch: ${path}`);
      },
    });

    const languageSelect = (await screen.findByLabelText("Language")) as HTMLSelectElement;
    await waitFor(() => expect(languageSelect.value).toBe("custom"));
    const customInput = (await screen.findByLabelText("Custom language")) as HTMLInputElement;
    expect(customInput.value).toBe("日本語");
  });
});
