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
import { apiError } from "../api/client";
import { getMe, listTeams } from "../api/queries";
import { logout as logoutAction } from "../api/actions";
import type { HumanMe } from "../api/types";

export type DeploymentProfile = "saas" | "onprem";

export type AuthState =
  | { kind: "loading" }
  | { kind: "unauthenticated" }
  | { kind: "not-configured" }
  | { kind: "no-membership"; me: HumanMe; profile: DeploymentProfile }
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
  if (me.membership_status === "suspended") return { kind: "suspended", me };
  return { kind: "active", me };
}

// Profile probe (M3): /v1/me omits `teams` both on on-prem and for a saas
// user with zero teams, so the no-membership branch cannot tell the two
// profiles apart from field presence. GET /v1/teams disambiguates (200 in
// saas, 501 not_configured on on-prem; any other failure is treated as
// on-prem so the bootstrap-claim entry stays reachable). The result is a
// deployment property, so it is cached in tab-scoped sessionStorage.
const PROFILE_PROBE_KEY = "portal.deployment-profile";

async function probeProfile(): Promise<DeploymentProfile> {
  try {
    const cached = sessionStorage.getItem(PROFILE_PROBE_KEY);
    if (cached === "saas" || cached === "onprem") return cached;
  } catch {
    // sessionStorage may be unavailable; fall through to the network probe.
  }
  let profile: DeploymentProfile = "onprem";
  try {
    await listTeams();
    profile = "saas";
  } catch {
    // 501 not_configured or any unexpected failure: on-prem profile.
  }
  try {
    sessionStorage.setItem(PROFILE_PROBE_KEY, profile);
  } catch {
    // Best-effort cache; a fresh probe per page load is acceptable.
  }
  return profile;
}

// Rolling upgrades may omit `capabilities` from /v1/me; normalize to an
// empty list so the Operations console stays hidden instead of the UI
// guessing access from the role (operations doc section 2.1).
function withCapabilities(me: HumanMe): HumanMe {
  return Array.isArray(me.capabilities) ? me : { ...me, capabilities: [] };
}

export function AuthProvider({ children }: { children: ReactNode }) {
  const [state, setState] = useState<AuthState>({ kind: "loading" });

  const refresh = useCallback(async () => {
    try {
      const me = withCapabilities(await getMe());
      if (!me.membership_id) {
        // No-membership needs the profile to choose between the saas
        // onboarding page and the on-prem bootstrap entry page.
        const profile = await probeProfile();
        setState({ kind: "no-membership", me, profile });
        return;
      }
      setState(classify(me));
    } catch (err) {
      if (apiError(err, 501)) {
        setState({ kind: "not-configured" });
      } else if (apiError(err, 401)) {
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
