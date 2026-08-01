// Cursor pagination footer shared by the enrollment and credential lists.

import { Button } from "../Button";

export function LoadMore({
  nextCursor,
  loadingMore,
  onLoadMore,
}: {
  nextCursor?: string;
  loadingMore: boolean;
  onLoadMore: () => void;
}) {
  if (!nextCursor) return null;
  return (
    <div style={{ marginTop: 10, textAlign: "center" }}>
      <Button size="sm" disabled={loadingMore} onClick={onLoadMore}>
        {loadingMore ? "Loading…" : "Load more"}
      </Button>
    </div>
  );
}
