// The /wiki/browse route renders the wiki full-screen, outside the portal
// shell (spec 2026-07-29-wiki-standalone-page section 1-2).
//
// Several cases below were ported from the retired web/tests/wiki.dom.test.tsx
// (see d874ac5~1), adapted to the /wiki/browse route: that file covered
// WikiPage's inline behavior directly; the browsing half of that behavior now
// lives in WikiBrowsePage while the ingestion-control half moved to
// WikiStatusPage (see wiki-status.dom.test.tsx).

import { act, render, screen, waitFor, within } from "@testing-library/react";
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
import { wikiFetch } from "./wikiFixtures";

setupDomTest();

describe("wiki browse route", () => {
  it("renders the wiki full-screen without the portal shell", async () => {
    await renderApp({ route: "/wiki/browse", me: makeMe(), fetch: wikiFetch });

    await waitFor(() => expect(screen.getByText("Alpha summary")).toBeTruthy());
    // No portal navigation: the shell's nav links must not render.
    expect(screen.queryByText("My Agents")).toBeNull();
    // Back link to the portal status page is present.
    expect(screen.getByRole("link", { name: /back to portal/i })).toBeTruthy();
    // Selecting the first page rewrote the URL under /wiki/browse.
    expect(window.location.pathname).toBe("/wiki/browse");
    expect(window.location.search).toBe("?page=alpha");
  });
});

// -- topics, search, and evidence folding (ported from the retired
// wiki.dom.test.tsx's "Page Wiki portal integration" describe block) --

const ENGINEERING_NAVIGATION = {
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

function sqliteRevision(id = "revision-current", title = "SQLite") {
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

const sqlitePage = {
  id: "page-sqlite",
  slug: "sqlite",
  title: "SQLite",
  current_revision_id: "revision-current",
  revision: sqliteRevision(),
};

const sqliteLinks = {
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

function sqliteFetch(path: string): Response {
  if (path === "/v1/wiki/ingestion") return jsonResponse({ auto_inject: false });
  if (path === "/v1/wiki/navigation") return jsonResponse(ENGINEERING_NAVIGATION);
  if (path === "/v1/wiki/pages/sqlite") return jsonResponse(sqlitePage);
  if (path === "/v1/wiki/pages/sqlite/revisions") {
    return jsonResponse({
      revisions: [sqliteRevision(), sqliteRevision("revision-old", "SQLite before WAL")],
    });
  }
  if (path === "/v1/wiki/pages/sqlite/backlinks") return jsonResponse(sqliteLinks);
  if (path === "/v1/wiki/pages/sqlite/revisions/revision-old") {
    return jsonResponse(sqliteRevision("revision-old", "SQLite before WAL"));
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
  throw new Error(`unexpected path: ${path}`);
}

describe("wiki browse route topics and search", () => {
  it("renders root-level pages above topic groups", async () => {
    await renderApp({
      route: "/wiki/browse?page=sqlite",
      me: makeMe(),
      fetch: (path) => {
        if (path === "/v1/wiki/navigation") {
          return jsonResponse({
            ...ENGINEERING_NAVIGATION,
            pages: [{ id: "page-alpha", slug: "alpha", title: "Alpha", rank: 0 }],
          });
        }
        return sqliteFetch(path);
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
      route: "/wiki/browse?page=sqlite",
      me: makeMe(),
      fetch: sqliteFetch,
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
    await waitFor(() => expect(window.location.search).toContain("revision=revision-old"));
  });

  it("shows a useful empty state when the wiki has no pages", async () => {
    await renderApp({
      route: "/wiki/browse",
      me: makeMe(),
      fetch: (path) => {
        if (path === "/v1/wiki/ingestion") return jsonResponse({ auto_inject: false });
        if (path === "/v1/wiki/navigation") return jsonResponse({ roots: [] });
        throw new Error(`unexpected fetch: ${path}`);
      },
    });

    await screen.findByRole("heading", { name: "Your wiki is ready for its first page" });
    screen.getByText("Pages will appear here after a Page Wiki source is processed.");
  });

  it("collapses the Source evidence section by default", async () => {
    await renderApp({ route: "/wiki/browse", me: makeMe(), fetch: sqliteFetch });

    await screen.findByRole("heading", { name: "SQLite" });
    const fold = document.querySelector("details.wiki-evidence-fold");
    expect(fold).not.toBeNull();
    expect(fold?.hasAttribute("open")).toBe(false);
    expect(within(fold as HTMLElement).getByText("Source evidence")).toBeTruthy();
    expect(screen.queryByRole("heading", { level: 2, name: "Source evidence" })).toBeNull();
  });
});

// Fake-timer coverage for the 3s navigation refresh (ported from the retired
// wiki.dom.test.tsx's "Page Wiki navigation refresh while auto inject is on"
// describe block). WikiBrowsePage no longer owns an ingestion toggle switch
// (that moved to WikiStatusPage), so unlike the retired test this drives the
// gate through the ingestion GET's auto_inject value directly rather than a
// mid-test UI click; usePolling's own visibility gating is already proven at
// a page level in admin-operations-polling.dom.test.tsx, so it is not
// re-verified here.
describe("wiki browse route navigation refresh", () => {
  afterEach(() => {
    vi.useRealTimers();
  });

  it("polls the navigation tree every 3s once ingestion reports auto inject on", async () => {
    vi.useFakeTimers();
    resetBrowserState();
    window.history.pushState({}, "", "/wiki/browse");
    const fetchMock = stubFetch((path) => {
      if (path === "/v1/me") return jsonResponse(makeMe());
      if (path === "/v1/wiki/ingestion") return jsonResponse({ auto_inject: true });
      return wikiFetch(path);
    });
    render(<App />);

    const navCalls = () => callsTo(fetchMock, "/v1/wiki/navigation");
    // Initial navigation load fires on mount; once the ingestion GET
    // resolves auto_inject: true, usePolling's deps flip (false -> true)
    // fires one more immediate cycle.
    for (let i = 0; i < 60; i++) {
      await act(async () => {
        await vi.advanceTimersByTimeAsync(5);
      });
      if (navCalls().length >= 2) break;
    }
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

  it("never polls the navigation tree while ingestion reports auto inject off", async () => {
    vi.useFakeTimers();
    resetBrowserState();
    window.history.pushState({}, "", "/wiki/browse");
    const fetchMock = stubFetch((path) => {
      if (path === "/v1/me") return jsonResponse(makeMe());
      if (path === "/v1/wiki/ingestion") return jsonResponse({ auto_inject: false });
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

    // Several 3s ticks must not refetch while auto inject stays off.
    await act(async () => {
      await vi.advanceTimersByTimeAsync(10_000);
    });
    expect(navCalls()).toHaveLength(1);
  });
});
