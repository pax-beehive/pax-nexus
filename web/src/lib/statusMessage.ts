// Central HTTP status -> UI behavior mapping (integration doc section 9).
// Stable error codes select precise recovery; the diagnostic message is never
// string-matched. A 429 surfaces the Retry-After hint parsed by the client;
// automatic retries stay forbidden.

import { ApiError } from "../api/client";

export type NoticeKind = "ok" | "warn" | "bad";

export interface Notice {
  kind: NoticeKind;
  message: string;
}

export function noticeForError(err: unknown, opts?: { conflict?: string }): Notice {
  if (err instanceof ApiError) {
    if (err.code === "last_active_owner") {
      return { kind: "bad", message: "必须先提升另一位 active Owner" };
    }
    if (err.code === "agent_id_conflict") {
      return { kind: "warn", message: "这个 Agent ID 已存在，请换一个 ID" };
    }
    if (err.code === "idempotency_conflict") {
      return { kind: "bad", message: "该操作标识已用于不同请求，请重新发起操作" };
    }
    if (err.code === "invalid_state_transition") {
      return { kind: "warn", message: "当前状态不允许执行此操作，请刷新后确认" };
    }
    if (err.code === "storage_not_available") {
      return { kind: "warn", message: "Storage 统计暂不可用，请稍后重试" };
    }
    switch (err.status) {
      case 400:
        return { kind: "warn", message: "请求格式有误，请检查输入后重试" };
      case 401:
        return { kind: "warn", message: "登录状态已失效，请重新登录" };
      case 403:
        return { kind: "bad", message: "没有权限执行此操作；若角色刚被调整，请刷新后重试" };
      case 404:
        return { kind: "warn", message: "目标资源不存在或不可见" };
      case 409:
        return { kind: "warn", message: opts?.conflict ?? "数据已被他人修改，请刷新后重试" };
      case 410:
        return { kind: "bad", message: "该凭据已失效（过期、吊销或已使用），请重新签发" };
      case 422:
        return { kind: "warn", message: "输入不合法，请修正后重试" };
      case 429:
        return {
          kind: "warn",
          message:
            err.retryAfterSeconds !== undefined
              ? `请求过于频繁，请 ${err.retryAfterSeconds} 秒后重试`
              : "请求过于频繁，请稍后重试",
        };
      case 500:
        return { kind: "bad", message: "服务端错误，请稍后重试" };
      case 501:
        return { kind: "bad", message: "Human Identity 未配置，请联系运维检查安装配置" };
      case 503:
        return { kind: "bad", message: "服务暂时不可用，请稍后重试" };
      default:
        return { kind: "bad", message: `请求失败（HTTP ${err.status}）` };
    }
  }
  return {
    kind: "bad",
    message: "网络错误；若刚提交了一次性凭据的创建，请先刷新列表确认是否已生成，不要直接重试",
  };
}
