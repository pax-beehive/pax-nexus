import type { ReactNode } from "react";
import { Crumbs } from "../../components/Crumbs";
import { AccessSummary } from "./AccessSummary";
import type { AccessSnapshot } from "./useAccessSnapshot";

/**
 * 访问树三层共用的外壳：页头 + 汇总条 + 面包屑栏，层内容作为 children。
 *
 * 抽出来不只是去重（原先三处逐字重复）。设计 §6 最后一条要求「第 3 层详情
 * 取数失败：该层报错可重试，**面包屑与上层仍可用**」——只要外壳还写在各层
 * 的成功分支里，loading / error 分支就必然是裸的 return，用户失去页内的返回
 * 路径。外壳变成包住整层（含它的 loading 与 error 态）的组件之后，「任何层
 * 状态都带面包屑」是结构性的，而不是每个分支各自记得抄一遍。
 *
 * 快照本身的 loading / error 态也走这里，此时 snapshot 为 undefined：汇总条
 * 三格退成 —（注解见 unavailableNote），面包屑只画它还敢说的那一层。
 */
export function AccessChrome({
  snapshot,
  crumbs,
  hint,
  lede,
  unavailableNote,
  children,
}: {
  /** undefined = 快照本身还没到 / 取不到，外壳照画，汇总条整体退化。 */
  snapshot?: AccessSnapshot;
  crumbs: { label: string; to?: string }[];
  /** 面包屑栏右端的本层计数提示；取不到时传 —，与汇总条同口径。 */
  hint: ReactNode;
  /** 仅根层有的导语。 */
  lede?: ReactNode;
  /** 汇总条取不到值时的注解，透传给 AccessSummary。 */
  unavailableNote?: string;
  children: ReactNode;
}) {
  return (
    <>
      <div className="page-head">
        <div>
          <p className="card-kicker">MANAGEMENT · ACCESS TREE</p>
          <h1>Access flows downward</h1>
          {lede}
        </div>
      </div>

      <AccessSummary snapshot={snapshot} unavailableNote={unavailableNote} />

      <div className="at-bar">
        <Crumbs items={crumbs} />
        <span className="at-bar-hint">{hint}</span>
      </div>

      {children}
    </>
  );
}
