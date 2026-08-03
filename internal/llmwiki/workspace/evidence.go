package workspace

import "fmt"

// EvidenceLocation points at where one Source turn landed in the rendered
// Markdown tree: the Source page path and the message anchor inside it.
type EvidenceLocation struct {
	SourcePath string
	Anchor     string
}

// SourceEvidenceMap loads the workspace manifest and maps every Source turn
// ID to its Markdown location. It fails on duplicate turn IDs, which would
// make evidence references ambiguous.
func SourceEvidenceMap(root string) (map[string]EvidenceLocation, error) {
	manifest, err := readManifest(root)
	if err != nil {
		return nil, fmt.Errorf("load workspace manifest: %w", err)
	}
	result := make(map[string]EvidenceLocation)
	for _, source := range manifest.Sources {
		for _, anchor := range source.Anchors {
			if _, duplicate := result[anchor.TurnID]; duplicate {
				return nil, fmt.Errorf("duplicate evidence ID %s", anchor.TurnID)
			}
			result[anchor.TurnID] = EvidenceLocation{
				SourcePath: source.Path,
				Anchor:     anchor.ID,
			}
		}
	}
	return result, nil
}
