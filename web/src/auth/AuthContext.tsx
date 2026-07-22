// Auth context (doc section 4): boot classifies GET /v1/me into a
// discriminated union and route guards branch on it. There is no global 401
// redirect in the fetch wrapper; pages report 401 through handleUnauthorized
// so cached identity is dropped and the guards take over.

import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from "react";
import { ApiError } from "../api/client";
import { getMe } from "../api/queries";
import { logout as logoutAction } from "../api/actions";
import type { HumanMe } from "../api/types";

export type AuthState =
  | { kind: "loading" }
  | { kind: "unauthenticated" }
  | { kind: "not-configured" }
  | { kind: "no-membership"; me: HumanMe }
  | { kind: "active"; me: HumanMe }
  | { kind: "suspended"; me: HumanMe };

interface AuthContextValue {
  state: AuthState;
  refresh: () => Promise<void>;
  handleUnauthorized: () => void;
  logout: () => Promise<void>;
}

const AuthContext = createContext<AuthContextValue | null>(null);

function classify(me: HumanMe): AuthState {
  if (!me.membership_id) return { kind: "no-membership", me };
  if (me.membership_status === "suspended") return { kind: "suspended", me };
  return { kind: "active", me };
}

export function AuthProvider({ children }: { children: ReactNode }) {
  const [state, setState] = useState<AuthState>({ kind: "loading" });

  const refresh = useCallback(async () => {
    try {
      const me = await getMe();
      setState(classify(me));
    } catch (err) {
      if (err instanceof ApiError && err.status === 501) {
        setState({ kind: "not-configured" });
      } else if (err instanceof ApiError && err.status === 401) {
        setState({ kind: "unauthenticated" });
      } else {
        // Network or unexpected failure: fall back to the login page, which
        // offers a manual retry instead of redirect-looping into OIDC.
        setState({ kind: "unauthenticated" });
      }
    }
  }, []);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  // A 401 mid-session means the session was revoked or expired (doc section
  // 4): drop the cached identity; route guards render the login page and all
  // member/agent views unmount, clearing their cached data.
  const handleUnauthorized = useCallback(() => {
    setState({ kind: "unauthenticated" });
  }, []);

  const logout = useCallback(async () => {
    try {
      await logoutAction();
    } catch {
      // Best-effort: even if the request fails, drop the local identity.
    }
    setState({ kind: "unauthenticated" });
  }, []);

  const value = useMemo(
    () => ({ state, refresh, handleUnauthorized, logout }),
    [state, refresh, handleUnauthorized, logout],
  );

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}

export function useAuth(): AuthContextValue {
  const ctx = useContext(AuthContext);
  if (!ctx) throw new Error("useAuth must be used within AuthProvider");
  return ctx;
}
