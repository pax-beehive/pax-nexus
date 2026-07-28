import { useCallback, useEffect, useMemo, useState, type ReactNode } from "react";
import { useNavigate } from "react-router-dom";
import type { HumanMe } from "../api/types";
import {
  getWikiLinks,
  getWikiNavigation,
  getWikiIngestionStatus,
  getWikiPage,
  getWikiRevision,
  getWikiRevisions,
  searchWiki,
  type WikiLinks,
  type WikiNavigationPage,
  type WikiNavigationTopic,
  type WikiResolvedLink,
  type WikiRevision,
  type WikiSearchResult,
  type WikiPage,
} from "../api/wiki";
import { beginAction, injectWikiSession, rebuildWiki, setWikiAutoInject } from "../api/actions";
import { ConfirmDialog } from "../components/ConfirmDialog";
import { useErrorHandler } from "../lib/useErrorHandler";

const EMPTY_LINKS: WikiLinks = { outgoing: [], incoming: [] };

function isAbortError(error: unknown): boolean {
  return error instanceof DOMException && error.name === "AbortError";
}

function collectPages(topics: WikiNavigationTopic[]): WikiNavigationPage[] {
  return topics.flatMap((topic) => [
    ...(topic.pages ?? []),
    ...collectPages(topic.children ?? []),
  ]);
}

function Topic({
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

function WikiMarkdown({
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
  let paragraph: string[] = [];
  let sectionKey = "";
  let blockKey = 0;
  const flush = () => {
    if (paragraph.length === 0) return;
    const text = paragraph.join(" ");
    blocks.push(
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
        blocks.push(<h3 key={`h3-${blockKey++}`}>{line.slice(4)}</h3>);
        return;
      }
      if (line.startsWith("## ")) {
        flush();
        const heading = line.slice(3);
        sectionKey = sectionKeys.get(heading) ?? "";
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

  return <div className="wiki-markdown">{blocks}</div>;
}

function RelationList({
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
            </a>
          </li>
        );
      })}
    </ul>
  );
}

export function WikiPage({ me }: { me: HumanMe }) {
  const navigate = useNavigate();
  const handleError = useErrorHandler();
  const [topics, setTopics] = useState<WikiNavigationTopic[]>([]);
  const [navigationLoading, setNavigationLoading] = useState(true);
  const [selectedSlug, setSelectedSlug] = useState(
    () => new URLSearchParams(window.location.search).get("page") ?? "",
  );
  const [page, setPage] = useState<WikiPage>();
  const [revision, setRevision] = useState<WikiRevision>();
  const [revisions, setRevisions] = useState<WikiRevision[]>([]);
  const [links, setLinks] = useState<WikiLinks>(EMPTY_LINKS);
  const [pageLoading, setPageLoading] = useState(false);
  const [pageError, setPageError] = useState(false);
  const [searchInput, setSearchInput] = useState("");
  const [searchResults, setSearchResults] = useState<WikiSearchResult[]>([]);
  const [searching, setSearching] = useState(false);
  const [searchOpen, setSearchOpen] = useState(false);
  const [autoInject, setAutoInject] = useState(false);
  const [ingestionLoading, setIngestionLoading] = useState(true);
  const [ingestionBusy, setIngestionBusy] = useState(false);
  const [sessionID, setSessionID] = useState(
    () => new URLSearchParams(window.location.search).get("session") ?? "",
  );
  const [ingestionMessage, setIngestionMessage] = useState("");
  const [navigationRevision, setNavigationRevision] = useState(0);
  const [rebuildOpen, setRebuildOpen] = useState(false);

  useEffect(() => {
    const controller = new AbortController();
    getWikiIngestionStatus(controller.signal)
      .then((status) => setAutoInject(status.auto_inject))
      .catch((error: unknown) => {
        if (!isAbortError(error)) handleError(error);
      })
      .finally(() => {
        if (!controller.signal.aborted) setIngestionLoading(false);
      });
    return () => controller.abort();
  }, [handleError]);

  useEffect(() => {
    if (!autoInject) return undefined;
    const timer = window.setInterval(() => {
      setNavigationRevision((current) => current + 1);
    }, 3000);
    return () => window.clearInterval(timer);
  }, [autoInject]);

  const updateLocation = useCallback(
    (slug: string, revisionID?: string) => {
      const parameters = new URLSearchParams();
      parameters.set("page", slug);
      if (revisionID) parameters.set("revision", revisionID);
      navigate({ pathname: "/wiki", search: `?${parameters.toString()}` }, { replace: true });
    },
    [navigate],
  );

  const selectPage = useCallback(
    (slug: string) => {
      setSelectedSlug(slug);
      setSearchOpen(false);
      updateLocation(slug);
    },
    [updateLocation],
  );

  useEffect(() => {
    const controller = new AbortController();
    setNavigationLoading(true);
    getWikiNavigation(controller.signal)
      .then((navigation) => {
        const roots = navigation.roots ?? [];
        const pages = collectPages(roots);
        setTopics(roots);
        const requestedSlug = new URLSearchParams(window.location.search).get("page") ?? "";
        if (pages.length > 0 && !pages.some((candidate) => candidate.slug === requestedSlug)) {
          setSelectedSlug(pages[0].slug);
          updateLocation(pages[0].slug);
        }
      })
      .catch((error: unknown) => {
        if (!isAbortError(error)) handleError(error);
      })
      .finally(() => {
        if (!controller.signal.aborted) setNavigationLoading(false);
      });
    return () => controller.abort();
  }, [handleError, navigationRevision, updateLocation]);

  useEffect(() => {
    if (!selectedSlug) {
      setPage(undefined);
      setRevision(undefined);
      return;
    }
    const controller = new AbortController();
    const requestedRevision = new URLSearchParams(window.location.search).get("revision");
    setPageLoading(true);
    setPageError(false);
    Promise.all([
      getWikiPage(selectedSlug, controller.signal),
      getWikiRevisions(selectedSlug, controller.signal),
      getWikiLinks(selectedSlug, controller.signal),
    ])
      .then(async ([loadedPage, loadedRevisions, loadedLinks]) => {
        let selectedRevision = loadedPage.revision;
        if (requestedRevision && requestedRevision !== loadedPage.current_revision_id) {
          selectedRevision = await getWikiRevision(
            selectedSlug,
            requestedRevision,
            controller.signal,
          );
        }
        if (controller.signal.aborted) return;
        setPage(loadedPage);
        setRevision(selectedRevision);
        setRevisions(loadedRevisions);
        setLinks({
          outgoing: loadedLinks.outgoing ?? [],
          incoming: loadedLinks.incoming ?? [],
        });
      })
      .catch((error: unknown) => {
        if (isAbortError(error)) return;
        setPageError(true);
        handleError(error);
      })
      .finally(() => {
        if (!controller.signal.aborted) setPageLoading(false);
      });
    return () => controller.abort();
  }, [handleError, selectedSlug]);

  const selectRevision = async (revisionID: string) => {
    if (!page || revisionID === revision?.id) return;
    setPageLoading(true);
    try {
      const loaded = revisionID === page.current_revision_id
        ? page.revision
        : await getWikiRevision(page.slug, revisionID);
      setRevision(loaded);
      updateLocation(page.slug, revisionID === page.current_revision_id ? undefined : revisionID);
    } catch (error) {
      handleError(error);
    } finally {
      setPageLoading(false);
    }
  };

  const submitSearch = async () => {
    const query = searchInput.trim();
    if (!query) return;
    setSearching(true);
    setSearchOpen(true);
    try {
      setSearchResults(await searchWiki(query));
    } catch (error) {
      setSearchResults([]);
      handleError(error);
    } finally {
      setSearching(false);
    }
  };

  const toggleAutoInject = async () => {
    const next = !autoInject;
    setIngestionBusy(true);
    setIngestionMessage("");
    try {
      const status = await setWikiAutoInject(next);
      setAutoInject(status.auto_inject);
      setIngestionMessage(
        status.auto_inject
          ? "Auto inject is on. New Session Lake evidence will appear here."
          : "Auto inject is off.",
      );
      if (status.auto_inject) setNavigationRevision((current) => current + 1);
    } catch (error) {
      handleError(error);
    } finally {
      setIngestionBusy(false);
    }
  };

  const injectFixedSession = async () => {
    const fixedSessionID = sessionID.trim();
    if (!fixedSessionID) return;
    setIngestionBusy(true);
    setIngestionMessage("");
    try {
      const result = await injectWikiSession(fixedSessionID, beginAction());
      setIngestionMessage(
        `Injected ${result.processed_streams} stream${result.processed_streams === 1 ? "" : "s"} from ${fixedSessionID}.`,
      );
      setNavigationRevision((current) => current + 1);
    } catch (error) {
      handleError(error);
    } finally {
      setIngestionBusy(false);
    }
  };

  const confirmRebuild = async () => {
    setIngestionBusy(true);
    setIngestionMessage("");
    try {
      const status = await rebuildWiki(beginAction());
      setAutoInject(status.auto_inject);
      setTopics([]);
      setSelectedSlug("");
      setPage(undefined);
      setRevision(undefined);
      setRevisions([]);
      setLinks(EMPTY_LINKS);
      setSearchOpen(false);
      navigate({ pathname: "/wiki" }, { replace: true });
      setIngestionMessage("Wiki cleared. Rebuilding from Session Lake…");
      setRebuildOpen(false);
      setNavigationRevision((current) => current + 1);
    } catch (error) {
      handleError(error);
    } finally {
      setIngestionBusy(false);
    }
  };

  const pages = collectPages(topics);
  const historical = Boolean(page && revision && revision.id !== page.current_revision_id);
  const inlineRelations = historical ? [] : links.outgoing;

  return (
    <div className="wiki">
      <header className="wiki-header">
        <div>
          <span className="wiki-eyebrow">Grounded team knowledge</span>
          <h1>Wiki</h1>
          <p className="muted">Durable pages, revision history, and evidence in one place.</p>
        </div>
        <form
          className="wiki-search"
          role="search"
          onSubmit={(event) => {
            event.preventDefault();
            void submitSearch();
          }}
        >
          <label className="sr-only" htmlFor="wiki-search">
            Search the wiki
          </label>
          <input
            id="wiki-search"
            type="search"
            aria-label="Search the wiki"
            placeholder="Search decisions, systems, evidence…"
            value={searchInput}
            onChange={(event) => setSearchInput(event.target.value)}
          />
          <button className="btn primary" type="submit" disabled={searching}>
            {searching ? "Searching…" : "Search"}
          </button>
        </form>
      </header>

      <section className="card wiki-ingestion" aria-label="Wiki ingestion controls">
        <div className="wiki-ingestion-copy">
          <span className="wiki-eyebrow">Session Lake</span>
          <strong>Automatic Wiki injection</strong>
          <span className="muted small">
            Uses an independent PageWiki cursor; Team Note extraction is unaffected.
          </span>
        </div>
        <button
          className={autoInject ? "wiki-switch active" : "wiki-switch"}
          type="button"
          role="switch"
          aria-checked={autoInject}
          disabled={ingestionLoading || ingestionBusy}
          onClick={() => void toggleAutoInject()}
        >
          <span aria-hidden="true" />
          {autoInject ? "On" : "Off"}
        </button>
        <div className="wiki-fixed-session">
          <label htmlFor="wiki-session-id">Fixed session ID</label>
          <input
            id="wiki-session-id"
            value={sessionID}
            placeholder="e.g. 019fa46f-…"
            onChange={(event) => setSessionID(event.target.value)}
          />
          <button
            className="btn primary"
            type="button"
            disabled={ingestionBusy || sessionID.trim() === ""}
            onClick={() => void injectFixedSession()}
          >
            {ingestionBusy ? "Injecting…" : "Inject session"}
          </button>
        </div>
        {me.role === "owner" && (
          <div className="wiki-reset">
            <div>
              <strong>Start over with current Session Lake evidence</strong>
              <span className="muted small">
                Clears PageWiki-derived data and rebuilds it with the currently configured organizer.
              </span>
            </div>
            <button
              className="btn danger"
              type="button"
              disabled={ingestionBusy}
              onClick={() => setRebuildOpen(true)}
            >
              Reset & rebuild
            </button>
          </div>
        )}
        {ingestionMessage && <p className="wiki-ingestion-message">{ingestionMessage}</p>}
      </section>

      {searchOpen && (
        <section className="card wiki-search-results" role="region" aria-label="Search results">
          <div className="row between">
            <div>
              <span className="wiki-eyebrow">Current revisions</span>
              <h2>Search results</h2>
            </div>
            <button className="btn ghost sm" type="button" onClick={() => setSearchOpen(false)}>
              Close
            </button>
          </div>
          {!searching && searchResults.length === 0 ? (
            <p className="muted small">No current revision matches.</p>
          ) : (
            <ol>
              {searchResults.map((result) => (
                <li key={`${result.page.id}-${result.section_key}`}>
                  <button type="button" onClick={() => selectPage(result.page.slug)}>
                    <strong>{result.page.title}</strong>
                    <span>{result.passage}</span>
                    <small>
                      {result.section_key} · score {result.score.toFixed(2)}
                    </small>
                  </button>
                </li>
              ))}
            </ol>
          )}
        </section>
      )}

      {!navigationLoading && pages.length === 0 ? (
        <section className="card wiki-empty">
          <span className="wiki-empty-mark" aria-hidden="true">W</span>
          <h2>Your wiki is ready for its first page</h2>
          <p className="muted">Pages will appear here after a Page Wiki source is processed.</p>
        </section>
      ) : (
        <div className="wiki-layout">
          <nav className="card wiki-topic-rail" aria-label="Wiki topics">
            <div className="wiki-rail-heading">
              <span>Topics</span>
              <span className="faint small">{pages.length} pages</span>
            </div>
            {topics.map((topic) => (
              <Topic
                key={topic.id}
                topic={topic}
                selectedSlug={selectedSlug}
                onSelect={selectPage}
              />
            ))}
            {navigationLoading && <p className="muted small">Loading topics…</p>}
          </nav>

          <article className="card wiki-article" aria-busy={pageLoading}>
            {pageError ? (
              <div className="wiki-empty compact">
                <h2>This wiki page could not be loaded</h2>
                <p className="muted">Choose another page from the topic list or try again.</p>
              </div>
            ) : !revision || !page ? (
              <p className="muted">{pageLoading ? "Loading page…" : "Select a page."}</p>
            ) : (
              <>
                <div className="row between wrap">
                  <span className="wiki-eyebrow">Wiki page</span>
                  <span className={historical ? "badge b-suspended" : "badge b-active"}>
                    {historical ? "Historical" : "Current"}
                  </span>
                </div>
                <h1>{revision.title}</h1>
                <p className="wiki-summary">{revision.summary}</p>
                <div className="wiki-meta">
                  <code>/{page.slug}</code>
                  <span className="mono faint">{revision.id}</span>
                </div>

                <section className="wiki-connected" aria-label="Connected knowledge">
                  <div>
                    <span className="wiki-eyebrow">Bidirectional context</span>
                    <h2>Connected knowledge</h2>
                  </div>
                  <div className="wiki-link-counts">
                    <span>{links.outgoing.length} outgoing</span>
                    <span>{links.incoming.length} incoming</span>
                  </div>
                </section>

                <WikiMarkdown
                  revision={revision}
                  relations={inlineRelations}
                  onSelect={selectPage}
                />

                <section className="wiki-article-section">
                  <div className="wiki-rail-heading">
                    <h2>Revision history</h2>
                    <span className="faint small">{revisions.length} revisions</span>
                  </div>
                  <ol className="wiki-revisions">
                    {revisions.map((item, index) => (
                      <li key={item.id}>
                        <button
                          type="button"
                          className={item.id === revision.id ? "active" : ""}
                          aria-current={item.id === revision.id}
                          onClick={() => void selectRevision(item.id)}
                        >
                          r{index + 1} · {item.id}
                        </button>
                      </li>
                    ))}
                  </ol>
                </section>

                <section className="wiki-article-section">
                  <h2>Xanadu links</h2>
                  <div className="wiki-relation-grid">
                    <div>
                      <h3>Outgoing</h3>
                      <RelationList
                        relations={links.outgoing}
                        direction="outgoing"
                        onSelect={selectPage}
                      />
                    </div>
                    <div>
                      <h3>Incoming</h3>
                      <RelationList
                        relations={links.incoming}
                        direction="incoming"
                        onSelect={selectPage}
                      />
                    </div>
                  </div>
                </section>
              </>
            )}
          </article>
        </div>
      )}

      {rebuildOpen && (
        <ConfirmDialog
          title="Reset and rebuild Wiki"
          consequences={[
            "All PageWiki pages, revisions, links, citations, and maintenance runs will be deleted.",
            "PageWiki ingestion cursors will reset and every Session Lake stream will be processed again.",
            "Session Lake events and Team Notes are preserved.",
            "An LLM-backed rebuild may make paid provider calls.",
          ]}
          confirmLabel="Confirm reset & rebuild"
          busy={ingestionBusy}
          onConfirm={() => void confirmRebuild()}
          onClose={() => setRebuildOpen(false)}
        />
      )}
    </div>
  );
}
