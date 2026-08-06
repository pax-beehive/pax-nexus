// Identity：Agent 的身份字段与乐观锁保存。
//
// Runtime（agent_type）保持自由文本框：后端不枚举也不校验取值，设计稿的
// Codex/Claude/Custom 单选会谎称存在一个封闭集合（spec §8 第 2 条）。
//
// 乐观锁：每次更新把 resource_version 同时放进 body 与 If-Match。
// 遇到 resource_version_conflict 就重取并重置表单——本地草稿绝不覆盖
// 别人已经落库的改动。

import { useEffect, useState } from "react";
import { updateAgent } from "../../api/actions";
import { apiError } from "../../api/client";
import type { AgentProfile } from "../../api/types";
import { Button } from "../../components/Button";
import { Card } from "../../components/Card";
import { useToast } from "../../components/Toasts";
import { useErrorHandler } from "../../lib/useErrorHandler";
import { validateDisplayName } from "../../lib/validation";
import type { AgentAccess } from "./agentScope";

export function AgentIdentityCard({
  agent,
  access,
  onChanged,
  refetch,
}: {
  agent: AgentProfile;
  access: AgentAccess;
  onChanged: (agent: AgentProfile) => void;
  refetch: () => Promise<AgentProfile>;
}) {
  const toast = useToast();
  const handleError = useErrorHandler();
  const [name, setName] = useState(agent.display_name);
  const [type, setType] = useState(agent.agent_type);
  const [description, setDescription] = useState(agent.description);
  const [visible, setVisible] = useState(agent.directory_visible);
  const [busy, setBusy] = useState(false);

  // 权威数据一变就重置草稿：加载、保存成功、409 重取三种情形共用。
  useEffect(() => {
    setName(agent.display_name);
    setType(agent.agent_type);
    setDescription(agent.description);
    setVisible(agent.directory_visible);
  }, [agent]);

  const disabled = !access.canEdit;

  const save = async () => {
    const nameError = validateDisplayName(name);
    if (nameError) return toast("warn", nameError);
    setBusy(true);
    try {
      const updated = await updateAgent(
        access.actScope,
        agent.agent_id,
        {
          display_name: name.trim(),
          description,
          agent_type: type.trim(),
          directory_visible: visible,
        },
        agent.resource_version,
      );
      onChanged(updated);
      toast("ok", `已保存（第 ${updated.resource_version} 版）`);
    } catch (err) {
      if (apiError(err, 409, "resource_version_conflict")) {
        // 别人已经落库的改动绝不能被本地陈旧草稿静默盖掉：重取，让服务端
        // 数据覆盖草稿，而不是反过来。
        const fresh = await refetch();
        onChanged(fresh);
        toast("warn", "有人在你之前改过它，已刷新到最新——你的改动没有提交。");
      } else {
        handleError(err);
      }
    } finally {
      setBusy(false);
    }
  };

  return (
    <Card title="Identity">
      <div className="field-row">
        <div>
          <label htmlFor="ag-name">显示名</label>
          <input
            id="ag-name"
            type="text"
            value={name}
            disabled={disabled}
            onChange={(e) => setName(e.target.value)}
          />
        </div>
        <div>
          <label htmlFor="ag-type">Runtime</label>
          <input
            id="ag-type"
            type="text"
            value={type}
            disabled={disabled}
            onChange={(e) => setType(e.target.value)}
          />
        </div>
      </div>
      <label htmlFor="ag-desc">它做什么</label>
      <textarea
        id="ag-desc"
        rows={2}
        value={description}
        disabled={disabled}
        onChange={(e) => setDescription(e.target.value)}
      />
      <label className="ck">
        <input
          type="checkbox"
          checked={visible}
          disabled={disabled}
          onChange={(e) => setVisible(e.target.checked)}
        />
        列进团队目录
        <span className="small muted"> —— 其他 Agent 能找到它并向它发送知识胶囊。</span>
      </label>
      {access.canEdit && (
        <div className="row between">
          <span className="small muted">
            第 <code>{agent.resource_version}</code> 版（提交时同时进 body 与 <code>If-Match</code>）
          </span>
          <Button variant="primary" size="sm" disabled={busy} onClick={() => void save()}>
            保存改动
          </Button>
        </div>
      )}
    </Card>
  );
}
