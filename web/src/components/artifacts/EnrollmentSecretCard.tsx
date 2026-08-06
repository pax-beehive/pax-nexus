// Agent 接入令牌的仪式：注册成功后只展示一次。
//
// Phase 4 停靠点：旧的 `SecretCard` 已删除（portal-modernist-phase4 任务
// 4），另外三处调用点都换成了全屏的 `SecretCeremony`。这处调用点属于任务
// 10（Agent identity/artifact 仪式）的地盘，正式重做留给它；这里只做最小
// 迁移，保住构建，外部 `{ secret, onClose }` 接口不变，`AgentArtifacts.tsx`
// 的调用点不动。
//
// 评审 Important 3：迁移刚落地时这里保留了英文文案，与 SecretCeremony 硬
// 编码的中文 chrome（按钮、侧栏小标题、底栏提示）拼在同一屏上，造成中英文
// 混排——旧 SecretCard 是纯英文 chrome，与英文内容自洽，这个问题在迁移前
// 不存在。这里补齐中文文案，语境换成 Agent 接入令牌（不是设备）。

import type { EnrollmentSecret } from "../../api/types";
import { enrollmentConnectCommand, isSelfDescribingEnrollmentToken } from "../../lib/enrollment";
import { SecretCeremony } from "../SecretCeremony";

export function EnrollmentSecretCard({
  secret,
  onClose,
}: {
  secret: EnrollmentSecret;
  onClose: () => void;
}) {
  return (
    <SecretCeremony
      title="一次性 Agent 接入令牌 · 只展示一次，不存任何地方"
      headline="现在就复制。我们没法再给你看一次。"
      body="这串令牌让这个 Agent 在目标机器上换取一把长期 API 密钥。它只存在于这个屏幕上——不在数据库里，不在邮件里，也不在审计日志里。"
      value={secret.token}
      valueLabel="令牌"
      expiresAt={secret.expires_at}
      steps={[
        "在目标机器上执行下面的命令。",
        "令牌兑换成长期 API 密钥并自毁。",
        "Portal 不会看到换回来的这把密钥。",
      ]}
      recovery={
        isSelfDescribingEnrollmentToken(secret.token)
          ? "什么都不会坏。吊销这条 enrollment、重新发一张即可；令牌内嵌了连接地址，客户端可以直接解析。Agent 的历史与既有密钥不受影响。"
          : "什么都不会坏。吊销这条 enrollment、重新发一张即可。Agent 的历史与既有密钥不受影响。"
      }
      command={enrollmentConnectCommand(secret.token, window.location.origin)}
      onClose={onClose}
    />
  );
}
