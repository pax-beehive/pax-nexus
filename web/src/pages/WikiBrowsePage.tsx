import { useCallback, useEffect, useRef, useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
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
  type WikiRevision,
  type WikiSearchResult,
  type WikiPage,
} from "../api/wiki";
import { Button } from "../components/Button";
import { RelationList } from "../components/wiki/RelationList";
import { collectPages, TopicTreePanel } from "../components/wiki/TopicTree";
import { WikiMarkdown } from "../components/wiki/WikiMarkdown";
import { isAbortError, usePolling } from "../lib/usePolling";
import { useErrorHandler } from "../lib/useErrorHandler";

const EMPTY_LINKS: WikiLinks = { outgoing: [], incoming: [] };

export function WikiBrowsePage() {
  const navigate = useNavigate();
  // react-router's non-data-router useNavigate() returns a new function
  // identity whenever the current pathname changes (it closes over
  // location.pathname internally), which is exactly what selecting a page
  // does now that the slug lives in the path. A ref keeps updateLocation
  // (below) — and therefore the navigation-tree effect that depends on it —
  // stable across selections instead of re-running on every one.
  const navigateRef = useRef(navigate);
  navigateRef.current = navigate;
  const handleError = useErrorHandler();
  // Mounted at both /apps/wiki and /apps/wiki/:slug — the slug lives in the
  // route path, not the query string; only `revision` still rides ?revision=.
  const { slug: routeSlug = "" } = useParams();
  // The navigation-tree effect below must fetch and auto-select on mount
  // and on navigationRevision only, never on slug change (that fetch is
  // the expensive one, and re-running the auto-select branch on every
  // selection would silently bounce a page that isn't in the tree — e.g. a
  // retired page reached via search — back to pages[0]). A ref lets the
  // effect read the current route slug without depending on it.
  const routeSlugRef = useRef(routeSlug);
  routeSlugRef.current = routeSlug;
  const [topics, setTopics] = useState<WikiNavigationTopic[]>([]);
  const [rootPages, setRootPages] = useState<WikiNavigationPage[]>([]);
  const [topicPath, setTopicPath] = useState<string[]>([]);
  const [navigationLoading, setNavigationLoading] = useState(true);
  const [selectedSlug, setSelectedSlug] = useState(() => routeSlug);
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
  const [navigationRevision, setNavigationRevision] = useState(0);

  // selectedSlug only self-updates via selectPage; nothing else syncs it
  // when the route param changes out from under the component (browser
  // Back/Forward, or a palette jump straight to /apps/wiki/:slug). Collapsing
  // the wiki route's remount key (routeKey.ts) removed the accidental
  // remount that used to paper over this, so it needs its own explicit
  // sync — deliberately separate from the navigation-tree effect above,
  // which must still never depend on routeSlug (see its comment).
  useEffect(() => {
    if (routeSlug !== selectedSlug) setSelectedSlug(routeSlug);
  }, [routeSlug, selectedSlug]);

  useEffect(() => {
    const controller = new AbortController();
    getWikiIngestionStatus(controller.signal)
      .then((status) => setAutoInject(status.auto_inject))
      .catch((error: unknown) => {
        if (!isAbortError(error)) handleError(error);
      });
    return () => controller.abort();
  }, [handleError]);

  // Refresh the navigation tree while auto inject is on so newly ingested
  // pages show up without a manual reload; usePolling supplies the
  // visibility gating and single refresh-on-wake cycle (doc 4.3 pattern).
  usePolling(
    async () => {
      if (!autoInject) return;
      setNavigationRevision((current) => current + 1);
    },
    3000,
    [autoInject],
  );

  const updateLocation = useCallback((slug: string, revisionID?: string) => {
    const pathname = `/apps/wiki/${encodeURIComponent(slug)}`;
    const search = revisionID ? `?revision=${encodeURIComponent(revisionID)}` : "";
    navigateRef.current({ pathname, search }, { replace: true });
  }, []);

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
        const rootLevelPages = navigation.pages ?? [];
        const pages = [...rootLevelPages, ...collectPages(roots)];
        setTopics(roots);
        setRootPages(rootLevelPages);
        if (
          pages.length > 0 &&
          !pages.some((candidate) => candidate.slug === routeSlugRef.current)
        ) {
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

  const pages = [...rootPages, ...collectPages(topics)];
  const historical = Boolean(page && revision && revision.id !== page.current_revision_id);
  const inlineRelations = historical ? [] : links.outgoing;

  return (
    <div className="wiki wiki-browse">
      <header className="wiki-header">
        <div>
          <Link className="app-back" to="/apps">← All apps</Link>
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
          <Button variant="primary" type="submit" disabled={searching}>
            {searching ? "Searching…" : "Search"}
          </Button>
        </form>
      </header>

      {searchOpen && (
        <section className="card wiki-search-results" role="region" aria-label="Search results">
          <div className="row between">
            <div>
              <span className="wiki-eyebrow">Current revisions</span>
              <h2>Search results</h2>
            </div>
            <Button variant="ghost" size="sm" type="button" onClick={() => setSearchOpen(false)}>
              Close
            </Button>
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
        <section className="wiki-empty">
          <span className="wiki-empty-mark" aria-hidden="true">W</span>
          <h2>Your wiki is ready for its first page</h2>
          <p className="muted">Pages will appear here after a Page Wiki source is processed.</p>
        </section>
      ) : (
        <div className="wiki-layout">
          <nav className="wiki-topic-rail" aria-label="Wiki topics">
            <div className="wiki-rail-heading">
              <span>Topics</span>
              <span className="faint small">{pages.length} pages</span>
            </div>
            <TopicTreePanel
              topics={topics}
              rootPages={rootPages}
              topicPath={topicPath}
              onNavigate={setTopicPath}
              selectedSlug={selectedSlug}
              onSelect={selectPage}
            />
            {navigationLoading && <p className="muted small">Loading topics…</p>}
          </nav>

          <article className="wiki-article" aria-busy={pageLoading}>
            {pageError ? (
              <div className="wiki-empty compact">
                <h2>This wiki page could not be loaded</h2>
                <p className="muted">Choose another page from the topic list or try again.</p>
              </div>
            ) : !revision || !page ? (
              <p className="muted">{pageLoading ? "Loading page…" : "Select a page."}</p>
            ) : (
              <>
                {page.status === "retired" && (
                  <div className="wiki-retired-banner" role="status">
                    <span>This page has been archived.</span>
                    {page.successor_slug && (
                      <a
                        href={`/apps/wiki/${encodeURIComponent(page.successor_slug)}`}
                        className="wiki-inline-link"
                        onClick={(event) => {
                          event.preventDefault();
                          selectPage(page.successor_slug!);
                        }}
                      >
                        See successor page
                      </a>
                    )}
                  </div>
                )}
                <div className="row between wrap">
                  <span className="wiki-eyebrow">Wiki page</span>
                  <span className={historical ? "badge b-suspended" : "badge b-active"}>
                    {historical ? "Historical" : "Current"}
                  </span>
                </div>
                <div className="row wiki-title-row">
                  <h1>{revision.title}</h1>
                  {page.entity_type && page.entity_type !== "concept" && (
                    <span className="wiki-type-badge">{page.entity_type}</span>
                  )}
                </div>
                <p className="wiki-summary">{revision.summary}</p>
                <div className="wiki-meta">
                  <code>/{page.slug}</code>
                  <span className="mono faint">{revision.id}</span>
                </div>

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
    </div>
  );
}
