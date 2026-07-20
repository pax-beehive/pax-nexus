package extractioneval

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"

	"github.com/pax-beehive/pax-nexus/internal/eval/extractionshadow"
	"github.com/pax-beehive/pax-nexus/internal/eval/stageeval"
)

const ProfileSchemaVersion = "pax-extraction-eval-v1-profile"

// Profile defines a bounded extraction cohort built around fixed source
// session events. Gold atom patterns remain in the fixture; the profile only controls
// which source context is replayed.
type Profile struct {
	SchemaVersion string        `json:"schema_version"`
	Name          string        `json:"name"`
	FullSource    bool          `json:"full_source,omitempty"`
	BeforeEvents  int           `json:"before_events"`
	AfterEvents   int           `json:"after_events"`
	MaxSlices     int           `json:"max_slices"`
	Cases         []ProfileCase `json:"cases"`
}

type ProfileCase struct {
	CaseID        string   `json:"case_id"`
	ExtraEventIDs []string `json:"extra_event_ids,omitempty"`
}

// Selection describes the paid input after source preflight and windowing.
type Selection struct {
	SourceEvents    int      `json:"source_events"`
	SelectedEvents  int      `json:"selected_events"`
	SelectedStreams int      `json:"selected_streams"`
	ExpectedSlices  int      `json:"expected_slices"`
	EventIDs        []string `json:"event_ids"`
}

func LoadProfile(path string) (Profile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Profile{}, fmt.Errorf("open extraction eval profile: %w", err)
	}
	var profile Profile
	if err := json.Unmarshal(data, &profile); err != nil {
		return Profile{}, fmt.Errorf("decode extraction eval profile: %w", err)
	}
	if err := validateProfile(profile); err != nil {
		return Profile{}, err
	}
	return profile, nil
}

func validateProfile(profile Profile) error {
	if profile.SchemaVersion != ProfileSchemaVersion {
		return fmt.Errorf("validate extraction eval profile: schema_version must be %q", ProfileSchemaVersion)
	}
	if profile.Name == "" || len(profile.Cases) == 0 {
		return fmt.Errorf("validate extraction eval profile: name and cases are required")
	}
	if profile.BeforeEvents < 0 || profile.AfterEvents < 0 || profile.MaxSlices < 1 {
		return fmt.Errorf("validate extraction eval profile: event windows must be non-negative and max_slices positive")
	}
	seen := make(map[string]struct{}, len(profile.Cases))
	for _, profileCase := range profile.Cases {
		if profileCase.CaseID == "" {
			return fmt.Errorf("validate extraction eval profile: case_id is required")
		}
		if _, duplicate := seen[profileCase.CaseID]; duplicate {
			return fmt.Errorf("validate extraction eval profile: duplicate case %q", profileCase.CaseID)
		}
		seen[profileCase.CaseID] = struct{}{}
	}
	return nil
}

// SelectProfileCases restricts the manifest and fixture set to the profile in
// profile order, failing before any provider is constructed.
func SelectProfileCases(
	profile Profile,
	manifest []extractionshadow.ManifestCase,
	fixtures stageeval.FixtureSet,
) ([]extractionshadow.ManifestCase, stageeval.FixtureSet, error) {
	manifestByID := make(map[string]extractionshadow.ManifestCase, len(manifest))
	for _, manifestCase := range manifest {
		manifestByID[manifestCase.ID] = manifestCase
	}
	fixtureByID := make(map[string]stageeval.Fixture, len(fixtures.Cases))
	for _, fixture := range fixtures.Cases {
		fixtureByID[fixture.CaseID] = fixture
	}
	selectedManifest := make([]extractionshadow.ManifestCase, 0, len(profile.Cases))
	selectedFixtures := stageeval.FixtureSet{SchemaVersion: fixtures.SchemaVersion, Dataset: fixtures.Dataset}
	for _, profileCase := range profile.Cases {
		manifestCase, manifestOK := manifestByID[profileCase.CaseID]
		fixture, fixtureOK := fixtureByID[profileCase.CaseID]
		if !manifestOK || !fixtureOK {
			return nil, stageeval.FixtureSet{}, fmt.Errorf(
				"select extraction eval profile: case %q is missing from manifest or fixtures", profileCase.CaseID,
			)
		}
		selectedManifest = append(selectedManifest, manifestCase)
		selectedFixtures.Cases = append(selectedFixtures.Cases, fixture)
	}
	return selectedManifest, selectedFixtures, nil
}

func FullSourceProfile(manifest []extractionshadow.ManifestCase) Profile {
	profile := Profile{
		SchemaVersion: ProfileSchemaVersion, Name: "full-source", FullSource: true,
		MaxSlices: int(^uint(0) >> 1),
	}
	for _, manifestCase := range manifest {
		profile.Cases = append(profile.Cases, ProfileCase{CaseID: manifestCase.ID})
	}
	return profile
}

// PreflightAndSelectStreams proves every selected case has source evidence and
// that every referenced session event exists, then returns bounded source windows.
func PreflightAndSelectStreams(
	profile Profile,
	caseIDs []string,
	fixtures stageeval.FixtureSet,
	streams []extractionshadow.StreamEvents,
	eventLimit int,
) ([]extractionshadow.StreamEvents, Selection, error) {
	if eventLimit < 1 {
		return nil, Selection{}, fmt.Errorf("preflight extraction eval source: event limit must be positive")
	}
	fixtureByID := make(map[string]stageeval.Fixture, len(fixtures.Cases))
	for _, fixture := range fixtures.Cases {
		fixtureByID[fixture.CaseID] = fixture
	}
	extraByID := make(map[string][]string, len(profile.Cases))
	for _, profileCase := range profile.Cases {
		extraByID[profileCase.CaseID] = profileCase.ExtraEventIDs
	}
	expected := make(map[string]struct{})
	for _, caseID := range caseIDs {
		fixture, ok := fixtureByID[caseID]
		if !ok {
			return nil, Selection{}, fmt.Errorf("preflight extraction eval source: fixture %q is missing", caseID)
		}
		caseEvents := supportingEventIDs(fixture)
		if len(caseEvents) == 0 {
			return nil, Selection{}, fmt.Errorf("preflight extraction eval source: case %q has no supporting event IDs", caseID)
		}
		for _, eventID := range append(caseEvents, extraByID[caseID]...) {
			if eventID != "" {
				expected[eventID] = struct{}{}
			}
		}
	}

	sourceEvents := 0
	found := make(map[string]struct{}, len(expected))
	for _, stream := range streams {
		sourceEvents += len(stream.Events)
		for _, event := range stream.Events {
			if _, wanted := expected[event.ID]; wanted {
				found[event.ID] = struct{}{}
			}
		}
	}
	missing := difference(expected, found)
	if len(missing) > 0 {
		return nil, Selection{}, fmt.Errorf("preflight extraction eval source: missing supporting events %v", missing)
	}

	selected := streams
	if !profile.FullSource {
		selected = windowStreams(streams, expected, profile.BeforeEvents, profile.AfterEvents)
	}
	selection := Selection{SourceEvents: sourceEvents, SelectedStreams: len(selected)}
	for _, stream := range selected {
		selection.SelectedEvents += len(stream.Events)
		selection.ExpectedSlices += (len(stream.Events) + eventLimit - 1) / eventLimit
		for _, event := range stream.Events {
			selection.EventIDs = append(selection.EventIDs, event.ID)
		}
	}
	if selection.ExpectedSlices > profile.MaxSlices {
		return nil, Selection{}, fmt.Errorf(
			"preflight extraction eval source: selected cohort needs %d slices, profile allows %d",
			selection.ExpectedSlices, profile.MaxSlices,
		)
	}
	return selected, selection, nil
}

func supportingEventIDs(fixture stageeval.Fixture) []string {
	seen := make(map[string]struct{})
	for _, atom := range append(append([]stageeval.Atom(nil), fixture.RequiredAtoms...), fixture.ForbiddenAtoms...) {
		for _, eventID := range atom.SupportingEventIDs {
			seen[eventID] = struct{}{}
		}
	}
	for _, eventID := range fixture.ForbiddenEventIDs {
		seen[eventID] = struct{}{}
	}
	result := make([]string, 0, len(seen))
	for eventID := range seen {
		result = append(result, eventID)
	}
	sort.Strings(result)
	return result
}

func difference(expected, found map[string]struct{}) []string {
	missing := make([]string, 0)
	for eventID := range expected {
		if _, ok := found[eventID]; !ok {
			missing = append(missing, eventID)
		}
	}
	sort.Strings(missing)
	return missing
}

func windowStreams(
	streams []extractionshadow.StreamEvents,
	seeds map[string]struct{},
	before int,
	after int,
) []extractionshadow.StreamEvents {
	selected := make([]extractionshadow.StreamEvents, 0, len(streams))
	for _, stream := range streams {
		indexes := make(map[int]struct{})
		for index, event := range stream.Events {
			if _, ok := seeds[event.ID]; !ok {
				continue
			}
			start := max(0, index-before)
			end := min(len(stream.Events)-1, index+after)
			for selectedIndex := start; selectedIndex <= end; selectedIndex++ {
				indexes[selectedIndex] = struct{}{}
			}
		}
		if len(indexes) == 0 {
			continue
		}
		copyStream := extractionshadow.StreamEvents{Actor: stream.Actor}
		for index, event := range stream.Events {
			if _, ok := indexes[index]; ok {
				copyStream.Events = append(copyStream.Events, event)
			}
		}
		selected = append(selected, copyStream)
	}
	return selected
}
