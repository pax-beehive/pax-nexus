package router

import (
	"github.com/cloudwego/hertz/pkg/app/server"
	generated "github.com/pax-beehive/pax-nexus/internal/teamnote/transport/httpapi/router/pagewiki/api"
)

func Register(server *server.Hertz) {
	generated.Register(server)
}
