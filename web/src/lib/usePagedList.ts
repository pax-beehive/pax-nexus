// Cursor-paginated list hook (doc section 3.4): the cursor is an opaque
// string passed back verbatim; any filter change must reset the cursor and
// the loaded items, which is what the `deps` argument is for.

import { useCallback, useEffect, useRef, useState } from "react";
import type { Page } from "../api/queries";

export interface PagedList<T> {
  items: T[];
  nextCursor?: string;
  loading: boolean;
  loadingMore: boolean;
  error: unknown;
  loadMore: () => Promise<void>;
  reload: () => void;
}

export function usePagedList<T>(
  fetchPage: (cursor?: string) => Promise<Page<T>>,
  deps: readonly unknown[],
): PagedList<T> {
  const [items, setItems] = useState<T[]>([]);
  const [nextCursor, setNextCursor] = useState<string | undefined>();
  const [loading, setLoading] = useState(true);
  const [loadingMore, setLoadingMore] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const [epoch, setEpoch] = useState(0);
  const fetchRef = useRef(fetchPage);
  fetchRef.current = fetchPage;
  const cursorRef = useRef<string | undefined>();

  useEffect(() => {
    let cancelled = false;
    cursorRef.current = undefined;
    setItems([]);
    setNextCursor(undefined);
    setLoading(true);
    setError(null);
    fetchRef
      .current(undefined)
      .then((page) => {
        if (cancelled) return;
        setItems(page.items);
        setNextCursor(page.nextCursor);
        cursorRef.current = page.nextCursor;
        setLoading(false);
      })
      .catch((err: unknown) => {
        if (cancelled) return;
        setError(err);
        setLoading(false);
      });
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [...deps, epoch]);

  const loadMore = useCallback(async () => {
    const cursor = cursorRef.current;
    if (!cursor) return;
    setLoadingMore(true);
    setError(null);
    try {
      const page = await fetchRef.current(cursor);
      setItems((prev) => [...prev, ...page.items]);
      setNextCursor(page.nextCursor);
      cursorRef.current = page.nextCursor;
    } catch (err) {
      setError(err);
    } finally {
      setLoadingMore(false);
    }
  }, []);

  const reload = useCallback(() => setEpoch((e) => e + 1), []);

  return { items, nextCursor, loading, loadingMore, error, loadMore, reload };
}
