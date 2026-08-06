// 访问树的根层取数：三个列表一次拉全，构成时点一致的快照。
// 第 1、2 层完全由这份快照内存派生，不再发请求（设计 §4.2）。

import { useCallback, useEffect, useState } from "react";
import { listAllAgents, listAllDevices, listAllMembers } from "../../api/queries";
import type { AgentProfile, DeviceSummary, Member } from "../../api/types";

export interface AccessSnapshot {
  members: Member[];
  /** undefined 表示这条腿失败了，与「空列表」是两件事。 */
  devices?: DeviceSummary[];
  agents?: AgentProfile[];
}

export interface AccessSnapshotState {
  status: "loading" | "ready" | "error";
  snapshot?: AccessSnapshot;
  /** 仅 members 腿失败时有值：没有人就没有树。 */
  error?: unknown;
  retry: () => void;
}

export function useAccessSnapshot(): AccessSnapshotState {
  const [state, setState] = useState<Omit<AccessSnapshotState, "retry">>({ status: "loading" });
  const [epoch, setEpoch] = useState(0);
  const retry = useCallback(() => setEpoch((value) => value + 1), []);

  useEffect(() => {
    let cancelled = false;
    setState({ status: "loading" });
    void Promise.allSettled([
      listAllMembers({ limit: 100 }),
      listAllDevices({ limit: 100 }),
      listAllAgents({ limit: 100 }),
    ]).then(([members, devices, agents]) => {
      if (cancelled) return;
      if (members.status === "rejected") {
        setState({ status: "error", error: members.reason });
        return;
      }
      setState({
        status: "ready",
        snapshot: {
          members: members.value,
          devices: devices.status === "fulfilled" ? devices.value : undefined,
          agents: agents.status === "fulfilled" ? agents.value : undefined,
        },
      });
    });
    return () => {
      cancelled = true;
    };
  }, [epoch]);

  return { ...state, retry };
}
