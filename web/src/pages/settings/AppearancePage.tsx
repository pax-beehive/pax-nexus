import { Kicker } from "../../components/Kicker";
import { THEMES, THEME_LABELS, useTheme, type Theme } from "../../lib/theme";

/**
 * 主题选择。阶段 6 会按设计稿做成带预览的卡片墙；本阶段只保证主题控件
 * 在侧边栏删除后仍有落点，行为与原来的下拉完全一致。
 */
export function AppearancePage() {
  const [theme, setTheme] = useTheme();

  return (
    <>
      <div className="page-head">
        <div>
          <Kicker>Settings · appearance</Kicker>
          <h1>Appearance</h1>
          <p className="muted flush">
            Applies to your account on this device only. Nobody else on the team sees your choice.
          </p>
        </div>
      </div>
      <div className="seg" role="group" aria-label="Theme">
        {THEMES.map((value) => (
          <button
            key={value}
            type="button"
            className={value === theme ? "on" : ""}
            aria-pressed={value === theme}
            onClick={() => setTheme(value as Theme)}
          >
            {THEME_LABELS[value]}
          </button>
        ))}
      </div>
    </>
  );
}
