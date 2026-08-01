namespace go pagewiki.api

struct SourceEventInput {
  1: required string id (api.body="id")
  2: required i32 start_byte (api.body="start_byte")
  3: required i32 end_byte (api.body="end_byte")
}

struct InjectRequest {
  1: required string source_id (api.body="source_id")
  2: required string idempotency_key (api.body="idempotency_key")
  3: required string raw (api.body="raw")
  4: required list<SourceEventInput> events (api.body="events")
}

struct SourceAnchor {
  1: required string id
  2: required string source_revision_id
  3: required string event_id
  4: required i32 start_byte
  5: required i32 end_byte
  6: required string exact_quote
}

struct PageCitation {
  1: required string id
  2: required string page_revision_id
  3: required string section_key
  4: required i32 start_byte
  5: required i32 end_byte
  6: required string exact_text
  7: required list<SourceAnchor> source_anchors
}

struct PageLink {
  1: required string id
  2: required string page_revision_id
  3: required string section_key
  4: required i32 start_byte
  5: required i32 end_byte
  6: required string exact_text
  7: required string target_page_id
}

struct PageSection {
  1: required string key
  2: required string heading
  3: required string markdown
}

struct PageRevision {
  1: required string id
  2: required string page_id
  3: optional string base_revision_id
  4: required string title
  5: required string summary
  6: required list<PageSection> sections
  7: required string markdown
  8: required list<PageCitation> citations
  9: required list<PageLink> links
}

struct Page {
  1: required string id
  2: required string slug
  3: required string title
  4: required string current_revision_id
}

struct MaintenanceTarget {
  1: required string id
  2: required string brief_key
  3: required string action
  4: optional string page_id
  5: optional string page_revision_id
  6: required string status
  7: optional string failure_reason
  8: optional string error
}

struct MaintenanceRun {
  1: required string id
  2: required string source_revision_id
  3: required string status
  4: required list<MaintenanceTarget> targets
}

struct InjectResponse {
  1: required string source_revision_id
  2: required MaintenanceRun run
}

struct MaintenanceRunRequest {
  1: required string id (api.path="id")
}

struct PageBySlugRequest {
  1: required string slug (api.path="slug")
}

struct PageRevisionRequest {
  1: required string slug (api.path="slug")
  2: required string revision (api.path="revision")
}

struct CurrentPageResponse {
  1: required string id
  2: required string slug
  3: required string title
  4: required string current_revision_id
  5: required PageRevision revision
  6: optional string status
  7: optional string successor_slug
}

struct PageRevisionList {
  1: required list<PageRevision> revisions
}

struct ResolvedPageLink {
  1: required PageLink link
  2: required Page source_page
  3: required string source_revision_id
  4: required Page target_page
}

struct PageLinksResponse {
  1: required list<ResolvedPageLink> outgoing
  2: required list<ResolvedPageLink> incoming
}

struct SearchRequest {
  1: required string q (api.query="q")
}

struct SearchResult {
  1: required Page page
  2: required string revision_id
  3: required string section_key
  4: required string passage
  5: required double score
  6: required list<PageCitation> citations
  7: required list<PageLink> links
}

struct SearchResponse {
  1: required list<SearchResult> results
}

struct SourceRevisionRequest {
  1: required string revision (api.path="revision")
}

struct SourceEvent {
  1: required string id
  2: required i32 start_byte
  3: required i32 end_byte
}

struct SourceRevision {
  1: required string id
  2: required string source_id
  3: required string sha256
  4: required string raw
  5: required list<SourceEvent> events
}

struct SourceBacklink {
  1: required Page page
  2: required string revision_id
  3: required list<PageCitation> citations
}

struct SourceBacklinksResponse {
  1: required list<SourceBacklink> backlinks
}

struct NavigationPage {
  1: required string id
  2: required string slug
  3: required string title
  4: required i32 rank
}

struct NavigationTopic {
  1: required string id
  2: required string slug
  3: required string title
  4: required list<NavigationTopic> children
  5: required list<NavigationPage> pages
}

struct NavigationRequest {}

struct NavigationResponse {
  1: required list<NavigationTopic> roots
  2: required list<NavigationPage> pages
}

struct ReaderRequest {}

struct ReaderAssetRequest {
  1: required string asset (api.path="asset")
}

struct ReaderDocument {
  1: required string content
}

service PageWikiService {
  InjectResponse InjectSession(1: InjectRequest request) (api.post="/sessions/inject")
  InjectResponse InjectFile(1: InjectRequest request) (api.post="/files/inject")
  MaintenanceRun GetMaintenanceRun(1: MaintenanceRunRequest request) (api.get="/maintenance/runs/:id")
  CurrentPageResponse GetPage(1: PageBySlugRequest request) (api.get="/v1/wiki/pages/:slug")
  PageRevisionList ListPageRevisions(1: PageBySlugRequest request) (api.get="/v1/wiki/pages/:slug/revisions")
  PageRevision GetPageRevision(1: PageRevisionRequest request) (api.get="/v1/wiki/pages/:slug/revisions/:revision")
  PageLinksResponse GetPageBacklinks(1: PageBySlugRequest request) (api.get="/v1/wiki/pages/:slug/backlinks")
  SearchResponse Search(1: SearchRequest request) (api.get="/v1/wiki/search")
  SourceRevision GetSourceRevision(1: SourceRevisionRequest request) (api.get="/sources/:revision")
  SourceBacklinksResponse GetSourceBacklinks(1: SourceRevisionRequest request) (api.get="/sources/:revision/backlinks")
  NavigationResponse GetNavigation(1: NavigationRequest request) (api.get="/v1/wiki/navigation")
  ReaderDocument GetReader(1: ReaderRequest request) (api.get="/wiki")
  ReaderDocument GetReaderAsset(1: ReaderAssetRequest request) (api.get="/wiki/assets/:asset")
}
