package router

import (
	"net/http"
	"testing"

	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/stretchr/testify/assert"
)

func TestGivenRouterWhenRegisteredThenVersionedReaderRouteIsReachable(t *testing.T) {
	t.Parallel()
	hertz := server.New()
	Register(hertz)

	response := ut.PerformRequest(
		hertz.Engine,
		http.MethodGet,
		"/v1/wiki/navigation",
		nil,
	)

	assert.Equal(t, http.StatusInternalServerError, response.Code)
}

func TestGivenRouterWhenRegisteredThenLegacyReaderRouteIsAbsent(t *testing.T) {
	t.Parallel()
	for _, path := range []string{
		"/navigation",
		"/pages/example",
		"/search?q=example",
	} {
		t.Run(path, func(t *testing.T) {
			t.Parallel()
			hertz := server.New()
			Register(hertz)

			response := ut.PerformRequest(hertz.Engine, http.MethodGet, path, nil)

			assert.Equal(t, http.StatusNotFound, response.Code)
		})
	}
}
