package stageeval

// AtomSupport identifies the fixed stage items that contain one required atom.
type AtomSupport struct {
	AtomID  string   `json:"atom_id"`
	ItemIDs []string `json:"item_ids,omitempty"`
}

// RequiredAtomSupports matches required atoms to fixed observation items using
// the same compiled patterns as stage scoring.
func RequiredAtomSupports(fixture Fixture, items []Item) ([]AtomSupport, error) {
	compiled, err := compileFixture(fixture)
	if err != nil {
		return nil, err
	}
	matches := matchAtoms(compiled.required, items)
	result := make([]AtomSupport, 0, len(compiled.required))
	for _, atom := range compiled.required {
		support := AtomSupport{AtomID: atom.ID}
		for _, index := range matches[atom.ID] {
			support.ItemIDs = append(support.ItemIDs, items[index].ID)
		}
		result = append(result, support)
	}
	return result, nil
}
