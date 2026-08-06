import { useEffect, useRef } from "react";
import { BrowserRouter, Navigate, Route, Routes, useLocation, useNavigate } from "react-router-dom";
import { AuthProvider, useAuth } from "./auth/AuthContext";
import { ErrorBoundary } from "./components/ErrorBoundary";
import { ToastProvider } from "./components/Toasts";
import { peekPendingInvitation, peekReturnUrl, takeReturnUrl } from "./lib/continuations";
import { LoginPage } from "./pages/LoginPage";
import { NotConfiguredPage } from "./pages/NotConfiguredPage";
import { BootstrapPage } from "./pages/BootstrapPage";
import { JoinPage } from "./pages/JoinPage";
import { WelcomePage } from "./pages/WelcomePage";
import { SuspendedPage } from "./pages/SuspendedPage";
import { AppShell } from "./app/AppShell";
import { PortalRoutes } from "./app/routes";

/**
 * After the OIDC round trip the backend always lands on the fixed
 * TEAM_MEMORY_PORTAL_URL. Restore the continuation exactly once: a pending
 * invitation wins over a plain return_url, and the two never mix (doc 4).
 */
function ContinuationRedirect() {
  const { state } = useAuth();
  const navigate = useNavigate();
  const done = useRef(false);

  useEffect(() => {
    if (done.current) return;
    if (state.kind !== "active" && state.kind !== "no-membership" && state.kind !== "suspended") {
      return;
    }
    done.current = true;
    if (peekPendingInvitation()) {
      navigate("/join", { replace: true });
      return;
    }
    const target = takeReturnUrl();
    const here = window.location.pathname + window.location.search;
    if (target && target !== here) navigate(target, { replace: true });
  }, [state.kind, navigate]);

  return null;
}

/**
 * Catch-all fallback for a no-membership session on any unmatched path
 * (mirrors app/routes.tsx's DefaultRedirect for the active-state shell, and
 * exists for the same reason: ContinuationRedirect above also mounts this
 * render and fires its own navigate() in the same commit, so redirecting
 * here unconditionally would sometimes win that race and strand a pending
 * invitation / return_url restore behind the plain /welcome landing page.
 */
function NoMembershipRedirect() {
  const location = useLocation();
  if (peekPendingInvitation()) return null;
  const here = location.pathname + location.search;
  const target = peekReturnUrl();
  if (target && target !== here) return null;
  return <Navigate to="/welcome" replace />;
}

function AppRoutes() {
  const { state } = useAuth();

  switch (state.kind) {
    case "loading":
      return (
        <div className="center-page">
          <p className="muted">Loading…</p>
        </div>
      );
    case "not-configured":
      return <NotConfiguredPage />;
  }

  return (
    <Routes>
      {/* /join must stay reachable while unauthenticated and while the user
          has no membership yet; the page branches on auth state itself. */}
      <Route path="/join" element={<JoinPage />} />
      {state.kind === "unauthenticated" && <Route path="*" element={<LoginPage />} />}
      {state.kind === "no-membership" && (
        <>
          {/* Bootstrap is a one-time, high-risk claim: it stays a directly
              reachable, linkable URL of its own instead of folding into
              /welcome (doc section 5.1). Onprem only — saas has no bootstrap
              concept. */}
          {state.profile === "onprem" && <Route path="/bootstrap" element={<BootstrapPage />} />}
          <Route path="/welcome" element={<WelcomePage />} />
          <Route path="*" element={<NoMembershipRedirect />} />
        </>
      )}
      {state.kind === "suspended" && <Route path="*" element={<SuspendedPage />} />}
      {state.kind === "active" && (
        <Route
          path="*"
          element={
            <AppShell me={state.me}>
              <PortalRoutes me={state.me} />
            </AppShell>
          }
        />
      )}
    </Routes>
  );
}

export default function App() {
  // Outermost boundary: even a shell-level render failure leaves a safe
  // recovery page instead of a blank document (narrower boundaries live in
  // AppShell and Modal).
  return (
    <ErrorBoundary region="app" fullPage>
      <ToastProvider>
        <AuthProvider>
          <BrowserRouter>
            <ContinuationRedirect />
            <AppRoutes />
          </BrowserRouter>
        </AuthProvider>
      </ToastProvider>
    </ErrorBoundary>
  );
}
