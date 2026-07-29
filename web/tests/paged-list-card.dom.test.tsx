// DOM tests for the shared PagedListCard scaffolding: the loading row, the
// inline error notice with retry (initial load vs. load more), the empty
// state, and the "Load more" button.

import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { PagedListCard } from "../src/components/PagedListCard";
import type { PagedList } from "../src/lib/usePagedList";

afterEach(cleanup);

interface Row {
  id: string;
  name: string;
}

function makeList(overrides: Partial<PagedList<Row>> = {}): PagedList<Row> {
  return {
    items: [],
    nextCursor: undefined,
    loading: false,
    loadingMore: false,
    error: null,
    loadMore: vi.fn(async () => {}),
    reload: vi.fn(),
    ...overrides,
  };
}

function renderCard(list: PagedList<Row>) {
  return render(
    <PagedListCard
      list={list}
      columns={["Name", ""]}
      emptyText="No rows yet"
      renderRow={(row) => (
        <tr key={row.id}>
          <td>{row.name}</td>
        </tr>
      )}
    />,
  );
}

describe("PagedListCard", () => {
  it("renders a loading row with the column headers while loading", () => {
    renderCard(makeList({ loading: true }));

    screen.getByText("Loading…");
    expect(screen.getByRole("columnheader", { name: "Name" })).toBeDefined();
    expect(screen.queryByText("No rows yet")).toBeNull();
    expect(screen.queryByRole("button", { name: "Load more" })).toBeNull();
  });

  it("shows an error notice with retry on initial load failure; retry reloads", async () => {
    const list = makeList({ error: new Error("boom") });
    renderCard(list);

    const alert = screen.getByRole("alert");
    expect(alert.textContent).toContain("Failed to load the list.");
    // The empty state is replaced by the error notice, not shown alongside it.
    expect(screen.queryByText("No rows yet")).toBeNull();

    await userEvent.setup().click(screen.getByRole("button", { name: "Retry" }));
    expect(list.reload).toHaveBeenCalledTimes(1);
    expect(list.loadMore).not.toHaveBeenCalled();
  });

  it("keeps loaded rows on a load-more failure; retry re-attempts the page", async () => {
    const list = makeList({
      items: [{ id: "1", name: "First row" }],
      nextCursor: "cursor-2",
      error: new Error("boom"),
    });
    renderCard(list);

    screen.getByText("First row");
    const alert = screen.getByRole("alert");
    expect(alert.textContent).toContain("Failed to load more.");

    await userEvent.setup().click(screen.getByRole("button", { name: "Retry" }));
    expect(list.loadMore).toHaveBeenCalledTimes(1);
    expect(list.reload).not.toHaveBeenCalled();
  });

  it("renders the empty state instead of an empty table", () => {
    renderCard(makeList());

    screen.getByText("No rows yet");
    expect(screen.queryByRole("table")).toBeNull();
    expect(screen.queryByRole("button", { name: "Load more" })).toBeNull();
  });

  it("renders a working Load more button while a next cursor exists", async () => {
    const list = makeList({
      items: [{ id: "1", name: "First row" }],
      nextCursor: "cursor-2",
    });
    renderCard(list);

    await userEvent.setup().click(screen.getByRole("button", { name: "Load more" }));
    expect(list.loadMore).toHaveBeenCalledTimes(1);
  });

  it("disables Load more and shows progress while the next page loads", () => {
    renderCard(
      makeList({
        items: [{ id: "1", name: "First row" }],
        nextCursor: "cursor-2",
        loadingMore: true,
      }),
    );

    const button = screen.getByRole("button", { name: "Loading…" });
    expect((button as HTMLButtonElement).disabled).toBe(true);
  });
});
