// 设备注册令牌的仪式。AccessTreePage 与 AdminDevicesPage 共用——两处此前
// 是逐字重复的两份 SecretCard 调用。

import type { DeviceEnrollmentSecret } from "../api/types";
import { deviceConnectCommand, isSelfDescribingEnrollmentToken } from "../lib/enrollment";
import { SecretCeremony } from "./SecretCeremony";

export function DeviceEnrollmentCeremony({
  secret,
  onClose,
}: {
  secret: DeviceEnrollmentSecret;
  onClose: () => void;
}) {
  return (
    <SecretCeremony
      title="一次性设备注册令牌 · 只展示一次，不存任何地方"
      headline="现在就复制。我们没法再给你看一次。"
      body={
        <>
          这串令牌让 <b>{secret.device_name}</b> 换取一把长期设备密钥。
          它只存在于这个屏幕上——不在数据库里，不在邮件里，也不在审计日志里。
        </>
      }
      value={secret.token}
      valueLabel="令牌"
      expiresAt={secret.expires_at}
      steps={[
        `在 ${secret.device_name} 上执行下面的命令。`,
        "令牌兑换成长期密钥并自毁。",
        "这台机器出现在访问树里，它上面的 Agent 可以自助注册。",
      ]}
      recovery={
        isSelfDescribingEnrollmentToken(secret.token)
          ? "什么都不会坏。吊销这台设备、重新创建一次注册即可；令牌内嵌了连接地址，客户端可以直接解析。"
          : "什么都不会坏。吊销这台设备、重新创建一次注册即可。"
      }
      command={deviceConnectCommand(secret.token, window.location.origin, secret.device_name)}
      onClose={onClose}
    />
  );
}
