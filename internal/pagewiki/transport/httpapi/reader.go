package httpapi

import (
	"context"
	"embed"
	"net/http"

	"github.com/cloudwego/hertz/pkg/app"
)

//go:embed assets/reader.html assets/reader.css assets/reader.js
var readerAssets embed.FS

func (h *Handler) GetReader(
	_ context.Context,
	requestContext *app.RequestContext,
) {
	h.serveReaderAsset(requestContext, "reader.html")
}

func (h *Handler) GetReaderAsset(
	_ context.Context,
	requestContext *app.RequestContext,
	asset string,
) {
	switch asset {
	case "reader.css", "reader.js":
		h.serveReaderAsset(requestContext, asset)
	default:
		requestContext.JSON(http.StatusNotFound, map[string]string{
			"code":    "not_found",
			"message": "reader asset not found",
		})
	}
}

func (h *Handler) serveReaderAsset(
	requestContext *app.RequestContext,
	asset string,
) {
	content, err := readerAssets.ReadFile("assets/" + asset)
	if err != nil {
		requestContext.JSON(http.StatusInternalServerError, map[string]string{
			"code":    "reader_unavailable",
			"message": "reader asset is unavailable",
		})
		return
	}
	var contentType string
	switch asset {
	case "reader.css":
		contentType = "text/css; charset=utf-8"
	case "reader.js":
		contentType = "text/javascript; charset=utf-8"
	default:
		contentType = "text/html; charset=utf-8"
	}
	requestContext.Header("Cache-Control", "no-cache")
	requestContext.Data(http.StatusOK, contentType, content)
}
