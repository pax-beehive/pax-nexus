import { useState } from "react";
import { NavLink, Navigate, Route, Routes, useLocation, useNavigate } from "react-router-dom";
import { useAuth } from "../auth/AuthContext";
import { can, hasServerCapability, type Capability } from "../lib/capabilities";
import { peekPendingInvitation, peekReturnUrl } from "../lib/continuations";
import { RoleBadge } from "../components/Badge";
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
import { AdminOperationsPage } from "./AdminOperationsPage";
import { AdminPulsePage } from "./AdminPulsePage";
import { AdminExplorerPage } from "./AdminExplorerPage";
import { AdminTeamNoteDetailPage } from "./AdminTeamNoteDetailPage";
import { WikiPage } from "./WikiPage";

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

export function PortalShell({ me }: { me: HumanMe }) {
  const { logout } = useAuth();
  const toast = useToast();
  const navigate = useNavigate();
  const location = useLocation();
  const adminLike = can(me.role, "view.members");
  const [sideCollapsed, setSideCollapsed] = useState(
    () => localStorage.getItem(SIDE_COLLAPSED_KEY) === "1",
  );

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
          <div className="nav-label">Personal</div>
          <NavLink to="/agents" className={navClass} end>
            My Agents
          </NavLink>
          <div className="nav-label">Knowledge</div>
          <NavLink to="/wiki" className={navClass} end>
            Wiki
          </NavLink>
          {adminLike && (
            <>
              <div className="nav-label">Directory</div>
              <NavLink to="/admin/members" className={navClass}>
                Members
              </NavLink>
              <NavLink to="/admin/invitations" className={navClass}>
                Invitations
              </NavLink>
              <div className="nav-label">Fleet</div>
              <NavLink to="/admin/agents" className={navClass}>
                All Agents
              </NavLink>
              <NavLink to="/admin/devices" className={navClass}>
                Devices
              </NavLink>
              <div className="nav-label">Insights</div>
              {hasServerCapability(me, "view.operations") && (
                <NavLink to="/admin/pulse" className={navClass}>
                  Pulse
                </NavLink>
              )}
              {hasServerCapability(me, "view.team-memory") && (
                <NavLink to="/admin/explorer" className={navClass}>
                  Explorer
                </NavLink>
              )}
              <NavLink to="/admin/audit" className={navClass}>
                Audit Events
              </NavLink>
              {hasServerCapability(me, "view.operations") && (
                <NavLink to="/admin/operations" className={navClass}>
                  Operations
                </NavLink>
              )}
            </>
          )}
        </nav>
        <div className="side-foot">
          <div className="small">{me.email ?? me.user_id}</div>
          <div className="row between" style={{ marginTop: 4 }}>
            <RoleBadge role={me.role ?? "member"} />
            <button className="btn ghost sm" onClick={() => void onLogout()}>
              Sign out
            </button>
          </div>
        </div>
          </>
        )}
      </aside>
      <main className={location.pathname === "/wiki" ? "main main-wide" : "main"}>
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
            <Route path="/wiki" element={<WikiPage me={me} />} />
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
