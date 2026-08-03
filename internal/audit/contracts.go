package audit

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// Query is the read-only seam consumed by administrative transports.
type Query interface {
	ListToolCalls(context.Context, ToolCallFilter) ([]ToolCall, error)
	ListFindings(context.Context, FindingFilter) ([]Finding, error)
	GetActivityDaily(context.Context, ActivityFilter) ([]ActivityDaily, error)
}

//go:generate mockgen -source=contracts.go -destination=mocks/query.go -package=mocks Query

// ToolCallFilter narrows ListToolCalls. Empty fields match everything; a
// non-positive Limit falls back to the store default.
type ToolCallFilter struct {
	ScopeID       string
	UserID        string
	AgentID       string
	SessionID     string
	RiskLevel     string
	ApprovalState string
	Limit         int
}

// FindingFilter narrows ListFindings.
type FindingFilter struct {
	ScopeID   string
	UserID    string
	AgentID   string
	SessionID string
	Kind      string
	Severity  string
	Limit     int
}

// ActivityFilter narrows GetActivityDaily. Zero FromDay/ToDay values leave
// the day range open on that side.
type ActivityFilter struct {
	ScopeID string
	UserID  string
	AgentID string
	FromDay time.Time
	ToDay   time.Time
	Limit   int
}

// ActivityDaily is one day of aggregated agent activity.
type ActivityDaily struct {
	ScopeID       string
	UserID        string
	AgentID       string
	Day           time.Time
	EventCount    int64
	ToolCallCount int64
	HighRiskCount int64
	SessionCount  int64
	ToolBreakdown map[string]int64
}

// New validates dependencies and returns the audit consumer controller. A
// non-positive interval falls back to the default scan interval.
func New(store Store, logger *slog.Logger, interval time.Duration) (*Controller, error) {
	if store == nil || logger == nil {
		return nil, fmt.Errorf("create session audit consumer: store and logger are required")
	}
	if interval <= 0 {
		interval = defaultScanInterval
	}
	return &Controller{store: store, logger: logger, interval: interval}, nil
}
