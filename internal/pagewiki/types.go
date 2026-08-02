// Package pagewiki implements the page-centric LLM Wiki domain.
package pagewiki

type SourceKind string

const (
	SourceKindSession SourceKind = "session"
	SourceKindFile    SourceKind = "file"
)

type Source struct {
	ID   string
	Kind SourceKind
}

type SourceEventInput struct {
	ID        string
	StartByte int
	EndByte   int
}

type SourceEvent struct {
	ID        string
	StartByte int
	EndByte   int
}

type SourceRevision struct {
	ID       string
	SourceID string
	SHA256   string
	Raw      []byte
	Events   []SourceEvent
}

type SourceAnchor struct {
	ID               string
	SourceRevisionID string
	EventID          string
	StartByte        int
	EndByte          int
	ExactQuote       string
}

type PageStatus string

const (
	PageStatusActive  PageStatus = "" // zero value: active, keeps old payloads valid
	PageStatusRetired PageStatus = "retired"
)

// EntityType classifies what a Page is about, drawn from a TypeRegistry
// (typed values) with EntityTypeConcept as the untyped fallback.
type EntityType string

const (
	EntityTypePerson     EntityType = "person"
	EntityTypeSystem     EntityType = "system"
	EntityTypeDecision   EntityType = "decision"
	EntityTypeConvention EntityType = "convention"
	EntityTypeConcept    EntityType = "concept" // fallback
)

// RelationType classifies how a PageLink relates its source and target,
// drawn from a TypeRegistry (typed values) with RelationTypeRelatesTo as the
// untyped fallback.
type RelationType string

const (
	RelationTypeOwns       RelationType = "owns"
	RelationTypeDependsOn  RelationType = "depends-on"
	RelationTypePartOf     RelationType = "part-of"
	RelationTypeSupersedes RelationType = "supersedes"
	RelationTypeAffects    RelationType = "affects"
	RelationTypeRelatesTo  RelationType = "relates-to" // fallback
)

type Page struct {
	ID                string
	Slug              string
	Title             string
	CurrentRevisionID string
	Status            PageStatus
	SuccessorPageID   string
	RetiredByRunID    string
	EntityType        EntityType
}

func (p Page) Retired() bool { return p.Status == PageStatusRetired }

// RetireRequest retires a page: it stops surfacing in catalog, navigation,
// and search, while its ID, slug, and revision history remain resolvable.
type RetireRequest struct {
	PageID                 string
	ExpectedBaseRevisionID string // CAS against CurrentRevisionID
	SuccessorPageID        string // optional
	RunID                  string
}

type PageCatalogEntry struct {
	ID                string
	Slug              string
	Title             string
	CurrentRevisionID string
	Summary           string
	EntityType        EntityType
}

type PageCatalog []PageCatalogEntry

type PageSection struct {
	Key      string
	Heading  string
	Markdown string
}

type PageRevision struct {
	ID             string
	PageID         string
	BaseRevisionID string
	Title          string
	Summary        string
	Sections       []PageSection
	Markdown       string
	Citations      []PageCitation
	Links          []PageLink
}

type PageCitation struct {
	ID             string
	PageRevisionID string
	SectionKey     string
	StartByte      int
	EndByte        int
	ExactText      string
	SourceAnchors  []SourceAnchor
}

type PageLink struct {
	ID             string
	PageRevisionID string
	SectionKey     string
	StartByte      int
	EndByte        int
	ExactText      string
	TargetPageID   string
	RelationType   RelationType
}

type Topic struct {
	ID       string
	ParentID string
	Slug     string
	Title    string
}

type PagePlacement struct {
	PageID  string
	TopicID string
	Rank    int
}

type TopicTree struct {
	Topics     []Topic
	Placements []PagePlacement
}

// DefaultTopicTreeMaxDepth caps topic nesting when no override is
// configured; root topics are level 1.
const DefaultTopicTreeMaxDepth = 5

type PagePublication struct {
	Page      Page
	Revision  PageRevision
	Topics    []Topic
	Placement *PagePlacement
	// Revive, when true, allows the publication to update a retired page and
	// flips it back to active, clearing SuccessorPageID/RetiredByRunID.
	Revive bool
}

type Navigation struct {
	Roots []NavigationTopic
	Pages []NavigationPage
}

type NavigationTopic struct {
	ID       string
	Slug     string
	Title    string
	Children []NavigationTopic
	Pages    []NavigationPage
}

type NavigationPage struct {
	ID    string
	Slug  string
	Title string
	Rank  int
}

type SearchChunk struct {
	ID             string
	PageID         string
	PageRevisionID string
	SectionKey     string
	Passage        string
}

type SearchResult struct {
	Page       Page
	RevisionID string
	SectionKey string
	Passage    string
	Score      float64
	Citations  []PageCitation
	Links      []PageLink
}

type ResolvedPageLink struct {
	Link             PageLink
	SourcePage       Page
	SourceRevisionID string
	TargetPage       Page
}

type PageLinkSet struct {
	Outgoing []ResolvedPageLink
	Incoming []ResolvedPageLink
}

type SourceBacklink struct {
	Page      Page
	Revision  PageRevision
	Citations []PageCitation
}

type PageAction string

const (
	PageActionCreate     PageAction = "create"
	PageActionUpdate     PageAction = "update"
	PageActionSourceOnly PageAction = "source-only"
	PageActionAmbiguous  PageAction = "ambiguous"
)

type PageBrief struct {
	Key                    string
	Action                 PageAction
	TargetPageID           string
	ExpectedBaseRevisionID string
	ProposedSlug           string
	ProposedTitle          string
	ReaderGoal             string
	EvidenceEventIDs       []string
	Evidence               []EvidenceQuoteDraft
	RelatedPages           []RelatedPage
	EntityType             EntityType
}

type RelatedPage struct {
	ID       string
	Title    string
	Relation RelationType
}

type PageDraft struct {
	Slug      string
	Title     string
	Summary   string
	Sections  []SectionDraft
	Citations []CitationDraft
	Links     []LinkDraft
}

type SectionDraft struct {
	Key      string
	Heading  string
	Markdown string
}

type EvidenceQuoteDraft struct {
	EventID   string
	ExactText string
}

type CitationDraft struct {
	SectionKey string
	ExactText  string
	Evidence   []EvidenceQuoteDraft
	// Anchors carries forward SourceAnchor rows verbatim for curation
	// rewrites/merges, whose evidence is a union of prior citations rather
	// than a fresh resolution against one source revision. Empty for the
	// LLM session editor path, which still populates Evidence instead.
	Anchors []SourceAnchor
}

type LinkDraft struct {
	SectionKey   string
	ExactText    string
	TargetPageID string
	RelationType RelationType
}

type RunStatus string

const (
	RunStatusSucceeded      RunStatus = "succeeded"
	RunStatusFailed         RunStatus = "failed"
	RunStatusPartialSuccess RunStatus = "partial_success"
	RunStatusPending        RunStatus = "pending"
)

type TargetStatus string

const (
	TargetStatusSucceeded TargetStatus = "succeeded"
	TargetStatusFailed    TargetStatus = "failed"
	TargetStatusPending   TargetStatus = "pending"
)

type TargetFailureReason string

const (
	TargetFailureNone                TargetFailureReason = ""
	TargetFailureInvalidBrief        TargetFailureReason = "invalid_brief"
	TargetFailureInvalidDraft        TargetFailureReason = "invalid_draft"
	TargetFailureInvalidCitation     TargetFailureReason = "invalid_citation"
	TargetFailureInvalidLink         TargetFailureReason = "invalid_link"
	TargetFailurePublicationConflict TargetFailureReason = "publication_conflict"
)

type MaintenanceTarget struct {
	ID             string
	BriefKey       string
	Action         PageAction
	PageID         string
	PageRevisionID string
	Status         TargetStatus
	FailureReason  TargetFailureReason
	Error          string
}

type MaintenanceRun struct {
	ID               string
	SourceRevisionID string
	Status           RunStatus
	Targets          []MaintenanceTarget
}

type CurationVerdict string

const (
	CurationVerdictMerge    CurationVerdict = "merge"
	CurationVerdictConflict CurationVerdict = "conflict"
	CurationVerdictDistinct CurationVerdict = "distinct"
	CurationVerdictRetire   CurationVerdict = "retire"
	CurationVerdictRewrite  CurationVerdict = "rewrite"
	CurationVerdictKeep     CurationVerdict = "keep"
)

type CurationOutcome struct {
	Kind      string // "pair" | "page"
	PageIDs   []string
	Verdict   CurationVerdict
	Rationale string
	Refuted   bool
	Status    TargetStatus
	Error     string
}

type CurationRun struct {
	ID          string
	Fingerprint string
	Status      RunStatus
	Outcomes    []CurationOutcome
}

type PageEmbedding struct {
	PageID     string
	RevisionID string
	Vector     []float32
}

type InjectSessionRequest struct {
	SourceID       string
	IdempotencyKey string
	Raw            []byte
	Events         []SourceEventInput
}

type InjectResult struct {
	SourceRevisionID string
	Run              MaintenanceRun
}
