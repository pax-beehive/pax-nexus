// Recoverable render-error boundaries. Three granularity levels are wired:
// an outer app boundary rendering a safe full page, a per-route boundary
// keeping the shell and navigation usable, and a boundary inside each modal
// keeping the page behind it alive (see App.tsx, PortalShell.tsx, Modal.tsx).
//
// Logging is deliberately minimal: only the error name and message. Props,
// state, component stacks and request data are never logged because they may
// carry invitation tokens, enrollment secrets, credentials, request bodies,
// or raw query/hit content.

import { Component, type ReactNode } from "react";

interface ErrorBoundaryProps {
  /** Short region name used in the log line ("app", "route", "modal"). */
  region: string;
  /**
   * Escape action rendered next to Retry: a safe-page navigation for routes,
   * onClose for modals.
   */
  escapeLabel?: string;
  onEscape?: () => void;
  /** Render the fallback as a full safe page (outermost boundary only). */
  fullPage?: boolean;
  children: ReactNode;
}

interface ErrorBoundaryState {
  error?: Error;
}

export class ErrorBoundary extends Component<ErrorBoundaryProps, ErrorBoundaryState> {
  state: ErrorBoundaryState = {};

  static getDerivedStateFromError(error: Error): ErrorBoundaryState {
    return { error };
  }

  componentDidCatch(error: Error): void {
    // Name and message only; never props, state, stacks or payloads.
    console.error(`[portal] ${this.props.region} render error: ${error.name}: ${error.message}`);
  }

  private reset = (): void => {
    this.setState({ error: undefined });
  };

  render(): ReactNode {
    if (!this.state.error) return this.props.children;
    if (this.props.fullPage) {
      return (
        <div className="center-page">
          <div className="center-box card" role="alert">
            <h1>This page failed to render</h1>
            <p className="muted">
              The interface hit an unexpected error; no changes were made. Reloading usually
              recovers it — if the problem persists, contact your administrator.
            </p>
            <button className="btn primary" onClick={() => window.location.reload()}>
              Reload
            </button>
          </div>
        </div>
      );
    }
    return (
      <div className="card" role="alert">
        <h2 style={{ marginTop: 0 }}>This section failed to render</h2>
        <p className="muted small">
          An unexpected error occurred in this section; the rest of the page is unaffected.
          You can retry or leave this section.
        </p>
        <div className="row">
          <button className="btn sm" onClick={this.reset}>
            Retry
          </button>
          {this.props.onEscape && (
            <button className="btn ghost sm" onClick={this.props.onEscape}>
              {this.props.escapeLabel ?? "Close"}
            </button>
          )}
        </div>
      </div>
    );
  }
}
