import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import App from "../src/App";
import {
  callsTo,
  jsonResponse,
  makeMe,
  renderApp,
  resetBrowserState,
  setupDomTest,
  stubFetch,
} from "./helpers";

setupDomTest();

const navigation = {
  roots: [
    {
      id: "topic-engineering",
      slug: "engineering",
      title: "Engineering",
      children: [],
      pages: [
        { id: "page-sqlite", slug: "sqlite", title: "SQLite", rank: 1 },
        { id: "page-runtime", slug: "runtime", title: "Runtime", rank: 2 },
      ],
    },
  ],
};

function revision(id = "revision-current", title = "SQLite") {
  return {
    id,
    page_id: "page-sqlite",
    title,
    summary: "The durable local knowledge store.",
    sections: [
      {
        key: "decision",
        heading: "Decision",
        markdown: "SQLite is searchable and links to the runtime.",
      },
      {
        key: "source-evidence",
        heading: "Source evidence",
        markdown: "SQLite is searchable.",
      },
    ],
    markdown: `# ${title}\n\n## Decision\n\nSQLite is searchable and links to the runtime.\n\n## Source evidence\n\nSQLite is searchable.`,
    citations: [
      {
        id: "citation-1",
        page_revision_id: id,
        section_key: "decision",
        start_byte: 0,
        end_byte: 20,
        exact_text: "SQLite is searchable",
        source_anchors: [
          {
            id: "anchor-1",
            source_revision_id: "source-1",
            event_id: "event-1",
            start_byte: 0,
            end_byte: 20,
            exact_quote: "SQLite is searchable.",
          },
        ],
      },
    ],
    links: [],
  };
}

const currentPage = {
  id: "page-sqlite",
  slug: "sqlite",
  title: "SQLite",
  current_revision_id: "revision-current",
  revision: revision(),
};

const links = {
  outgoing: [
    {
      link: {
        id: "link-1",
        page_revision_id: "revision-current",
        section_key: "decision",
        start_byte: 34,
        end_byte: 41,
        exact_text: "runtime",
        target_page_id: "page-runtime",
      },
      source_page: {
        id: "page-sqlite",
        slug: "sqlite",
        title: "SQLite",
        current_revision_id: "revision-current",
      },
      source_revision_id: "revision-current",
      target_page: {
        id: "page-runtime",
        slug: "runtime",
        title: "Runtime",
        current_revision_id: "revision-runtime",
      },
    },
  ],
  incoming: [],
};

function wikiFetch(path: string) {
  if (path === "/v1/wiki/ingestion") return jsonResponse({ auto_inject: false });
  if (path === "/v1/wiki/navigation") return jsonResponse(navigation);
  if (path === "/v1/wiki/pages/sqlite") return jsonResponse(currentPage);
  if (path === "/v1/wiki/pages/sqlite/revisions") {
    return jsonResponse({
      revisions: [revision(), revision("revision-old", "SQLite before WAL")],
    });
  }
  if (path === "/v1/wiki/pages/sqlite/backlinks") return jsonResponse(links);
  if (path === "/v1/wiki/pages/sqlite/revisions/revision-old") {
    return jsonResponse(revision("revision-old", "SQLite before WAL"));
  }
  if (path === "/v1/wiki/search?q=searchable") {
    return jsonResponse({
      results: [
        {
          page: {
            id: "page-sqlite",
            slug: "sqlite",
            title: "SQLite",
            current_revision_id: "revision-current",
          },
          revision_id: "revision-current",
          section_key: "decision",
          passage: "SQLite is searchable.",
          score: 0.97,
          citations: [],
          links: [],
        },
      ],
    });
  }
  throw new Error(`unexpected fetch: ${path}`);
}

describe("Page Wiki portal integration", () => {
  it("adds a dedicated Knowledge sidebar and renders grounded wiki context", async () => {
    await renderApp({
      route: "/wiki",
      me: makeMe({ role: "member" }),
      fetch: (path) => wikiFetch(path),
    });

    const portalNav = screen.getByRole("navigation", { name: "Portal navigation" });
    within(portalNav).getByText("Knowledge");
    within(portalNav).getByRole("link", { name: "Wiki" });

    await screen.findByRole("heading", { name: "SQLite" });
    screen.getByRole("navigation", { name: "Wiki topics" });
    screen.getByText("The durable local knowledge store.");
    screen.getByRole("link", { name: "runtime" });
    screen.getByText("1 outgoing");
    screen.getByText("0 incoming");
    expect(screen.queryByText("Xanadu map")).toBeNull();
  });

  it("renders root-level pages above topic groups", async () => {
    await renderApp({
      route: "/wiki?page=sqlite",
      me: makeMe({ role: "member" }),
      fetch: (path) => {
        if (path === "/v1/wiki/navigation") {
          return jsonResponse({
            ...navigation,
            pages: [{ id: "page-alpha", slug: "alpha", title: "Alpha", rank: 0 }],
          });
        }
        return wikiFetch(path);
      },
    });

    await screen.findByRole("heading", { name: "SQLite" });
    const rail = screen.getByRole("navigation", { name: "Wiki topics" });
    const alphaButton = within(rail).getByRole("button", { name: "Alpha" });
    const topicHeading = within(rail).getByRole("heading", { name: "Engineering" });
    expect(
      alphaButton.compareDocumentPosition(topicHeading) & Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();
  });

  it("searches current revisions and opens a historical revision", async () => {
    const { user } = await renderApp({
      route: "/wiki?page=sqlite",
      me: makeMe({ role: "member" }),
      fetch: (path) => wikiFetch(path),
    });

    await screen.findByRole("heading", { name: "SQLite" });
    await user.type(screen.getByRole("searchbox", { name: "Search the wiki" }), "searchable");
    await user.click(screen.getByRole("button", { name: "Search" }));
    const results = await screen.findByRole("region", { name: "Search results" });
    within(results).getByText("SQLite");
    within(results).getByText("SQLite is searchable.");

    await user.click(screen.getByRole("button", { name: /revision-old/ }));
    await screen.findByRole("heading", { name: "SQLite before WAL" });
    screen.getByText("Historical");
    await waitFor(() =>
      expect(window.location.search).toContain("revision=revision-old"),
    );
  });

  it("shows a useful empty state when the wiki has no pages", async () => {
    await renderApp({
      route: "/wiki",
      me: makeMe({ role: "member" }),
      fetch: (path) => {
        if (path === "/v1/wiki/ingestion") return jsonResponse({ auto_inject: false });
        if (path === "/v1/wiki/navigation") return jsonResponse({ roots: [] });
        throw new Error(`unexpected fetch: ${path}`);
      },
    });

    await screen.findByRole("heading", { name: "Your wiki is ready for its first page" });
    screen.getByText("Pages will appear here after a Page Wiki source is processed.");
  });

  it("toggles auto injection and manually injects a fixed session", async () => {
    const requests: Array<{ path: string; method: string }> = [];
    const { user } = await renderApp({
      route: "/wiki",
      me: makeMe({ role: "member" }),
      fetch: (path, init) => {
        const method = init?.method ?? "GET";
        requests.push({ path, method });
        if (path === "/v1/wiki/ingestion" && method === "PUT") {
          return jsonResponse({ auto_inject: true });
        }
        if (path === "/v1/wiki/ingestion") return jsonResponse({ auto_inject: false });
        if (path === "/v1/wiki/sessions/runtime-session/inject") {
          return jsonResponse({ processed_streams: 1 });
        }
        if (path === "/v1/wiki/navigation") return jsonResponse({ roots: [] });
        throw new Error(`unexpected fetch: ${path}`);
      },
    });

    const toggle = await screen.findByRole("switch", { name: "Off" });
    await user.click(toggle);
    await waitFor(() => expect(toggle.getAttribute("aria-checked")).toBe("true"));

    await user.type(screen.getByLabelText("Fixed session ID"), "runtime-session");
    await user.click(screen.getByRole("button", { name: "Inject session" }));
    await screen.findByText("Injected 1 stream from runtime-session.");
    expect(requests).toContainEqual({ path: "/v1/wiki/ingestion", method: "PUT" });
    expect(requests).toContainEqual({
      path: "/v1/wiki/sessions/runtime-session/inject",
      method: "POST",
    });
  });

  it("lets an owner confirm a full Wiki rebuild without deleting Session Lake", async () => {
    const requests: Array<{ path: string; method: string }> = [];
    const { user } = await renderApp({
      route: "/wiki",
      me: makeMe({ role: "owner" }),
      fetch: (path, init) => {
        const method = init?.method ?? "GET";
        requests.push({ path, method });
        if (path === "/v1/wiki/rebuild" && method === "POST") {
          return jsonResponse({ auto_inject: true });
        }
        return wikiFetch(path);
      },
    });

    await screen.findByRole("heading", { name: "SQLite" });
    await user.click(screen.getByRole("button", { name: "Reset & rebuild" }));
    const dialog = screen.getByRole("dialog", { name: "Reset and rebuild Wiki" });
    within(dialog).getByText("Session Lake events and Team Notes are preserved.");
    await user.click(within(dialog).getByRole("button", { name: "Confirm reset & rebuild" }));

    await screen.findByText("Wiki cleared. Rebuilding from Session Lake…");
    expect(requests).toContainEqual({ path: "/v1/wiki/rebuild", method: "POST" });
  });

  it("hides the destructive rebuild control from members", async () => {
    await renderApp({
      route: "/wiki",
      me: makeMe({ role: "member" }),
      fetch: (path) => wikiFetch(path),
    });

    await screen.findByRole("heading", { name: "SQLite" });
    expect(screen.queryByRole("button", { name: "Reset & rebuild" })).toBeNull();
  });

  it("collapses the Source evidence section by default", async () => {
    await renderApp({
      route: "/wiki",
      me: makeMe({ role: "member" }),
      fetch: (path) => wikiFetch(path),
    });

    await screen.findByRole("heading", { name: "SQLite" });
    const fold = document.querySelector("details.wiki-evidence-fold");
    expect(fold).not.toBeNull();
    expect(fold?.hasAttribute("open")).toBe(false);
    expect(within(fold as HTMLElement).getByText("Source evidence")).toBeTruthy();
    expect(screen.queryByRole("heading", { level: 2, name: "Source evidence" })).toBeNull();
  });
});

// Fake-timer coverage for the 3s navigation refresh added by the usePolling
// migration (WikiPage no longer hand-rolls setInterval): the poll must run
// only while auto inject is on, and usePolling's own visibility gating is
// already proven at a page level in admin-operations-polling.dom.test.tsx,
// so it is not re-verified here.
describe("Page Wiki navigation refresh while auto inject is on", () => {
  afterEach(() => {
    vi.useRealTimers();
  });

  it("polls the navigation tree every 3s only once auto inject is switched on", async () => {
    vi.useFakeTimers();
    resetBrowserState();
    window.history.pushState({}, "", "/wiki");
    const fetchMock = stubFetch((path, init) => {
      if (path === "/v1/me") return jsonResponse(makeMe({ role: "member" }));
      if (path === "/v1/wiki/ingestion" && (init?.method ?? "GET") === "PUT") {
        return jsonResponse({ auto_inject: true });
      }
      return wikiFetch(path);
    });
    render(<App />);

    const navCalls = () => callsTo(fetchMock, "/v1/wiki/navigation");
    for (let i = 0; i < 40; i++) {
      await act(async () => {
        await vi.advanceTimersByTimeAsync(5);
      });
      if (navCalls().length > 0) break;
    }
    expect(navCalls()).toHaveLength(1);

    // Auto inject starts off: several 3s ticks must not refetch.
    await act(async () => {
      await vi.advanceTimersByTimeAsync(10_000);
    });
    expect(navCalls()).toHaveLength(1);

    // Switch auto inject on: usePolling's immediate cycle on the deps change
    // (false -> true) refreshes navigation exactly once. toggleAutoInject
    // itself no longer bumps navigationRevision, so this must not double up.
    const toggle = screen.getByRole("switch", { name: "Off" });
    fireEvent.click(toggle);
    for (let i = 0; i < 40; i++) {
      await act(async () => {
        await vi.advanceTimersByTimeAsync(5);
      });
      if (toggle.getAttribute("aria-checked") === "true") break;
    }
    expect(toggle.getAttribute("aria-checked")).toBe("true");
    expect(navCalls()).toHaveLength(2);

    // The 3s cadence now runs, one refetch per tick, no duplicates.
    await act(async () => {
      await vi.advanceTimersByTimeAsync(3_000);
    });
    expect(navCalls()).toHaveLength(3);

    await act(async () => {
      await vi.advanceTimersByTimeAsync(3_000);
    });
    expect(navCalls()).toHaveLength(4);
  });
});
