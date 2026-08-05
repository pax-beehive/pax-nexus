// The wiki reader, mounted at /apps/wiki and /apps/wiki/:slug inside the
// portal shell (the pre-redesign route rendered it full-screen outside the
// shell; the top-bar IA replaced that).
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
  it("auto-selects the first page and moves its slug into the path", async () => {
    await renderApp({ route: "/apps/wiki", me: makeMe(), fetch: wikiFetch });

    await waitFor(() => expect(screen.getByText("Alpha summary")).toBeTruthy());
    // Selecting the first page rewrote the URL under /apps/wiki/:slug — the
    // slug lives in the path now, not in ?page=.
    expect(window.location.pathname).toBe("/apps/wiki/alpha");
    expect(window.location.search).toBe("");
    // The wiki renders inside AppShell now, so the section nav is present.
    // (This case used to assert queryByText("My Agents") was null, as proof
    // the wiki rendered outside the portal shell — vacuous once the shell
    // became a top bar, because that string no longer appears in it.)
    within(screen.getByRole("navigation", { name: "Sections" })).getByRole("link", {
      name: "Apps",
    });
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
  it("renders the root layer's topic groups above its unclassified pages", async () => {
    await renderApp({
      route: "/apps/wiki/sqlite",
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
    const topicButton = within(rail).getByRole("button", { name: /^Engineering/ });
    expect(
      topicButton.compareDocumentPosition(alphaButton) & Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();
  });

  it("searches current revisions and opens a historical revision", async () => {
    const { user } = await renderApp({
      route: "/apps/wiki/sqlite",
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
      route: "/apps/wiki",
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
    await renderApp({ route: "/apps/wiki", me: makeMe(), fetch: sqliteFetch });

    await screen.findByRole("heading", { name: "SQLite" });
    const fold = document.querySelector("details.wiki-evidence-fold");
    expect(fold).not.toBeNull();
    expect(fold?.hasAttribute("open")).toBe(false);
    expect(within(fold as HTMLElement).getByText("Source evidence")).toBeTruthy();
    expect(screen.queryByRole("heading", { level: 2, name: "Source evidence" })).toBeNull();
  });
});

describe("wiki browse route retired page banner", () => {
  function retiredRevision() {
    return {
      id: "revision-retired",
      page_id: "page-retired",
      title: "Retired Page",
      summary: "The former home of this decision.",
      sections: [],
      markdown: "# Retired Page",
      citations: [],
      links: [],
    };
  }

  function retiredPage(successorSlug?: string) {
    return {
      id: "page-retired",
      slug: "retired-page",
      title: "Retired Page",
      current_revision_id: "revision-retired",
      status: "retired",
      ...(successorSlug ? { successor_slug: successorSlug } : {}),
      revision: retiredRevision(),
    };
  }

  function retiredFetch(successorSlug?: string) {
    return (path: string): Response => {
      if (path === "/v1/wiki/ingestion") return jsonResponse({ auto_inject: false });
      if (path === "/v1/wiki/navigation") {
        return jsonResponse({
          roots: [],
          pages: [{ id: "page-retired", slug: "retired-page", title: "Retired Page", rank: 0 }],
        });
      }
      if (path === "/v1/wiki/pages/retired-page") return jsonResponse(retiredPage(successorSlug));
      if (path === "/v1/wiki/pages/retired-page/revisions") {
        return jsonResponse({ revisions: [retiredRevision()] });
      }
      if (path === "/v1/wiki/pages/retired-page/backlinks") {
        return jsonResponse({ outgoing: [], incoming: [] });
      }
      throw new Error(`unexpected path: ${path}`);
    };
  }

  it("shows an archived banner with a link to the successor page", async () => {
    await renderApp({
      route: "/apps/wiki/retired-page",
      me: makeMe(),
      fetch: retiredFetch("sqlite"),
    });

    await screen.findByRole("heading", { name: "Retired Page" });
    screen.getByText("This page has been archived.");
    const link = screen.getByRole("link", { name: "See successor page" });
    expect(link.getAttribute("href")).toBe("/apps/wiki/sqlite");
  });

  it("omits the successor link when no successor slug is present", async () => {
    await renderApp({
      route: "/apps/wiki/retired-page",
      me: makeMe(),
      fetch: retiredFetch(),
    });

    await screen.findByRole("heading", { name: "Retired Page" });
    screen.getByText("This page has been archived.");
    expect(screen.queryByRole("link", { name: "See successor page" })).toBeNull();
  });
});

describe("wiki browse route entity ontology", () => {
  function typedFetch(path: string): Response {
    if (path === "/v1/wiki/pages/sqlite") {
      return jsonResponse({ ...sqlitePage, entity_type: "system" });
    }
    if (path === "/v1/wiki/pages/sqlite/backlinks") {
      return jsonResponse({
        outgoing: [
          {
            ...sqliteLinks.outgoing[0],
            link: { ...sqliteLinks.outgoing[0].link, relation_type: "depends-on" },
          },
        ],
        incoming: [],
      });
    }
    return sqliteFetch(path);
  }

  function fallbackFetch(path: string): Response {
    if (path === "/v1/wiki/pages/sqlite") {
      return jsonResponse({ ...sqlitePage, entity_type: "concept" });
    }
    if (path === "/v1/wiki/pages/sqlite/backlinks") {
      return jsonResponse({
        outgoing: [
          {
            ...sqliteLinks.outgoing[0],
            link: { ...sqliteLinks.outgoing[0].link, relation_type: "relates-to" },
          },
        ],
        incoming: [],
      });
    }
    return sqliteFetch(path);
  }

  it("shows the entity type badge and relation label when they say something", async () => {
    await renderApp({ route: "/apps/wiki/sqlite", me: makeMe(), fetch: typedFetch });

    await screen.findByRole("heading", { name: "SQLite" });
    expect(screen.getByText("system")).toBeTruthy();
    expect(screen.getByText("(depends-on)")).toBeTruthy();
  });

  it("hides the badge and relation label for concept/relates-to fallbacks", async () => {
    await renderApp({ route: "/apps/wiki/sqlite", me: makeMe(), fetch: fallbackFetch });

    await screen.findByRole("heading", { name: "SQLite" });
    // The link row itself still renders (target page title present)...
    expect(screen.getByRole("link", { name: /Runtime/ })).toBeTruthy();
    // ...but the fallback entity type and relation type say nothing new, so
    // neither the badge nor the relation label should render.
    expect(document.querySelector(".wiki-type-badge")).toBeNull();
    expect(screen.queryByText("(relates-to)")).toBeNull();
    expect(screen.queryByText("concept")).toBeNull();
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
    window.history.pushState({}, "", "/apps/wiki");
    const fetchMock = stubFetch((path) => {
      if (path === "/v1/me") return jsonResponse(makeMe());
      if (path === "/v1/wiki/ingestion") return jsonResponse({ auto_inject: true });
      return wikiFetch(path);
    });
    render(<App />);

    const navCalls = () => callsTo(fetchMock, "/v1/wiki/navigation");
    // Initial navigation load fires on mount at /apps/wiki (no slug); it
    // auto-selects the first page and rewrites the URL to /apps/wiki/alpha,
    // which is a different Route match (slug now lives in the path) and so
    // remounts the page for one more initial fetch. Once the ingestion GET
    // resolves auto_inject: true, usePolling's deps flip (false -> true)
    // fires one more immediate cycle on top of those two.
    for (let i = 0; i < 60; i++) {
      await act(async () => {
        await vi.advanceTimersByTimeAsync(5);
      });
      if (navCalls().length >= 3) break;
    }
    expect(navCalls()).toHaveLength(3);

    // The 3s cadence now runs, one refetch per tick, no duplicates.
    await act(async () => {
      await vi.advanceTimersByTimeAsync(3_000);
    });
    expect(navCalls()).toHaveLength(4);

    await act(async () => {
      await vi.advanceTimersByTimeAsync(3_000);
    });
    expect(navCalls()).toHaveLength(5);
  });

  it("never polls the navigation tree while ingestion reports auto inject off", async () => {
    vi.useFakeTimers();
    resetBrowserState();
    window.history.pushState({}, "", "/apps/wiki");
    const fetchMock = stubFetch((path) => {
      if (path === "/v1/me") return jsonResponse(makeMe());
      if (path === "/v1/wiki/ingestion") return jsonResponse({ auto_inject: false });
      return wikiFetch(path);
    });
    render(<App />);

    const navCalls = () => callsTo(fetchMock, "/v1/wiki/navigation");
    // Initial mount at /apps/wiki fetches once, then auto-selecting the
    // first page rewrites the URL to /apps/wiki/alpha (a different Route
    // match) and remounts for one more fetch; auto inject stays off so no
    // polling cycle follows either of those.
    for (let i = 0; i < 40; i++) {
      await act(async () => {
        await vi.advanceTimersByTimeAsync(5);
      });
      if (navCalls().length >= 2) break;
    }
    expect(navCalls()).toHaveLength(2);

    // Several 3s ticks must not refetch while auto inject stays off.
    await act(async () => {
      await vi.advanceTimersByTimeAsync(10_000);
    });
    expect(navCalls()).toHaveLength(2);
  });
});

// -- fix round 1/2: the navigation-tree effect must fetch and auto-select on
// mount only, never on slug change (findings 1 and 2). This also requires
// AppShell's content ErrorBoundary to key on routeKey(pathname), not the raw
// pathname, so selecting a different wiki page doesn't remount the whole
// page and re-trigger the effect from scratch (fix round 2). --

function twoPageNavigation() {
  return {
    roots: [],
    pages: [
      { id: "page-alpha", slug: "alpha", title: "Alpha", rank: 0 },
      { id: "page-beta", slug: "beta", title: "Beta", rank: 1 },
    ],
  };
}

function simplePage(slug: string, title: string) {
  return {
    id: `page-${slug}`,
    slug,
    title,
    current_revision_id: `revision-${slug}`,
    revision: {
      id: `revision-${slug}`,
      page_id: `page-${slug}`,
      title,
      summary: `${title} summary`,
      sections: [],
      markdown: `# ${title}`,
      citations: [],
      links: [],
    },
  };
}

function cadenceFetch(path: string): Response {
  if (path === "/v1/wiki/ingestion") return jsonResponse({ auto_inject: false });
  if (path === "/v1/wiki/navigation") return jsonResponse(twoPageNavigation());
  if (path === "/v1/wiki/pages/alpha") return jsonResponse(simplePage("alpha", "Alpha"));
  if (path === "/v1/wiki/pages/alpha/revisions") {
    return jsonResponse({ revisions: [simplePage("alpha", "Alpha").revision] });
  }
  if (path === "/v1/wiki/pages/alpha/backlinks") return jsonResponse({ outgoing: [], incoming: [] });
  if (path === "/v1/wiki/pages/beta") return jsonResponse(simplePage("beta", "Beta"));
  if (path === "/v1/wiki/pages/beta/revisions") {
    return jsonResponse({ revisions: [simplePage("beta", "Beta").revision] });
  }
  if (path === "/v1/wiki/pages/beta/backlinks") return jsonResponse({ outgoing: [], incoming: [] });
  throw new Error(`unexpected path: ${path}`);
}

describe("wiki browse route navigation fetch cadence", () => {
  it("fetches the navigation tree once per mount, not once per page selection", async () => {
    const { user, fetchMock } = await renderApp({
      route: "/apps/wiki/alpha",
      me: makeMe(),
      fetch: cadenceFetch,
    });

    await screen.findByRole("heading", { name: "Alpha" });
    expect(callsTo(fetchMock, "/v1/wiki/navigation")).toHaveLength(1);

    await user.click(screen.getByRole("button", { name: "Beta" }));
    await screen.findByRole("heading", { name: "Beta" });

    // Selecting a different page must not refetch the (expensive) navigation
    // tree; only the fetches scoped to the newly selected page happen.
    expect(callsTo(fetchMock, "/v1/wiki/navigation")).toHaveLength(1);
  });

  it("does not bounce back to the tree once already viewing a page absent from it", async () => {
    // The very first mount legitimately auto-selects a tree page when no
    // valid slug was requested (unchanged, and correct — there's nothing
    // else to show). The bug this pins is different: having ALREADY landed
    // on a valid page, selecting a page that isn't in the navigation tree
    // (a retired page or search hit outside it) must not be silently
    // bounced back once the tree effect settles again.
    const { user } = await renderApp({
      route: "/apps/wiki/alpha",
      me: makeMe(),
      fetch: (path) => {
        if (path === "/v1/wiki/pages/hidden") return jsonResponse(simplePage("hidden", "Hidden"));
        if (path === "/v1/wiki/pages/hidden/revisions") {
          return jsonResponse({ revisions: [simplePage("hidden", "Hidden").revision] });
        }
        if (path === "/v1/wiki/pages/hidden/backlinks") {
          return jsonResponse({ outgoing: [], incoming: [] });
        }
        if (path === "/v1/wiki/search?q=hidden") {
          return jsonResponse({
            results: [
              {
                page: { id: "page-hidden", slug: "hidden", title: "Hidden", current_revision_id: "revision-hidden" },
                revision_id: "revision-hidden",
                section_key: "body",
                passage: "Hidden passage.",
                score: 0.9,
                citations: [],
                links: [],
              },
            ],
          });
        }
        return cadenceFetch(path);
      },
    });

    await screen.findByRole("heading", { name: "Alpha" });

    await user.type(screen.getByRole("searchbox", { name: "Search the wiki" }), "hidden");
    await user.click(screen.getByRole("button", { name: "Search" }));
    await user.click(await screen.findByRole("button", { name: /Hidden/ }));

    await screen.findByRole("heading", { name: "Hidden" });
    await waitFor(() => expect(window.location.pathname).toBe("/apps/wiki/hidden"));
    // Give the (correctly non-refetching) navigation effect no chance to
    // silently bounce this back to the first tree page.
    expect(screen.queryByRole("heading", { name: "Alpha" })).toBeNull();
  });

  it("renders the page the URL points to after an external navigation (browser Back/Forward, palette jump)", async () => {
    // selectedSlug is seeded once from the route param and otherwise only
    // changed by selectPage; nothing previously synced it when the URL
    // changed from OUTSIDE the component's own click handlers — a browser
    // Back/Forward (pushState + popstate, simulated below) or a ⌘K palette
    // jump straight to /apps/wiki/:slug both do exactly that. Collapsing
    // the wiki route's remount key (routeKey.ts) removed the accidental
    // full remount that used to paper over this.
    await renderApp({
      route: "/apps/wiki/alpha",
      me: makeMe(),
      fetch: cadenceFetch,
    });

    await screen.findByRole("heading", { name: "Alpha" });

    await act(async () => {
      window.history.pushState({}, "", "/apps/wiki/beta");
      window.dispatchEvent(new PopStateEvent("popstate"));
    });

    await screen.findByRole("heading", { name: "Beta" });
    expect(screen.queryByRole("heading", { name: "Alpha" })).toBeNull();
  });
});
