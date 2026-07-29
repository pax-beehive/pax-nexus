// Shared scaffolding for the cursor-paginated admin list pages: the card
// shell around the table, the loading and empty states, an inline error
// notice with retry, and the "Load more" button. Page-specific pieces —
// filters, columns, row actions — stay in the pages; the list state itself
// comes from usePagedList.

import type { ReactNode } from "react";
import type { PagedList } from "../lib/usePagedList";

interface PagedListCardProps<T> {
  /** The usePagedList result driving this table. */
  list: PagedList<T>;
  /** Header cells, one per column; use "" for an unlabeled action column. */
  columns: ReactNode[];
  /** Renders one <tr> (or a keyed fragment of rows) per item. */
  renderRow: (item: T) => ReactNode;
  /** Empty-state copy shown when the loaded list has no rows. */
  emptyText: string;
  /** Optional slot rendered above the table inside the card. */
  header?: ReactNode;
}

function ErrorNotice({ message, onRetry }: { message: string; onRetry: () => void }) {
  return (
    <div
      className="note bad row"
      role="alert"
      style={{ justifyContent: "space-between", alignItems: "center" }}
    >
      <span>{message}</span>
      <button className="btn sm" onClick={onRetry}>
        Retry
      </button>
    </div>
  );
}

export function PagedListCard<T>({
  list,
  columns,
  renderRow,
  emptyText,
  header,
}: PagedListCardProps<T>) {
  const tableHead = (
    <thead>
      <tr>
        {columns.map((column, index) => (
          <th key={index}>{column}</th>
        ))}
      </tr>
    </thead>
  );

  let body: ReactNode;
  if (list.loading) {
    body = (
      <table>
        {tableHead}
        <tbody>
          <tr>
            <td colSpan={columns.length} className="muted small">
              Loading…
            </td>
          </tr>
        </tbody>
      </table>
    );
  } else if (list.error && list.items.length === 0) {
    // Initial load failed: the cursor is reset, so retry restarts the list.
    body = <ErrorNotice message="Failed to load the list." onRetry={list.reload} />;
  } else if (list.items.length === 0) {
    body = <p className="muted small">{emptyText}</p>;
  } else {
    body = (
      <>
        <table>
          {tableHead}
          <tbody>{list.items.map(renderRow)}</tbody>
        </table>
        {list.error ? (
          // A failed page keeps the loaded rows and the cursor, so retry
          // re-attempts the same page.
          <ErrorNotice message="Failed to load more." onRetry={() => void list.loadMore()} />
        ) : null}
      </>
    );
  }

  return (
    <div className="card">
      {header}
      {body}
      {/* A failed "load more" already shows a Retry notice above (in `body`);
          hiding the button here avoids offering two affordances for the
          same retry. */}
      {list.nextCursor && !(list.error && list.items.length > 0) ? (
        <div style={{ marginTop: 12, textAlign: "center" }}>
          <button className="btn sm" disabled={list.loadingMore} onClick={() => void list.loadMore()}>
            {list.loadingMore ? "Loading…" : "Load more"}
          </button>
        </div>
      ) : null}
    </div>
  );
}
