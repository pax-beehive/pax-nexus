// Shared error handling for pages (doc section 9): 401 drops the cached
// identity via the auth context, 403 triggers a /v1/me refresh (the role may
// have changed in another window), everything else becomes a toast from the
// central status mapping.

import { useCallback } from "react";
import { ApiError } from "../api/client";
import { noticeForError } from "../lib/statusMessage";
import { useAuth } from "../auth/AuthContext";
import { useToast } from "../components/Toasts";

export function useErrorHandler(): (err: unknown, opts?: { conflict?: string }) => void {
  const toast = useToast();
  const { handleUnauthorized, refresh } = useAuth();

  return useCallback(
    (err: unknown, opts?: { conflict?: string }) => {
      if (err instanceof ApiError && err.status === 401) {
        handleUnauthorized();
        return;
      }
      if (err instanceof ApiError && err.status === 403) {
        void refresh();
      }
      const notice = noticeForError(err, opts);
      toast(notice.kind, notice.message);
    },
    [toast, handleUnauthorized, refresh],
  );
}
