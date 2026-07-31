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
import { callsTo, jsonResponse, makeMe, renderApp, setupDomTest } from "./helpers";
import { wikiFetch } from "./wikiFixtures";
import { INSTRUCTION_PRESETS, composeInstructions } from "../src/lib/instructionPresets";

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
        throw new Error(`unexpected fetch: ${path}`);
      },
    });

    const portalNav = screen.getByRole("navigation", { name: "Portal navigation" });
    within(portalNav).getByText("Knowledge");
    await user.click(within(portalNav).getByRole("link", { name: "Wiki" }));

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
          return jsonResponse({ auto_inject: true });
        }
        if (path === "/v1/wiki/ingestion") return jsonResponse({ auto_inject: false });
        if (path === "/v1/wiki/settings") {
          return jsonResponse({ language: "", custom_instructions: "" });
        }
        throw new Error(`unexpected fetch: ${path}`);
      },
    });

    await screen.findByRole("switch");
    await user.click(screen.getByRole("button", { name: "Reset & rebuild" }));
    const dialog = screen.getByRole("dialog", { name: "Reset and rebuild Wiki" });
    within(dialog).getByText("Session Lake events and Team Notes are preserved.");
    await user.click(within(dialog).getByRole("button", { name: "Confirm reset & rebuild" }));

    await screen.findByText("Wiki cleared. Rebuilding from Session Lake…");
    expect(callsTo(fetchMock, "/v1/wiki/rebuild", "POST")).toHaveLength(1);
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
        throw new Error(`unexpected fetch: ${path}`);
      },
    });

    await screen.findByRole("switch");
    expect(screen.queryByRole("button", { name: "Reset & rebuild" })).toBeNull();
  });
});

// -- generation settings card, chip-based (spec 2026-07-30-wiki-instruction-presets) --
describe("wiki status page generation settings", () => {
  it("renders language chips with Follow source evidence selected by default, and saves a picked language", async () => {
    const { user, fetchMock } = await renderApp({
      route: "/wiki",
      me: makeMe(),
      fetch: (path, init) => {
        const method = init?.method ?? "GET";
        if (path === "/v1/wiki/ingestion") return jsonResponse({ auto_inject: false });
        if (path === "/v1/wiki/settings" && method === "PUT") {
          return jsonResponse({ language: "简体中文", custom_instructions: "" });
        }
        if (path === "/v1/wiki/settings") {
          return jsonResponse({ language: "", custom_instructions: "" });
        }
        throw new Error(`unexpected fetch: ${path}`);
      },
    });

    await screen.findByRole("button", { name: "Follow source evidence" });
    for (const label of ["Follow source evidence", "简体中文", "English", "Custom…"]) {
      expect(screen.getByRole("button", { name: label })).toBeTruthy();
    }
    expect(
      screen.getByRole("button", { name: "Follow source evidence" }).getAttribute("aria-pressed"),
    ).toBe("true");
    expect(screen.queryByLabelText("Custom language")).toBeNull();

    await user.click(screen.getByRole("button", { name: "简体中文" }));
    await user.click(screen.getByRole("button", { name: "Save generation settings" }));

    await screen.findByText(
      "Generation settings saved. They apply to future runs only; use Rebuild to regenerate existing pages.",
    );
    const calls = callsTo(fetchMock, "/v1/wiki/settings", "PUT");
    expect(calls).toHaveLength(1);
    expect(JSON.parse(calls[0].init.body as string).language).toBe("简体中文");
  });

  it("falls back to a custom-language chip for a value outside the presets", async () => {
    await renderApp({
      route: "/wiki",
      me: makeMe(),
      fetch: (path, init) => {
        const method = init?.method ?? "GET";
        if (path === "/v1/wiki/ingestion") return jsonResponse({ auto_inject: false });
        if (path === "/v1/wiki/settings" && method === "GET") {
          return jsonResponse({ language: "日本語", custom_instructions: "" });
        }
        throw new Error(`unexpected fetch: ${path}`);
      },
    });

    const customChip = await screen.findByRole("button", { name: "Custom…" });
    await waitFor(() => expect(customChip.getAttribute("aria-pressed")).toBe("true"));
    const customInput = (await screen.findByLabelText("Custom language")) as HTMLInputElement;
    expect(customInput.value).toBe("日本語");
  });

  it("composes selected style-preset chips with hand-written additional instructions on save", async () => {
    const { user, fetchMock } = await renderApp({
      route: "/wiki",
      me: makeMe(),
      fetch: (path, init) => {
        const method = init?.method ?? "GET";
        if (path === "/v1/wiki/ingestion") return jsonResponse({ auto_inject: false });
        if (path === "/v1/wiki/settings" && method === "PUT") {
          return jsonResponse({ language: "", custom_instructions: "stored" });
        }
        if (path === "/v1/wiki/settings") {
          return jsonResponse({ language: "", custom_instructions: "" });
        }
        throw new Error(`unexpected fetch: ${path}`);
      },
    });

    await screen.findByRole("button", { name: "Prefer tables" });
    await user.click(screen.getByRole("button", { name: "Prefer tables" }));
    await user.click(screen.getByRole("button", { name: "Concise bullets" }));
    await user.type(screen.getByLabelText("Additional instructions"), "extra guidance");
    await user.click(screen.getByRole("button", { name: "Save generation settings" }));

    await screen.findByText(
      "Generation settings saved. They apply to future runs only; use Rebuild to regenerate existing pages.",
    );
    const calls = callsTo(fetchMock, "/v1/wiki/settings", "PUT");
    expect(calls).toHaveLength(1);
    expect(JSON.parse(calls[0].init.body as string).custom_instructions).toBe(
      composeInstructions(["tables", "bullets"], "extra guidance"),
    );
  });

  it("lights matching chips from stored custom_instructions, leaving the remainder as additional text", async () => {
    const stored = composeInstructions(["tables", "tldr"], "hand-written note");
    await renderApp({
      route: "/wiki",
      me: makeMe(),
      fetch: (path, init) => {
        const method = init?.method ?? "GET";
        if (path === "/v1/wiki/ingestion") return jsonResponse({ auto_inject: false });
        if (path === "/v1/wiki/settings" && method === "GET") {
          return jsonResponse({ language: "", custom_instructions: stored });
        }
        throw new Error(`unexpected fetch: ${path}`);
      },
    });

    const tablesChip = await screen.findByRole("button", { name: "Prefer tables" });
    await waitFor(() => expect(tablesChip.getAttribute("aria-pressed")).toBe("true"));
    expect(
      screen.getByRole("button", { name: "TL;DR first" }).getAttribute("aria-pressed"),
    ).toBe("true");
    expect(
      screen.getByRole("button", { name: "Concise bullets" }).getAttribute("aria-pressed"),
    ).toBe("false");
    const additional = (await screen.findByLabelText(
      "Additional instructions",
    )) as HTMLTextAreaElement;
    expect(additional.value).toBe("hand-written note");
  });

  it("treats an edited preset sentence as free text instead of matching its chip", async () => {
    const altered = INSTRUCTION_PRESETS[0].sentence.replace("structured", "organized");
    await renderApp({
      route: "/wiki",
      me: makeMe(),
      fetch: (path, init) => {
        const method = init?.method ?? "GET";
        if (path === "/v1/wiki/ingestion") return jsonResponse({ auto_inject: false });
        if (path === "/v1/wiki/settings" && method === "GET") {
          return jsonResponse({ language: "", custom_instructions: altered });
        }
        throw new Error(`unexpected fetch: ${path}`);
      },
    });

    const tablesChip = await screen.findByRole("button", { name: "Prefer tables" });
    await waitFor(() => expect(tablesChip.getAttribute("aria-pressed")).toBe("false"));
    const additional = (await screen.findByLabelText(
      "Additional instructions",
    )) as HTMLTextAreaElement;
    expect(additional.value).toBe(altered);
  });

  it("disables save and shows a negative counter when composed instructions exceed the limit", async () => {
    const { user } = await renderApp({
      route: "/wiki",
      me: makeMe(),
      fetch: (path, init) => {
        const method = init?.method ?? "GET";
        if (path === "/v1/wiki/ingestion") return jsonResponse({ auto_inject: false });
        if (path === "/v1/wiki/settings" && method === "GET") {
          return jsonResponse({ language: "", custom_instructions: "" });
        }
        throw new Error(`unexpected fetch: ${path}`);
      },
    });

    await user.click(await screen.findByRole("button", { name: "Prefer tables" }));
    const additional = await screen.findByLabelText("Additional instructions");
    fireEvent.change(additional, { target: { value: "a".repeat(2000) } });

    const saveButton = screen.getByRole("button", { name: "Save generation settings" });
    await waitFor(() => expect((saveButton as HTMLButtonElement).disabled).toBe(true));
    expect(screen.getByText(/^-\d+ characters left$/)).toBeTruthy();
  });
});
