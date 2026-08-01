package todoapp

import "time"

type TodoStatus string

const (
	TodoOpen TodoStatus = "open"
	TodoDone TodoStatus = "done"
)

type TodoSource string

const (
	TodoSourceManual     TodoSource = "manual"
	TodoSourceSuggestion TodoSource = "suggestion"
)

type Todo struct {
	ID           string
	Title        string
	Body         string
	Status       TodoStatus
	Source       TodoSource
	SuggestionID string
	NoteID       string
	CreatedBy    string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type SuggestionStatus string

const (
	SuggestionPending   SuggestionStatus = "pending"
	SuggestionAccepted  SuggestionStatus = "accepted"
	SuggestionDismissed SuggestionStatus = "dismissed"
)

type Suggestion struct {
	ID          string
	Fingerprint string
	NoteID      string
	Kind        string
	Title       string
	Body        string
	Status      SuggestionStatus
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type ActionItem struct {
	NoteID    string
	Kind      string
	Subject   string
	Body      string
	UpdatedAt time.Time
}

type ReportEventType string

const (
	EventSuggestionAccepted  ReportEventType = "suggestion_accepted"
	EventSuggestionDismissed ReportEventType = "suggestion_dismissed"
	EventTodoCompleted       ReportEventType = "todo_completed"
)

type ReportEvent struct {
	Type         ReportEventType
	UserID       string
	TodoID       string
	SuggestionID string
	NoteID       string
	Summary      string
	OccurredAt   time.Time
}
