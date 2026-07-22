import { NavLink, Navigate, Route, Routes, useNavigate } from "react-router-dom";
import { useAuth } from "../auth/AuthContext";
import { can, type Capability } from "../lib/capabilities";
import { RoleBadge } from "../components/Badge";
import { useToast } from "../components/Toasts";
import type { HumanMe } from "../api/types";
import { MyAgentsPage } from "./MyAgentsPage";
import { AgentDetailPage } from "./AgentDetailPage";
import { AdminMembersPage } from "./AdminMembersPage";
import { AdminInvitationsPage } from "./AdminInvitationsPage";
import { AdminAgentsPage } from "./AdminAgentsPage";
import { AdminAgentDetailPage } from "./AdminAgentDetailPage";
import { AdminAuditPage } from "./AdminAuditPage";

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

export function PortalShell({ me }: { me: HumanMe }) {
  const { logout } = useAuth();
  const toast = useToast();
  const navigate = useNavigate();
  const adminLike = can(me.role, "view.members");

  const onLogout = async () => {
    await logout();
    toast("ok", "已退出登录");
    navigate("/", { replace: true });
  };

  return (
    <div className="shell">
      <aside className="side">
        <div className="brand">
          Team Memory <span>Portal</span>
        </div>
        <nav className="nav">
          <div className="nav-label">Personal</div>
          <NavLink to="/agents" className={navClass} end>
            My Agents
          </NavLink>
          {adminLike && (
            <>
              <div className="nav-label">Admin Console</div>
              <NavLink to="/admin/members" className={navClass}>
                Members
              </NavLink>
              <NavLink to="/admin/invitations" className={navClass}>
                Invitations
              </NavLink>
              <NavLink to="/admin/agents" className={navClass}>
                All Agents
              </NavLink>
              <NavLink to="/admin/audit" className={navClass}>
                Audit Events
              </NavLink>
            </>
          )}
        </nav>
        <div className="side-foot">
          <div className="small">{me.email ?? me.user_id}</div>
          <div className="row between" style={{ marginTop: 4 }}>
            <RoleBadge role={me.role ?? "member"} />
            <button className="btn ghost sm" onClick={() => void onLogout()}>
              退出
            </button>
          </div>
        </div>
      </aside>
      <main className="main">
        <Routes>
          <Route path="/agents" element={<MyAgentsPage />} />
          <Route path="/agents/:agentId" element={<AgentDetailPage />} />
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
            path="/admin/audit"
            element={
              <RequireCapability me={me} cap="view.audit">
                <AdminAuditPage />
              </RequireCapability>
            }
          />
          <Route path="*" element={<Navigate to="/agents" replace />} />
        </Routes>
      </main>
    </div>
  );
}
