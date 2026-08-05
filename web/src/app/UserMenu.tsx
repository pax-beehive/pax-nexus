// 顶栏右侧的用户菜单。设计稿的顶栏只画了邮箱 + 角色徽标，没有给登出留位置；
// 登出必须存在，所以这里把它做成一个菜单：身份信息 + Appearance 快捷 + Sign out。

import { useEffect, useRef, useState } from "react";
import { Link, useNavigate } from "react-router-dom";
import { useAuth } from "../auth/AuthContext";
import { RoleBadge } from "../components/Badge";
import { Button } from "../components/Button";
import { useToast } from "../components/Toasts";
import type { HumanMe } from "../api/types";

export function UserMenu({ me }: { me: HumanMe }) {
  const { logout } = useAuth();
  const toast = useToast();
  const navigate = useNavigate();
  const [open, setOpen] = useState(false);
  const wrapRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") setOpen(false);
    };
    const onPointerDown = (event: MouseEvent) => {
      if (!wrapRef.current?.contains(event.target as Node)) setOpen(false);
    };
    document.addEventListener("keydown", onKeyDown);
    document.addEventListener("mousedown", onPointerDown);
    return () => {
      document.removeEventListener("keydown", onKeyDown);
      document.removeEventListener("mousedown", onPointerDown);
    };
  }, [open]);

  const signOut = async () => {
    setOpen(false);
    await logout();
    toast("ok", "Signed out");
    navigate("/", { replace: true });
  };

  const label = me.email ?? me.user_id;

  return (
    <div className="topbar-cell topbar-user" ref={wrapRef} style={{ position: "relative" }}>
      <Button
        variant="ghost"
        aria-haspopup="menu"
        aria-expanded={open}
        onClick={() => setOpen((current) => !current)}
      >
        <span className="small">{label}</span>
        <RoleBadge role={me.role ?? "member"} />
      </Button>
      {open && (
        <div className="menu-pop" role="menu" aria-label="Account">
          <div className="menu-head">Signed in as {label}</div>
          <Link role="menuitem" to="/settings/appearance" onClick={() => setOpen(false)}>
            Appearance
          </Link>
          <button role="menuitem" type="button" onClick={() => void signOut()}>
            Sign out
          </button>
        </div>
      )}
    </div>
  );
}
