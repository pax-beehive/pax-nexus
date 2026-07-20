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

func (s *shadowSuite) TestRunCaseResumeTransitions() {
	first := s.event("event-1", 1)
	// Resolve the deterministic checksum once, then use it as a completed
	// journal record for the resume assertion.
	firstRun, err := extractionshadow.RunCase(
		context.Background(), "case-a", "shadow-scope",
		[]extractionshadow.StreamEvents{{Actor: s.actor, Events: []teamnote.SessionEvent{first}}},
		&stubExtractor{result: extractor.Result{}},
	)
	s.Require().NoError(err)
	s.Require().Len(firstRun.Slices, 1)
	tests := []struct {
		name          string
		checksum      string
		wantAppended  int
		wantSlices    int
		wantFirstCall int
	}{
		{name: "completed slice is replayed", checksum: firstRun.Slices[0].InputChecksum, wantSlices: 1, wantFirstCall: 99},
		{name: "different slice continues", checksum: "different-checksum", wantAppended: 1, wantSlices: 2, wantFirstCall: 99},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			var appended []extractionshadow.SliceRecord
			initial := extractionshadow.SliceRecord{
				InputChecksum: test.checksum, SessionID: s.actor.SessionID,
				Usage: extractor.Usage{InputTokens: 99}, DurationMS: 1234,
			}
			resumed, runErr := extractionshadow.RunCaseWithOptions(
				context.Background(), "case-a", "shadow-scope",
				[]extractionshadow.StreamEvents{{Actor: s.actor, Events: []teamnote.SessionEvent{first}}},
				&stubExtractor{result: extractor.Result{}}, extractionshadow.RunOptions{
					InitialSlices: []extractionshadow.SliceRecord{initial},
					OnSlice: func(record extractionshadow.SliceRecord) error {
						appended = append(appended, record)
						return nil
					},
				},
			)
			s.Require().NoError(runErr)
			s.Len(appended, test.wantAppended)
			s.Require().Len(resumed.Slices, test.wantSlices)
			s.Equal(test.wantFirstCall, resumed.Slices[0].Usage.InputTokens)
			s.Equal(int64(1234), resumed.Slices[0].DurationMS)
		})
	}
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

func (s *shadowSuite) TestBuildReportScoresTemporalWindowFacts() {
	fixtures := stageeval.FixtureSet{
		SchemaVersion: stageeval.SchemaVersion, Dataset: "shadow-test",
		Cases: []stageeval.Fixture{{
			CaseID: "case-a", SourceRevision: strings.Repeat("ab", 32),
			RecallContext: stageeval.RecallContext{ConsumerUserID: "owner", Query: "deadline", TokenBudget: 100},
			RequiredAtoms: []stageeval.Atom{{ID: "deadline", Patterns: []string{"(?i)reporting.{0,80}(2025-07-18|july 18)"}}},
		}},
	}
	first := s.event("event-1", 1)
	validAt := time.Date(2025, time.July, 18, 23, 59, 59, 0, time.UTC)
	stub := &stubExtractor{result: extractor.Result{Candidates: []teamnote.Candidate{{
		ID: "candidate-1", Action: teamnote.ActionCreate, Kind: teamnote.KindStatus,
		Subject: "reporting validation", Body: "Reporting validates fit against the standard.", TaskRef: "release-42",
		Origin: s.actor, EvidenceEventIDs: []string{first.ID}, ValidAt: &validAt,
	}}}}
	run, err := extractionshadow.RunCase(context.Background(), "case-a", "shadow-scope",
		[]extractionshadow.StreamEvents{{Actor: s.actor, Events: []teamnote.SessionEvent{first}}}, stub)
	s.Require().NoError(err)

	report, err := extractionshadow.BuildReport("run-1", "team_note", extractor.ExtractionVersionV1, fixtures, []extractionshadow.CaseRun{run})
	s.Require().NoError(err)
	s.InDelta(1.0, report.Stage.ExtractionFactRecall, 0.001)
}

func (s *shadowSuite) TestBuildReportValidation() {
	fixtures := stageeval.FixtureSet{
		SchemaVersion: stageeval.SchemaVersion,
		Cases: []stageeval.Fixture{{
			CaseID: "case-missing", SourceRevision: strings.Repeat("cd", 32),
			RecallContext: stageeval.RecallContext{ConsumerUserID: "owner", Query: "q", TokenBudget: 10},
			RequiredAtoms: []stageeval.Atom{{ID: "atom", Patterns: []string{"x"}}},
		}},
	}
	tests := []struct {
		name string
		runs []extractionshadow.CaseRun
	}{
		{name: "requires a case run"},
		{name: "requires every fixture case", runs: []extractionshadow.CaseRun{{CaseID: "case-other"}}},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			_, err := extractionshadow.BuildReport(
				"run-1", "team_note", extractor.ExtractionVersionV2, fixtures, test.runs,
			)
			s.Require().Error(err)
		})
	}
}

func (s *shadowSuite) TestBuildReportCountsSharedScopeTelemetryOnce() {
	fixtures := stageeval.FixtureSet{
		SchemaVersion: stageeval.SchemaVersion,
		Cases: []stageeval.Fixture{
			{
				CaseID: "case-a", SourceRevision: strings.Repeat("ab", 32),
				RecallContext: stageeval.RecallContext{ConsumerUserID: "owner", Query: "q", TokenBudget: 10},
				RequiredAtoms: []stageeval.Atom{{ID: "atom-a", Patterns: []string{"ready"}}},
			},
			{
				CaseID: "case-b", SourceRevision: strings.Repeat("cd", 32),
				RecallContext: stageeval.RecallContext{ConsumerUserID: "owner", Query: "q", TokenBudget: 10},
				RequiredAtoms: []stageeval.Atom{{ID: "atom-b", Patterns: []string{"ready"}}},
			},
		},
	}
	slices := []extractionshadow.SliceRecord{{DurationMS: 100, Usage: extractor.Usage{InputTokens: 20}}}
	notes := []teamnote.Note{{ID: "note-1", Body: "ready", State: teamnote.StateActive}}
	runs := []extractionshadow.CaseRun{
		{CaseID: "case-a", ScopeID: "shared-scope", Notes: notes, Slices: slices},
		{CaseID: "case-b", ScopeID: "shared-scope", Notes: notes, Slices: slices},
	}

	report, err := extractionshadow.BuildReport("run-1", "source", extractor.ExtractionVersionV2, fixtures, runs)
	s.Require().NoError(err)
	s.Equal(1, report.Telemetry.Calls)
	s.Equal(20, report.Telemetry.InputTokens)
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
		ProviderCalls: []extractor.ProviderCall{
			{Type: extractor.ProviderCallPrimary, Usage: extractor.Usage{InputTokens: 80, OutputTokens: 10}, DurationMS: 900},
			{Type: extractor.ProviderCallSummary, Usage: extractor.Usage{InputTokens: 20, OutputTokens: 5}, DurationMS: 100},
		},
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
	s.Equal(2, report.Telemetry.ProviderCalls)
	s.Equal(1, report.Telemetry.ProviderCallTypes[string(extractor.ProviderCallPrimary)].Calls)
	s.Equal(80, report.Telemetry.ProviderCallTypes[string(extractor.ProviderCallPrimary)].InputTokens)
	s.Equal(1, report.Telemetry.ProviderCallTypes[string(extractor.ProviderCallSummary)].Calls)
}

func (s *shadowSuite) TestTelemetryClassifiesProviderExecutionAttempts() {
	fixtures := stageeval.FixtureSet{
		SchemaVersion: stageeval.SchemaVersion,
		Cases: []stageeval.Fixture{{
			CaseID: "case-a", SourceRevision: strings.Repeat("ab", 32),
			RecallContext: stageeval.RecallContext{ConsumerUserID: "owner", Query: "q", TokenBudget: 10},
			RequiredAtoms: []stageeval.Atom{{ID: "atom", Patterns: []string{"ready"}}},
		}},
	}
	run := extractionshadow.CaseRun{
		CaseID: "case-a", ScopeID: "scope", Notes: []teamnote.Note{{ID: "note", Body: "ready"}},
		ProviderCalls: []extractor.ProviderCall{
			{Type: extractor.ProviderCallPrimary, Attempt: 1, Error: "deadline", FailureClass: extractor.ProviderFailureDeadline},
			{Type: extractor.ProviderCallPrimary, Attempt: 2, Error: "invalid", FailureClass: extractor.ProviderFailureInvalidResponse},
		},
	}

	report, err := extractionshadow.BuildReport("run", "arm", "v2", fixtures, []extractionshadow.CaseRun{run})

	s.Require().NoError(err)
	s.Equal(1, report.Telemetry.ProviderRetryAttempts)
	s.Equal(1, report.Telemetry.ProviderTimeouts)
	s.Equal(1, report.Telemetry.ProviderInvalidResponses)
	s.InDelta(0.5, report.Telemetry.ProviderRetryRate, 0.0001)
	s.InDelta(0.5, report.Telemetry.ProviderTimeoutRate, 0.0001)
	s.InDelta(0.5, report.Telemetry.ProviderInvalidResponseRate, 0.0001)
}

func (s *shadowSuite) TestBuildReportAttributesFirstExtractionLossPerAtom() {
	fixtures := stageeval.FixtureSet{
		SchemaVersion: stageeval.SchemaVersion,
		Cases: []stageeval.Fixture{{
			CaseID: "case-a", SourceRevision: strings.Repeat("cd", 32),
			RecallContext: stageeval.RecallContext{ConsumerUserID: "owner", Query: "q", TokenBudget: 10},
			RequiredAtoms: []stageeval.Atom{
				{ID: "owner", Patterns: []string{"Compliance owns"}, SupportingEventIDs: []string{"event-1"}},
				{ID: "deadline", Patterns: []string{"Friday"}, SupportingEventIDs: []string{"event-2"}},
			},
		}},
	}
	run := extractionshadow.CaseRun{
		CaseID: "case-a", Notes: []teamnote.Note{{ID: "owner", Body: "Compliance owns", EvidenceEventIDs: []string{"event-1"}}},
		Slices: []extractionshadow.SliceRecord{{
			NewEventIDs: []string{"event-1", "event-2"},
			Trace: &extractor.TraceV2{
				StateDecisions: []extractor.StateDecision{{
					Decision: extractor.DecisionCreate, EvidenceEventIDs: []string{"event-1"},
				}},
				UnreviewedEventIDs: []string{"event-2"},
			},
		}},
	}

	report, err := extractionshadow.BuildReport("run", "arm", "v2", fixtures, []extractionshadow.CaseRun{run})

	s.Require().NoError(err)
	s.Require().Len(report.LossLedger, 2)
	s.True(report.LossLedger[0].Matched)
	s.Empty(report.LossLedger[0].LostAt)
	s.Equal(extractionshadow.ExtractionLossEventReview, report.LossLedger[1].LostAt)
	s.Equal("supporting_event_unreviewed", report.LossLedger[1].Reason)
	s.False(report.LossLedger[1].Reviewed)
}

func (s *shadowSuite) TestLoadManifestCases() {
	tests := []struct {
		name      string
		content   string
		wantErr   bool
		wantCase  string
		wantScope string
	}{
		{
			name: "parses cases", content: `{"cases":[{"id":"case-a","category":"multi_hop","scope_id":"groupmembench-case-a"}]}`,
			wantCase: "case-a", wantScope: "groupmembench-case-a",
		},
		{name: "rejects empty cases", content: `{"cases":[]}`, wantErr: true},
		{name: "rejects invalid json", content: `{"cases":`, wantErr: true},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			path := s.T().TempDir() + "/manifest.json"
			s.Require().NoError(writeFile(path, test.content))
			cases, err := extractionshadow.LoadManifestCases(path)
			if test.wantErr {
				s.Require().Error(err)
				return
			}
			s.Require().NoError(err)
			s.Require().Len(cases, 1)
			s.Equal(test.wantCase, cases[0].ID)
			s.Equal(test.wantScope, cases[0].ScopeID)
		})
	}
}

func (s *shadowSuite) TestFileEpisodeStoreResumesSavedResponses() {
	path := s.T().TempDir() + "/resume/episodes.json"
	store, err := extractionshadow.OpenFileEpisodeStore(path, false)
	s.Require().NoError(err)
	key := extractor.EpisodeKey{ScopeID: "scope", TaskRef: "task"}
	episode := extractor.Episode{
		Key: key, Model: "model", PromptVersion: "v2",
		Runs: map[string]extractor.EpisodeRun{"checksum": {Response: `{"state_decisions":[],"claims":[]}`}},
	}
	var resumed *extractionshadow.FileEpisodeStore
	var loaded extractor.Episode
	steps := []struct {
		name    string
		action  func() error
		wantErr error
		check   func()
	}{
		{name: "save response", action: func() error {
			return store.SaveEpisode(context.Background(), episode, 0)
		}},
		{name: "reopen for resume", action: func() error {
			var openErr error
			resumed, openErr = extractionshadow.OpenFileEpisodeStore(path, true)
			return openErr
		}},
		{name: "load saved response", action: func() error {
			var ok bool
			var loadErr error
			loaded, ok, loadErr = resumed.LoadEpisode(context.Background(), key)
			if loadErr == nil {
				s.True(ok)
			}
			return loadErr
		}, check: func() {
			s.Equal(int64(1), loaded.Version)
			s.Equal(episode.Runs["checksum"], loaded.Runs["checksum"])
		}},
		{name: "reject stale version", action: func() error {
			return resumed.SaveEpisode(context.Background(), loaded, 0)
		}, wantErr: extractor.ErrEpisodeConflict},
	}
	for _, step := range steps {
		s.Run(step.name, func() {
			stepErr := step.action()
			if step.wantErr != nil {
				s.ErrorIs(stepErr, step.wantErr)
				return
			}
			s.Require().NoError(stepErr)
			if step.check != nil {
				step.check()
			}
		})
	}
}

func writeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o644)
}
