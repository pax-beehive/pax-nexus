import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { WikiMarkdown } from "../src/components/wiki/WikiMarkdown";
import type { WikiResolvedLink, WikiRevision } from "../src/api/wiki";
import { setupDomTest } from "./helpers";

setupDomTest();

function revisionFixture(markdown: string): WikiRevision {
  return {
    id: "revision-1",
    page_id: "page-1",
    title: "SQLite",
    summary: "SQLite notes.",
    sections: [
      { key: "decision", heading: "Decision", markdown: "" },
      { key: "source-evidence", heading: "Source evidence", markdown: "" },
    ],
    markdown,
    citations: [],
    links: [],
  };
}

function relationFixture(exactText: string, sectionKey: string): WikiResolvedLink {
  return {
    link: {
      id: "link-1",
      page_revision_id: "revision-1",
      section_key: sectionKey,
      start_byte: 0,
      end_byte: exactText.length,
      exact_text: exactText,
      target_page_id: "page-2",
    },
    source_page: { id: "page-1", slug: "sqlite", title: "SQLite", current_revision_id: "revision-1" },
    source_revision_id: "revision-1",
    target_page: { id: "page-2", slug: "wal", title: "WAL", current_revision_id: "revision-2" },
  };
}

describe("WikiMarkdown", () => {
  it("renders GFM tables, emphasis, lists, and fenced code", () => {
    const markdown = [
      "# SQLite",
      "",
      "## Decision",
      "",
      "Use **WAL mode** with *care*:",
      "",
      "- first `PRAGMA` item",
      "- second item",
      "",
      "| Mode | Durable |",
      "| ---- | ------- |",
      "| WAL  | yes     |",
      "",
      "```bash",
      "# keep this comment",
      "sqlite3 app.db",
      "```",
      "",
      "> quoted rationale",
    ].join("\n");
    const { container } = render(
      <WikiMarkdown revision={revisionFixture(markdown)} relations={[]} onSelect={() => {}} />,
    );

    expect(screen.getByText("WAL mode").tagName).toBe("STRONG");
    expect(screen.getByText("care").tagName).toBe("EM");
    const items = container.querySelectorAll("li");
    expect(items).toHaveLength(2);
    expect(items[0].textContent).toContain("first");
    expect(items[1].textContent).toContain("second");
    expect(screen.getByText("PRAGMA").tagName).toBe("CODE");
    expect(screen.getByRole("table")).toBeTruthy();
    expect(screen.getByRole("columnheader", { name: "Mode" })).toBeTruthy();
    expect(screen.getByRole("cell", { name: "yes" })).toBeTruthy();
    const pre = container.querySelector("pre");
    expect(pre?.textContent).toContain("# keep this comment");
    expect(pre?.textContent).toContain("sqlite3 app.db");
    expect(container.querySelector("blockquote")?.textContent).toContain("quoted rationale");
    // The H1 line never renders: the article header owns the title.
    expect(screen.queryByRole("heading", { level: 1 })).toBeNull();
  });

  it("folds the source-evidence section instead of rendering its heading", () => {
    const markdown = "## Decision\n\nBody.\n\n## Source evidence\n\nExact quote.";
    const { container } = render(
      <WikiMarkdown revision={revisionFixture(markdown)} relations={[]} onSelect={() => {}} />,
    );

    expect(screen.queryByRole("heading", { name: "Source evidence" })).toBeNull();
    const fold = container.querySelector("details.wiki-evidence-fold");
    expect(fold).toBeTruthy();
    expect(fold?.querySelector("summary")?.textContent).toBe("Source evidence");
    expect(fold?.textContent).toContain("Exact quote.");
  });

  it("links exact texts to their target pages, including inside list items", async () => {
    const user = userEvent.setup();
    const onSelect = vi.fn();
    const markdown = "## Decision\n\n- WAL survives crashes\n\nPlain paragraph.";
    render(
      <WikiMarkdown
        revision={revisionFixture(markdown)}
        relations={[relationFixture("WAL", "decision")]}
        onSelect={onSelect}
      />,
    );

    const anchor = screen.getByRole("link", { name: "WAL" });
    expect(anchor.className).toBe("wiki-inline-link");
    await user.click(anchor);
    expect(onSelect).toHaveBeenCalledWith("wal");
  });

  it("does not link inside code spans", () => {
    const markdown = "## Decision\n\n`WAL` stays code.";
    render(
      <WikiMarkdown
        revision={revisionFixture(markdown)}
        relations={[relationFixture("WAL", "decision")]}
        onSelect={() => {}}
      />,
    );

    expect(screen.queryByRole("link", { name: "WAL" })).toBeNull();
    expect(screen.getByText("WAL").tagName).toBe("CODE");
  });
});
