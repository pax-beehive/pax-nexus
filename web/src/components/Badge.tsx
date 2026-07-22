export function Badge({ status }: { status: string }) {
  return <span className={`badge b-${status}`}>{status}</span>;
}

export function RoleBadge({ role }: { role: string }) {
  return <span className="badge b-role">{role}</span>;
}
