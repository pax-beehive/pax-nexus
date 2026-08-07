import type { ReactNode } from "react";
import { Kicker } from "./Kicker";

/**
 * 页面标题区。门户里每一屏的开头都是同一块结构——小号导语、标题、可选的
 * 一句说明，右边可选一组操作——七个阶段各写了一遍，于是 `.page-head`、
 * `.gv-head`、`.wiki-head`、`.ag-head` 四个类的 flex 规则几乎逐字相同。
 * 结构收敛到这里，外观差异收敛成两个修饰类。
 *
 * `variant`：
 * - `shell`（默认）——嵌在 `.page` 容器里的常规页面，用 margin 与正文拉开；
 * - `bleed`——governance / wiki / agent 详情这类整幅页面，用 padding 顶到
 *   容器边缘。两者是真实存在的两种节奏，不是可以随便统一掉的偶然差异。
 *
 * `lede` 给字符串时套上全站默认的 `<p className="muted flush">`；需要别的
 * 处理（governance 的淡化长句 `.lede-dim`、wiki 的窄栏 `.lede-narrow`）时
 * 直接传整个 `<p>`。刻意不在这里替调用方选颜色类：低对比度文字类的统一是
 * 另一件还没做的设计系统工作，顺手改会在没验证过的屏上动对比度。
 */
export function PageHeader({
  kicker,
  title,
  titleAside,
  lede,
  variant = "shell",
  alignStart = false,
  actions,
}: {
  /** 省略时标题直接顶格（设备详情、My Agents 本来就没有导语）。 */
  kicker?: ReactNode;
  title: ReactNode;
  /**
   * 与标题同一行的补充物（Agent 详情的状态徽标与类型标签）。放在这里而不是
   * 让调用方把整行塞进 `title`：那样会渲染出嵌套的 `<h1>`，标题结构就不再
   * 可断言了。
   */
  titleAside?: ReactNode;
  lede?: ReactNode;
  variant?: "shell" | "bleed";
  /** 右侧内容比左侧高很多时顶端对齐（Agent 详情的归属事实行）。 */
  alignStart?: boolean;
  actions?: ReactNode;
}) {
  const className = [
    "page-head",
    variant === "bleed" ? "bleed" : "",
    alignStart ? "align-start" : "",
  ]
    .filter(Boolean)
    .join(" ");

  return (
    <div className={className}>
      <div>
        {kicker !== undefined && <Kicker>{kicker}</Kicker>}
        {titleAside === undefined ? (
          <h1>{title}</h1>
        ) : (
          <div className="row">
            <h1>{title}</h1>
            {titleAside}
          </div>
        )}
        {typeof lede === "string" ? <p className="muted flush">{lede}</p> : lede}
      </div>
      {actions}
    </div>
  );
}
