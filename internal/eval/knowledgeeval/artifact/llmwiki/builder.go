package llmwiki

import (
	"context"
	"fmt"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/eval/knowledgeeval"
)

type DirectoryBuilder struct {
	store        *knowledgeeval.ArtifactStore
	now          func() time.Time
	codeRevision string
}

func NewDirectoryBuilder(
	store *knowledgeeval.ArtifactStore,
	now func() time.Time,
	codeRevision string,
) (*DirectoryBuilder, error) {
	if store == nil {
		return nil, fmt.Errorf("%w: artifact store is required", knowledgeeval.ErrInvalidRecord)
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &DirectoryBuilder{store: store, now: now, codeRevision: codeRevision}, nil
}

func (b *DirectoryBuilder) Descriptor() knowledgeeval.BuilderDescriptor {
	return knowledgeeval.BuilderDescriptor{ID: "llmwiki-directory", Version: "v1"}
}

func (b *DirectoryBuilder) Build(
	ctx context.Context,
	request knowledgeeval.BuildRequest,
) (knowledgeeval.ArtifactRecord, error) {
	root, err := b.store.OpenDirectory(ctx, request.Group.BuildInput)
	if err != nil {
		return knowledgeeval.ArtifactRecord{}, fmt.Errorf("open LLM Wiki build input: %w", err)
	}
	payload, err := b.store.PutDirectory(ctx, ArtifactKind, ArtifactSchema, root)
	if err != nil {
		return knowledgeeval.ArtifactRecord{}, fmt.Errorf("publish LLM Wiki artifact: %w", err)
	}
	baseID := ""
	if request.BaseArtifact != nil {
		baseID = request.BaseArtifact.ArtifactID
	}
	descriptor := b.Descriptor()
	record := knowledgeeval.ArtifactRecord{
		ArtifactID: stableID(
			request.Group.WorldID,
			request.Group.GroupID,
			request.Group.CheckpointID,
			payload.SHA256,
		),
		Kind: ArtifactKind, WorldID: request.Group.WorldID,
		GroupID: request.Group.GroupID, CheckpointID: request.Group.CheckpointID,
		BaseID: baseID, Payload: payload, CreatedAt: b.now(),
		Provenance: knowledgeeval.Provenance{
			BuilderID: descriptor.ID, BuilderVersion: descriptor.Version,
			CodeRevision: b.codeRevision,
		},
	}
	if err := record.Validate(); err != nil {
		return knowledgeeval.ArtifactRecord{}, err
	}
	return record, nil
}
