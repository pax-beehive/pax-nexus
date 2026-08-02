import { useEffect } from "react";
import type { WikiNavigationPage, WikiNavigationTopic } from "../../api/wiki";

/** Flattens the topic tree depth-first so pages are listed in rail order. */
export function collectPages(topics: WikiNavigationTopic[]): WikiNavigationPage[] {
  return topics.flatMap((topic) => [
    ...(topic.pages ?? []),
    ...collectPages(topic.children ?? []),
  ]);
}

interface BreadcrumbCrumb {
  title: string;
  path: string[];
}

interface ResolvedLayer {
  /** The longest valid prefix of the requested path (may be shorter). */
  path: string[];
  /** Child topics at the resolved layer, in API order. */
  topics: WikiNavigationTopic[];
  /** Pages directly attached to the resolved layer. */
  pages: WikiNavigationPage[];
  /** Titled breadcrumb entries for each segment of `path`. */
  crumbs: BreadcrumbCrumb[];
  /** True when the requested path had to be shortened to stay valid. */
  truncated: boolean;
}

/**
 * Walks `topicPath` through `topics` one slug at a time. A segment that
 * doesn't match any child (e.g. the tree was rebuilt and the topic is gone)
 * stops the walk there instead of throwing; the caller is told via
 * `truncated` so it can correct the navigation state.
 */
function resolveLayer(
  topics: WikiNavigationTopic[],
  rootPages: WikiNavigationPage[],
  topicPath: string[],
): ResolvedLayer {
  let layerTopics = topics;
  let layerPages = rootPages;
  const path: string[] = [];
  const crumbs: BreadcrumbCrumb[] = [];

  for (const slug of topicPath) {
    const match = layerTopics.find((topic) => topic.slug === slug);
    if (!match) {
      return { path, topics: layerTopics, pages: layerPages, crumbs, truncated: true };
    }
    path.push(slug);
    crumbs.push({ title: match.title, path: [...path] });
    layerTopics = match.children ?? [];
    layerPages = match.pages ?? [];
  }

  return { path, topics: layerTopics, pages: layerPages, crumbs, truncated: false };
}

/**
 * Single-layer drill-down sidebar for the wiki topic tree: renders one level
 * of child topics and that level's directly attached pages at a time, with a
 * breadcrumb trail back to the root, instead of the whole tree expanded.
 */
export function TopicTreePanel({
  topics,
  rootPages,
  topicPath,
  onNavigate,
  selectedSlug,
  onSelect,
}: {
  topics: WikiNavigationTopic[];
  rootPages: WikiNavigationPage[];
  topicPath: string[];
  onNavigate: (path: string[]) => void;
  selectedSlug: string;
  onSelect: (slug: string) => void;
}) {
  const layer = resolveLayer(topics, rootPages, topicPath);
  const truncatedKey = layer.path.join("/");

  // A stale segment (topic removed by a rebuild) is corrected once rendering
  // settles, so the caller's navigation state stays in sync with the tree it
  // is browsing.
  useEffect(() => {
    if (layer.truncated) onNavigate(layer.path);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [layer.truncated, truncatedKey]);

  const breadcrumb: BreadcrumbCrumb[] = [{ title: "All", path: [] }, ...layer.crumbs];

  return (
    <>
      <nav className="wiki-breadcrumb" aria-label="Topic path">
        {breadcrumb.map((crumb, index) => {
          const isCurrent = index === breadcrumb.length - 1;
          return (
            <button
              key={crumb.path.join("/") || "root"}
              type="button"
              className="wiki-breadcrumb-item"
              aria-current={isCurrent ? "location" : undefined}
              onClick={() => onNavigate(crumb.path)}
            >
              {crumb.title}
            </button>
          );
        })}
      </nav>
      {layer.topics.length > 0 && (
        <section className="wiki-topic">
          {layer.topics.map((child) => (
            <button
              key={child.id}
              type="button"
              className="wiki-topic-item"
              onClick={() => onNavigate([...layer.path, child.slug])}
            >
              <span>{child.title}</span>
              <span className="wiki-topic-count faint small">{collectPages([child]).length}</span>
            </button>
          ))}
        </section>
      )}
      {layer.pages.length > 0 && (
        <section className="wiki-topic">
          {layer.pages.map((page) => (
            <button
              key={page.id}
              type="button"
              className={page.slug === selectedSlug ? "wiki-page-link active" : "wiki-page-link"}
              aria-current={page.slug === selectedSlug ? "page" : undefined}
              onClick={() => onSelect(page.slug)}
            >
              {page.title}
            </button>
          ))}
        </section>
      )}
    </>
  );
}
