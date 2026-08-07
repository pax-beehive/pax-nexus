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
import { Kicker } from "../components/Kicker";
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
      <div className="page-head">
        <div>
          <Kicker>Apps · todos</Kicker>
          <h1>Agent 替你发现的活儿</h1>
          <p className="muted flush">
            团队写下的阻塞与交接会被转成建议。接受一条它就归你；忽略它就不会再回来。
          </p>
        </div>
        <Button
          variant="primary"
          type="button"
          disabled={refreshing}
          onClick={() => void checkTeamMemory()}
        >
          {refreshing ? "扫描中…" : "扫描团队记忆"}
        </Button>
      </div>

      <div className="todo-columns">
        <section className="card" aria-label="Suggestions">
          <h2 className="card-title flush">建议</h2>
          {loading ? (
            <p className="muted">加载中…</p>
          ) : suggestionsError ? (
            <p className="muted small">建议暂时不可用，请稍后重试。</p>
          ) : suggestions.length === 0 ? (
            <p className="muted small">现在没有新的建议。</p>
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
                      接下来我做
                    </Button>
                    <Button
                      size="sm"
                      type="button"
                      disabled={busyId === suggestion.suggestion_id}
                      onClick={() => void dismiss(suggestion.suggestion_id)}
                    >
                      忽略
                    </Button>
                  </div>
                </li>
              ))}
            </ul>
          )}
        </section>

        <section className="card" aria-label="Todos">
          <h2 className="card-title flush">你的清单</h2>
          <form className="row" onSubmit={(event) => void addTodo(event)}>
            <label className="sr-only" htmlFor="todo-new-title">
              待办标题
            </label>
            <input
              id="todo-new-title"
              type="text"
              placeholder="添加一件事…"
              value={newTitle}
              onChange={(event) => setNewTitle(event.target.value)}
            />
            <Button variant="primary" type="submit" disabled={adding || newTitle.trim() === ""}>
              {adding ? "添加中…" : "添加"}
            </Button>
          </form>

          {loading ? (
            <p className="muted">加载中…</p>
          ) : todosError ? (
            <p className="muted small">清单暂时不可用，请稍后重试。</p>
          ) : (
            <>
              {openTodos.length === 0 ? (
                <p className="muted small">没有未完成的待办。</p>
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
                        完成
                      </Button>
                    </li>
                  ))}
                </ul>
              )}

              <details className="todo-done-group">
                <summary className="muted small">已完成（{doneTodos.length}）</summary>
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
