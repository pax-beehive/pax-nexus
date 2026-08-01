import { Children, cloneElement, isValidElement, type ReactNode } from "react";
import ReactMarkdown, { type Components } from "react-markdown";
import remarkGfm from "remark-gfm";
import type { WikiResolvedLink, WikiRevision } from "../../api/wiki";

interface LinkMatch {
  relation: WikiResolvedLink;
  text: string;
}

function sectionMatches(relations: WikiResolvedLink[], sectionKey: string): LinkMatch[] {
  return relations
    .filter((relation) => relation.link.section_key === sectionKey)
    .map((relation) => ({ relation, text: relation.link.exact_text }))
    .filter((match) => match.text !== "");
}

function linkString(
  text: string,
  matches: LinkMatch[],
  onSelect: (slug: string) => void,
): ReactNode {
  const hits = matches
    .map((match) => ({ match, start: text.indexOf(match.text) }))
    .filter((hit) => hit.start >= 0)
    .sort((left, right) => left.start - right.start);
  if (hits.length === 0) return text;
  const content: ReactNode[] = [];
  let cursor = 0;
  hits.forEach((hit) => {
    const text2 = hit.match.text;
    if (hit.start < cursor) return;
    content.push(text.slice(cursor, hit.start));
    content.push(
      <a
        key={`${hit.match.relation.link.id}-${hit.start}`}
        href={`/wiki?page=${encodeURIComponent(hit.match.relation.target_page.slug)}`}
        className="wiki-inline-link"
        onClick={(event) => {
          event.preventDefault();
          onSelect(hit.match.relation.target_page.slug);
        }}
      >
        {text2}
      </a>,
    );
    cursor = hit.start + text2.length;
  });
  content.push(text.slice(cursor));
  return content;
}

function linkify(
  children: ReactNode,
  matches: LinkMatch[],
  onSelect: (slug: string) => void,
): ReactNode {
  if (matches.length === 0) return children;
  return Children.map(children, (child) => {
    if (typeof child === "string") return linkString(child, matches, onSelect);
    if (isValidElement(child)) {
      // Never re-link inside existing anchors or code spans.
      if (child.type === "a" || child.type === "code") return child;
      const inner = (child.props as { children?: ReactNode }).children;
      if (inner) return cloneElement(child, undefined, linkify(inner, matches, onSelect));
    }
    return child;
  });
}

function linkedComponents(
  sectionKey: string,
  relations: WikiResolvedLink[],
  onSelect: (slug: string) => void,
): Components {
  const matches = sectionMatches(relations, sectionKey);
  const wrap = (children: ReactNode) => linkify(children, matches, onSelect);
  return {
    p: ({ children }) => <p data-section={sectionKey || undefined}>{wrap(children)}</p>,
    li: ({ children }) => <li>{wrap(children)}</li>,
    td: ({ children }) => <td>{wrap(children)}</td>,
    th: ({ children }) => <th>{wrap(children)}</th>,
  };
}

interface MarkdownSegment {
  heading: string;
  sectionKey: string;
  body: string;
}

/**
 * Splits the composed revision markdown (`# title`, lead, then `## heading`
 * sections) into renderable segments. The H1 line is dropped because the
 * article header already shows the title.
 */
function splitSegments(
  markdown: string,
  sectionKeys: Map<string, string>,
): { intro: string; segments: MarkdownSegment[] } {
  const intro: string[] = [];
  const segments: MarkdownSegment[] = [];
  let current: MarkdownSegment | null = null;
  let fenced = false;
  for (const line of markdown.split(/\r?\n/)) {
    if (line.trimStart().startsWith("```")) fenced = !fenced;
    if (!fenced && line.startsWith("# ")) continue;
    if (!fenced && line.startsWith("## ")) {
      const heading = line.slice(3).trim();
      current = { heading, sectionKey: sectionKeys.get(heading) ?? "", body: "" };
      segments.push(current);
      continue;
    }
    if (current) {
      current.body += line + "\n";
    } else {
      intro.push(line);
    }
  }
  return { intro: intro.join("\n").trim(), segments };
}

/**
 * Full markdown renderer for wiki revisions (GFM tables, lists, code blocks,
 * quotes, inline emphasis) with Xanadu inline links resolved from the
 * revision's outgoing relations. Source evidence sections fold into a
 * collapsed details block.
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
  const sectionKeys = new Map(
    (revision.sections ?? []).map((section) => [section.heading, section.key]),
  );
  const { intro, segments } = splitSegments(String(revision.markdown ?? ""), sectionKeys);

  const renderBody = (body: string, sectionKey: string) => (
    <ReactMarkdown
      remarkPlugins={[remarkGfm]}
      components={linkedComponents(sectionKey, relations, onSelect)}
    >
      {body}
    </ReactMarkdown>
  );

  return (
    <div className="wiki-markdown">
      {intro !== "" && renderBody(intro, "")}
      {segments.map((segment) => {
        if (segment.sectionKey === "source-evidence") {
          return (
            <details key={segment.sectionKey} className="wiki-evidence-fold">
              <summary>{segment.heading}</summary>
              {renderBody(segment.body, segment.sectionKey)}
            </details>
          );
        }
        return (
          <section key={segment.sectionKey || segment.heading}>
            <h2 data-section={segment.sectionKey || undefined}>{segment.heading}</h2>
            {renderBody(segment.body, segment.sectionKey)}
          </section>
        );
      })}
    </div>
  );
}
