import { humanFetch } from "./client";

export interface WikiPageSummary {
  id: string;
  slug: string;
  title: string;
  current_revision_id: string;
}

export interface WikiNavigationPage {
  id: string;
  slug: string;
  title: string;
  rank: number;
}

export interface WikiNavigationTopic {
  id: string;
  slug: string;
  title: string;
  children: WikiNavigationTopic[];
  pages: WikiNavigationPage[];
}

export interface WikiNavigation {
  roots: WikiNavigationTopic[];
}

export interface WikiSourceAnchor {
  id: string;
  source_revision_id: string;
  event_id: string;
  start_byte: number;
  end_byte: number;
  exact_quote: string;
}

export interface WikiCitation {
  id: string;
  page_revision_id: string;
  section_key: string;
  start_byte: number;
  end_byte: number;
  exact_text: string;
  source_anchors: WikiSourceAnchor[];
}

export interface WikiPageLink {
  id: string;
  page_revision_id: string;
  section_key: string;
  start_byte: number;
  end_byte: number;
  exact_text: string;
  target_page_id: string;
}

export interface WikiSection {
  key: string;
  heading: string;
  markdown: string;
}

export interface WikiRevision {
  id: string;
  page_id: string;
  base_revision_id?: string;
  title: string;
  summary: string;
  sections: WikiSection[];
  markdown: string;
  citations: WikiCitation[];
  links: WikiPageLink[];
}

export interface WikiPage extends WikiPageSummary {
  revision: WikiRevision;
}

export interface WikiResolvedLink {
  link: WikiPageLink;
  source_page: WikiPageSummary;
  source_revision_id: string;
  target_page: WikiPageSummary;
}

export interface WikiLinks {
  outgoing: WikiResolvedLink[];
  incoming: WikiResolvedLink[];
}

export interface WikiSearchResult {
  page: WikiPageSummary;
  revision_id: string;
  section_key: string;
  passage: string;
  score: number;
  citations: WikiCitation[];
  links: WikiPageLink[];
}

export interface WikiIngestionStatus {
  auto_inject: boolean;
}

function get<T>(path: string, signal?: AbortSignal): Promise<T> {
  return humanFetch<T>(path, { signal });
}

export function getWikiNavigation(signal?: AbortSignal): Promise<WikiNavigation> {
  return get<WikiNavigation>("/navigation", signal);
}

export function getWikiIngestionStatus(signal?: AbortSignal): Promise<WikiIngestionStatus> {
  return get<WikiIngestionStatus>("/v1/wiki/ingestion", signal);
}

export function getWikiPage(slug: string, signal?: AbortSignal): Promise<WikiPage> {
  return get<WikiPage>(`/pages/${encodeURIComponent(slug)}`, signal);
}

export async function getWikiRevisions(
  slug: string,
  signal?: AbortSignal,
): Promise<WikiRevision[]> {
  const response = await get<{ revisions: WikiRevision[] }>(
    `/pages/${encodeURIComponent(slug)}/revisions`,
    signal,
  );
  return response.revisions ?? [];
}

export function getWikiRevision(
  slug: string,
  revision: string,
  signal?: AbortSignal,
): Promise<WikiRevision> {
  return get<WikiRevision>(
    `/pages/${encodeURIComponent(slug)}/revisions/${encodeURIComponent(revision)}`,
    signal,
  );
}

export function getWikiLinks(slug: string, signal?: AbortSignal): Promise<WikiLinks> {
  return get<WikiLinks>(`/pages/${encodeURIComponent(slug)}/backlinks`, signal);
}

export async function searchWiki(
  search: string,
  signal?: AbortSignal,
): Promise<WikiSearchResult[]> {
  const response = await get<{ results: WikiSearchResult[] }>(
    `/search?q=${encodeURIComponent(search)}`,
    signal,
  );
  return response.results ?? [];
}
