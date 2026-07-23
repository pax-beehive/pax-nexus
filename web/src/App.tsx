import { useEffect, useRef } from "react";
import { BrowserRouter, Route, Routes, useNavigate } from "react-router-dom";
import { AuthProvider, useAuth } from "./auth/AuthContext";
import { ErrorBoundary } from "./components/ErrorBoundary";
import { ToastProvider } from "./components/Toasts";
import { peekPendingInvitation, takeReturnUrl } from "./lib/continuations";
import { LoginPage } from "./pages/LoginPage";
import { NotConfiguredPage } from "./pages/NotConfiguredPage";
import { BootstrapPage } from "./pages/BootstrapPage";
import { JoinPage } from "./pages/JoinPage";
import { EntryPage } from "./pages/EntryPage";
import { SuspendedPage } from "./pages/SuspendedPage";
import { PortalShell } from "./pages/PortalShell";

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

function AppRoutes() {
  const { state } = useAuth();

  switch (state.kind) {
    case "loading":
      return (
        <div className="center-page">
          <p className="muted">加载中…</p>
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
          <Route path="/bootstrap" element={<BootstrapPage />} />
          <Route path="*" element={<EntryPage />} />
        </>
      )}
      {state.kind === "suspended" && <Route path="*" element={<SuspendedPage />} />}
      {state.kind === "active" && <Route path="*" element={<PortalShell me={state.me} />} />}
    </Routes>
  );
}

export default function App() {
  // Outermost boundary: even a shell-level render failure leaves a safe
  // recovery page instead of a blank document (narrower boundaries live in
  // PortalShell and Modal).
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
