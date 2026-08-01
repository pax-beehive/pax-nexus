import { screen, waitFor } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { jsonResponse, makeMe, renderApp, setupDomTest } from "./helpers";
import { wikiFetch } from "./wikiFixtures";

setupDomTest();

function todoFetch(path: string) {
  if (path === "/v1/todo/suggestions") return jsonResponse({ suggestions: [] });
  if (path === "/v1/todo/todos") return jsonResponse({ todos: [] });
  throw new Error(`unexpected fetch: ${path}`);
}

describe("Apps launcher", () => {
  it("renders both app cards and the wiki policy settings card", async () => {
    await renderApp({
      route: "/apps",
      me: makeMe(),
      fetch: (path) => {
        throw new Error(`unexpected fetch: ${path}`);
      },
    });

    screen.getByRole("heading", { level: 1, name: "Apps" });
    screen.getByRole("link", { name: /Read the knowledge/ });
    screen.getByRole("link", { name: /Track work/ });
    screen.getByRole("link", { name: /Wiki policy/ });
  });

  it("opens the todo app full-screen outside the shell and returns via the back link", async () => {
    const { user } = await renderApp({ route: "/apps", me: makeMe(), fetch: todoFetch });

    await user.click(screen.getByRole("link", { name: /Track work/ }));

    await waitFor(() => expect(window.location.pathname).toBe("/todo"));
    await screen.findByRole("heading", { level: 1, name: "Todos" });
    // Full-screen app: the portal sidebar is gone.
    expect(screen.queryByRole("navigation", { name: "Portal navigation" })).toBeNull();

    await user.click(screen.getByRole("link", { name: "← All apps" }));

    await waitFor(() => expect(window.location.pathname).toBe("/apps"));
    screen.getByRole("navigation", { name: "Portal navigation" });
  });

  it("opens the wiki app on the full-screen browse route", async () => {
    const { user } = await renderApp({ route: "/apps", me: makeMe(), fetch: wikiFetch });

    await user.click(screen.getByRole("link", { name: /Read the knowledge/ }));

    await waitFor(() => expect(window.location.pathname).toBe("/wiki/browse"));
    expect(screen.queryByRole("navigation", { name: "Portal navigation" })).toBeNull();
  });

  it("links the wiki policy settings card to the in-shell wiki status page", async () => {
    const { user } = await renderApp({ route: "/apps", me: makeMe(), fetch: wikiFetch });

    await user.click(screen.getByRole("link", { name: /Wiki policy/ }));

    await waitFor(() => expect(window.location.pathname).toBe("/wiki"));
    screen.getByRole("navigation", { name: "Portal navigation" });
  });
});
