import type { WikiResolvedLink } from "../../api/wiki";

/**
 * One direction of a page's Xanadu links: the linked page title plus the
 * exact source text of the relation.
 */
export function RelationList({
  relations,
  direction,
  onSelect,
}: {
  relations: WikiResolvedLink[];
  direction: "outgoing" | "incoming";
  onSelect: (slug: string) => void;
}) {
  if (relations.length === 0) {
    return (
      <p className="muted small">
        {direction === "outgoing" ? "No references from this page." : "No pages point here yet."}
      </p>
    );
  }
  return (
    <ul className="wiki-relation-list">
      {relations.map((relation) => {
        const page = direction === "outgoing" ? relation.target_page : relation.source_page;
        return (
          <li key={`${direction}-${relation.link.id}`}>
            <a
              href={`/wiki?page=${encodeURIComponent(page.slug)}`}
              onClick={(event) => {
                event.preventDefault();
                onSelect(page.slug);
              }}
            >
              <strong>
                {direction === "outgoing" ? "→" : "←"} {page.title}
              </strong>
              <span>“{relation.link.exact_text}”</span>
              {relation.link.relation_type && relation.link.relation_type !== "relates-to" && (
                <span>({relation.link.relation_type})</span>
              )}
            </a>
          </li>
        );
      })}
    </ul>
  );
}
