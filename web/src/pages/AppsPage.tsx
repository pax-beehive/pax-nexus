import { Link } from "react-router-dom";

/**
 * App launcher: the entry point for the portal's full-screen apps (wiki,
 * todos) plus links to per-app configuration. Intentionally data-free — it is
 * pure navigation, so it renders instantly and cannot fail on a backend call.
 */
export function AppsPage() {
  return (
    <div>
      <div className="page-head">
        <div>
          <h1>Apps</h1>
          <p className="muted flush">Tools built on your team memory.</p>
        </div>
      </div>
      <div className="app-grid">
        <Link className="card app-card" to="/wiki/browse">
          <span className="app-logo wiki" aria-hidden="true">
            W
          </span>
          <div>
            <h3>Wiki</h3>
            <p className="muted small">Read the knowledge the team has accumulated.</p>
          </div>
        </Link>
        <Link className="card app-card" to="/todo">
          <span className="app-logo todo" aria-hidden="true">
            T
          </span>
          <div>
            <h3>Todos</h3>
            <p className="muted small">Track work, and act on team-memory suggestions.</p>
          </div>
        </Link>
      </div>
      <h2 className="section">App settings</h2>
      <div className="app-grid">
        <Link className="card app-card" to="/wiki">
          <div>
            <h3>Wiki policy</h3>
            <p className="muted small">
              Language, custom instructions, and ingestion for the LLM wiki.
            </p>
          </div>
        </Link>
      </div>
    </div>
  );
}
