// 右栏「它是怎么来的」：把 buildProvenance(detail) 的输出原样渲染成
// 版本 × 五段的网格。纯展示层——链条本身的派生、缺段判定全在 provenance.ts，
// 这里只负责把 ProvenanceStep 摆出来，并给 missing 的段套上降级样式。
import type { TeamNoteDetail } from "../../api/types";
import { EmptyState } from "../../components/EmptyState";
import { formatTime } from "../../lib/format";
import { buildProvenance, type ProvenanceStep } from "./provenance";

function ProvenanceStepRow({ step }: { step: ProvenanceStep }) {
  return (
    <div className={`gv-prov-step${step.missing ? " gv-prov-missing" : ""}`}>
      <div className="gv-prov-stage">{step.label}</div>
      <div>
        <div className="gv-prov-title">{step.title}</div>
        <p className="gv-prov-body">{step.body}</p>
        {step.ref && <div className="gv-prov-ref">{step.ref}</div>}
      </div>
    </div>
  );
}

export function NoteProvenance({ detail }: { detail: TeamNoteDetail }) {
  const chain = buildProvenance(detail);

  return (
    <section className="card">
      <h2>它是怎么来的</h2>
      {chain.length === 0 ? (
        <EmptyState
          title="还没有版本历史"
          body="这条 Team Note 目前没有可展示的溯源记录——它可能还没经过完整的抽取/候选/落库流程。"
        />
      ) : (
        chain.map((revision) => (
          <div key={revision.revision} className="gv-prov-revision">
            <div className="row between wrap">
              <h3 className="flush">版本 {revision.revision}</h3>
              <span className="small faint" title={revision.createdAt}>
                {formatTime(revision.createdAt)}
              </span>
            </div>
            {revision.steps.map((step) => (
              <ProvenanceStepRow key={step.stage} step={step} />
            ))}
          </div>
        ))
      )}
    </section>
  );
}
