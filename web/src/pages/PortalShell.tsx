import { useState } from "react";
import { matchPath, NavLink, Navigate, Route, Routes, useLocation, useNavigate } from "react-router-dom";
import { useAuth } from "../auth/AuthContext";
import { can, hasServerCapability, type Capability } from "../lib/capabilities";
import { peekPendingInvitation, peekReturnUrl } from "../lib/continuations";
import { RoleBadge } from "../components/Badge";
import { Button } from "../components/Button";
import { THEMES, THEME_LABELS, useTheme, type Theme } from "../lib/theme";
import { ErrorBoundary } from "../components/ErrorBoundary";
import { useToast } from "../components/Toasts";
import type { HumanMe } from "../api/types";
import { MyAgentsPage } from "./MyAgentsPage";
import { AgentDetailPage } from "./AgentDetailPage";
import { AdminMembersPage } from "./AdminMembersPage";
import { AdminInvitationsPage } from "./AdminInvitationsPage";
import { AdminAgentsPage } from "./AdminAgentsPage";
import { AdminAgentDetailPage } from "./AdminAgentDetailPage";
import { AdminDevicesPage } from "./AdminDevicesPage";
import { AdminDeviceDetailPage } from "./AdminDeviceDetailPage";
import { AdminAuditPage } from "./AdminAuditPage";
import { AdminSessionAuditPage } from "./AdminSessionAuditPage";
import { AdminOperationsPage } from "./AdminOperationsPage";
import { AdminPulsePage } from "./AdminPulsePage";
import { AdminExplorerPage } from "./AdminExplorerPage";
import { AdminTeamNoteDetailPage } from "./AdminTeamNoteDetailPage";
import { WikiStatusPage } from "./WikiStatusPage";
import { AppsPage } from "./AppsPage";

function navClass({ isActive }: { isActive: boolean }): string {
  return isActive ? "active" : "";
}

/** Buttons are hidden by role, but the backend still enforces per request. */
function RequireCapability({
  me,
  cap,
  children,
}: {
  me: HumanMe;
  cap: Capability;
  children: JSX.Element;
}) {
  if (!can(me.role, cap)) return <Navigate to="/agents" replace />;
  return children;
}

/**
 * Default redirect for unmatched paths. When a login continuation is still
 * pending (return_url about to be restored, or an invitation moving the user
 * to /join), ContinuationRedirect performs that navigation in the same commit;
 * this catch-all must stay out of the way or its effect would overwrite the
 * restored target (identity doc section 4).
 */
function DefaultRedirect() {
  const location = useLocation();
  if (peekPendingInvitation()) return null;
  const here = location.pathname + location.search;
  const target = peekReturnUrl();
  if (target && target !== here) return null;
  return <Navigate to="/agents" replace />;
}

/**
 * Operations routes key off server-issued capabilities, not the client role
 * matrix (operations doc section 2.1); without the capability the route
 * redirects and the page never mounts, so no Operations request is fired.
 */
function RequireServerCapability({
  me,
  capability,
  children,
}: {
  me: HumanMe;
  capability: string;
  children: JSX.Element;
}) {
  if (!hasServerCapability(me, capability)) return <Navigate to="/agents" replace />;
  return children;
}

const SIDE_COLLAPSED_KEY = "portal.side-collapsed";
const NAV_GROUPS_KEY = "portal.nav-groups";

interface NavItem {
  to: string;
  label: string;
  end?: boolean;
}

interface NavGroupDef {
  id: string;
  label: string;
  items: NavItem[];
}

/** Default open state per nav group; stored toggles merge on top of this. */
const NAV_GROUP_DEFAULTS: Record<string, boolean> = {
  personal: true,
  knowledge: true,
  directory: false,
  fleet: false,
  insights: false,
};

function loadNavGroupState(): Record<string, boolean> {
  try {
    const raw = localStorage.getItem(NAV_GROUPS_KEY);
    const stored = raw ? (JSON.parse(raw) as Record<string, boolean>) : {};
    return { ...NAV_GROUP_DEFAULTS, ...stored };
  } catch {
    // Corrupted JSON falls back to the defaults.
    return { ...NAV_GROUP_DEFAULTS };
  }
}

export function PortalShell({ me }: { me: HumanMe }) {
  const { logout } = useAuth();
  const toast = useToast();
  const navigate = useNavigate();
  const location = useLocation();
  const adminLike = can(me.role, "view.members");
  const [theme, setTheme] = useTheme();
  const [sideCollapsed, setSideCollapsed] = useState(
    () => localStorage.getItem(SIDE_COLLAPSED_KEY) === "1",
  );
  const [groupOpen, setGroupOpen] = useState<Record<string, boolean>>(loadNavGroupState);

  const groups: NavGroupDef[] = [
    {
      id: "personal",
      label: "Personal",
      items: [{ to: "/agents", label: "My Agents", end: true }],
    },
    { id: "knowledge", label: "Knowledge", items: [{ to: "/apps", label: "Apps" }] },
  ];
  if (adminLike) {
    groups.push(
      {
        id: "directory",
        label: "Directory",
        items: [
          { to: "/admin/members", label: "Members" },
          { to: "/admin/invitations", label: "Invitations" },
        ],
      },
      {
        id: "fleet",
        label: "Fleet",
        items: [
          { to: "/admin/agents", label: "All Agents" },
          { to: "/admin/devices", label: "Devices" },
        ],
      },
      {
        id: "insights",
        label: "Insights",
        items: [
          ...(hasServerCapability(me, "view.operations")
            ? [{ to: "/admin/pulse", label: "Pulse" }]
            : []),
          ...(hasServerCapability(me, "view.team-memory")
            ? [{ to: "/admin/explorer", label: "Explorer" }]
            : []),
          { to: "/admin/audit", label: "Audit Events" },
          { to: "/admin/session-audit", label: "Session Audit" },
          ...(hasServerCapability(me, "view.operations")
            ? [{ to: "/admin/operations", label: "Operations" }]
            : []),
        ],
      },
    );
  }

  // The group holding the active route always renders open; this is a render
  // overlay only, the user's stored toggles stay untouched.
  const activeGroupId = groups.find((group) =>
    group.items.some((item) =>
      matchPath({ path: item.to, end: item.end ?? false }, location.pathname),
    ),
  )?.id;

  const isGroupOpen = (id: string): boolean => id === activeGroupId || (groupOpen[id] ?? false);

  const toggleGroup = (id: string) => {
    setGroupOpen((current) => {
      const open = id === activeGroupId || (current[id] ?? false);
      const next = { ...current, [id]: !open };
      localStorage.setItem(NAV_GROUPS_KEY, JSON.stringify(next));
      return next;
    });
  };

  const toggleSide = () => {
    setSideCollapsed((current) => {
      const next = !current;
      localStorage.setItem(SIDE_COLLAPSED_KEY, next ? "1" : "0");
      return next;
    });
  };

  const onLogout = async () => {
    await logout();
    toast("ok", "Signed out");
    navigate("/", { replace: true });
  };

  return (
    <div className="shell">
      <aside className={sideCollapsed ? "side collapsed" : "side"}>
        <div className="side-top">
          {!sideCollapsed && (
            <div className="brand">
              Team Memory <span>Portal</span>
            </div>
          )}
          <button
            className="side-toggle"
            type="button"
            aria-expanded={!sideCollapsed}
            aria-label={sideCollapsed ? "Expand navigation" : "Collapse navigation"}
            onClick={toggleSide}
          >
            {sideCollapsed ? "»" : "«"}
          </button>
        </div>
        {!sideCollapsed && (
          <>
        <nav className="nav" aria-label="Portal navigation">
          {groups.map((group) => {
            const open = isGroupOpen(group.id);
            return (
              <div className="nav-group" key={group.id}>
                <button
                  type="button"
                  className="nav-label nav-group-toggle"
                  aria-expanded={open}
                  onClick={() => toggleGroup(group.id)}
                >
                  <span>{group.label}</span>
                  <span className="nav-chevron" aria-hidden="true">
                    ▾
                  </span>
                </button>
                {open &&
                  group.items.map((item) => (
                    <NavLink key={item.to} to={item.to} className={navClass} end={item.end}>
                      {item.label}
                    </NavLink>
                  ))}
              </div>
            );
          })}
        </nav>
        <div className="side-foot">
          <div className="theme-picker">
            <select
              aria-label="Theme"
              value={theme}
              onChange={(event) => setTheme(event.target.value as Theme)}
            >
              {THEMES.map((t) => (
                <option key={t} value={t}>
                  {THEME_LABELS[t]} theme
                </option>
              ))}
            </select>
          </div>
          <div className="small">{me.email ?? me.user_id}</div>
          <div className="row between" style={{ marginTop: 4 }}>
            <RoleBadge role={me.role ?? "member"} />
            <Button variant="ghost" size="sm" onClick={() => void onLogout()}>
              Sign out
            </Button>
          </div>
        </div>
          </>
        )}
      </aside>
      <main className="main">
        {/* Route-level boundary: a failing route keeps the shell and nav
            usable. Keying by pathname remounts the boundary on navigation,
            so moving to another route always recovers the content area. */}
        <ErrorBoundary
          key={location.pathname}
          region="route"
          escapeLabel="Back to My Agents"
          onEscape={() => navigate("/agents")}
        >
          <Routes>
            <Route path="/agents" element={<MyAgentsPage />} />
            <Route path="/agents/:agentId" element={<AgentDetailPage />} />
            <Route path="/apps" element={<AppsPage />} />
            <Route path="/wiki" element={<WikiStatusPage me={me} />} />
            <Route
              path="/admin/members"
              element={
                <RequireCapability me={me} cap="view.members">
                  <AdminMembersPage me={me} />
                </RequireCapability>
              }
            />
            <Route
              path="/admin/invitations"
              element={
                <RequireCapability me={me} cap="invite.member">
                  <AdminInvitationsPage me={me} />
                </RequireCapability>
              }
            />
            <Route
              path="/admin/agents"
              element={
                <RequireCapability me={me} cap="view.all-agents">
                  <AdminAgentsPage me={me} />
                </RequireCapability>
              }
            />
            <Route
              path="/admin/agents/:agentId"
              element={
                <RequireCapability me={me} cap="view.all-agents">
                  <AdminAgentDetailPage me={me} />
                </RequireCapability>
              }
            />
            <Route
              path="/admin/devices"
              element={
                <RequireCapability me={me} cap="view.devices">
                  <AdminDevicesPage />
                </RequireCapability>
              }
            />
            <Route
              path="/admin/devices/:credentialId"
              element={
                <RequireCapability me={me} cap="view.devices">
                  <AdminDeviceDetailPage />
                </RequireCapability>
              }
            />
            <Route
              path="/admin/audit"
              element={
                <RequireCapability me={me} cap="view.audit">
                  <AdminAuditPage />
                </RequireCapability>
              }
            />
            <Route
              path="/admin/session-audit"
              element={
                <RequireCapability me={me} cap="view.audit">
                  <AdminSessionAuditPage />
                </RequireCapability>
              }
            />
            <Route
              path="/admin/operations"
              element={
                <RequireServerCapability me={me} capability="view.operations">
                  <AdminOperationsPage
                    canInspectTeamMemory={hasServerCapability(me, "view.team-memory")}
                  />
                </RequireServerCapability>
              }
            />
            <Route
              path="/admin/pulse"
              element={
                <RequireServerCapability me={me} capability="view.operations">
                  <AdminPulsePage />
                </RequireServerCapability>
              }
            />
            <Route
              path="/admin/explorer"
              element={
                <RequireServerCapability me={me} capability="view.team-memory">
                  <AdminExplorerPage />
                </RequireServerCapability>
              }
            />
            <Route
              path="/admin/explorer/notes/:noteId"
              element={
                <RequireServerCapability me={me} capability="view.team-memory">
                  <AdminTeamNoteDetailPage />
                </RequireServerCapability>
              }
            />
            <Route path="*" element={<DefaultRedirect />} />
          </Routes>
        </ErrorBoundary>
      </main>
    </div>
  );
}
