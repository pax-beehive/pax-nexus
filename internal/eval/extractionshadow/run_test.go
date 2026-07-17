package extractionshadow_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/eval/extractionshadow"
	"github.com/pax-beehive/pax-nexus/internal/eval/stageeval"
	"github.com/pax-beehive/pax-nexus/internal/sessionlake"
	"github.com/pax-beehive/pax-nexus/internal/teamnote"
	"github.com/pax-beehive/pax-nexus/internal/teamnote/extractor"
	"github.com/stretchr/testify/suite"
)

type shadowSuite struct {
	suite.Suite
	actor teamnote.Actor
}

func TestShadowSuite(t *testing.T) {
	t.Parallel()
	suite.Run(t, new(shadowSuite))
}

func (s *shadowSuite) SetupTest() {
	s.actor = teamnote.Actor{UserID: "owner", AgentID: "producer", SessionID: "producer-session"}
}

type stubExtractor struct {
	result extractor.Result
	calls  int
}

func (e *stubExtractor) Extract(_ context.Context, _ sessionlake.Slice) (extractor.Result, error) {
	e.calls++
	return e.result, nil
}

func (s *shadowSuite) event(id string, sequence int64) teamnote.SessionEvent {
	return teamnote.SessionEvent{
		ID: id, Actor: s.actor, Sequence: sequence, Type: "assistant", Content: "The release is ready.",
		TaskRef: "release-42", Visibility: "team_note_eligible", OccurredAt: time.Date(2026, time.July, 16, 12, 0, 0, 0, time.UTC),
	}
}

func (s *shadowSuite) TestRunCaseAdmitsCandidatesThroughRealPipeline() {
	first := s.event("event-1", 1)
	second := s.event("event-2", 2)
	stub := &stubExtractor{result: extractor.Result{Candidates: []teamnote.Candidate{{
		ID: "candidate-1", Action: teamnote.ActionCreate, Kind: teamnote.KindStatus,
		Subject: "release", Body: "The release is ready.", TaskRef: "release-42",
		Origin: s.actor, EvidenceEventIDs: []string{first.ID},
	}}}}
	run, err := extractionshadow.RunCase(context.Background(), "case-a", "shadow-scope",
		[]extractionshadow.StreamEvents{{Actor: s.actor, Events: []teamnote.SessionEvent{first, second}}}, stub)
	s.Require().NoError(err)
	s.Equal(1, run.Streams)
	s.Equal(2, run.Events)
	s.Require().NotEmpty(run.Slices)
	s.Require().Len(run.Notes, 1)
	s.Equal("The release is ready.", run.Notes[0].Body)
	s.Equal([]string{first.ID}, run.Notes[0].EvidenceEventIDs)
}

func (s *shadowSuite) TestBuildReportScoresAgainstStageFixtures() {
	fixtures := stageeval.FixtureSet{
		SchemaVersion: stageeval.SchemaVersion, Dataset: "shadow-test",
		Cases: []stageeval.Fixture{{
			CaseID: "case-a", SourceRevision: strings.Repeat("ab", 32),
			RecallContext: stageeval.RecallContext{ConsumerUserID: "owner", Query: "release state", TokenBudget: 100},
			RequiredAtoms: []stageeval.Atom{{ID: "release_ready", Patterns: []string{"(?i)release is ready"}}},
		}},
	}
	first := s.event("event-1", 1)
	stub := &stubExtractor{result: extractor.Result{Candidates: []teamnote.Candidate{{
		ID: "candidate-1", Action: teamnote.ActionCreate, Kind: teamnote.KindStatus,
		Subject: "release", Body: "The release is ready.", TaskRef: "release-42",
		Origin: s.actor, EvidenceEventIDs: []string{first.ID},
	}}}}
	run, err := extractionshadow.RunCase(context.Background(), "case-a", "shadow-scope",
		[]extractionshadow.StreamEvents{{Actor: s.actor, Events: []teamnote.SessionEvent{first}}}, stub)
	s.Require().NoError(err)

	report, err := extractionshadow.BuildReport("run-1", "team_note", extractor.ExtractionVersionV1, fixtures, []extractionshadow.CaseRun{run})
	s.Require().NoError(err)
	s.Equal(1, report.Stage.Cases)
	s.InDelta(1.0, report.Stage.ExtractionFactRecall, 0.001)
	s.Equal(0, report.Stage.ExtractionLeakageItems)
	s.Equal(run.Events, report.Cases[0].Events)
	s.GreaterOrEqual(report.Telemetry.Calls, 1)
}

func (s *shadowSuite) TestBuildReportRequiresEveryFixtureCase() {
	fixtures := stageeval.FixtureSet{
		SchemaVersion: stageeval.SchemaVersion,
		Cases: []stageeval.Fixture{{
			CaseID: "case-missing", SourceRevision: strings.Repeat("cd", 32),
			RecallContext: stageeval.RecallContext{ConsumerUserID: "owner", Query: "q", TokenBudget: 10},
			RequiredAtoms: []stageeval.Atom{{ID: "atom", Patterns: []string{"x"}}},
		}},
	}
	_, err := extractionshadow.BuildReport("run-1", "team_note", extractor.ExtractionVersionV2, fixtures,
		[]extractionshadow.CaseRun{{CaseID: "case-other"}})
	s.Require().Error(err)
	s.Contains(err.Error(), "case-missing")
}

func (s *shadowSuite) TestTelemetryAggregatesV2Products() {
	fixtures := stageeval.FixtureSet{
		SchemaVersion: stageeval.SchemaVersion,
		Cases: []stageeval.Fixture{{
			CaseID: "case-a", SourceRevision: strings.Repeat("ef", 32),
			RecallContext: stageeval.RecallContext{ConsumerUserID: "owner", Query: "q", TokenBudget: 10},
			RequiredAtoms: []stageeval.Atom{{ID: "atom", Patterns: []string{"(?i)ready"}}},
		}},
	}
	run := extractionshadow.CaseRun{
		CaseID: "case-a", Streams: 1, Events: 2,
		Notes: []teamnote.Note{{ID: "note-1", Body: "ready", State: teamnote.StateActive}},
		Slices: []extractionshadow.SliceRecord{{
			Events: 2, Candidates: 1, Claims: 2, StateDecisions: 1,
			NoStateEvents: 1, UnreviewedEvents: 1, InvalidNoStateEvents: 1, OrphanClaims: 1,
			WouldVerify: []string{"conflicting_reports"},
			Usage:       extractor.Usage{InputTokens: 100, OutputTokens: 20, PromptCacheHitTokens: 60, PromptCacheMissTokens: 40},
			DurationMS:  1000,
		}},
	}
	report, err := extractionshadow.BuildReport("run-1", "team_note", extractor.ExtractionVersionV2, fixtures, []extractionshadow.CaseRun{run})
	s.Require().NoError(err)
	s.Equal(2, report.Telemetry.Claims)
	s.Equal(1, report.Telemetry.StateDecisions)
	s.Equal(1, report.Telemetry.NoStateEvents)
	s.Equal(1, report.Telemetry.UnreviewedEvents)
	s.Equal(1, report.Telemetry.SlicesWithUnreviewedEvents)
	s.Equal(1, report.Telemetry.InvalidNoStateEvents)
	s.Equal(1, report.Telemetry.OrphanClaims)
	s.Equal(1, report.Telemetry.WouldVerifySlices)
	s.Equal(1, report.Telemetry.WouldVerifyDist["conflicting_reports"])
	s.InDelta(0.6, report.Telemetry.CacheHitRate, 0.001)
	s.Equal(int64(1000), report.Telemetry.P95DurationMS)
}

func (s *shadowSuite) TestLoadManifestCases() {
	s.Run("parses cases", func() {
		path := s.T().TempDir() + "/manifest.json"
		content := `{"cases":[{"id":"case-a","category":"multi_hop","scope_id":"groupmembench-case-a"}]}`
		s.Require().NoError(writeFile(path, content))
		cases, err := extractionshadow.LoadManifestCases(path)
		s.Require().NoError(err)
		s.Require().Len(cases, 1)
		s.Equal("case-a", cases[0].ID)
		s.Equal("groupmembench-case-a", cases[0].ScopeID)
	})
	s.Run("rejects empty cases", func() {
		path := s.T().TempDir() + "/manifest.json"
		s.Require().NoError(writeFile(path, `{"cases":[]}`))
		_, err := extractionshadow.LoadManifestCases(path)
		s.Require().Error(err)
	})
}

func writeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o644)
}
