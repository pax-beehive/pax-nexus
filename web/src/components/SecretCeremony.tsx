// 一次性密钥仪式：全屏、朱红铺底、两段式关闭。
//
// 与站内其他 Modal 的两处刻意不同：
// 1. 不响应 Escape，不响应点击遮罩——一次性密钥被误触销毁的代价远高于
//    交互一致性。
// 2. 关闭要点两次。第一次只是把底栏切成确认态。
//
// 关闭不取消这条待认领记录：令牌在有效期内仍可兑换，只是不再可见。
// 调用方负责在 onClose 里给出相应提示。
//
// 密钥只存在于 props 与本组件的渲染树中：不写存储、不进 URL、不进日志。

import { useEffect, useId, useRef, useState, type ReactNode } from "react";
import { copyTextToClipboard } from "../lib/clipboard";
import { Button } from "./Button";
import { Countdown } from "./Countdown";
import { useToast } from "./Toasts";

export function SecretCeremony({
  title,
  headline,
  body,
  value,
  valueLabel,
  expiresAt,
  steps,
  recovery,
  meta,
  command,
  onClose,
}: {
  title: string;
  headline: string;
  body: ReactNode;
  value: string;
  valueLabel: string;
  expiresAt?: string;
  steps: string[];
  recovery: string;
  meta?: ReactNode;
  command?: string;
  onClose: () => void;
}) {
  const toast = useToast();
  const headlineId = useId();
  const [confirming, setConfirming] = useState(false);
  const dialogRef = useRef<HTMLDivElement>(null);

  // 打开时把焦点移进来，这样键盘用户不会停在背后的页面上。
  useEffect(() => {
    dialogRef.current?.focus();
  }, []);

  const copy = async (text: string, what: string) => {
    if (await copyTextToClipboard(text)) {
      toast("ok", `${what} copied`);
      return;
    }
    // 剪贴板不可用（权限或非安全上下文）：退回手动复制提示。
    // 密钥仍然不落任何存储。
    window.prompt("Copy it manually:", text);
  };

  return (
    <div className="ceremony">
      <div
        className="ceremony-panel"
        role="dialog"
        aria-modal="true"
        aria-labelledby={headlineId}
        tabIndex={-1}
        ref={dialogRef}
      >
        <header className="ceremony-top">
          <span className="ceremony-kicker">{title}</span>
          {expiresAt !== undefined && (
            <span className="ceremony-clock">
              Claimable for <Countdown to={expiresAt} />
            </span>
          )}
        </header>

        <div className="ceremony-main">
          <div className="ceremony-primary">
            <h1 id={headlineId}>{headline}</h1>
            <p className="ceremony-body">{body}</p>
            <div className="ceremony-value">{value}</div>
            <div className="row wrap">
              <Button variant="primary" onClick={() => void copy(value, valueLabel)}>
                Copy {valueLabel}
              </Button>
              {command !== undefined && (
                <Button onClick={() => void copy(command, "Client command")}>Copy client command</Button>
              )}
            </div>
            {command !== undefined && <div className="ceremony-command">{command}</div>}
          </div>

          <aside className="ceremony-aside">
            <span className="ceremony-kicker">What happens next</span>
            <ol className="ceremony-steps">
              {steps.map((step) => (
                <li key={step}>{step}</li>
              ))}
            </ol>
            <hr className="ceremony-rule" />
            <span className="ceremony-kicker">If you lose it</span>
            <p className="ceremony-body">{recovery}</p>
            {meta !== undefined && <div className="ceremony-meta">{meta}</div>}
          </aside>
        </div>

        <footer className="ceremony-foot">
          <span className="ceremony-hint">
            {confirming
              ? "Last chance — closing destroys the only copy of this token."
              : "Closing doesn't void it — the token stays redeemable until it expires; it just won't be shown again."}
          </span>
          <div className="row">
            {confirming && <Button onClick={() => setConfirming(false)}>Keep it open</Button>}
            <Button
              variant="primary"
              onClick={() => (confirming ? onClose() : setConfirming(true))}
            >
              {confirming ? "Yes, close it" : "I've stored it — close"}
            </Button>
          </div>
        </footer>
      </div>
    </div>
  );
}
