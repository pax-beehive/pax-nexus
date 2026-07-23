# TM-WKS-003: Add recoverable Portal error boundaries

- Area: frontend
- Priority: P2
- Status: open
- Source: [2026-07-22 workstation exploration](../../docs/workstation-portal-exploration-2026-07-22.md)

## Problem

A render exception in an action form removes the navigation shell and the
entire current page. The Portal has no React error boundary and shows no
recovery action. During exploration, the only recovery from the Create Agent
exception was a full browser reload.

The shell, authenticated identity, and unaffected navigation should remain
usable when one route or modal fails.

## Acceptance criteria

- The Portal has an outer error boundary that renders a safe recovery page
  instead of a blank document.
- Route content and action modals have boundaries narrow enough to preserve the
  shell when only one region fails.
- Recovery UI provides an appropriate retry, close, or return-to-safe-page
  action.
- Closing or recovering from a failed modal restores focus to its trigger.
- Error reporting never includes invitation tokens, enrollment secrets,
  credentials, request bodies, or raw query/hit content.
- Vitest verifies failures in a route and a modal independently.
- A browser smoke test injects a rendering failure and confirms that navigation
  and recovery controls remain usable.

## Dependencies

- TM-WKS-001 should still be fixed at its source. The error boundary is defense
  in depth, not a substitute for correct action-key generation.
