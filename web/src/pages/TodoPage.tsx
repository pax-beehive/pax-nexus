import { useCallback, useEffect, useState, type FormEvent } from "react";
import {
  acceptTodoSuggestion,
  completeTodo,
  createTodo,
  dismissTodoSuggestion,
  refreshTodoSuggestions,
} from "../api/actions";
import { listTodoSuggestions, listTodos, type TodoItem, type TodoSuggestion } from "../api/todo";
import { Button } from "../components/Button";
import { PageHeader } from "../components/PageHeader";
import { RegionError } from "../components/RegionError";
import { Tag } from "../components/Tag";
import { isAbortError } from "../lib/usePolling";
import { useErrorHandler } from "../lib/useErrorHandler";

const NO_LIST_STYLE = { listStyle: "none", padding: 0, margin: 0 } as const;

export function TodoPage() {
  const handleError = useErrorHandler();
  const [suggestions, setSuggestions] = useState<TodoSuggestion[]>([]);
  const [suggestionsError, setSuggestionsError] = useState(false);
  const [todos, setTodos] = useState<TodoItem[]>([]);
  const [todosError, setTodosError] = useState(false);
  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const [busyId, setBusyId] = useState("");
  const [newTitle, setNewTitle] = useState("");
  const [adding, setAdding] = useState(false);

  // 建议列表与个人清单是两个不同的取数源。用 allSettled 而不是 all：
  // 一边失败只置那一边的 error 位，另一边该怎么渲染还怎么渲染，不能
  // 因为一次失败就把两栏一起拖垮。
  const load = useCallback(async (signal?: AbortSignal) => {
    const [suggestionsResult, todosResult] = await Promise.allSettled([
      listTodoSuggestions(signal),
      listTodos("", signal),
    ]);

    if (suggestionsResult.status === "fulfilled") {
      setSuggestions(suggestionsResult.value);
      setSuggestionsError(false);
    } else if (!isAbortError(suggestionsResult.reason)) {
      setSuggestionsError(true);
    }

    if (todosResult.status === "fulfilled") {
      setTodos(todosResult.value);
      setTodosError(false);
    } else if (!isAbortError(todosResult.reason)) {
      setTodosError(true);
    }
  }, []);

  useEffect(() => {
    const controller = new AbortController();
    setLoading(true);
    load(controller.signal).finally(() => {
      if (!controller.signal.aborted) setLoading(false);
    });
    return () => controller.abort();
  }, [load]);

  // No optimistic updates for any mutation below: every action awaits the
  // request, then refetches both lists from the server.
  const checkTeamMemory = async () => {
    setRefreshing(true);
    try {
      await refreshTodoSuggestions();
      await load();
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
      await load();
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
      await load();
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
      await load();
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
      await load();
    } catch (error) {
      handleError(error);
    } finally {
      setAdding(false);
    }
  };

  const openTodos = todos.filter((todo) => todo.status === "open");
  const doneTodos = todos.filter((todo) => todo.status === "done");

  return (
    <>
      <PageHeader
        kicker="Apps · todos"
        title="Work the agents surfaced"
        lede="Blockers and handoffs the team wrote down get turned into suggestions. Accept one and it becomes yours; dismiss it and it won't come back."
        actions={
          <Button
            variant="primary"
            type="button"
            disabled={refreshing}
            onClick={() => void checkTeamMemory()}
          >
            {refreshing ? "Reading team memory…" : "Check team memory"}
          </Button>
        }
      />

      <div className="split wide-left">
        <section className="card" aria-label="Suggestions">
          <h2 className="card-title flush">Suggestions</h2>
          {loading ? (
            <p className="muted">Loading…</p>
          ) : suggestionsError ? (
            // 此前是一行没有出路的 muted 文本：读者被告知「稍后重试」，
            // 却没有任何可以按的东西，只能整页刷新（PR #91 follow-up）。
            <RegionError message="Suggestions are unavailable." onRetry={() => void load()} />
          ) : suggestions.length === 0 ? (
            <p className="muted small">No new suggestions right now.</p>
          ) : (
            <ul style={NO_LIST_STYLE}>
              {suggestions.map((suggestion) => (
                <li key={suggestion.suggestion_id} className="todo-row">
                  <div>
                    <Tag tone={suggestion.kind === "blocker" ? "attention" : "neutral"}>
                      {suggestion.kind}
                    </Tag>
                    <div>
                      <strong>{suggestion.title}</strong>
                    </div>
                    <p className="muted small">{suggestion.body}</p>
                  </div>
                  <div className="todo-row-actions">
                    <Button
                      variant="primary"
                      size="sm"
                      type="button"
                      disabled={busyId === suggestion.suggestion_id}
                      onClick={() => void accept(suggestion.suggestion_id)}
                    >
                      Take it
                    </Button>
                    <Button
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
          <h2 className="card-title flush">Your list</h2>
          <form className="row" onSubmit={(event) => void addTodo(event)}>
            <label className="sr-only" htmlFor="todo-new-title">
              Todo title
            </label>
            <input
              id="todo-new-title"
              type="text"
              placeholder="Add something…"
              value={newTitle}
              onChange={(event) => setNewTitle(event.target.value)}
            />
            <Button variant="primary" type="submit" disabled={adding || newTitle.trim() === ""}>
              {adding ? "Adding…" : "Add"}
            </Button>
          </form>

          {loading ? (
            <p className="muted">Loading…</p>
          ) : todosError ? (
            <RegionError message="Your list is unavailable." onRetry={() => void load()} />
          ) : (
            <>
              {openTodos.length === 0 ? (
                <p className="muted small">No open todos.</p>
              ) : (
                <ul style={NO_LIST_STYLE}>
                  {openTodos.map((todo) => (
                    <li key={todo.todo_id} className="todo-item-row">
                      <span>{todo.title}</span>
                      <Button
                        size="sm"
                        type="button"
                        disabled={busyId === todo.todo_id}
                        onClick={() => void complete(todo.todo_id)}
                      >
                        Done
                      </Button>
                    </li>
                  ))}
                </ul>
              )}

              <details className="todo-done-group">
                <summary className="muted small">Done ({doneTodos.length})</summary>
                <ul style={NO_LIST_STYLE}>
                  {doneTodos.map((todo) => (
                    <li key={todo.todo_id} className="muted small todo-done-item">
                      {todo.title}
                    </li>
                  ))}
                </ul>
              </details>
            </>
          )}
        </section>
      </div>
    </>
  );
}
