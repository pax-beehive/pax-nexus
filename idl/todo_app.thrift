namespace go todoapp.api

struct TodoItem {
  1: required string todo_id
  2: required string title
  3: required string body
  4: required string status
  5: required string source
  6: optional string suggestion_id
  7: optional string note_id
  8: required string created_by
  9: required string created_at
  10: required string updated_at
}

struct TodoSuggestionItem {
  1: required string suggestion_id
  2: required string note_id
  3: required string kind
  4: required string title
  5: required string body
  6: required string status
  7: required string created_at
}

struct ListTodosRequest { 1: optional string status (api.query="status") }
struct ListTodosResponse { 1: required list<TodoItem> todos }
struct CreateTodoRequest {
  1: required string title (api.body="title")
  2: optional string body (api.body="body")
}
struct TodoByIDRequest { 1: required string todo_id (api.path="todo_id") }
struct ListTodoSuggestionsRequest {}
struct ListTodoSuggestionsResponse { 1: required list<TodoSuggestionItem> suggestions }
struct RefreshTodoSuggestionsRequest {}
struct RefreshTodoSuggestionsResponse { 1: required i32 created }
struct TodoSuggestionByIDRequest { 1: required string suggestion_id (api.path="suggestion_id") }
struct DismissTodoSuggestionResponse {}

service TodoAppService {
  ListTodosResponse ListTodos(1: ListTodosRequest request) (api.get="/v1/todo/todos")
  TodoItem CreateTodo(1: CreateTodoRequest request) (api.post="/v1/todo/todos")
  TodoItem CompleteTodo(1: TodoByIDRequest request) (api.post="/v1/todo/todos/:todo_id/complete")
  ListTodoSuggestionsResponse ListTodoSuggestions(1: ListTodoSuggestionsRequest request) (api.get="/v1/todo/suggestions")
  RefreshTodoSuggestionsResponse RefreshTodoSuggestions(1: RefreshTodoSuggestionsRequest request) (api.post="/v1/todo/suggestions/refresh")
  TodoItem AcceptTodoSuggestion(1: TodoSuggestionByIDRequest request) (api.post="/v1/todo/suggestions/:suggestion_id/accept")
  DismissTodoSuggestionResponse DismissTodoSuggestion(1: TodoSuggestionByIDRequest request) (api.post="/v1/todo/suggestions/:suggestion_id/dismiss")
}
