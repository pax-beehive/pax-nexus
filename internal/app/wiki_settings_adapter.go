package app

import (
	"context"

	"github.com/pax-beehive/pax-nexus/internal/pagewiki"
)

// wikiSettingsAdapter satisfies handler.WikiSettings by resolving the
// per-scope pagewiki service on every call: pagewiki.Service's own methods
// stay scope-implicit (one Service per scope), so the scope param the
// handler-facing interface carries is resolved here, through the
// ServiceManager, rather than threaded into Service itself.
type wikiSettingsAdapter struct {
	services *pagewiki.ServiceManager
}

func (a wikiSettingsAdapter) GenerationSettings(
	ctx context.Context, scopeID string,
) (pagewiki.GenerationDirectives, error) {
	service, err := a.services.ForScope(ctx, scopeID)
	if err != nil {
		return pagewiki.GenerationDirectives{}, err
	}
	return service.GenerationSettings(ctx)
}

func (a wikiSettingsAdapter) SetGenerationSettings(
	ctx context.Context, scopeID string, directives pagewiki.GenerationDirectives,
) (pagewiki.GenerationDirectives, error) {
	service, err := a.services.ForScope(ctx, scopeID)
	if err != nil {
		return pagewiki.GenerationDirectives{}, err
	}
	return service.SetGenerationSettings(ctx, directives)
}
