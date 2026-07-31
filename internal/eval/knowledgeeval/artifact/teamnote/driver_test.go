package teamnote

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/eval/knowledgeeval"
	"github.com/pax-beehive/pax-nexus/internal/session"
	domain "github.com/pax-beehive/pax-nexus/internal/teamnote"
	"github.com/stretchr/testify/suite"
)

type DriverSuite struct {
	suite.Suite
	ctx    context.Context
	store  *knowledgeeval.ArtifactStore
	driver *Driver
	group  knowledgeeval.BenchmarkGroup
}

func TestDriverSuite(t *testing.T) {
	suite.Run(t, new(DriverSuite))
}

func (s *DriverSuite) SetupTest() {
	s.ctx = context.Background()
	var err error
	s.store, err = knowledgeeval.NewArtifactStore(s.T().TempDir())
	s.Require().NoError(err)
	s.driver, err = NewDriver(s.store, func() time.Time {
		return time.Date(2026, 7, 30, 9, 0, 0, 0, time.UTC)
	})
	s.Require().NoError(err)
	s.group = knowledgeeval.BenchmarkGroup{
		GroupID: "group", WorldID: "world", CheckpointID: "checkpoint",
	}
}

func (s *DriverSuite) TestPublishesRecallsAndRenders() {
	artifact, err := s.driver.Publish(s.ctx, validSnapshot(), s.group, knowledgeeval.Provenance{})
	s.Require().NoError(err)
	opened, err := s.driver.Open(s.ctx, artifact)
	s.Require().NoError(err)
	s.Equal(artifact.ArtifactID, opened.ID())
	s.Len(opened.Capabilities(), 2)
	recaller, ok := opened.(knowledgeeval.PassiveRecaller)
	s.Require().True(ok)
	response, err := recaller.Recall(
		s.ctx,
		knowledgeeval.PassiveRecallRequest{
			Query: "evaluation blocked", MaxItems: 1, TokenBudget: 100,
		},
	)
	s.Require().NoError(err)
	s.Require().Len(response.Items, 1)
	s.Equal("note-1", response.Items[0].Ref)
	s.Contains(string(response.Trace), "selected_set")
	for _, kind := range []string{"native", "raw"} {
		view, err := s.driver.RenderView(s.ctx, knowledgeeval.ArtifactViewRequest{
			Artifact: artifact, Kind: kind,
		})
		s.Require().NoError(err)
		content, err := s.store.OpenBytes(s.ctx, view.Payload)
		s.Require().NoError(err)
		s.Contains(string(content), "Evaluation")
	}
}

func (s *DriverSuite) TestRejectsInvalidDataAndRequests() {
	_, err := NewDriver(nil, nil)
	s.Require().ErrorIs(err, knowledgeeval.ErrInvalidRecord)
	_, err = s.driver.Publish(s.ctx, Snapshot{}, s.group, knowledgeeval.Provenance{})
	s.Require().ErrorIs(err, knowledgeeval.ErrInvalidRecord)
	duplicate := validSnapshot()
	duplicate.Notes = append(duplicate.Notes, duplicate.Notes[0])
	_, err = s.driver.Publish(s.ctx, duplicate, s.group, knowledgeeval.Provenance{})
	s.Require().ErrorIs(err, knowledgeeval.ErrInvalidRecord)

	artifact, err := s.driver.Publish(s.ctx, validSnapshot(), s.group, knowledgeeval.Provenance{})
	s.Require().NoError(err)
	opened, err := s.driver.Open(s.ctx, artifact)
	s.Require().NoError(err)
	recaller, ok := opened.(knowledgeeval.PassiveRecaller)
	s.Require().True(ok)
	_, err = recaller.Recall(s.ctx, knowledgeeval.PassiveRecallRequest{})
	s.Require().ErrorIs(err, knowledgeeval.ErrInvalidRecord)
	_, err = s.driver.RenderView(s.ctx, knowledgeeval.ArtifactViewRequest{
		Artifact: artifact, Kind: "diff",
	})
	s.Require().ErrorIs(err, knowledgeeval.ErrInvalidRecord)
	artifact.Kind = "other"
	_, err = s.driver.Open(s.ctx, artifact)
	s.Require().ErrorIs(err, knowledgeeval.ErrInvalidRecord)
}

func (s *DriverSuite) TestWrapsProductionRuntime() {
	runtime := &fakeRuntime{envelope: domain.NoteEnvelope{
		Items: []string{"Fallback item"},
		Decision: domain.RecallDecisionSummary{
			EvidenceSufficient: true,
		},
	}}
	subject, err := NewRuntimeSubject(
		"runtime",
		runtime,
		session.Actor{AgentID: "agent"},
	)
	s.Require().NoError(err)
	s.Equal("runtime", subject.ID())
	s.Len(subject.Capabilities(), 1)
	response, err := subject.Recall(s.ctx, knowledgeeval.PassiveRecallRequest{
		Query: "evaluation", MaxItems: 2, TokenBudget: 100,
	})
	s.Require().NoError(err)
	s.Require().Len(response.Items, 1)
	s.Equal("Fallback item", response.Items[0].Text)
	s.Equal("evaluation", runtime.request.Query)

	runtime.err = errors.New("runtime unavailable")
	_, err = subject.Recall(s.ctx, knowledgeeval.PassiveRecallRequest{Query: "q"})
	s.Require().ErrorContains(err, "runtime unavailable")
	_, err = NewRuntimeSubject("", runtime, session.Actor{})
	s.Require().ErrorIs(err, knowledgeeval.ErrInvalidRecord)
}

func validSnapshot() Snapshot {
	return Snapshot{Notes: []domain.Note{
		{
			ID: "note-1", Kind: domain.KindBlocker, Subject: "Evaluation",
			Body: "Evaluation is blocked on fixtures.", State: domain.StateActive,
		},
		{
			ID: "note-2", Kind: domain.KindStatus, Subject: "Dashboard",
			Body: "The dashboard is deployed.", State: domain.StateResolved,
		},
	}}
}

type fakeRuntime struct {
	envelope domain.NoteEnvelope
	request  domain.RecallRequest
	err      error
}

func (r *fakeRuntime) ObserveSession(
	context.Context,
	domain.SessionBatch,
) (domain.IngestReceipt, error) {
	return domain.IngestReceipt{}, nil
}
func (r *fakeRuntime) ObserveStream(
	context.Context,
	domain.StreamBatch,
) (domain.IngestReceipt, error) {
	return domain.IngestReceipt{}, nil
}
func (r *fakeRuntime) RecallNotes(
	_ context.Context,
	request domain.RecallRequest,
) (domain.NoteEnvelope, error) {
	r.request = request
	return r.envelope, r.err
}
