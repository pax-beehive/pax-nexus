// Package observability provides the shared structured logging policy.
package observability

import (
	"io"
	"log/slog"
)

// NewLogger writes JSON logs suitable for Docker and log aggregation.
func NewLogger(writer io.Writer) *slog.Logger {
	return slog.New(slog.NewJSONHandler(writer, nil))
}

// DiscardLogger keeps optional logging dependencies silent in tests and
// embedded runtimes unless a caller explicitly supplies a logger.
func DiscardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
