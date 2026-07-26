package pagewiki

import (
	"errors"
	"fmt"
	"strings"
)

var (
	ErrInvalidPageBrief  = errors.New("invalid page brief")
	ErrInvalidSource     = errors.New("invalid source")
	ErrInvalidDraft      = errors.New("invalid page draft")
	ErrInvalidCitation   = errors.New("invalid citation")
	ErrInvalidLink       = errors.New("invalid page link")
	ErrInvalidRequest    = errors.New("invalid injection request")
	ErrNotFound          = errors.New("not found")
	ErrImmutableConflict = errors.New("immutable value conflict")
	ErrRevisionConflict  = errors.New("revision conflict")
)

func ValidateInjectSessionRequest(request InjectSessionRequest) error {
	if strings.TrimSpace(request.IdempotencyKey) == "" {
		return fmt.Errorf("%w: idempotency key is required", ErrInvalidRequest)
	}
	return nil
}

func ValidatePageBrief(brief PageBrief, catalog PageCatalog) error {
	if strings.TrimSpace(brief.Key) == "" {
		return fmt.Errorf("%w: key is required", ErrInvalidPageBrief)
	}
	if len(brief.TopicPath) > 2 {
		return fmt.Errorf("%w: topic path exceeds two levels", ErrInvalidPageBrief)
	}
	for _, segment := range brief.TopicPath {
		if strings.TrimSpace(segment) == "" {
			return fmt.Errorf("%w: topic path contains an empty segment", ErrInvalidPageBrief)
		}
	}
	if hasBlankOrDuplicate(brief.EvidenceEventIDs) {
		return fmt.Errorf("%w: evidence event IDs must be unique and non-empty", ErrInvalidPageBrief)
	}
	switch brief.Action {
	case PageActionCreate:
		if brief.TargetPageID != "" || brief.ExpectedBaseRevisionID != "" {
			return fmt.Errorf("%w: create cannot choose page identity", ErrInvalidPageBrief)
		}
		if len(brief.TopicPath) == 0 {
			return fmt.Errorf("%w: create requires a topic path", ErrInvalidPageBrief)
		}
		if strings.TrimSpace(brief.ProposedSlug) == "" ||
			strings.TrimSpace(brief.ProposedTitle) == "" {
			return fmt.Errorf("%w: create requires slug and title", ErrInvalidPageBrief)
		}
	case PageActionUpdate:
		entry, found := catalogByID(catalog, brief.TargetPageID)
		if !found {
			return fmt.Errorf("%w: update page is absent from catalog", ErrInvalidPageBrief)
		}
		if brief.ExpectedBaseRevisionID == "" ||
			brief.ExpectedBaseRevisionID != entry.CurrentRevisionID {
			return fmt.Errorf("%w: update base is not current", ErrInvalidPageBrief)
		}
	case PageActionSourceOnly, PageActionAmbiguous:
		if brief.TargetPageID != "" || brief.ExpectedBaseRevisionID != "" {
			return fmt.Errorf("%w: non-publishing action cannot target a page", ErrInvalidPageBrief)
		}
	default:
		return fmt.Errorf("%w: unsupported action %q", ErrInvalidPageBrief, brief.Action)
	}
	return nil
}

func hasBlankOrDuplicate(values []string) bool {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			return true
		}
		if _, exists := seen[value]; exists {
			return true
		}
		seen[value] = struct{}{}
	}
	return false
}

func catalogByID(catalog PageCatalog, id string) (PageCatalogEntry, bool) {
	for _, entry := range catalog {
		if entry.ID == id {
			return entry, true
		}
	}
	return PageCatalogEntry{}, false
}
