// The /wiki/browse route renders the wiki full-screen, outside the portal
// shell (spec 2026-07-29-wiki-standalone-page section 1-2).

import { describe, expect, it } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import { jsonResponse, makeMe, renderApp, setupDomTest } from "./helpers";

setupDomTest();

const REVISION = {
  id: "rev-1",
  page_id: "p1",
  title: "Alpha",
  summary: "Alpha summary",
  sections: [{ key: "s1", heading: "Overview", markdown: "Alpha body." }],
  markdown: "Alpha body.",
  citations: [],
  links: [],
};

export function wikiFetch(path: string): Response {
  if (path === "/v1/wiki/navigation") {
    return jsonResponse({ roots: [], pages: [{ id: "p1", slug: "alpha", title: "Alpha", rank: 1 }] });
  }
  if (path === "/v1/wiki/ingestion") return jsonResponse({ auto_inject: false });
  if (path === "/v1/wiki/pages/alpha") {
    return jsonResponse({
      id: "p1", slug: "alpha", title: "Alpha", current_revision_id: "rev-1", revision: REVISION,
    });
  }
  if (path === "/v1/wiki/pages/alpha/revisions") return jsonResponse({ revisions: [REVISION] });
  if (path === "/v1/wiki/pages/alpha/backlinks") return jsonResponse({ outgoing: [], incoming: [] });
  throw new Error(`unexpected path: ${path}`);
}

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
