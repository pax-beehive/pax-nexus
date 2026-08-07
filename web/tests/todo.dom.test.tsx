import { screen, waitFor, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { apiErrorResponse, callsTo, jsonResponse, makeMe, renderApp, setupDomTest } from "./helpers";

setupDomTest();

const suggestion = {
  suggestion_id: "s1",
  note_id: "note-1",
  kind: "blocker",
  title: "Runtime migration is stuck",
  body: "Waiting on the SQLite WAL cutover before the runtime team can proceed.",
  status: "pending",
  created_at: "2026-07-28T00:00:00Z",
};

const openTodo = {
  todo_id: "t1",
  title: "Cut the WAL migration ticket",
  body: "",
  status: "open" as const,
  source: "manual" as const,
  created_by: "usr_01",
  created_at: "2026-07-27T00:00:00Z",
  updated_at: "2026-07-27T00:00:00Z",
};

const doneTodo = {
  ...openTodo,
  todo_id: "t2",
  title: "Write the runbook",
  status: "done" as const,
};

/** Navigate to /apps/todos the way a user would: sidebar Apps link, then the Todos card. */
async function goToTodos(user: { click: (el: Element) => Promise<void> }) {
  await user.click(screen.getByRole("link", { name: "Apps" }));
  await user.click(await screen.findByRole("link", { name: /Todos/ }));
  await screen.findByRole("heading", { level: 1, name: "Work the agents surfaced" });
}

describe("Todo app portal integration", () => {
  it("redirects the legacy /todo route to /apps/todos", async () => {
    await renderApp({
      route: "/todo",
      me: makeMe({ role: "member" }),
      fetch: (path, init) => {
        const method = init.method ?? "GET";
        if (path === "/v1/todo/suggestions" && method === "GET") {
          return jsonResponse({ suggestions: [] });
        }
        if (path === "/v1/todo/todos" && method === "GET") {
          return jsonResponse({ todos: [] });
        }
        throw new Error(`unexpected fetch: ${path}`);
      },
    });

    await waitFor(() => expect(window.location.pathname).toBe("/apps/todos"));
    await screen.findByRole("heading", { level: 1, name: "Work the agents surfaced" });
  });

  it("renders pending suggestions and open/done todos from the stubbed lists", async () => {
    const { user } = await renderApp({
      route: "/management",
      me: makeMe({ role: "member" }),
      fetch: (path, init) => {
        const method = init.method ?? "GET";
        if (path === "/v1/todo/suggestions" && method === "GET") {
          return jsonResponse({ suggestions: [suggestion] });
        }
        if (path === "/v1/todo/todos" && method === "GET") {
          return jsonResponse({ todos: [openTodo, doneTodo] });
        }
        throw new Error(`unexpected fetch: ${path}`);
      },
    });

    await goToTodos(user);

    within(screen.getByLabelText("Suggestions")).getByText("Runtime migration is stuck");
    within(screen.getByLabelText("Suggestions")).getByText("blocker");
    within(screen.getByLabelText("Todos")).getByText("Cut the WAL migration ticket");
    // Done todos live inside a collapsed <details>; jsdom does not apply the
    // native closed-details hidden rendering, so assert on the `open`
    // attribute rather than visibility.
    const doneFold = document.querySelector("details");
    expect(doneFold?.hasAttribute("open")).toBe(false);
    within(screen.getByLabelText("Todos")).getByText("Done (1)");
    within(screen.getByLabelText("Todos")).getByText("Write the runbook");
  });

  it("shows the empty state when there are no pending suggestions", async () => {
    const { user } = await renderApp({
      route: "/management",
      me: makeMe({ role: "member" }),
      fetch: (path, init) => {
        const method = init.method ?? "GET";
        if (path === "/v1/todo/suggestions" && method === "GET") {
          return jsonResponse({ suggestions: [] });
        }
        if (path === "/v1/todo/todos" && method === "GET") {
          return jsonResponse({ todos: [] });
        }
        throw new Error(`unexpected fetch: ${path}`);
      },
    });

    await goToTodos(user);
    within(screen.getByLabelText("Suggestions")).getByText("No new suggestions right now.");
    within(screen.getByLabelText("Todos")).getByText("No open todos.");
  });

  it("accepting a suggestion posts accept then refetches both lists", async () => {
    let accepted = false;
    const { user, fetchMock } = await renderApp({
      route: "/management",
      me: makeMe({ role: "member" }),
      fetch: (path, init) => {
        const method = init.method ?? "GET";
        if (path === "/v1/todo/suggestions" && method === "GET") {
          return jsonResponse({ suggestions: accepted ? [] : [suggestion] });
        }
        if (path === "/v1/todo/todos" && method === "GET") {
          return jsonResponse({ todos: accepted ? [openTodo] : [] });
        }
        if (path === "/v1/todo/suggestions/s1/accept" && method === "POST") {
          accepted = true;
          return jsonResponse(openTodo);
        }
        throw new Error(`unexpected fetch: ${path}`);
      },
    });

    await goToTodos(user);
    await screen.findByText("Runtime migration is stuck");

    await user.click(screen.getByRole("button", { name: "Take it" }));

    expect(callsTo(fetchMock, "/v1/todo/suggestions/s1/accept", "POST")).toHaveLength(1);
    await screen.findByText("No new suggestions right now.");
    within(screen.getByLabelText("Todos")).getByText("Cut the WAL migration ticket");
    expect(callsTo(fetchMock, "/v1/todo/suggestions", "GET")).toHaveLength(2);
    expect(callsTo(fetchMock, "/v1/todo/todos", "GET")).toHaveLength(2);
  });

  it("completing a todo posts complete then refetches both lists", async () => {
    let completed = false;
    const { user, fetchMock } = await renderApp({
      route: "/management",
      me: makeMe({ role: "member" }),
      fetch: (path, init) => {
        const method = init.method ?? "GET";
        if (path === "/v1/todo/suggestions" && method === "GET") {
          return jsonResponse({ suggestions: [] });
        }
        if (path === "/v1/todo/todos" && method === "GET") {
          return jsonResponse({ todos: completed ? [{ ...openTodo, status: "done" }] : [openTodo] });
        }
        if (path === "/v1/todo/todos/t1/complete" && method === "POST") {
          completed = true;
          return jsonResponse({ ...openTodo, status: "done" });
        }
        throw new Error(`unexpected fetch: ${path}`);
      },
    });

    await goToTodos(user);
    await screen.findByText("Cut the WAL migration ticket");

    await user.click(screen.getByRole("button", { name: "Done" }));

    expect(callsTo(fetchMock, "/v1/todo/todos/t1/complete", "POST")).toHaveLength(1);
    await screen.findByText("No open todos.");
    expect(callsTo(fetchMock, "/v1/todo/todos", "GET")).toHaveLength(2);
  });

  // Suggestions and the personal list come from two independent endpoints
  // (/v1/todo/suggestions vs /v1/todo/todos) and must degrade independently:
  // one failing must not blank out the other side.
  it("still renders the personal list when the suggestions endpoint fails", async () => {
    // The suggestions endpoint fails on the first call and succeeds after, so
    // the Retry button has something to prove: before this the failed side was
    // a dead "请稍后重试" line with nothing to press (PR #91 follow-up).
    let suggestionCalls = 0;
    const { user } = await renderApp({
      route: "/management",
      me: makeMe({ role: "member" }),
      fetch: (path, init) => {
        const method = init.method ?? "GET";
        if (path === "/v1/todo/suggestions" && method === "GET") {
          return suggestionCalls++ === 0
            ? apiErrorResponse(500, "internal", "boom")
            : jsonResponse({ suggestions: [suggestion] });
        }
        if (path === "/v1/todo/todos" && method === "GET") {
          return jsonResponse({ todos: [openTodo] });
        }
        throw new Error(`unexpected fetch: ${path}`);
      },
    });

    await goToTodos(user);

    const suggestions = within(screen.getByLabelText("Suggestions"));
    suggestions.getByText("Suggestions are unavailable.");
    // The other column is untouched by its neighbour's failure.
    within(screen.getByLabelText("Todos")).getByText("Cut the WAL migration ticket");

    await user.click(suggestions.getByRole("button", { name: "Retry" }));

    await waitFor(() => expect(screen.queryByText("Suggestions are unavailable.")).toBeNull());
    within(screen.getByLabelText("Suggestions")).getByText(suggestion.title);
  });

  it("still renders suggestions when the personal list endpoint fails", async () => {
    const { user } = await renderApp({
      route: "/management",
      me: makeMe({ role: "member" }),
      fetch: (path, init) => {
        const method = init.method ?? "GET";
        if (path === "/v1/todo/suggestions" && method === "GET") {
          return jsonResponse({ suggestions: [suggestion] });
        }
        if (path === "/v1/todo/todos" && method === "GET") {
          return apiErrorResponse(500, "internal", "boom");
        }
        throw new Error(`unexpected fetch: ${path}`);
      },
    });

    await goToTodos(user);

    within(screen.getByLabelText("Suggestions")).getByText("Runtime migration is stuck");
    within(screen.getByLabelText("Todos")).getByText("Your list is unavailable.");
  });
});
