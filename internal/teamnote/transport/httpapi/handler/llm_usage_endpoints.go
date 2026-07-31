package handler

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/pax-beehive/pax-nexus/internal/deployment/onprem"
	api "github.com/pax-beehive/pax-nexus/internal/teamnote/transport/httpapi/model/teammemory/api"
)

func (h *Handler) GetLlmUsage(ctx context.Context, c *app.RequestContext) {
	if h.llmUsage == nil {
		writeHumanAPIError(c, consts.StatusNotImplemented, "not_configured", "LLM usage is not configured")
		return
	}
	if _, ok := h.authorizeHumanMember(ctx, c, false); !ok {
		return
	}
	days, err := queryDays(c)
	if err != nil {
		writeHumanAPIError(c, consts.StatusBadRequest, "invalid_request", "the request is invalid")
		return
	}
	rows, err := h.llmUsage.UsageSummary(ctx, onprem.LocalScopeID, time.Duration(days)*24*time.Hour)
	if err != nil {
		h.logger.Error("get LLM usage", "error", err)
		writeHumanAPIError(c, consts.StatusInternalServerError, "internal_error", "the request could not be completed")
		return
	}
	response := &api.LlmUsageResponse{Rows: make([]*api.LlmUsageRow, 0, len(rows))}
	for _, row := range rows {
		response.Rows = append(response.Rows, &api.LlmUsageRow{
			Component: row.Component, Model: row.Model, Calls: row.Calls,
			InputTokens: row.InputTokens, CacheHitTokens: row.CacheHitTokens,
			CacheMissTokens: row.CacheMissTokens, OutputTokens: row.OutputTokens,
		})
	}
	c.JSON(consts.StatusOK, response)
}

// queryDays parses the "days" query parameter, defaulting to 7 when absent
// and rejecting values outside the valid 1..365 range.
func queryDays(c *app.RequestContext) (int, error) {
	raw := strings.TrimSpace(c.Query("days"))
	if raw == "" {
		return 7, nil
	}
	days, err := strconv.Atoi(raw)
	if err != nil || days < 1 || days > 365 {
		return 0, errors.New("days must be between 1 and 365")
	}
	return days, nil
}
