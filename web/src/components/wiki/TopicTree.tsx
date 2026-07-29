import type { WikiNavigationPage, WikiNavigationTopic } from "../../api/wiki";

/** Flattens the topic tree depth-first so pages are listed in rail order. */
export function collectPages(topics: WikiNavigationTopic[]): WikiNavigationPage[] {
  return topics.flatMap((topic) => [
    ...(topic.pages ?? []),
    ...collectPages(topic.children ?? []),
  ]);
}

/**
 * One topic in the wiki rail: its pages as selectable buttons, with child
 * topics nested recursively.
 */
export function Topic({
  topic,
  selectedSlug,
  onSelect,
}: {
  topic: WikiNavigationTopic;
  selectedSlug: string;
  onSelect: (slug: string) => void;
}) {
  return (
    <section className="wiki-topic">
      <h2>{topic.title}</h2>
      {(topic.pages ?? []).map((page) => (
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
      {(topic.children ?? []).length > 0 && (
        <div className="wiki-topic-children">
          {topic.children.map((child) => (
            <Topic
              key={child.id}
              topic={child}
              selectedSlug={selectedSlug}
              onSelect={onSelect}
            />
          ))}
        </div>
      )}
    </section>
  );
}
