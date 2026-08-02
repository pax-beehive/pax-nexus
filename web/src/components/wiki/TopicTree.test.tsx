import { cleanup, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { TopicTreePanel } from "./TopicTree";
import type { WikiNavigationPage, WikiNavigationTopic } from "../../api/wiki";

afterEach(() => {
  cleanup();
});

function page(slug: string, title: string, rank = 1): WikiNavigationPage {
  return { id: `page-${slug}`, slug, title, rank };
}

// Topic "engineering" has one direct page and one child topic ("backend"),
// which in turn has its own page. Titles are chosen not to collide with the
// breadcrumb's "All" label so role queries stay unambiguous.
const topicBackend: WikiNavigationTopic = {
  id: "topic-backend",
  slug: "backend",
  title: "Backend",
  children: [],
  pages: [page("backend-page", "Backend Page")],
};

const topicEngineering: WikiNavigationTopic = {
  id: "topic-engineering",
  slug: "engineering",
  title: "Engineering",
  children: [topicBackend],
  pages: [page("engineering-page", "Engineering Page")],
};

const topics: WikiNavigationTopic[] = [topicEngineering];
const rootPages: WikiNavigationPage[] = [page("root-page", "Root Page")];

describe("TopicTreePanel", () => {
  it("renders the root layer's child topics and unclassified pages", () => {
    render(
      <TopicTreePanel
        topics={topics}
        rootPages={rootPages}
        topicPath={[]}
        onNavigate={vi.fn()}
        selectedSlug=""
        onSelect={vi.fn()}
      />,
    );

    screen.getByRole("button", { name: /^Engineering/ });
    screen.getByRole("button", { name: "Root Page" });
  });

  it("navigates into a child topic when it is clicked", async () => {
    const user = userEvent.setup();
    const onNavigate = vi.fn();
    render(
      <TopicTreePanel
        topics={topics}
        rootPages={rootPages}
        topicPath={[]}
        onNavigate={onNavigate}
        selectedSlug=""
        onSelect={vi.fn()}
      />,
    );

    await user.click(screen.getByRole("button", { name: /^Engineering/ }));

    expect(onNavigate).toHaveBeenCalledWith(["engineering"]);
  });

  it("renders the drilled-down layer's children and a breadcrumb", () => {
    render(
      <TopicTreePanel
        topics={topics}
        rootPages={rootPages}
        topicPath={["engineering"]}
        onNavigate={vi.fn()}
        selectedSlug=""
        onSelect={vi.fn()}
      />,
    );

    const crumb = screen.getByRole("navigation", { name: "Topic path" });
    within(crumb).getByRole("button", { name: "All" });
    const current = within(crumb).getByRole("button", { name: "Engineering" });
    expect(current.getAttribute("aria-current")).toBe("location");

    screen.getByRole("button", { name: /^Backend/ });
    screen.getByRole("button", { name: "Engineering Page" });
  });

  it("navigates back to root when the breadcrumb's All entry is clicked", async () => {
    const user = userEvent.setup();
    const onNavigate = vi.fn();
    render(
      <TopicTreePanel
        topics={topics}
        rootPages={rootPages}
        topicPath={["engineering"]}
        onNavigate={onNavigate}
        selectedSlug=""
        onSelect={vi.fn()}
      />,
    );

    await user.click(screen.getByRole("button", { name: "All" }));

    expect(onNavigate).toHaveBeenCalledWith([]);
  });

  it("falls back to the root layer and corrects the path when it points at a topic that no longer exists", () => {
    const onNavigate = vi.fn();
    render(
      <TopicTreePanel
        topics={topics}
        rootPages={rootPages}
        topicPath={["ghost"]}
        onNavigate={onNavigate}
        selectedSlug=""
        onSelect={vi.fn()}
      />,
    );

    screen.getByRole("button", { name: /^Engineering/ });
    screen.getByRole("button", { name: "Root Page" });
    expect(onNavigate).toHaveBeenCalledWith([]);
  });
});
