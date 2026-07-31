// Shared fixtures for the standalone wiki DOM tests (spec
// 2026-07-29-wiki-standalone-page). Both wiki-browse.dom.test.tsx and
// wiki-status.dom.test.tsx stub the same page/navigation endpoints, so the
// fixture and fetch stub live here rather than being exported from one test
// file and imported by the other (no test file may import from another).

import { jsonResponse } from "./helpers";

export const REVISION = {
  id: "rev-1",
  page_id: "p1",
  title: "Alpha",
  summary: "Alpha summary",
  sections: [{ key: "s1", heading: "Overview", markdown: "Alpha body." }],
  markdown: "Alpha body.",
  citations: [],
  links: [],
};

export const llmUsageFixture = {
  rows: [
    {
      component: "extractor",
      model: "deepseek-chat",
      calls: 12,
      input_tokens: 120000,
      cache_hit_tokens: 90000,
      cache_miss_tokens: 30000,
      output_tokens: 8000,
    },
    {
      component: "wiki-editor",
      model: "deepseek-chat",
      calls: 30,
      input_tokens: 400000,
      cache_hit_tokens: 250000,
      cache_miss_tokens: 150000,
      output_tokens: 60000,
    },
  ],
};

export function wikiFetch(path: string): Response {
  if (path === "/v1/wiki/navigation") {
    return jsonResponse({ roots: [], pages: [{ id: "p1", slug: "alpha", title: "Alpha", rank: 1 }] });
  }
  if (path === "/v1/wiki/ingestion") return jsonResponse({ auto_inject: false });
  if (path === "/v1/wiki/settings") return jsonResponse({ language: "", custom_instructions: "" });
  if (path.startsWith("/v1/llm-usage")) return jsonResponse(llmUsageFixture);
  if (path === "/v1/wiki/pages/alpha") {
    return jsonResponse({
      id: "p1", slug: "alpha", title: "Alpha", current_revision_id: "rev-1", revision: REVISION,
    });
  }
  if (path === "/v1/wiki/pages/alpha/revisions") return jsonResponse({ revisions: [REVISION] });
  if (path === "/v1/wiki/pages/alpha/backlinks") return jsonResponse({ outgoing: [], incoming: [] });
  throw new Error(`unexpected path: ${path}`);
}
