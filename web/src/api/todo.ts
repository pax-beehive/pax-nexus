import { humanFetch } from "./client";

export interface TodoItem {
  todo_id: string;
  title: string;
  body: string;
  status: "open" | "done";
  source: "manual" | "suggestion";
  suggestion_id?: string;
  note_id?: string;
  created_by: string;
  created_at: string;
  updated_at: string;
}

export interface TodoSuggestion {
  suggestion_id: string;
  note_id: string;
  kind: string;
  title: string;
  body: string;
  status: string;
  created_at: string;
}

export async function listTodos(
  status: "" | "open" | "done",
  signal?: AbortSignal,
): Promise<TodoItem[]> {
  const query = status ? `?status=${encodeURIComponent(status)}` : "";
  const response = await humanFetch<{ todos: TodoItem[] }>(`/v1/todo/todos${query}`, { signal });
  return response.todos ?? [];
}

export async function listTodoSuggestions(signal?: AbortSignal): Promise<TodoSuggestion[]> {
  const response = await humanFetch<{ suggestions: TodoSuggestion[] }>(
    "/v1/todo/suggestions",
    { signal },
  );
  return response.suggestions ?? [];
}
