import { useEffect, useReducer } from "react";

/** Live mm:ss countdown to an ISO timestamp; renders 已过期 past the deadline. */
export function Countdown({ to }: { to: string }) {
  const [, tick] = useReducer((x: number) => x + 1, 0);
  useEffect(() => {
    const timer = setInterval(tick, 1000);
    return () => clearInterval(timer);
  }, []);

  const left = new Date(to).getTime() - Date.now();
  if (Number.isNaN(left)) return <span className="countdown">—</span>;
  if (left <= 0) return <span className="countdown danger-text">已过期</span>;
  const m = Math.floor(left / 60000);
  const s = Math.floor((left % 60000) / 1000);
  return (
    <span className="countdown">
      {m}:{String(s).padStart(2, "0")}
    </span>
  );
}
