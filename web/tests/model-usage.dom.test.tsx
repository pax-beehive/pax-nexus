// /settings/usage is the model-usage settings page: just the LLM token
// usage card. Split out of the former WikiStatusPage into its own route
// (ModelUsagePage, phase 6 task 1) — see memory-rules.dom.test.tsx for the
// progress/ingestion/generation cards that used to share this component.
// The "does not render the memory-rules cards" assertion below is the one
// that actually proves the split: without it, rendering the same component
// at both routes would pass everything else here too.

import { describe, expect, it } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import { apiErrorResponse, callsTo, makeMe, renderApp, setupDomTest } from "./helpers";
import { wikiFetch } from "./wikiFixtures";

setupDomTest();

describe("model usage page", () => {
  it("renders the LLM usage card but not the memory-rules cards", async () => {
    await renderApp({ route: "/settings/usage", me: makeMe(), fetch: wikiFetch });

    await screen.findByRole("region", { name: "LLM token usage" });
    expect(screen.queryByRole("region", { name: "Extraction progress" })).toBeNull();
    expect(screen.queryByRole("region", { name: "Wiki ingestion controls" })).toBeNull();
    expect(screen.queryByRole("region", { name: "Wiki generation settings" })).toBeNull();
  });

  it("renders a table with a row per component plus a totals row", async () => {
    await renderApp({ route: "/settings/usage", me: makeMe(), fetch: wikiFetch });

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
    const { user, fetchMock } = await renderApp({
      route: "/settings/usage",
      me: makeMe(),
      fetch: wikiFetch,
    });

    const card = await screen.findByRole("region", { name: "LLM token usage" });
    expect(callsTo(fetchMock, "/v1/llm-usage?days=7")).toHaveLength(1);

    await user.selectOptions(within(card).getByLabelText("Window"), "30");

    await waitFor(() => expect(callsTo(fetchMock, "/v1/llm-usage?days=30")).toHaveLength(1));
  });

  it("shows an unavailable note when the usage endpoint fails", async () => {
    await renderApp({
      route: "/settings/usage",
      me: makeMe(),
      fetch: (path) => {
        if (path.startsWith("/v1/llm-usage")) return apiErrorResponse(500, "internal", "boom");
        throw new Error(`unexpected fetch: ${path}`);
      },
    });

    const card = await screen.findByRole("region", { name: "LLM token usage" });
    within(card).getByText("LLM usage is unavailable.");
    expect(screen.queryByText("extractor")).toBeNull();
  });

  // Design brief §4: this screen is a single card, and a failed fetch must
  // be retryable — not just recoverable by picking a different window
  // (which happens to refetch as a side effect, but does nothing for a
  // reader who wants the *same* window again).
  it("recovers via the card's own Retry button, without touching the window", async () => {
    let attempt = 0;
    const { user, fetchMock } = await renderApp({
      route: "/settings/usage",
      me: makeMe(),
      fetch: (path) => {
        if (path.startsWith("/v1/llm-usage")) {
          attempt += 1;
          if (attempt === 1) return apiErrorResponse(500, "internal", "boom");
          return wikiFetch(path);
        }
        throw new Error(`unexpected fetch: ${path}`);
      },
    });

    const card = await screen.findByRole("region", { name: "LLM token usage" });
    within(card).getByText("LLM usage is unavailable.");

    await user.click(within(card).getByRole("button", { name: "Retry" }));

    await waitFor(() => within(card).getByText("extractor"));
    expect(within(card).queryByText("LLM usage is unavailable.")).toBeNull();
    // Both requests asked for the window already selected (7d) — Retry
    // re-issues the same query, it does not switch windows.
    expect(callsTo(fetchMock, "/v1/llm-usage?days=7")).toHaveLength(2);
  });
});
