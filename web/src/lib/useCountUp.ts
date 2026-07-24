// Count-up animation for live counters (Team Pulse page): when the target
// value changes, the displayed number eases from the previous displayed value
// to the new target over a short requestAnimationFrame interpolation. No
// dependency; honors prefers-reduced-motion by jumping straight to the target.

import { useEffect, useRef, useState } from "react";

const DURATION_MS = 700;

function prefersReducedMotion(): boolean {
  return (
    typeof window !== "undefined" &&
    typeof window.matchMedia === "function" &&
    window.matchMedia("(prefers-reduced-motion: reduce)").matches
  );
}

/** Ease-out cubic: fast start, gentle landing on the final value. */
function easeOut(t: number): number {
  return 1 - Math.pow(1 - t, 3);
}

/**
 * Returns the animated display value for `target`. The first render shows the
 * target immediately (no count-up from zero on mount); later changes animate.
 */
export function useCountUp(target: number): number {
  const [display, setDisplay] = useState(target);
  const displayRef = useRef(target);
  const frameRef = useRef<number | undefined>();

  useEffect(() => {
    const from = displayRef.current;
    if (from === target || prefersReducedMotion()) {
      displayRef.current = target;
      setDisplay(target);
      return;
    }
    const started = performance.now();
    const step = (now: number) => {
      const t = Math.min(1, (now - started) / DURATION_MS);
      const value = Math.round(from + (target - from) * easeOut(t));
      displayRef.current = value;
      setDisplay(value);
      if (t < 1) {
        frameRef.current = requestAnimationFrame(step);
      } else {
        displayRef.current = target;
        setDisplay(target);
      }
    };
    frameRef.current = requestAnimationFrame(step);
    return () => {
      if (frameRef.current !== undefined) cancelAnimationFrame(frameRef.current);
      frameRef.current = undefined;
    };
  }, [target]);

  return display;
}
