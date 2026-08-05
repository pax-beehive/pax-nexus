import { Navigate, useLocation } from "react-router-dom";
import { resolveLegacy } from "./legacyRoutes";

/**
 * 挂在每条旧路径上：把当前 location 翻译成新路径并 replace 跳转。
 * 表里查不到（理论上不会发生）就落到 /management，不留白屏。
 */
export function LegacyRedirect() {
  const location = useLocation();
  const target = resolveLegacy(location.pathname, location.search) ?? "/management";
  return <Navigate to={target} replace />;
}
