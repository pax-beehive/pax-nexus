package router

import (
	"net/http"
	"testing"

	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/stretchr/testify/assert"
)

func TestGivenRouterWhenRegisteredThenReaderRouteIsReachable(t *testing.T) {
	t.Parallel()
	hertz := server.New()
	Register(hertz)

	response := ut.PerformRequest(hertz.Engine, http.MethodGet, "/wiki", nil)

	assert.Equal(t, http.StatusInternalServerError, response.Code)
}
