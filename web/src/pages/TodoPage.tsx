import { useCallback, useEffect, useState, type FormEvent } from "react";
import { Link } from "react-router-dom";
import {
  acceptTodoSuggestion,
  completeTodo,
  createTodo,
  dismissTodoSuggestion,
  refreshTodoSuggestions,
} from "../api/actions";
import { listTodoSuggestions, listTodos, type TodoItem, type TodoSuggestion } from "../api/todo";
import { Button } from "../components/Button";
import { isAbortError } from "../lib/usePolling";
import { useErrorHandler } from "../lib/useErrorHandler";

const NO_LIST_STYLE = { listStyle: "none", padding: 0, margin: 0 } as const;

export function TodoPage() {
  const handleError = useErrorHandler();
  const [suggestions, setSuggestions] = useState<TodoSuggestion[]>([]);
  const [todos, setTodos] = useState<TodoItem[]>([]);
  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const [busyId, setBusyId] = useState("");
  const [newTitle, setNewTitle] = useState("");
  const [adding, setAdding] = useState(false);

  const load = useCallback(async (signal?: AbortSignal) => {
    const [loadedSuggestions, loadedTodos] = await Promise.all([
      listTodoSuggestions(signal),
      listTodos("", signal),
    ]);
    setSuggestions(loadedSuggestions);
    setTodos(loadedTodos);
  }, []);

  useEffect(() => {
    const controller = new AbortController();
    setLoading(true);
    load(controller.signal)
      .catch((error: unknown) => {
        if (!isAbortError(error)) handleError(error);
      })
      .finally(() => {
        if (!controller.signal.aborted) setLoading(false);
      });
    return () => controller.abort();
  }, [handleError, load]);

  // No optimistic updates for any mutation below: every action awaits the
  // request, then refetches both lists from the server.
  const refetch = async () => {
    try {
      await load();
    } catch (error) {
      handleError(error);
    }
  };

  const checkTeamMemory = async () => {
    setRefreshing(true);
    try {
      await refreshTodoSuggestions();
      await refetch();
    } catch (error) {
      handleError(error);
    } finally {
      setRefreshing(false);
    }
  };

  const accept = async (suggestionId: string) => {
    setBusyId(suggestionId);
    try {
      await acceptTodoSuggestion(suggestionId);
      await refetch();
    } catch (error) {
      handleError(error);
    } finally {
      setBusyId("");
    }
  };

  const dismiss = async (suggestionId: string) => {
    setBusyId(suggestionId);
    try {
      await dismissTodoSuggestion(suggestionId);
      await refetch();
    } catch (error) {
      handleError(error);
    } finally {
      setBusyId("");
    }
  };

  const complete = async (todoId: string) => {
    setBusyId(todoId);
    try {
      await completeTodo(todoId);
      await refetch();
    } catch (error) {
      handleError(error);
    } finally {
      setBusyId("");
    }
  };

  const addTodo = async (event: FormEvent) => {
    event.preventDefault();
    const title = newTitle.trim();
    if (!title) return;
    setAdding(true);
    try {
      await createTodo(title, "");
      setNewTitle("");
      await refetch();
    } catch (error) {
      handleError(error);
    } finally {
      setAdding(false);
    }
  };

  const openTodos = todos.filter((todo) => todo.status === "open");
  const doneTodos = todos.filter((todo) => todo.status === "done");

  return (
    <div className="app-fullscreen">
      <div className="app-fullscreen-inner">
        <Link className="app-back" to="/apps">
          ← All apps
        </Link>
        <div className="page-head">
        <div>
          <h1>Todos</h1>
          <p className="muted flush">
            Track your work, and act on suggestions team memory has spotted.
          </p>
        </div>
        <Button
          variant="primary"
          type="button"
          disabled={refreshing}
          onClick={() => void checkTeamMemory()}
        >
          {refreshing ? "Checking…" : "Check team memory"}
        </Button>
      </div>

      <section className="card" aria-label="Suggestions">
        <h2>Suggestions</h2>
        {loading ? (
          <p className="muted">Loading…</p>
        ) : suggestions.length === 0 ? (
          <p className="muted small">No suggestions right now.</p>
        ) : (
          <ul style={NO_LIST_STYLE}>
            {suggestions.map((suggestion) => (
              <li
                key={suggestion.suggestion_id}
                className="row between wrap"
                style={{ padding: "10px 0", borderBottom: "1px solid var(--border)" }}
              >
                <div>
                  <span
                    className={suggestion.kind === "blocker" ? "badge b-suspended" : "badge b-pending"}
                  >
                    {suggestion.kind}
                  </span>
                  <div>
                    <strong>{suggestion.title}</strong>
                  </div>
                  <p className="muted small">{suggestion.body}</p>
                </div>
                <div className="row">
                  <Button
                    variant="primary"
                    size="sm"
                    type="button"
                    disabled={busyId === suggestion.suggestion_id}
                    onClick={() => void accept(suggestion.suggestion_id)}
                  >
                    Accept
                  </Button>
                  <Button
                    variant="ghost"
                    size="sm"
                    type="button"
                    disabled={busyId === suggestion.suggestion_id}
                    onClick={() => void dismiss(suggestion.suggestion_id)}
                  >
                    Dismiss
                  </Button>
                </div>
              </li>
            ))}
          </ul>
        )}
      </section>

      <section className="card" aria-label="Todos">
        <h2>Todos</h2>
        <form className="row" onSubmit={(event) => void addTodo(event)}>
          <label className="sr-only" htmlFor="todo-new-title">
            Todo title
          </label>
          <input
            id="todo-new-title"
            type="text"
            placeholder="Add a todo…"
            value={newTitle}
            onChange={(event) => setNewTitle(event.target.value)}
          />
          <Button variant="primary" type="submit" disabled={adding || newTitle.trim() === ""}>
            {adding ? "Adding…" : "Add"}
          </Button>
        </form>

        {loading ? (
          <p className="muted">Loading…</p>
        ) : openTodos.length === 0 ? (
          <p className="muted small">No open todos.</p>
        ) : (
          <ul style={NO_LIST_STYLE}>
            {openTodos.map((todo) => (
              <li
                key={todo.todo_id}
                className="row between"
                style={{ padding: "8px 0", borderBottom: "1px solid var(--border)" }}
              >
                <span>{todo.title}</span>
                <Button
                  size="sm"
                  type="button"
                  disabled={busyId === todo.todo_id}
                  onClick={() => void complete(todo.todo_id)}
                >
                  Complete
                </Button>
              </li>
            ))}
          </ul>
        )}

        <details style={{ marginTop: 14 }}>
          <summary className="muted small">Done ({doneTodos.length})</summary>
          <ul style={NO_LIST_STYLE}>
            {doneTodos.map((todo) => (
              <li key={todo.todo_id} className="muted small" style={{ padding: "4px 0" }}>
                {todo.title}
              </li>
            ))}
          </ul>
        </details>
      </section>
      </div>
    </div>
  );
}
