package handler_test

import (
	"context"
	"fmt"
	"net/http"

	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/pax-beehive/pax-nexus/internal/session"
	"github.com/pax-beehive/pax-nexus/internal/teamnote"
	"go.uber.org/mock/gomock"
)

func (s *onPremHandlerSuite) TestObserveStreamAcceptsRegisteredBatch() {
	s.runtime.EXPECT().
		ObserveStream(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, batch teamnote.StreamBatch) (teamnote.IngestReceipt, error) {
			s.Require().Len(batch.Events, 1)
			s.Equal("im-channel", batch.Events[0].Stream.Source)
			s.Equal("U0AB12", batch.Events[0].Author.NativeID)
			return teamnote.IngestReceipt{Accepted: 1, Cursor: 1}, nil
		})

	response := perform(s.handler.ObserveStream, http.MethodPost, `{
		"events": [{
			"id": "evt-1",
			"source": "im-channel",
			"stream_id": "channel-9",
			"author": {"kind": "user", "native_id": "U0AB12"},
			"kind": "text",
			"type": "message",
			"content": "ship it Friday",
			"visibility": "team",
			"occurred_at": "2026-07-28T10:00:00Z"
		}],
		"complete": false
	}`, "agent")
	s.Equal(consts.StatusOK, response.Code)
	s.Contains(response.Body.String(), `"accepted":1`)
}

func (s *onPremHandlerSuite) TestObserveStreamRejectsContractViolations() {
	s.runtime.EXPECT().
		ObserveStream(gomock.Any(), gomock.Any()).
		Return(teamnote.IngestReceipt{}, fmt.Errorf("observe stream: %w", session.ErrVisibilityRejected))

	response := perform(s.handler.ObserveStream, http.MethodPost, `{
		"events": [{
			"id": "evt-1",
			"source": "im-channel",
			"stream_id": "channel-9",
			"author": {"kind": "user", "native_id": "U0AB12"},
			"kind": "text",
			"type": "message",
			"content": "x",
			"visibility": "private",
			"occurred_at": "2026-07-28T10:00:00Z"
		}],
		"complete": false
	}`, "agent")
	s.Equal(consts.StatusBadRequest, response.Code)
}

func (s *onPremHandlerSuite) TestObserveStreamRejectsNullEvents() {
	// No EXPECT() set on s.runtime: a null array element must be rejected by
	// mapping before the runtime is ever called, otherwise gomock fails the
	// test on the unexpected call.
	response := perform(s.handler.ObserveStream, http.MethodPost,
		`{"events": [null], "complete": false}`, "agent")
	s.Equal(consts.StatusBadRequest, response.Code)

	responseNilAuthor := perform(s.handler.ObserveStream, http.MethodPost, `{
		"events": [{
			"id": "evt-1",
			"source": "im-channel",
			"stream_id": "channel-9",
			"author": null,
			"kind": "text",
			"type": "message",
			"content": "x",
			"visibility": "team",
			"occurred_at": "2026-07-28T10:00:00Z"
		}],
		"complete": false
	}`, "agent")
	s.Equal(consts.StatusBadRequest, responseNilAuthor.Code)
}
