"use strict";

const state = {
  page: null,
  revision: null,
  history: [],
  links: { outgoing: [], incoming: [] },
};

const byID = (id) => document.getElementById(id);

function element(tag, className, text) {
  const node = document.createElement(tag);
  if (className) node.className = className;
  if (text !== undefined) node.textContent = text;
  return node;
}

async function requestJSON(path) {
  const response = await fetch(path, {
    headers: { Accept: "application/json" },
  });
  const body = await response.json().catch(() => ({}));
  if (!response.ok) {
    throw new Error(body.message || `Request failed with ${response.status}`);
  }
  return body;
}

function setStatus(message, failed = false) {
  const target = byID("reader-status");
  target.textContent = message;
  target.style.color = failed ? "var(--red)" : "";
}

// Sidebar drill-down position: the slug path from the tree root down to the
// currently displayed layer. Reset to [] means the top level (all root
// topics) is shown.
let topicPath = [];

function collectPages(topics) {
  return topics.flatMap((topic) => [
    ...(topic.pages || []),
    ...collectPages(topic.children || []),
  ]);
}

/**
 * Walks `path` through `topics` one slug at a time. A segment that doesn't
 * match any child (e.g. the tree was rebuilt and the topic is gone) stops
 * the walk there instead of throwing; `truncated` tells the caller the
 * requested path had to be shortened so it can correct `topicPath`.
 */
function resolveTopicLayer(topics, rootPages, path) {
  let layerTopics = topics;
  let layerPages = rootPages;
  const resolvedPath = [];
  const crumbs = [];
  for (const slug of path) {
    const match = layerTopics.find((topic) => topic.slug === slug);
    if (!match) {
      return { path: resolvedPath, topics: layerTopics, pages: layerPages, crumbs, truncated: true };
    }
    resolvedPath.push(slug);
    crumbs.push({ title: match.title, path: [...resolvedPath] });
    layerTopics = match.children || [];
    layerPages = match.pages || [];
  }
  return { path: resolvedPath, topics: layerTopics, pages: layerPages, crumbs, truncated: false };
}

function renderNavigation(navigation) {
  const roots = navigation.roots || [];
  const rootPages = navigation.pages || [];
  const layer = resolveTopicLayer(roots, rootPages, topicPath);
  if (layer.truncated) topicPath = layer.path;

  const goTo = (path) => {
    topicPath = path;
    renderNavigation(navigation);
  };

  renderBreadcrumb(layer, goTo);
  renderTopicLayer(layer, goTo);

  // Root-level pages (never assigned to a topic — the common "stay at
  // root" outcome of incremental placement) plus every page nested under a
  // topic, in the same order the React reader uses for its page list.
  const pages = [...rootPages, ...collectPages(roots)];
  byID("page-count").textContent = `${pages.length} ${pages.length === 1 ? "page" : "pages"}`;
  if (state.page) markCurrentPage(state.page.slug);
  return pages;
}

function renderBreadcrumb(layer, goTo) {
  const container = byID("topic-breadcrumb");
  if (!container) return;
  container.replaceChildren();
  const crumbs = [{ title: "All", path: [] }, ...layer.crumbs];
  crumbs.forEach((crumb, index) => {
    if (index > 0) container.append(element("span", "breadcrumb-separator", "›"));
    const isCurrent = index === crumbs.length - 1;
    const button = element("button", "breadcrumb-item", crumb.title);
    button.type = "button";
    if (isCurrent) button.setAttribute("aria-current", "location");
    button.addEventListener("click", () => goTo(crumb.path));
    container.append(button);
  });
}

function renderTopicLayer(layer, goTo) {
  const tree = byID("topic-tree");
  tree.replaceChildren();
  if (layer.topics.length) {
    const topicsSection = element("section", "topic-group");
    for (const child of layer.topics) {
      const button = element("button", "topic-button");
      button.type = "button";
      button.append(
        element("span", "", child.title),
        element("span", "topic-count", String(collectPages([child]).length)),
      );
      button.addEventListener("click", () => goTo([...layer.path, child.slug]));
      topicsSection.append(button);
    }
    tree.append(topicsSection);
  }
  if (layer.pages.length) {
    const pagesSection = element("section", "topic-group");
    for (const page of layer.pages) pagesSection.append(pageButton(page));
    tree.append(pagesSection);
  }
}

function pageButton(page) {
  const button = element("button", "page-button", page.title);
  button.type = "button";
  button.dataset.slug = page.slug;
  button.addEventListener("click", () => loadPage(page.slug));
  return button;
}

function markCurrentPage(slug) {
  document.querySelectorAll(".page-button").forEach((button) => {
    if (button.dataset.slug === slug) {
      button.setAttribute("aria-current", "page");
    } else {
      button.removeAttribute("aria-current");
    }
  });
}

function pageURL(slug) {
  const parameters = new URLSearchParams(window.location.search);
  parameters.set("page", slug);
  parameters.delete("revision");
  return `/wiki?${parameters.toString()}`;
}

function pageLink(page, text, className) {
  const link = element("a", className, text);
  link.href = pageURL(page.slug);
  link.addEventListener("click", (event) => {
    event.preventDefault();
    loadPage(page.slug);
  });
  return link;
}

async function loadPage(slug, requestedRevision = "") {
  setStatus("Loading page");
  try {
    const [page, historyResponse, links] = await Promise.all([
      requestJSON(`/v1/wiki/pages/${encodeURIComponent(slug)}`),
      requestJSON(`/v1/wiki/pages/${encodeURIComponent(slug)}/revisions`),
      requestJSON(`/v1/wiki/pages/${encodeURIComponent(slug)}/backlinks`),
    ]);
    const revision = requestedRevision
      ? await requestJSON(
        `/v1/wiki/pages/${encodeURIComponent(slug)}/revisions/${encodeURIComponent(requestedRevision)}`,
      )
      : page.revision;
    const changedPage = state.page && state.page.slug !== slug;
    state.page = page;
    state.revision = revision;
    state.history = historyResponse.revisions || [];
    state.links = links;
    renderPage(page, revision, links);
    renderHistory(slug, revision.id, state.history);
    renderLinks(links);
    renderEvidence(revision.citations || []);
    markCurrentPage(slug);
    if (changedPage) window.scrollTo({ top: 0, behavior: "auto" });
    const parameters = new URLSearchParams(window.location.search);
    parameters.set("page", slug);
    if (requestedRevision) parameters.set("revision", requestedRevision);
    else parameters.delete("revision");
    window.history.replaceState(null, "", `/wiki?${parameters.toString()}`);
    setStatus("Current");
  } catch (error) {
    setStatus(error.message, true);
  }
}

function renderPage(page, revision, links) {
  byID("article-empty").hidden = true;
  byID("article-view").hidden = false;
  byID("article-title").textContent = revision.title;
  byID("article-summary").textContent = revision.summary;
  byID("article-slug").textContent = `/${page.slug}`;
  byID("article-revision").textContent = revision.id;
  const historical = revision.id !== page.current_revision_id;
  const badge = byID("revision-badge");
  badge.textContent = historical ? "Historical" : "Current";
  badge.classList.toggle("historical", historical);
  const inlineLinks = historical ? [] : links.outgoing || [];
  renderMarkdown(
    revision.markdown,
    byID("article-content"),
    inlineLinks,
    revision.sections || [],
  );
}

function renderMarkdown(markdown, container, relations, sections) {
  container.replaceChildren();
  const lines = String(markdown || "").split(/\r?\n/);
  const sectionKeys = new Map(
    sections.map((section) => [section.heading, section.key]),
  );
  let paragraph = [];
  let currentSection = "";
  const flush = () => {
    if (!paragraph.length) return;
    const node = element("p");
    if (currentSection) node.dataset.section = currentSection;
    appendLinkedText(
      node,
      paragraph.join(" "),
      currentSection,
      relations,
    );
    container.append(node);
    paragraph = [];
  };
  for (const rawLine of lines) {
    const line = rawLine.trim();
    if (!line) {
      flush();
      continue;
    }
    if (line.startsWith("### ")) {
      flush();
      const heading = element("h3", "", line.slice(4));
      if (currentSection) heading.dataset.section = currentSection;
      container.append(heading);
    } else if (line.startsWith("## ")) {
      flush();
      const headingText = line.slice(3);
      currentSection = sectionKeys.get(headingText) || "";
      const heading = element("h2", "", headingText);
      if (currentSection) heading.dataset.section = currentSection;
      container.append(heading);
    } else if (line.startsWith("# ")) {
      flush();
    } else {
      paragraph.push(line);
    }
  }
  flush();
}

function appendLinkedText(container, text, sectionKey, relations) {
  const matches = relations
    .filter((relation) => relation.link.section_key === sectionKey)
    .map((relation) => ({
      relation,
      start: text.indexOf(relation.link.exact_text),
    }))
    .filter((match) => match.start >= 0)
    .sort((left, right) => left.start - right.start);
  let cursor = 0;
  for (const match of matches) {
    const exactText = match.relation.link.exact_text;
    if (match.start < cursor) continue;
    container.append(document.createTextNode(text.slice(cursor, match.start)));
    const link = pageLink(
      match.relation.target_page,
      exactText,
      "xanadu-inline-link",
    );
    link.dataset.xanaduLink = "outgoing";
    link.title =
      `Open ${match.relation.target_page.title} · exact-text Xanadu link`;
    container.append(link);
    cursor = match.start + exactText.length;
  }
  container.append(document.createTextNode(text.slice(cursor)));
}

function renderHistory(slug, selectedID, revisions) {
  const list = byID("revision-history");
  list.replaceChildren();
  byID("history-count").textContent = `${revisions.length} revisions`;
  revisions.forEach((revision, index) => {
    const item = element("li");
    const button = element(
      "button",
      "revision-button",
      `r${index + 1} · ${revision.id.slice(-8)}`,
    );
    button.type = "button";
    button.setAttribute("aria-current", String(revision.id === selectedID));
    button.addEventListener("click", () => loadPage(slug, revision.id));
    item.append(button);
    list.append(item);
  });
}

function renderLinks(links) {
  const outgoing = links.outgoing || [];
  const incoming = links.incoming || [];
  renderLinkList(byID("outgoing-links"), outgoing, true);
  renderLinkList(byID("incoming-links"), incoming, false);
  renderLinkList(byID("xanadu-outgoing-links"), outgoing, true, true);
  renderLinkList(byID("xanadu-incoming-links"), incoming, false, true);
  byID("xanadu-outgoing-count").textContent =
    `${outgoing.length} outgoing`;
  byID("xanadu-incoming-count").textContent =
    `${incoming.length} incoming`;
  const total = outgoing.length + incoming.length;
  byID("xanadu-link-count").textContent =
    `${total} ${total === 1 ? "link" : "links"}`;
  byID("xanadu-summary-copy").textContent = total
    ? "Highlighted phrases travel forward; backlinks reveal who depends on this page."
    : "This page is not connected yet. Exact-text links will appear inline.";
}

function renderLinkList(list, links, outgoing, contextual = false) {
  list.replaceChildren();
  if (!links.length) {
    list.append(
      element(
        "li",
        "empty-note",
        outgoing ? "No references from this page." : "No pages point here yet.",
      ),
    );
    return;
  }
  for (const relation of links) {
    const page = outgoing ? relation.target_page : relation.source_page;
    const item = element("li", contextual ? "context-link-item" : "");
    const link = pageLink(
      page,
      "",
      "relation-button",
    );
    link.replaceChildren(
      element("strong", "", `${outgoing ? "→" : "←"} ${page.title}`),
      element("span", "", `“${relation.link.exact_text}”`),
    );
    item.append(link);
    list.append(item);
  }
}

function renderEvidence(citations) {
  const list = byID("evidence-list");
  list.replaceChildren();
  byID("evidence-count").textContent =
    `${citations.length} ${citations.length === 1 ? "citation" : "citations"}`;
  if (!citations.length) {
    list.append(element("p", "muted", "This revision has no citations."));
    return;
  }
  for (const citation of citations) {
    const card = element("article", "evidence-card");
    card.append(element("strong", "", `“${citation.exact_text}”`));
    for (const anchor of citation.source_anchors || []) {
      card.append(element("blockquote", "", anchor.exact_quote));
      card.append(
        element(
          "small",
          "",
          `${anchor.event_id} · bytes ${anchor.start_byte}–${anchor.end_byte}`,
        ),
      );
      card.append(element("small", "", anchor.source_revision_id));
    }
    list.append(card);
  }
}

async function searchWiki(query) {
  const drawer = byID("search-drawer");
  const list = byID("search-results");
  drawer.hidden = false;
  list.replaceChildren(element("li", "empty-note", "Searching…"));
  try {
    const response = await requestJSON(`/v1/wiki/search?q=${encodeURIComponent(query)}`);
    list.replaceChildren();
    if (!(response.results || []).length) {
      list.append(element("li", "empty-note", "No current revision matches."));
      return;
    }
    for (const result of response.results) {
      const item = element("li");
      const button = element("button", "search-result-button");
      button.type = "button";
      button.append(element("strong", "", result.page.title));
      button.append(
        element(
          "span",
          "",
          `${result.section_key} · score ${result.score} · ${result.passage}`,
        ),
      );
      button.addEventListener("click", async () => {
        drawer.hidden = true;
        await loadPage(result.page.slug);
        document
          .querySelector(`[data-section="${CSS.escape(result.section_key)}"]`)
          ?.scrollIntoView({ behavior: "smooth", block: "start" });
      });
      item.append(button);
      list.append(item);
    }
  } catch (error) {
    list.replaceChildren(element("li", "empty-note", error.message));
  }
}

async function loadRun(runID) {
  const panel = byID("run-panel");
  panel.replaceChildren(element("p", "muted", "Loading run…"));
  try {
    const run = await requestJSON(
      `/maintenance/runs/${encodeURIComponent(runID)}`,
    );
    const card = element("div", "run-card");
    card.append(element("strong", "", run.status.replaceAll("_", " ")));
    for (const target of run.targets || []) {
      const targetCard = element("div", `run-target ${target.status}`);
      targetCard.append(
        element("strong", "", `${target.brief_key} · ${target.status}`),
      );
      const detail =
        target.error || target.failure_reason || target.page_revision_id || target.action;
      targetCard.append(element("span", "", detail));
      card.append(targetCard);
    }
    panel.replaceChildren(card);
  } catch (error) {
    panel.replaceChildren(element("p", "muted", error.message));
  }
}

async function initialize() {
  try {
    const navigation = await requestJSON("/v1/wiki/navigation");
    const pages = renderNavigation(navigation);
    const parameters = new URLSearchParams(window.location.search);
    const slug = parameters.get("page") || pages[0]?.slug;
    const revision = parameters.get("revision") || "";
    const runID = parameters.get("run") || "";
    if (runID) {
      byID("run-id").value = runID;
      loadRun(runID);
    }
    if (slug) await loadPage(slug, revision);
    else setStatus("No pages");
  } catch (error) {
    setStatus(error.message, true);
  }
}

byID("search-form").addEventListener("submit", (event) => {
  event.preventDefault();
  const query = byID("wiki-search").value.trim();
  if (query) searchWiki(query);
});

byID("close-search").addEventListener("click", () => {
  byID("search-drawer").hidden = true;
  byID("wiki-search").focus();
});

byID("run-form").addEventListener("submit", (event) => {
  event.preventDefault();
  const runID = byID("run-id").value.trim();
  if (runID) loadRun(runID);
});

initialize();
