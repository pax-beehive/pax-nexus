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

export function useDeviceDetail(credentialId?: string): DeviceDetailState {
  const [state, setState] = useState<Omit<DeviceDetailState, "retry">>({ status: "loading" });
  const [epoch, setEpoch] = useState(0);
  const retry = useCallback(() => setEpoch((value) => value + 1), []);

  useEffect(() => {
    if (credentialId === undefined) {
      setState({ status: "ready" });
      return;
    }
    let cancelled = false;
    setState({ status: "loading" });
    getDevice(credentialId)
      .then((detail) => {
        if (!cancelled) setState({ status: "ready", detail });
      })
      .catch(() => {
        if (!cancelled) setState({ status: "error" });
      });
    return () => {
      cancelled = true;
    };
  }, [credentialId, epoch]);

  return { ...state, retry };
}
