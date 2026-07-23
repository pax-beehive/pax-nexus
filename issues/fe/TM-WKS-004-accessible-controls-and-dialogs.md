# TM-WKS-004: Give filters, refresh actions, and modals unambiguous semantics

- Area: frontend
- Priority: P2
- Status: open
- Source: [2026-07-22 workstation exploration](../../docs/workstation-portal-exploration-2026-07-22.md)

## Problem

Several controls cannot be distinguished reliably by keyboard users,
assistive technology, or browser tests.

Observed examples:

- Members renders separate status and role filter groups whose first buttons
  are both named `all`; neither group has a label.
- All Agents renders the Owner filter `<select>` without an accessible name.
- Operations Recent activity renders operation and outcome `<select>` controls
  without accessible names.
- Operations has separate Storage and Recent activity buttons both named
  `刷新`.
- the shared `Modal` component does not expose dialog semantics, manage focus,
  close on Escape, or restore focus to the trigger.

## Relevant code

- `web/src/pages/AdminMembersPage.tsx`
- `web/src/pages/AdminAgentsPage.tsx`
- `web/src/pages/AdminOperationsPage.tsx`
- `web/src/components/Modal.tsx`

## Acceptance criteria

- Each filter group has a visible or programmatic label, such as member status
  and member role.
- Every `<select>` has a unique associated `<label>` or `aria-label`.
- Same-label actions are distinguishable by region, for example `刷新存储` and
  `刷新最近活动`.
- Selected filter buttons expose state with `aria-pressed`, tabs semantics, or
  another appropriate accessible pattern.
- `Modal` exposes `role="dialog"`, `aria-modal="true"`, and an accessible name
  tied to its title.
- Opening a modal moves focus into it, Tab remains within it, Escape closes it,
  and close restores focus to the trigger.
- Automated accessibility tests cover Members, All Agents, Operations, and at
  least one shared modal.
