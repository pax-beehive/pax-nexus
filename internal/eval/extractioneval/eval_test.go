package extractioneval_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pax-beehive/pax-nexus/internal/eval/extractioneval"
	"github.com/pax-beehive/pax-nexus/internal/eval/extractionshadow"
	"github.com/pax-beehive/pax-nexus/internal/eval/stageeval"
	"github.com/pax-beehive/pax-nexus/internal/teamnote"
	"github.com/pax-beehive/pax-nexus/internal/teamnote/extractor"
	"github.com/stretchr/testify/suite"
)

type extractionEvalSuite struct{ suite.Suite }

func TestExtractionEvalSuite(t *testing.T) {
	t.Parallel()
	suite.Run(t, new(extractionEvalSuite))
}

func (s *extractionEvalSuite) TestPlanDomainsGroupsSharedScopes() {
	cases := []extractionshadow.ManifestCase{
		{ID: "case-a", ScopeID: "finance"},
		{ID: "case-b", ScopeID: "finance"},
		{ID: "case-c", ScopeID: "legal"},
	}

	plans, err := extractioneval.PlanDomains("source-run", "-hybrid", cases)
	s.Require().NoError(err)
	s.Require().Len(plans, 2)
	s.Equal("source-run-finance-hybrid", plans[0].ScopeID)
	s.Equal([]string{"case-a", "case-b"}, plans[0].CaseIDs)
	s.Equal("source-run-legal-hybrid", plans[1].ScopeID)
}

func (s *extractionEvalSuite) TestPlanDomainsValidation() {
	tests := []struct {
		name        string
		sourceRunID string
		cases       []extractionshadow.ManifestCase
	}{
		{name: "missing source run", cases: []extractionshadow.ManifestCase{{ID: "case-a", ScopeID: "finance"}}},
		{name: "missing cases", sourceRunID: "source-run"},
		{name: "missing scope", sourceRunID: "source-run", cases: []extractionshadow.ManifestCase{{ID: "case-a"}}},
		{name: "duplicate case", sourceRunID: "source-run", cases: []extractionshadow.ManifestCase{
			{ID: "case-a", ScopeID: "finance"}, {ID: "case-a", ScopeID: "legal"},
		}},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			_, err := extractioneval.PlanDomains(test.sourceRunID, "", test.cases)
			s.Require().Error(err)
		})
	}
}

func (s *extractionEvalSuite) TestBuildReportScoresCasesAndCountsDomainOnce() {
	fixtures := stageeval.FixtureSet{
		SchemaVersion: stageeval.SchemaVersion, Dataset: "GroupMemBench test",
		Cases: []stageeval.Fixture{
			fixture("case-a", "atom-a"),
			fixture("case-b", "atom-b"),
		},
	}
	plans := []extractioneval.DomainPlan{{ScopeID: "source-finance", CaseIDs: []string{"case-a", "case-b"}}}
	runs := []extractionshadow.CaseRun{{
		CaseID: "source-finance", ScopeID: "source-finance", Streams: 2, Events: 20,
		Notes:  []teamnote.Note{{ID: "note-1", Body: "ready", State: teamnote.StateActive}},
		Slices: []extractionshadow.SliceRecord{{DurationMS: 10, Usage: extractor.Usage{InputTokens: 100}}},
	}}

	report, err := extractioneval.BuildReport("eval-run", "source", extractor.ExtractionVersionV2, fixtures, plans, runs)
	s.Require().NoError(err)
	s.Equal(extractioneval.SchemaVersion, report.SchemaVersion)
	s.Equal(2, report.Summary.Cases)
	s.InDelta(1.0, report.Summary.FactRecall, 0.001)
	s.Equal(1, report.Telemetry.Calls)
	s.Equal(100, report.Telemetry.InputTokens)
	s.Require().Len(report.Domains, 1)
	s.Equal([]string{"case-a", "case-b"}, report.Domains[0].CaseIDs)
	s.Require().Len(report.Cases, 2)
	s.Equal("source-finance", report.Cases[0].ScopeID)
}

func (s *extractionEvalSuite) TestBuildReportPropagatesSuppressedLeakage() {
	fixtures := stageeval.FixtureSet{
		SchemaVersion: stageeval.SchemaVersion,
		Cases: []stageeval.Fixture{{
			CaseID: "case-a", SourceRevision: strings.Repeat("ab", 32),
			RecallContext:  stageeval.RecallContext{ConsumerUserID: "owner", Query: "q", TokenBudget: 10},
			ForbiddenAtoms: []stageeval.Atom{{ID: "compliance_owner", Patterns: []string{"(?i)compliance.{0,100}(designated|owner)"}}},
		}},
	}
	plans := []extractioneval.DomainPlan{{ScopeID: "source-finance", CaseIDs: []string{"case-a"}}}
	runs := []extractionshadow.CaseRun{{
		CaseID: "source-finance", ScopeID: "source-finance",
		Notes:  []teamnote.Note{{ID: "note-1", Body: "Compliance is not the designated owner.", State: teamnote.StateActive}},
		Slices: []extractionshadow.SliceRecord{{DurationMS: 10}},
	}}

	report, err := extractioneval.BuildReport("eval-run", "source", extractor.ExtractionVersionV2, fixtures, plans, runs)

	s.Require().NoError(err)
	s.Equal(1, report.Summary.LeakageItems)
	s.Equal(1, report.Summary.SuppressedLeakageItems)
	s.Require().Len(report.Cases, 1)
	s.Equal(1, report.Cases[0].Extraction.LeakageItems)
	s.Equal(1, report.Cases[0].Extraction.SuppressedLeakageItems)
	s.Equal([]stageeval.LeakageDetail{
		{ItemID: "note-1", AtomID: "compliance_owner", Suppressed: true, SuppressionCue: "not"},
	}, report.Cases[0].Extraction.LeakageDetails)
}

func (s *extractionEvalSuite) TestBuildReportValidation() {
	fixtures := stageeval.FixtureSet{
		SchemaVersion: stageeval.SchemaVersion,
		Cases:         []stageeval.Fixture{fixture("case-a", "atom")},
	}
	tests := []struct {
		name  string
		runID string
		plans []extractioneval.DomainPlan
		runs  []extractionshadow.CaseRun
	}{
		{
			name: "missing run id", plans: []extractioneval.DomainPlan{{ScopeID: "source-finance", CaseIDs: []string{"case-a"}}},
			runs: []extractionshadow.CaseRun{{ScopeID: "source-finance"}},
		},
		{
			name: "missing domain", runID: "eval-run",
			plans: []extractioneval.DomainPlan{{ScopeID: "source-finance", CaseIDs: []string{"case-a"}}},
		},
		{
			name: "duplicate domain run", runID: "eval-run",
			plans: []extractioneval.DomainPlan{{ScopeID: "source-finance", CaseIDs: []string{"case-a"}}},
			runs:  []extractionshadow.CaseRun{{ScopeID: "source-finance"}, {ScopeID: "source-finance"}},
		},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			_, err := extractioneval.BuildReport(
				test.runID, "source", extractor.ExtractionVersionV2, fixtures, test.plans, test.runs,
			)
			s.Require().Error(err)
		})
	}
}

func (s *extractionEvalSuite) TestFinanceMicro6FixtureIsValid() {
	path := filepath.Join("..", "..", "..", "evals", "extraction-v1", "groupmembench-finance-micro6.json")
	fixtures, err := stageeval.LoadFixtureSet(path)
	s.Require().NoError(err)
	s.Require().Len(fixtures.Cases, 6)
	plans := []extractioneval.DomainPlan{{ScopeID: "source-finance", CaseIDs: []string{
		"multi_hop_1", "knowledge_update_20", "temporal_7", "user_implicit_7", "term_ambiguity_11", "abstention_4",
	}}}
	runs := []extractionshadow.CaseRun{{ScopeID: "source-finance"}}

	_, err = extractioneval.BuildReport("fixture-check", "source", extractor.ExtractionVersionV2, fixtures, plans, runs)
	s.Require().NoError(err)
}

func (s *extractionEvalSuite) TestQuickProfileSelectsBoundedEvidenceWindows() {
	profile := extractioneval.Profile{
		SchemaVersion: extractioneval.ProfileSchemaVersion, Name: "quick", BeforeEvents: 1, AfterEvents: 1, MaxSlices: 3,
		Cases: []extractioneval.ProfileCase{{CaseID: "case-a", ExtraEventIDs: []string{"event-5"}}},
	}
	fixtures := stageeval.FixtureSet{SchemaVersion: stageeval.SchemaVersion, Cases: []stageeval.Fixture{{
		CaseID: "case-a", RequiredAtoms: []stageeval.Atom{{ID: "atom", SupportingEventIDs: []string{"event-2"}}},
	}}}
	actor := teamnote.Actor{UserID: "owner", AgentID: "agent", SessionID: "session"}
	stream := extractionshadow.StreamEvents{Actor: actor}
	for sequence := 1; sequence <= 6; sequence++ {
		stream.Events = append(stream.Events, teamnote.SessionEvent{ID: fmt.Sprintf("event-%d", sequence), Actor: actor, Sequence: int64(sequence)})
	}

	selected, report, err := extractioneval.PreflightAndSelectStreams(
		profile, []string{"case-a"}, fixtures, []extractionshadow.StreamEvents{stream}, 3,
	)

	s.Require().NoError(err)
	s.Require().Len(selected, 1)
	s.Equal([]string{"event-1", "event-2", "event-3", "event-4", "event-5", "event-6"}, report.EventIDs)
	s.Equal(6, report.SourceEvents)
	s.Equal(6, report.SelectedEvents)
	s.Equal(2, report.ExpectedSlices)
}

func (s *extractionEvalSuite) TestSourcePreflightFailsBeforePaidReplay() {
	tests := []struct {
		name       string
		fixture    stageeval.Fixture
		stream     extractionshadow.StreamEvents
		wantReason string
	}{
		{
			name: "case lacks support IDs", fixture: stageeval.Fixture{CaseID: "case-a"},
			wantReason: "has no supporting event IDs",
		},
		{
			name: "support event absent",
			fixture: stageeval.Fixture{CaseID: "case-a", RequiredAtoms: []stageeval.Atom{{
				ID: "atom", SupportingEventIDs: []string{"missing-event"},
			}}},
			stream:     extractionshadow.StreamEvents{Events: []teamnote.SessionEvent{{ID: "other-event"}}},
			wantReason: "missing supporting events",
		},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			profile := extractioneval.Profile{
				SchemaVersion: extractioneval.ProfileSchemaVersion, Name: "quick", MaxSlices: 1,
				Cases: []extractioneval.ProfileCase{{CaseID: "case-a"}},
			}
			_, _, err := extractioneval.PreflightAndSelectStreams(
				profile, []string{"case-a"}, stageeval.FixtureSet{Cases: []stageeval.Fixture{test.fixture}},
				[]extractionshadow.StreamEvents{test.stream}, 25,
			)
			s.Require().Error(err)
			s.Contains(err.Error(), test.wantReason)
		})
	}
}

func (s *extractionEvalSuite) TestLoadsTrackedQuickProfile() {
	path := filepath.Join("..", "..", "..", "evals", "extraction-v1", "profiles", "finance-micro3-quick.json")
	profile, err := extractioneval.LoadProfile(path)
	s.Require().NoError(err)
	s.Equal("finance-micro3-quick", profile.Name)
	s.Len(profile.Cases, 3)
	s.LessOrEqual(profile.MaxSlices, 6)
}

func (s *extractionEvalSuite) TestLoadProfileValidation() {
	tests := []struct {
		name string
		body string
	}{
		{name: "wrong schema", body: `{"schema_version":"wrong","name":"quick","max_slices":1,"cases":[{"case_id":"case-a"}]}`},
		{name: "missing cases", body: `{"schema_version":"pax-extraction-eval-v1-profile","name":"quick","max_slices":1}`},
		{name: "invalid windows", body: `{"schema_version":"pax-extraction-eval-v1-profile","name":"quick","before_events":-1,"max_slices":1,"cases":[{"case_id":"case-a"}]}`},
		{name: "duplicate case", body: `{"schema_version":"pax-extraction-eval-v1-profile","name":"quick","max_slices":1,"cases":[{"case_id":"case-a"},{"case_id":"case-a"}]}`},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			path := filepath.Join(s.T().TempDir(), "profile.json")
			s.Require().NoError(os.WriteFile(path, []byte(test.body), 0o600))
			_, err := extractioneval.LoadProfile(path)
			s.Require().Error(err)
		})
	}
}

func fixture(caseID, atomID string) stageeval.Fixture {
	return stageeval.Fixture{
		CaseID: caseID, SourceRevision: strings.Repeat("ab", 32),
		RecallContext: stageeval.RecallContext{ConsumerUserID: "owner", Query: "q", TokenBudget: 10},
		RequiredAtoms: []stageeval.Atom{{ID: atomID, Patterns: []string{"ready"}}},
	}
}
