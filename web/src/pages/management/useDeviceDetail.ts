// 访问树第 3 层的唯一一次取数。展示行与级联预览都从这一份详情派生，
// 所以「预览行数 = 实际级联数」是结构性的，不靠两个数据源保持同步。

import { useCallback, useEffect, useState } from "react";
import { getDevice } from "../../api/queries";
import type { DeviceDetail } from "../../api/types";

export interface DeviceDetailState {
  detail?: DeviceDetail;
  status: "loading" | "ready" | "error";
  retry: () => void;
}

/** 内部态多记一个「这份结果是替哪台机器取的」，见下方的落后一帧防护。 */
interface Settled {
  detail?: DeviceDetail;
  status: "loading" | "ready" | "error";
  /** 产生这份 state 的 credentialId；undefined = 不在第 3 层。 */
  requested?: string;
}

export function useDeviceDetail(credentialId?: string): DeviceDetailState {
  const [state, setState] = useState<Settled>({ status: "loading" });
  const [epoch, setEpoch] = useState(0);
  const retry = useCallback(() => setEpoch((value) => value + 1), []);

  useEffect(() => {
    if (credentialId === undefined) {
      setState({ status: "ready", requested: undefined });
      return;
    }
    let cancelled = false;
    setState({ status: "loading", requested: credentialId });
    getDevice(credentialId)
      .then((detail) => {
        if (cancelled) return;
        // 服务端答非所问：响应体里的机器不是我们问的那台。这是契约破了，
        // 不是「还在取」——判成 error，第 3 层会画出可重试的报错卡片。判成
        // loading 的话，页面是一个永远转不完、且没有任何退路的圈。
        // `detail.device` 整个缺席时这一行抛 TypeError，落进下面的 catch，
        // 归到同一个 error 态：所以这里不需要 `?.`（缺字段的响应体一样是
        // 契约破了，不该被静默当成「另一台机器的结果」）。
        if (detail.device.credential_id !== credentialId) {
          setState({ status: "error", requested: credentialId });
          return;
        }
        setState({ status: "ready", detail, requested: credentialId });
      })
      .catch(() => {
        if (!cancelled) setState({ status: "error", requested: credentialId });
      });
    return () => {
      cancelled = true;
    };
  }, [credentialId, epoch]);

  // 落后一帧防护。effect 是被动的：credentialId 变了之后，React 先用旧
  // state 提交一次，effect 才把 state 翻成 loading。那一帧里裸 state 会替
  // **上一个坐标**回答当前坐标的问题——
  //   · 从第 2 层下钻时上一份 state 是 {ready, detail: undefined}（不在第 3
  //     层时的静息态），调用方于是提交一次「取不到这台机器的 Agent」红框，
  //     role="alert" 会被读屏念出来，然后立刻被真正的第 3 层顶掉；
  //   · 机器 A → 机器 B 时上一份 state 是 A 的 detail 或 A 的 error，于是 B
  //     的头部会配上 A 的 agents（级联预览与展示行同源这条不变式在这一帧
  //     里是破的）。
  // 所以：结果不是替当前 credentialId 取的，就一律报 loading。
  //
  // 这里只管「落后一帧」这一件事。「服务端答非所问」在上面的 then 里就已经
  // 被判成 error 了——两者必须分开：前者下一帧就自愈（真的还在取），后者
  // 永远不会自愈（判成 loading 就是一个不可恢复的永久转圈）。因为 detail 与
  // requested 是同一次 setState 写进去的，走到这里时「detail 属于
  // state.requested 这台机器」已经是结构性不变式，不需要再核一遍。
  if (state.requested !== credentialId) {
    return { status: "loading", retry };
  }

  return { status: state.status, detail: state.detail, retry };
}
