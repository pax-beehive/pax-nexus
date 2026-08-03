// The in-shell /wiki route is an observability page: ingestion controls,
// extraction progress, an Open Wiki entry, and a legacy deep-link redirect
// (spec 2026-07-29-wiki-standalone-page sections 1-3).
//
// The ingestion-control cases below were ported from the retired
// web/tests/wiki.dom.test.tsx (see d874ac5~1), adapted to the /wiki route:
// that file exercised WikiPage's inline ingestion controls directly; those
// controls now live on WikiStatusPage while browsing moved to
// WikiBrowsePage (see wiki-browse.dom.test.tsx). The retired file's
// "adds a dedicated Knowledge sidebar and renders grounded wiki context"
// case asserted the old inline wiki at /wiki and no longer applies; it is
// replaced below by a case proving the sidebar Wiki link still routes to
// this status page.

import { describe, expect, it } from "vitest";
import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import { apiErrorResponse, callsTo, jsonResponse, makeMe, renderApp, setupDomTest } from "./helpers";
import { llmUsageFixture, wikiFetch } from "./wikiFixtures";

setupDomTest();

describe("wiki status page", () => {
  it("shows ingestion controls, progress, and opens the full-screen wiki", async () => {
    const { user } = await renderApp({
      route: "/wiki",
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
    // Portal shell stays visible around the status page.
    expect(screen.getByText("My Agents")).toBeTruthy();

    await user.click(screen.getByRole("button", { name: "Open Wiki" }));
    await waitFor(() => expect(window.location.pathname).toBe("/wiki/browse"));
  });

  it("degrades to a progress-unavailable notice without blocking controls", async () => {
    await renderApp({
      route: "/wiki",
      me: makeMe(),
      fetch: (path) => {
        if (path === "/v1/wiki/ingestion") return jsonResponse({ auto_inject: false });
        return wikiFetch(path);
      },
    });

    await waitFor(() => expect(screen.getByText("Progress is unavailable.")).toBeTruthy());
    expect(screen.getByRole("switch")).toBeTruthy();
  });

  it("redirects legacy /wiki?page= deep links to the browse route", async () => {
    await renderApp({ route: "/wiki?page=alpha", me: makeMe(), fetch: wikiFetch });

    await waitFor(() => expect(window.location.pathname).toBe("/wiki/browse"));
    expect(window.location.search).toBe("?page=alpha");
    await waitFor(() => expect(screen.getByText("Alpha summary")).toBeTruthy());
  });

  it("routes to the status page from the portal sidebar Wiki link", async () => {
    const { user } = await renderApp({
      route: "/agents",
      me: makeMe(),
      fetch: (path) => {
        if (path === "/v1/wiki/ingestion") return jsonResponse({ auto_inject: false });
        if (path === "/v1/wiki/settings") {
          return jsonResponse({ language: "", custom_instructions: "" });
        }
        if (path.startsWith("/v1/me/agents")) return jsonResponse({ agents: [] });
        if (path.startsWith("/v1/llm-usage")) return jsonResponse(llmUsageFixture);
        throw new Error(`unexpected fetch: ${path}`);
      },
    });

    const portalNav = screen.getByRole("navigation", { name: "Portal navigation" });
    within(portalNav).getByText("Knowledge");
    await user.click(within(portalNav).getByRole("link", { name: "Apps" }));
    await user.click(screen.getByRole("link", { name: /Wiki policy/ }));

    await waitFor(() => expect(window.location.pathname).toBe("/wiki"));
    await screen.findByRole("switch");
    expect(screen.getByRole("button", { name: "Open Wiki" })).toBeTruthy();
  });
});

// -- ingestion controls: toggle, fixed-session injection, and owner-only
// reset & rebuild (ported from the retired wiki.dom.test.tsx's "Page Wiki
// portal integration" describe block) --
describe("wiki status page ingestion controls", () => {
  it("toggles auto injection and manually injects a fixed session", async () => {
    const { user, fetchMock } = await renderApp({
      route: "/wiki",
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
      route: "/wiki",
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
      route: "/wiki",
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
      route: "/wiki",
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
      route: "/wiki",
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
      route: "/wiki",
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
      route: "/wiki",
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
      route: "/wiki",
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
describe("wiki status page generation settings", () => {
  it("renders defaults when no language or instructions are configured", async () => {
    await renderApp({
      route: "/wiki",
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
      route: "/wiki",
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
      route: "/wiki",
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

// -- LLM usage card (2026-07-31-llm-token-metering task 4) --
describe("wiki status page LLM usage", () => {
  it("renders a table with a row per component plus a totals row", async () => {
    await renderApp({ route: "/wiki", me: makeMe(), fetch: wikiFetch });

    const card = await screen.findByRole("region", { name: "LLM token usage" });
    within(card).getByText("extractor");
    within(card).getByText("wiki-editor");
    within(card).getByText("120,000");
    within(card).getByText("400,000");
    // Totals row: calls 12+30=42, input 120000+400000=520,000.
    within(card).getByText("42");
    within(card).getByText("520,000");
  });

  it("refetches with the selected window when the select changes", async () => {
    const { user, fetchMock } = await renderApp({ route: "/wiki", me: makeMe(), fetch: wikiFetch });

    const card = await screen.findByRole("region", { name: "LLM token usage" });
    expect(callsTo(fetchMock, "/v1/llm-usage?days=7")).toHaveLength(1);

    await user.selectOptions(within(card).getByLabelText("Window"), "30");

    await waitFor(() => expect(callsTo(fetchMock, "/v1/llm-usage?days=30")).toHaveLength(1));
  });

  it("shows an unavailable note when the usage endpoint fails, page otherwise intact", async () => {
    await renderApp({
      route: "/wiki",
      me: makeMe(),
      fetch: (path) => {
        if (path.startsWith("/v1/llm-usage")) return apiErrorResponse(500, "internal", "boom");
        return wikiFetch(path);
      },
    });

    await screen.findByRole("switch");
    const card = await screen.findByRole("region", { name: "LLM token usage" });
    within(card).getByText("LLM usage is unavailable.");
    expect(screen.queryByText("extractor")).toBeNull();
  });
});
