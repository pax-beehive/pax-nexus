// usePagedList 的竞态守卫：`loadMore()` 在途时切换筛选（deps 变化），那一页
// 姗姗来迟的响应必须被丢弃，不能和已经切到新筛选的列表拼在一起。
//
// 这条断言之前挂在 web/tests/admin-explorer.dom.test.tsx 里（"filters
// resolved notes and ignores stale pagination results"），随 Task 6 对
// AdminExplorerPage 的整页重写一并删除了——但守卫是 usePagedList 自己的契约、
// 被 9 个页面复用（usePagedList.ts 里那条 "Mirror invariant" 注释就是证据：
// 任何修复都要同步到 pages/operations/hooks.ts 的姊妹实现），删测不该带走
// 这条覆盖。搬到这里，直接用一个最小 harness 驱动 hook 本身，不再依赖
// Explorer 的具体 UI，往后不管哪个页面用 usePagedList 都受它保护。
//
// 注意：这条守卫具体落在 loadMore() 的 generationRef 检查上（usePagedList.ts
// 第一版初始加载的 .then 分支单靠 useEffect 自带的 `cancelled` 闭包变量就已
// 经能防住 deps 变化，不需要 generationRef 也会通过）——所以测试必须让
// loadMore 在途，而不是只切换筛选本身，否则测不到 generationRef 那一支。
import { useState } from "react";
import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { Page } from "../src/api/types";
import { usePagedList } from "../src/lib/usePagedList";

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

interface Row {
  id: string;
}

function Harness({
  fetchPage,
}: {
  fetchPage: (filter: string, cursor?: string) => Promise<Page<Row>>;
}) {
  const [filter, setFilter] = useState("initial");
  const list = usePagedList((cursor) => fetchPage(filter, cursor), [filter]);
  return (
    <div>
      <button onClick={() => setFilter("switched")}>switch filter</button>
      <button onClick={() => void list.loadMore()}>load more</button>
      <ul>
        {list.items.map((row) => (
          <li key={row.id}>{row.id}</li>
        ))}
      </ul>
    </div>
  );
}

describe("usePagedList — 竞态守卫", () => {
  it("load more 在途时切换筛选，姗姗来迟的旧筛选第二页被丢弃", async () => {
    let resolveOldPage!: (page: Page<Row>) => void;
    const oldPage = new Promise<Page<Row>>((resolve) => {
      resolveOldPage = resolve;
    });
    const user = userEvent.setup();

    render(
      <Harness
        fetchPage={(filter, cursor) => {
          if (filter === "initial" && cursor === undefined) {
            return Promise.resolve({ items: [{ id: "row-1" }], nextCursor: "cursor-1" });
          }
          if (filter === "initial" && cursor === "cursor-1") {
            // "load more" 的响应，故意晚到——晚到 resolve 的时机在下面手动控制。
            return oldPage;
          }
          if (filter === "switched" && cursor === undefined) {
            return Promise.resolve({ items: [{ id: "switched-row" }], nextCursor: undefined });
          }
          throw new Error(`unexpected fetchPage(${filter}, ${cursor})`);
        }}
      />,
    );

    await screen.findByText("row-1");
    await user.click(screen.getByRole("button", { name: "load more" }));
    await user.click(screen.getByRole("button", { name: "switch filter" }));
    await screen.findByText("switched-row");

    // load more 的旧响应现在才姗姗来迟——generationRef 必须让它被丢弃，不能
    // 把已经切到新筛选的列表和旧筛选的第二页拼在一起。
    //
    // 注意：不能直接 `await waitFor(() => expect(...).toBeNull())`——waitFor
    // 的第一次检查是同步执行的（在 resolveOldPage 的 .then continuation 真正
    // 跑起来、setItems 落地之前），对于「断言不存在」这种反向断言，第一次检查
    // 会平白通过，根本等不到那次 microtask 落地，测不出问题。这里先 resolve，
    // 再用一个真实的宏任务把 microtask 队列排空，让「若有 bug 就会发生」的
    // 那次 setItems 有机会真正执行，再做断言。
    resolveOldPage({ items: [{ id: "stale-more-row" }], nextCursor: undefined });
    await new Promise((resolve) => setTimeout(resolve, 0));

    expect(screen.queryByText("stale-more-row")).toBeNull();
    screen.getByText("switched-row");
  });
});
