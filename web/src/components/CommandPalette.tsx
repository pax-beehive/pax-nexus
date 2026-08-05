// ⌘K 命令面板。三路来源：本地导航动作（永远可用）、Agent、Wiki 页面。
// 远端两路失败时静默降级为只剩导航动作 —— 搜索框里冒错误提示比搜不到更打断人。

import { useEffect, useMemo, useRef, useState } from "react";
import { useNavigate } from "react-router-dom";
import type { HumanMe } from "../api/types";
import type { NavSection } from "../app/navModel";
import { can } from "../lib/capabilities";
import { listAdminAgents, listMyAgents } from "../api/queries";
import { searchWiki } from "../api/wiki";

interface Entry {
  id: string;
  group: string;
  label: string;
  hint?: string;
  to: string;
}

const DEBOUNCE_MS = 200;

function navigationEntries(sections: NavSection[]): Entry[] {
  const entries: Entry[] = [];
  for (const section of sections) {
    if (section.items.length === 0) {
      entries.push({
        id: `nav:${section.to}`,
        group: "Go to",
        label: section.label,
        to: section.to,
      });
      continue;
    }
    for (const item of section.items) {
      entries.push({
        id: `nav:${item.to}`,
        group: "Go to",
        label: item.label,
        hint: section.label,
        to: item.to,
      });
    }
  }
  return entries;
}

export function CommandPalette({
  me,
  open,
  onOpenChange,
  sections,
}: {
  me: HumanMe;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  sections: NavSection[];
}) {
  const navigate = useNavigate();
  const [query, setQuery] = useState("");
  const [remote, setRemote] = useState<Entry[]>([]);
  const [active, setActive] = useState(0);
  const inputRef = useRef<HTMLInputElement>(null);

  const navEntries = useMemo(() => navigationEntries(sections), [sections]);

  // 打开时清空上一轮状态并聚焦输入框。
  useEffect(() => {
    if (!open) return;
    setQuery("");
    setRemote([]);
    setActive(0);
    inputRef.current?.focus();
  }, [open]);

  // 远端检索：debounce + AbortController，两路各自独立降级。
  useEffect(() => {
    if (!open) return;
    const trimmed = query.trim();
    if (trimmed.length < 2) {
      setRemote([]);
      return;
    }
    const controller = new AbortController();
    const timer = setTimeout(() => {
      const agents = can(me.role, "view.all-agents")
        ? listAdminAgents({ q: trimmed, limit: 5 })
        : listMyAgents({ limit: 50 });
      void Promise.allSettled([agents, searchWiki(trimmed, controller.signal)]).then(
        ([agentResult, wikiResult]) => {
          if (controller.signal.aborted) return;
          const entries: Entry[] = [];
          if (agentResult.status === "fulfilled") {
            for (const agent of agentResult.value.items.slice(0, 5)) {
              entries.push({
                id: `agent:${agent.agent_id}`,
                group: "Agents",
                label: agent.display_name,
                hint: agent.agent_id,
                to: `/management/agents/${encodeURIComponent(agent.agent_id)}`,
              });
            }
          }
          if (wikiResult.status === "fulfilled") {
            for (const result of wikiResult.value.slice(0, 5)) {
              entries.push({
                id: `wiki:${result.page.id}:${result.section_key}`,
                group: "Wiki",
                label: result.page.title,
                hint: result.section_key,
                to: `/apps/wiki/${encodeURIComponent(result.page.slug)}`,
              });
            }
          }
          setRemote(entries);
        },
      );
    }, DEBOUNCE_MS);
    return () => {
      controller.abort();
      clearTimeout(timer);
    };
  }, [open, query, me.role]);

  const results = useMemo(() => {
    const trimmed = query.trim().toLowerCase();
    const filteredNav = trimmed
      ? navEntries.filter((entry) => entry.label.toLowerCase().includes(trimmed))
      : navEntries;
    return [...filteredNav, ...remote];
  }, [navEntries, remote, query]);

  // 结果集变化后把选中项夹回有效范围。
  useEffect(() => {
    setActive((current) => (current >= results.length ? 0 : current));
  }, [results.length]);

  if (!open) return null;

  const go = (entry: Entry) => {
    onOpenChange(false);
    navigate(entry.to);
  };

  const onKeyDown = (event: React.KeyboardEvent) => {
    if (event.key === "Escape") {
      event.preventDefault();
      onOpenChange(false);
      return;
    }
    if (event.key === "ArrowDown") {
      event.preventDefault();
      setActive((current) => Math.min(current + 1, results.length - 1));
      return;
    }
    if (event.key === "ArrowUp") {
      event.preventDefault();
      setActive((current) => Math.max(current - 1, 0));
      return;
    }
    if (event.key === "Enter" && results[active]) {
      event.preventDefault();
      go(results[active]);
    }
  };

  return (
    <div
      className="dialog-backdrop"
      onClick={(event) => {
        if (event.target === event.currentTarget) onOpenChange(false);
      }}
    >
      <div className="palette" role="dialog" aria-modal="true" aria-label="Command palette">
        <input
          ref={inputRef}
          className="palette-input"
          role="combobox"
          aria-label="Search"
          aria-expanded="true"
          aria-controls="palette-results"
          aria-autocomplete="list"
          placeholder="Search agents, notes, actions…"
          value={query}
          onChange={(event) => setQuery(event.target.value)}
          onKeyDown={onKeyDown}
        />
        <ul className="palette-results" id="palette-results" role="listbox" aria-label="Results">
          {results.map((entry, index) => (
            <li
              key={entry.id}
              role="option"
              aria-selected={index === active}
              className={index === active ? "on" : ""}
              onMouseEnter={() => setActive(index)}
              onClick={() => go(entry)}
            >
              <span className="palette-group">{entry.group}</span>
              <span className="palette-label">{entry.label}</span>
              {entry.hint !== undefined && <span className="palette-hint">{entry.hint}</span>}
            </li>
          ))}
        </ul>
        <div className="palette-foot small muted">↑↓ move · ↵ open · esc close</div>
      </div>
    </div>
  );
}
