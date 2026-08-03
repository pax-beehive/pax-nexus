package handler_test

import (
	"errors"
	"net/http"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/common/config"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/cloudwego/hertz/pkg/route"
	"github.com/pax-beehive/pax-nexus/internal/deployment/onprem"
	"github.com/pax-beehive/pax-nexus/internal/operations"
)

// performWithQuery drives a handler function with an optional query string,
// mirroring the package's perform helper.
func performWithQuery(handlerFunction app.HandlerFunc, method, query, apiKey string) *ut.ResponseRecorder {
	engine := route.NewEngine(config.NewOptions([]config.Option{}))
	engine.Handle(method, "/", handlerFunction)
	headers := []ut.Header{{Key: "Content-Type", Value: "application/json"}}
	if apiKey != "" {
		headers = append(headers, ut.Header{Key: "Authorization", Value: "Bearer " + apiKey})
	}
	return ut.PerformRequest(engine, method, "/"+query, nil, headers...)
}

// TestListChannelEnvelopesDirectionSwapsRequiredPermission pins the
// direction=sent permission swap: listing sent envelopes requires
// channel_send, while the default (received) listing requires
// channel_receive.
func (s *onPremHandlerSuite) TestListChannelEnvelopesDirectionSwapsRequiredPermission() {
	tests := []struct {
		name       string
		query      string
		apiKey     string
		wantStatus int
	}{
		{name: "direction sent with receive-only credential is forbidden",
			query: "?direction=sent", apiKey: "channel-receive-only", wantStatus: consts.StatusForbidden},
		{name: "direction sent with send-only credential succeeds",
			query: "?direction=sent", apiKey: "channel-send-only", wantStatus: consts.StatusOK},
		{name: "default direction with send-only credential is forbidden",
			query: "", apiKey: "channel-send-only", wantStatus: consts.StatusForbidden},
		{name: "default direction with receive-only credential succeeds",
			query: "", apiKey: "channel-receive-only", wantStatus: consts.StatusOK},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			response := performWithQuery(s.handler.ListChannelEnvelopes, http.MethodGet, test.query, test.apiKey)

			s.Equal(test.wantStatus, response.Code)
			if test.wantStatus == consts.StatusOK {
				s.Contains(response.Body.String(), `"envelope_id":"tm_env_1"`)
			}
		})
	}
}

// TestListChannelEnvelopesRejectsInvalidLimit pins the strict 1..100 limit
// contract shared with queryLimit: non-numeric, non-positive, and oversized
// limits answer 400 instead of being silently clamped by the store.
func (s *onPremHandlerSuite) TestListChannelEnvelopesRejectsInvalidLimit() {
	for _, test := range []struct {
		name  string
		query string
	}{
		{name: "non-numeric", query: "?limit=abc"},
		{name: "zero", query: "?limit=0"},
		{name: "negative", query: "?limit=-5"},
		{name: "over maximum", query: "?limit=101"},
	} {
		s.Run(test.name, func() {
			response := performWithQuery(s.handler.ListChannelEnvelopes, http.MethodGet, test.query, "agent")

			s.Equal(consts.StatusBadRequest, response.Code)
			s.Equal("invalid channel envelope limit", response.Body.String())
		})
	}

	s.Run("valid limit passes", func() {
		response := performWithQuery(s.handler.ListChannelEnvelopes, http.MethodGet, "?limit=100", "agent")
		s.Equal(consts.StatusOK, response.Code)
	})
}

// TestGetAndArchiveChannelEnvelopeMapDomainErrors walks writeChannelError's
// sentinel table through the read (Get) and mutation (Archive) paths.
func (s *onPremHandlerSuite) TestGetAndArchiveChannelEnvelopeMapDomainErrors() {
	tests := []struct {
		name       string
		err        error
		wantStatus int
	}{
		{name: "envelope not found", err: onprem.ErrEnvelopeNotFound, wantStatus: consts.StatusNotFound},
		{name: "target agent not found", err: onprem.ErrTargetAgentNotFound, wantStatus: consts.StatusNotFound},
		{name: "envelope state conflict", err: onprem.ErrEnvelopeState, wantStatus: consts.StatusConflict},
		{name: "idempotency conflict", err: onprem.ErrIdempotencyConflict, wantStatus: consts.StatusConflict},
		{name: "foreign principal is forbidden", err: onprem.ErrForbidden, wantStatus: consts.StatusForbidden},
		{name: "invalid request", err: onprem.ErrInvalidChannelRequest, wantStatus: consts.StatusBadRequest},
		{name: "unknown failure stays internal", err: errors.New("pg: connection reset"), wantStatus: consts.StatusInternalServerError},
	}
	for _, test := range tests {
		s.Run("get "+test.name, func() {
			s.channel.getErr = test.err
			defer func() { s.channel.getErr = nil }()

			response := performWithPath(s.handler.GetChannelEnvelope, http.MethodGet, "", "agent",
				"envelope_id", "tm_env_1")

			s.Equal(test.wantStatus, response.Code)
			s.NotContains(response.Body.String(), "connection reset")
		})
		s.Run("archive "+test.name, func() {
			s.channel.archiveErr = test.err
			defer func() { s.channel.archiveErr = nil }()

			response := performWithPath(s.handler.ArchiveChannelEnvelope, http.MethodPost, "", "agent",
				"envelope_id", "tm_env_1")

			s.Equal(test.wantStatus, response.Code)
			s.NotContains(response.Body.String(), "connection reset")
		})
	}
}

// TestArchiveChannelEnvelopeHappyPath pins the archive success contract:
// 200, archived status on the wire, the envelope ID handed to the service,
// and a succeeded channel_archive operation event.
func (s *onPremHandlerSuite) TestArchiveChannelEnvelopeHappyPath() {
	response := performWithPath(s.handler.ArchiveChannelEnvelope, http.MethodPost, "", "agent",
		"envelope_id", "tm_env_1")

	s.Equal(consts.StatusOK, response.Code)
	s.Equal("tm_env_1", s.channel.archivedID)
	s.Contains(response.Body.String(), `"status":"archived"`)
	s.Require().Len(s.recorder.events, 1)
	s.Equal(operations.KindChannelArchive, s.recorder.events[0].Kind)
	s.Equal(operations.OutcomeSucceeded, s.recorder.events[0].Outcome)
	s.Equal("tm_env_1", s.recorder.events[0].DetailID)
}
