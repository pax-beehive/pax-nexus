import { useMemo, type ReactNode } from "react";
import type { WikiResolvedLink, WikiRevision } from "../../api/wiki";

function linkedText(
  text: string,
  sectionKey: string,
  relations: WikiResolvedLink[],
  onSelect: (slug: string) => void,
): ReactNode[] {
  const matches = relations
    .filter((relation) => relation.link.section_key === sectionKey)
    .map((relation) => ({
      relation,
      start: text.indexOf(relation.link.exact_text),
    }))
    .filter((match) => match.start >= 0)
    .sort((left, right) => left.start - right.start);
  const content: ReactNode[] = [];
  let cursor = 0;
  matches.forEach((match) => {
    const exactText = match.relation.link.exact_text;
    if (match.start < cursor) return;
    content.push(text.slice(cursor, match.start));
    content.push(
      <a
        key={`${match.relation.link.id}-${match.start}`}
        href={`/wiki?page=${encodeURIComponent(match.relation.target_page.slug)}`}
        className="wiki-inline-link"
        onClick={(event) => {
          event.preventDefault();
          onSelect(match.relation.target_page.slug);
        }}
      >
        {exactText}
      </a>,
    );
    cursor = match.start + exactText.length;
  });
  content.push(text.slice(cursor));
  return content;
}

/**
 * Minimal markdown renderer for wiki revisions: headings, paragraphs, and
 * inline links resolved from the revision's outgoing relations. Source
 * evidence sections fold into a collapsed details block.
 */
export function WikiMarkdown({
  revision,
  relations,
  onSelect,
}: {
  revision: WikiRevision;
  relations: WikiResolvedLink[];
  onSelect: (slug: string) => void;
}) {
  const sectionKeys = useMemo(
    () => new Map((revision.sections ?? []).map((section) => [section.heading, section.key])),
    [revision.sections],
  );
  const blocks: ReactNode[] = [];
  const evidenceBlocks: ReactNode[] = [];
  let evidenceHeading = "Source evidence";
  let evidenceAnchor = -1;
  let paragraph: string[] = [];
  let sectionKey = "";
  let blockKey = 0;
  const push = (node: ReactNode) => {
    if (sectionKey === "source-evidence") {
      if (evidenceAnchor < 0) evidenceAnchor = blocks.length;
      evidenceBlocks.push(node);
    } else {
      blocks.push(node);
    }
  };
  const flush = () => {
    if (paragraph.length === 0) return;
    const text = paragraph.join(" ");
    push(
      <p key={`p-${blockKey++}`} data-section={sectionKey || undefined}>
        {linkedText(text, sectionKey, relations, onSelect)}
      </p>,
    );
    paragraph = [];
  };

  String(revision.markdown ?? "")
    .split(/\r?\n/)
    .forEach((rawLine) => {
      const line = rawLine.trim();
      if (!line) {
        flush();
        return;
      }
      if (line.startsWith("### ")) {
        flush();
        push(<h3 key={`h3-${blockKey++}`}>{line.slice(4)}</h3>);
        return;
      }
      if (line.startsWith("## ")) {
        flush();
        const heading = line.slice(3);
        sectionKey = sectionKeys.get(heading) ?? "";
        if (sectionKey === "source-evidence") {
          evidenceHeading = heading;
          return;
        }
        blocks.push(
          <h2 key={`h2-${blockKey++}`} data-section={sectionKey || undefined}>
            {heading}
          </h2>,
        );
        return;
      }
      if (line.startsWith("# ")) {
        flush();
        return;
      }
      paragraph.push(line);
    });
  flush();

  if (evidenceBlocks.length > 0) {
    blocks.splice(
      evidenceAnchor < 0 ? blocks.length : evidenceAnchor,
      0,
      <details key="source-evidence-fold" className="wiki-evidence-fold">
        <summary>{evidenceHeading}</summary>
        {evidenceBlocks}
      </details>,
    );
  }
  return <div className="wiki-markdown">{blocks}</div>;
}
