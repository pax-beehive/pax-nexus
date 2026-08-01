package main

import (
	"context"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
)

func customizedRegister(server *server.Hertz) {
	server.GET("/healthz", func(_ context.Context, request *app.RequestContext) {
		request.JSON(consts.StatusOK, map[string]string{"status": "ok"})
	})
}
