// Agent 详情页的密钥现状：待认领的一次性令牌 + 活跃的长期密钥。
//
// 两条腿各自独立降级——一条挂了另一条照常渲染。返回的数组引用同时喂给
// 展示卡与销毁类确认弹窗的后果预览，所以「预览计数 = 页面行数」是结构性
// 成立的，不靠测试盯两个数据源不漂移。

import { useCallback, useEffect, useState } from "react";
import type { AgentScope } from "../../api/actions";
import { listAllCredentials, listAllEnrollments } from "../../api/queries";
import type { CredentialMetadata, EnrollmentMetadata } from "../../api/types";

export interface KeyLeg<T> {
  items?: T[];
  error?: unknown;
  loading: boolean;
}

export interface AgentKeys {
  enrollments: KeyLeg<EnrollmentMetadata>;
  credentials: KeyLeg<CredentialMetadata>;
  reload: () => void;
}

const LOADING: KeyLeg<never> = { loading: true };

export function useAgentKeys(scope: AgentScope, agentId: string): AgentKeys {
  const [enrollments, setEnrollments] = useState<KeyLeg<EnrollmentMetadata>>(LOADING);
  const [credentials, setCredentials] = useState<KeyLeg<CredentialMetadata>>(LOADING);
  const [epoch, setEpoch] = useState(0);

  const reload = useCallback(() => setEpoch((n) => n + 1), []);

  useEffect(() => {
    let cancelled = false;
    setEnrollments(LOADING);
    setCredentials(LOADING);

    listAllEnrollments(scope, agentId, "pending")
      .then((items) => {
        if (!cancelled) setEnrollments({ items, loading: false });
      })
      .catch((error: unknown) => {
        if (!cancelled) setEnrollments({ error, loading: false });
      });

    listAllCredentials(scope, agentId, "active")
      .then((items) => {
        if (!cancelled) setCredentials({ items, loading: false });
      })
      .catch((error: unknown) => {
        if (!cancelled) setCredentials({ error, loading: false });
      });

    return () => {
      cancelled = true;
    };
  }, [scope, agentId, epoch]);

  return { enrollments, credentials, reload };
}
