import { useCallback, useEffect, useRef, useState } from "react";
import { useNavigate, useParams } from "react-router-dom";
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
import { EmptyState } from "../components/EmptyState";
import { Kicker } from "../components/Kicker";
import { RegionError } from "../components/RegionError";
import { Tag } from "../components/Tag";
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
  // Distinct from the "no pages yet" empty state: this is the request
  // itself failing, and it must not be silently read as an empty wiki
  // (design brief for this screen — the retired review round that deleted
  // every failure branch across the redesign passed 626 green tests
  // precisely because no task named its failure states; this screen's are
  // named and tested below).
  const [navigationError, setNavigationError] = useState<unknown>(undefined);
  const [selectedSlug, setSelectedSlug] = useState(() => routeSlug);
  const [page, setPage] = useState<WikiPage>();
  const [revision, setRevision] = useState<WikiRevision>();
  const [revisions, setRevisions] = useState<WikiRevision[]>([]);
  const [links, setLinks] = useState<WikiLinks>(EMPTY_LINKS);
  const [pageLoading, setPageLoading] = useState(false);
  // The page-content fetch's error, or undefined. Kept as the error value
  // itself (not a boolean) so the retryable region can show a precise
  // message via RegionError/noticeForError instead of one generic line.
  const [pageError, setPageError] = useState<unknown>(undefined);
  // Bumped by the article's Retry button; included in the page-fetch
  // effect's deps below so retrying re-issues the same three requests
  // without touching selectedSlug (same pattern as AdminExplorerPage's
  // NoteDetail retryKey).
  const [pageRetryKey, setPageRetryKey] = useState(0);
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
    setNavigationError(undefined);
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
        if (isAbortError(error)) return;
        setNavigationError(error);
        handleError(error);
      })
      .finally(() => {
        if (!controller.signal.aborted) setNavigationLoading(false);
      });
    return () => controller.abort();
  }, [handleError, navigationRevision, updateLocation]);

  const retryNavigation = useCallback(() => {
    setNavigationRevision((current) => current + 1);
  }, []);

  useEffect(() => {
    if (!selectedSlug) {
      setPage(undefined);
      setRevision(undefined);
      return;
    }
    const controller = new AbortController();
    const requestedRevision = new URLSearchParams(window.location.search).get("revision");
    setPageLoading(true);
    setPageError(undefined);
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
        setPageError(error);
        handleError(error);
      })
      .finally(() => {
        if (!controller.signal.aborted) setPageLoading(false);
      });
    return () => controller.abort();
  }, [handleError, selectedSlug, pageRetryKey]);

  const retryPage = useCallback(() => {
    setPageRetryKey((current) => current + 1);
  }, []);

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
    <>
      <div className="wiki-head">
        <div>
          {/* 没有「← All apps」返回链接：启动页已经不存在，/apps 会重定向回
              /apps/wiki，点一下等于自我跳转并把阅读器重挂载、静默回到第一页。
              分区间的导航现在由顶栏 + 二级导航提供。 */}
          <Kicker>Apps · wiki</Kicker>
          <h1>团队百科</h1>
          <p className="muted flush">
            {pages.length} 页 · 由会话写成，不是手写 · 新页面自己出现
          </p>
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
            placeholder="搜索决策、系统、证据…"
            value={searchInput}
            onChange={(event) => setSearchInput(event.target.value)}
          />
          <Button variant="primary" type="submit" disabled={searching}>
            {searching ? "搜索中…" : "搜索"}
          </Button>
        </form>
      </div>

      {searchOpen && (
        <section className="card wiki-search-results" role="region" aria-label="Search results">
          <div className="row between">
            <div>
              <span className="kicker">当前版本</span>
              <h2>搜索结果</h2>
            </div>
            <Button variant="ghost" size="sm" type="button" onClick={() => setSearchOpen(false)}>
              关闭
            </Button>
          </div>
          {!searching && searchResults.length === 0 ? (
            <p className="muted small">没有匹配的当前版本。</p>
          ) : (
            <ol className="wiki-search-list">
              {searchResults.map((result) => (
                <li key={`${result.page.id}-${result.section_key}`}>
                  <button type="button" onClick={() => selectPage(result.page.slug)}>
                    <strong>{result.page.title}</strong>
                    <span>{result.passage}</span>
                    <small>
                      {result.section_key} · 得分 {result.score.toFixed(2)}
                    </small>
                  </button>
                </li>
              ))}
            </ol>
          )}
        </section>
      )}

      {navigationError !== undefined ? (
        <div className="wiki-region-error">
          <RegionError error={navigationError} onRetry={retryNavigation} />
        </div>
      ) : !navigationLoading && pages.length === 0 ? (
        <EmptyState
          mark="W"
          title="你的百科还没有第一页"
          body="Page Wiki 来源处理完成后，页面会出现在这里。"
        />
      ) : (
        <div className="wiki-layout">
          <details className="wiki-rail" open>
            <summary className="wiki-rail-summary">
              <span>主题</span>
              <span className="faint small">{pages.length} 页</span>
            </summary>
            <nav aria-label="Wiki topics" className="wiki-rail-nav">
              <TopicTreePanel
                topics={topics}
                rootPages={rootPages}
                topicPath={topicPath}
                onNavigate={setTopicPath}
                selectedSlug={selectedSlug}
                onSelect={selectPage}
              />
              {navigationLoading && <p className="muted small">加载主题中…</p>}
            </nav>
          </details>

          <article className="wiki-article" aria-busy={pageLoading}>
            {pageError !== undefined ? (
              // Only the article collapses — the rail above and the
              // relations rail below stay mounted on whatever they already
              // have, so a broken single page never takes the rest of the
              // screen down with it.
              <RegionError error={pageError} onRetry={retryPage} />
            ) : !revision || !page ? (
              <p className="muted">{pageLoading ? "加载页面中…" : "请选择一个页面。"}</p>
            ) : (
              <>
                {page.status === "retired" && (
                  <div className="note warn wiki-retired-notice" role="status">
                    <span>这个页面已被归档。</span>
                    {page.successor_slug && (
                      <a
                        href={`/apps/wiki/${encodeURIComponent(page.successor_slug)}`}
                        onClick={(event) => {
                          event.preventDefault();
                          selectPage(page.successor_slug!);
                        }}
                      >
                        查看继任页面
                      </a>
                    )}
                  </div>
                )}
                <div className="row between wrap">
                  <span className="kicker">百科页面</span>
                  <Tag tone={historical ? "attention" : "neutral"}>
                    {historical ? "历史版本" : "当前版本"}
                  </Tag>
                </div>
                <div className="row wiki-title-row">
                  <h1>{revision.title}</h1>
                  {page.entity_type && page.entity_type !== "concept" && (
                    <Tag tone="outline" className="wiki-type-badge">
                      {page.entity_type}
                    </Tag>
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
                  <div className="wiki-row-head">
                    <h2>版本历史</h2>
                    <span className="faint small">{revisions.length} 个版本</span>
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
              </>
            )}
          </article>

          <details className="wiki-relations" open>
            <summary className="wiki-relations-summary">关系</summary>
            <div className="wiki-relation-groups">
              <div>
                <h3>链出</h3>
                <RelationList relations={links.outgoing} direction="outgoing" onSelect={selectPage} />
              </div>
              <div>
                <h3>链入</h3>
                <RelationList relations={links.incoming} direction="incoming" onSelect={selectPage} />
              </div>
            </div>
          </details>
        </div>
      )}
    </>
  );
}
