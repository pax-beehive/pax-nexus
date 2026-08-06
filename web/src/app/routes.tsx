import { Navigate, Route, Routes, useLocation } from "react-router-dom";
import type { HumanMe } from "../api/types";
import { can, hasServerCapability, type Capability } from "../lib/capabilities";
import { peekPendingInvitation, peekReturnUrl } from "../lib/continuations";
import { hasTeams } from "../lib/teams";
import { LegacyRedirect } from "./LegacyRedirect";
import { LEGACY_ROUTES } from "./legacyRoutes";
import { landingPath } from "./navModel";

import { MyAgentsPage } from "../pages/MyAgentsPage";
import { AgentDetailPage } from "../pages/AgentDetailPage";
import { AdminAgentsPage } from "../pages/AdminAgentsPage";
import { AdminAgentDetailPage } from "../pages/AdminAgentDetailPage";
import { AdminMembersPage } from "../pages/AdminMembersPage";
import { AdminInvitationsPage } from "../pages/AdminInvitationsPage";
import { AdminDevicesPage } from "../pages/AdminDevicesPage";
import { AdminDeviceDetailPage } from "../pages/AdminDeviceDetailPage";
import { AdminAuditPage } from "../pages/AdminAuditPage";
import { AdminSessionAuditPage } from "../pages/AdminSessionAuditPage";
import { AdminOperationsPage } from "../pages/AdminOperationsPage";
import { OverviewPage } from "../pages/OverviewPage";
import { AdminExplorerPage } from "../pages/AdminExplorerPage";
import { AdminTeamNoteDetailPage } from "../pages/AdminTeamNoteDetailPage";
import { WikiBrowsePage } from "../pages/WikiBrowsePage";
import { TodoPage } from "../pages/TodoPage";
import { WikiStatusPage } from "../pages/WikiStatusPage";
import { TeamSettingsPage } from "../pages/TeamSettingsPage";
import { AppearancePage } from "../pages/settings/AppearancePage";
import { OnboardingPage } from "../pages/OnboardingPage";

/** 角色矩阵门控；无权限一律回落地页，页面不挂载 = 不发请求。 */
function RequireRole({
  me,
  cap,
  children,
}: {
  me: HumanMe;
  cap: Capability;
  children: JSX.Element;
}) {
  if (!can(me.role, cap)) return <Navigate to={landingPath(me)} replace />;
  return children;
}

/** 服务端能力门控（Operations / Explorer）。 */
function RequireCapability({
  me,
  capability,
  children,
}: {
  me: HumanMe;
  capability: string;
  children: JSX.Element;
}) {
  if (!hasServerCapability(me, capability)) return <Navigate to={landingPath(me)} replace />;
  return children;
}

/**
 * Catch-all fallback for unmatched paths. Mirrors the identity doc's
 * DefaultRedirect (section 4): while App.tsx's ContinuationRedirect has a
 * pending invitation or return_url still to restore, this yields (renders
 * nothing) instead of racing it to the first `replace` — both mount and
 * fire their effects in the same commit, so redirecting here unconditionally
 * would sometimes win that race and strand the user on the plain landing
 * page instead of their saved continuation.
 */
function DefaultRedirect({ me }: { me: HumanMe }) {
  const location = useLocation();
  if (peekPendingInvitation()) return null;
  const here = location.pathname + location.search;
  const target = peekReturnUrl();
  if (target && target !== here) return null;
  return <Navigate to={landingPath(me)} replace />;
}

export function PortalRoutes({ me }: { me: HumanMe }) {
  const adminLike = can(me.role, "view.members");

  return (
    <Routes>
      {/* 旧路由：整表挂重定向，逐条由 tests/legacy-routes.test.ts 覆盖。 */}
      {LEGACY_ROUTES.map((route) => (
        <Route key={route.from} path={route.from} element={<LegacyRedirect />} />
      ))}

      <Route path="/onboarding" element={<OnboardingPage />} />

      <Route
        path="/overview"
        element={
          <RequireCapability me={me} capability="view.operations">
            <OverviewPage />
          </RequireCapability>
        }
      />

      {/*
        Management 根节点对所有角色都是「我的访问」视图（spec §"阶段 3 ·
        Management"：根节点是本人）。阶段 3 用真正的 AccessTree 替换它。
        这里**不能**按角色顶替成 AdminAgentsPage —— 注册个人 Agent 的入口
        （+ Create Agent）只存在于 MyAgentsPage，一旦按 adminLike 分派，
        owner/admin 在整个门户里就没有任何地方能注册自己的 Agent。
        团队全量列表留在 /management/agents，由二级导航进入。
      */}
      <Route path="/management" element={<MyAgentsPage />} />
      <Route
        path="/management/members"
        element={
          <RequireRole me={me} cap="view.members">
            <AdminMembersPage me={me} />
          </RequireRole>
        }
      />
      <Route
        path="/management/invitations"
        element={
          <RequireRole me={me} cap="invite.member">
            <AdminInvitationsPage me={me} />
          </RequireRole>
        }
      />
      <Route
        path="/management/agents"
        element={
          <RequireRole me={me} cap="view.all-agents">
            <AdminAgentsPage me={me} />
          </RequireRole>
        }
      />
      {/* 阶段 4 合并成一个 scope 自适应页面；现在按角色分派。 */}
      <Route
        path="/management/agents/:agentId"
        element={adminLike ? <AdminAgentDetailPage me={me} /> : <AgentDetailPage />}
      />
      <Route
        path="/management/devices"
        element={
          <RequireRole me={me} cap="view.devices">
            <AdminDevicesPage />
          </RequireRole>
        }
      />
      <Route
        path="/management/devices/:credentialId"
        element={
          <RequireRole me={me} cap="view.devices">
            <AdminDeviceDetailPage />
          </RequireRole>
        }
      />

      <Route
        path="/governance/audit"
        element={
          <RequireRole me={me} cap="view.audit">
            <AdminAuditPage />
          </RequireRole>
        }
      />
      <Route
        path="/governance/sessions"
        element={
          <RequireRole me={me} cap="view.audit">
            <AdminSessionAuditPage />
          </RequireRole>
        }
      />
      <Route
        path="/governance/pipeline"
        element={
          <RequireCapability me={me} capability="view.operations">
            <AdminOperationsPage
              canInspectTeamMemory={hasServerCapability(me, "view.team-memory")}
            />
          </RequireCapability>
        }
      />
      <Route
        path="/governance/memory"
        element={
          <RequireCapability me={me} capability="view.team-memory">
            <AdminExplorerPage />
          </RequireCapability>
        }
      />
      <Route
        path="/governance/memory/:noteId"
        element={
          <RequireCapability me={me} capability="view.team-memory">
            <AdminTeamNoteDetailPage />
          </RequireCapability>
        }
      />

      <Route path="/apps/wiki" element={<WikiBrowsePage />} />
      <Route path="/apps/wiki/:slug" element={<WikiBrowsePage />} />
      <Route path="/apps/todos" element={<TodoPage />} />

      {hasTeams(me) && <Route path="/settings/team" element={<TeamSettingsPage me={me} />} />}
      <Route path="/settings/memory" element={<WikiStatusPage me={me} />} />
      {/* 阶段 6 把 LLM 用量从 WikiStatusPage 拆出来独立成页。 */}
      <Route path="/settings/usage" element={<WikiStatusPage me={me} />} />
      <Route path="/settings/appearance" element={<AppearancePage />} />

      <Route path="*" element={<DefaultRedirect me={me} />} />
    </Routes>
  );
}
