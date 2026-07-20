package router_test

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"testing"

	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/pax-beehive/pax-nexus/internal/teamnote"
	"github.com/pax-beehive/pax-nexus/internal/teamnote/transport/httpapi/handler"
	"github.com/pax-beehive/pax-nexus/internal/teamnote/transport/httpapi/router"
	"github.com/stretchr/testify/require"
)

func TestRegisterIsolatesConcurrentHandlerInstances(t *testing.T) {
	t.Parallel()
	firstRuntime := &recordingRuntime{}
	secondRuntime := &recordingRuntime{}
	first := newServer(t, firstRuntime, "first-key", "first-scope")
	second := newServer(t, secondRuntime, "second-key", "second-scope")

	const requestsPerServer = 20
	var group sync.WaitGroup
	statusCodes := make(chan int, requestsPerServer*2)
	for index := range requestsPerServer {
		group.Add(2)
		go recall(&group, statusCodes, first, "first-key", fmt.Sprintf("first-%d", index))
		go recall(&group, statusCodes, second, "second-key", fmt.Sprintf("second-%d", index))
	}
	group.Wait()
	close(statusCodes)
	for statusCode := range statusCodes {
		require.Equal(t, consts.StatusOK, statusCode)
	}

	require.Equal(t, requestsPerServer, firstRuntime.count("first-scope"))
	require.Zero(t, firstRuntime.count("second-scope"))
	require.Equal(t, requestsPerServer, secondRuntime.count("second-scope"))
	require.Zero(t, secondRuntime.count("first-scope"))
}

func newServer(t *testing.T, runtime teamnote.Runtime, apiKey, scopeID string) *server.Hertz {
	t.Helper()
	configured, err := handler.New(runtime, handler.StaticAPIKeys{apiKey: scopeID}, slog.New(slog.DiscardHandler))
	require.NoError(t, err)
	hertz := server.New()
	hertz.Use(handler.InstanceMiddleware(configured))
	router.GeneratedRegister(hertz)
	return hertz
}

func recall(group *sync.WaitGroup, statusCodes chan<- int, hertz *server.Hertz, apiKey, query string) {
	defer group.Done()
	body := fmt.Sprintf(`{"actor":{"user_id":"owner","agent_id":"consumer","session_id":"session"},"token_budget":64,"query":%q}`, query)
	response := ut.PerformRequest(hertz.Engine, http.MethodPost, "/v1/notes/recall", &ut.Body{
		Body: bytes.NewBufferString(body), Len: len(body),
	}, ut.Header{Key: "Content-Type", Value: "application/json"}, ut.Header{Key: "Authorization", Value: "Bearer " + apiKey})
	statusCodes <- response.Code
}

type recordingRuntime struct {
	mu     sync.Mutex
	scopes map[string]int
}

func (r *recordingRuntime) ObserveSession(context.Context, teamnote.SessionBatch) (teamnote.IngestReceipt, error) {
	return teamnote.IngestReceipt{}, nil
}

func (r *recordingRuntime) RecallNotes(ctx context.Context, _ teamnote.RecallRequest) (teamnote.NoteEnvelope, error) {
	scopeID, err := teamnote.ScopeFromContext(ctx)
	if err != nil {
		return teamnote.NoteEnvelope{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.scopes == nil {
		r.scopes = make(map[string]int)
	}
	r.scopes[scopeID]++
	return teamnote.NoteEnvelope{}, nil
}

func (r *recordingRuntime) count(scopeID string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.scopes[scopeID]
}
