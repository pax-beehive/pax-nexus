import { useRef, useState } from "react";
import { useNavigate, useSearchParams } from "react-router-dom";
import { apiError } from "../api/client";
import { acceptInvitation, beginAction, createTeam, switchCurrentTeam } from "../api/actions";
import { useAuth } from "../auth/AuthContext";
import { deriveSlug } from "../lib/teams";
import { noticeForError } from "../lib/statusMessage";
import { Button } from "../components/Button";
import { useToast } from "../components/Toasts";

/**
 * SaaS onboarding (design/m3-teams 01): create a team or join one with an
 * invitation. Rendered as the catch-all route for a saas no-membership
 * session, and as /onboarding for active sessions arriving from the team
 * switcher (a signed-in user may belong to multiple teams). The on-prem
 * profile keeps the existing EntryPage (bootstrap claim) instead.
 *
 * Creating a team does not re-scope the session server-side, so the create
 * flow immediately switches the current team to the new one before
 * refreshing the auth state.
 */

type Mode = "create" | "join";

/** Accept a raw token or a full join link (`.../join#invite=<token>`). */
function parseInvitationInput(raw: string): string {
  const trimmed = raw.trim();
  const match = trimmed.match(/#invite=([^\s&]+)/);
  return match ? match[1] : trimmed;
}

export function OnboardingPage() {
  const { state, refresh, logout, handleUnauthorized } = useAuth();
  const toast = useToast();
  const navigate = useNavigate();
  const [searchParams] = useSearchParams();
  const me = state.kind === "no-membership" || state.kind === "active" ? state.me : undefined;

  const [mode, setMode] = useState<Mode>(searchParams.get("mode") === "join" ? "join" : "create");
  const [name, setName] = useState("");
  const [token, setToken] = useState("");
  const [busy, setBusy] = useState(false);
  const [formError, setFormError] = useState<string | undefined>();
  // One Idempotency-Key per action instance; a retry after a network failure
  // replays safely, a new attempt gets a fresh key (doc section 3.3).
  const createKeyRef = useRef<string | undefined>(undefined);
  const joinKeyRef = useRef<string | undefined>(undefined);

  const slug = deriveSlug(name);

  const submitCreate = async () => {
    const trimmed = name.trim();
    if (!trimmed || busy) return;
    if (!createKeyRef.current) createKeyRef.current = beginAction();
    setBusy(true);
    setFormError(undefined);
    try {
      const team = await createTeam(trimmed, createKeyRef.current);
      await switchCurrentTeam(team.team_id, beginAction());
      toast("ok", `Team ${team.name} created`);
      await refresh();
      navigate("/agents", { replace: true });
    } catch (err) {
      if (apiError(err, 409, "team_slug_conflict")) {
        setFormError("A team with this slug already exists; choose a different name");
        createKeyRef.current = undefined;
      } else if (apiError(err, 401)) {
        handleUnauthorized();
      } else {
        setFormError(noticeForError(err).message);
      }
    } finally {
      setBusy(false);
    }
  };

  const submitJoin = async () => {
    const parsed = parseInvitationInput(token);
    if (!parsed || busy) return;
    if (!joinKeyRef.current) joinKeyRef.current = beginAction();
    setBusy(true);
    setFormError(undefined);
    try {
      await acceptInvitation(parsed, joinKeyRef.current);
      toast("ok", "Joined the team");
      await refresh();
      navigate("/agents", { replace: true });
    } catch (err) {
      if (apiError(err, 410)) {
        // Expired / revoked / used / malformed / email mismatch: one uniform
        // message to avoid leaking invitation details (same rule as JoinPage).
        setFormError(
          "This invitation is invalid (expired, revoked, already used, or issued to a different email). Ask the team owner for a new invitation.",
        );
      } else if (apiError(err, 401)) {
        handleUnauthorized();
      } else {
        setFormError(noticeForError(err).message);
      }
    } finally {
      setBusy(false);
    }
  };

  const signOut = async () => {
    await logout();
    navigate("/", { replace: true });
  };

  return (
    <div className="center-page">
      <div className="center-box">
        <h1 style={{ textAlign: "center" }}>Welcome to Team Memory</h1>
        <p className="muted" style={{ textAlign: "center" }}>
          {state.kind === "active"
            ? "Create another team or join an existing one."
            : "Your account is not a member of any team yet. Create one or join an existing team."}
        </p>
        <div style={{ textAlign: "center", marginBottom: 14 }}>
          <div className="seg" role="group" aria-label="onboarding mode">
            <button
              type="button"
              className={mode === "create" ? "on" : ""}
              aria-pressed={mode === "create"}
              onClick={() => {
                setMode("create");
                setFormError(undefined);
              }}
            >
              Create a team
            </button>
            <button
              type="button"
              className={mode === "join" ? "on" : ""}
              aria-pressed={mode === "join"}
              onClick={() => {
                setMode("join");
                setFormError(undefined);
              }}
            >
              Join with invitation
            </button>
          </div>
        </div>

        {mode === "create" ? (
          <div className="card">
            <h3 style={{ marginTop: 0 }}>Create a team</h3>
            <label htmlFor="team-name">Team name</label>
            <input
              id="team-name"
              type="text"
              value={name}
              maxLength={200}
              onChange={(e) => setName(e.target.value)}
              autoComplete="off"
            />
            <p className="small muted" style={{ margin: "6px 0 0" }}>
              Slug: <code>{slug || "…"}</code> — derived from the team name; used in URLs and API
              scope identifiers. Lowercase letters, digits, and dashes only.
            </p>
            {formError && <div className="note bad">{formError}</div>}
            <div style={{ marginTop: 12 }}>
              <Button variant="primary" disabled={!name.trim() || busy} onClick={() => void submitCreate()}>
                {busy ? "Creating…" : "Create team"}
              </Button>
            </div>
          </div>
        ) : (
          <div className="card">
            <h3 style={{ marginTop: 0 }}>Join with invitation</h3>
            <label htmlFor="invite-token">Invitation token</label>
            <input
              id="invite-token"
              type="text"
              placeholder="tm_invite_inv_01.xxxxxxxx"
              value={token}
              onChange={(e) => setToken(e.target.value)}
              autoComplete="off"
            />
            <p className="small muted" style={{ margin: "6px 0 0" }}>
              Invitation links arrive as <code>https://app.example.com/join#invite=tm_invite_...</code> —
              paste the link or just the token.
            </p>
            {formError && <div className="note bad">{formError}</div>}
            <div style={{ marginTop: 12 }}>
              <Button variant="primary" disabled={!token.trim() || busy} onClick={() => void submitJoin()}>
                {busy ? "Accepting…" : "Accept invitation"}
              </Button>
            </div>
          </div>
        )}

        <div className="note small">
          Accepting an invitation adds you to that team with the invited role. Creating a team makes
          you its <b>owner</b>. You can belong to multiple teams and switch between them later.
        </div>

        {me && (
          <p className="small muted" style={{ textAlign: "center" }}>
            Signed in as {me.email ?? me.user_id} ·{" "}
            <Button variant="ghost" size="sm" onClick={() => void signOut()}>
              Sign out
            </Button>
          </p>
        )}
      </div>
    </div>
  );
}
