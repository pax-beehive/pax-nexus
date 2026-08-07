// tokens.css 是 Modernist 设计系统的单一真源。这些断言防止重构中意外删掉
// 某个 token —— 删掉后引用它的组件会静默退化成浏览器默认值，不会报错。
import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";
import { contrastRatio } from "./contrast";

const tokens = readFileSync(new URL("../src/styles/tokens.css", import.meta.url), "utf8");

/**
 * 抓取所有 :root 块里声明的自定义属性名。tokens.css 有两个 :root：
 * 正式 token 一个，过渡期的兼容别名一个 —— 两个都要算进来。
 */
export function declaredTokens(css: string): string[] {
  const names: string[] = [];
  for (const block of css.matchAll(/:root\s*\{([\s\S]*?)\n\}/g)) {
    names.push(...[...block[1].matchAll(/^\s*(--[a-z0-9-]+)\s*:/gm)].map((m) => m[1]));
  }
  return names;
}

const REQUIRED = [
  "--color-bg", "--color-surface", "--color-text",
  "--color-accent", "--color-accent-2", "--color-divider",
  "--font-heading", "--font-heading-weight", "--font-body", "--font-mono",
  "--space-1", "--space-2", "--space-3", "--space-4", "--space-6", "--space-8",
  "--radius-sm", "--radius-md", "--radius-lg",
  "--shadow-sm", "--shadow-md", "--shadow-lg",
];

describe("design tokens", () => {
  it("declares every required token", () => {
    const declared = new Set(declaredTokens(tokens));
    expect(REQUIRED.filter((t) => !declared.has(t))).toEqual([]);
  });

  it("declares full neutral / accent / accent-2 ramps", () => {
    const declared = new Set(declaredTokens(tokens));
    for (const role of ["neutral", "accent", "accent-2"]) {
      for (let step = 100; step <= 900; step += 100) {
        expect(declared.has(`--color-${role}-${step}`)).toBe(true);
      }
    }
  });

  it("pins the Modernist base values verbatim", () => {
    expect(tokens).toContain("--color-bg: #f3f2f2");
    expect(tokens).toContain("--color-text: #201e1d");
    expect(tokens).toContain("--color-accent: #ec3013");
  });

  it("keeps every radius at zero", () => {
    for (const name of ["--radius-sm", "--radius-md", "--radius-lg"]) {
      expect(tokens).toMatch(new RegExp(`${name}:\\s*0(px)?;`));
    }
  });

  it("self-hosts Archivo and references no external font host", () => {
    expect(tokens).toContain("@font-face");
    expect(tokens).toContain("./fonts/archivo-latin.woff2");
    expect(tokens).not.toMatch(/https?:\/\//);
  });

  // styles/features/ 的六个特性样式表在阶段 1 不重写，仍引用旧 token 名。
  // 删掉任何一个别名都会让对应页面掉进浏览器默认样式且不报错，所以在这里钉住。
  // 这些别名随各特性样式表的重写逐个删除，阶段 6 结束时本用例一并删除。
  it("keeps the deprecated token aliases the un-migrated feature sheets need", () => {
    const declared = new Set(declaredTokens(tokens));
    const legacy = [
      "--bg", "--surface", "--surface-2", "--border", "--border-strong",
      "--text", "--muted", "--faint", "--accent", "--accent-hover", "--accent-soft",
      "--ok", "--ok-bg", "--warn", "--warn-bg", "--bad", "--bad-bg", "--info", "--info-bg",
      "--input-bg", "--on-accent", "--danger-border", "--warn-border",
      "--bad-border", "--accent-border", "--shadow", "--mono", "--serif", "--sans",
    ];
    expect(legacy.filter((t) => !declared.has(t))).toEqual([]);
  });
});

const themes = readFileSync(new URL("../src/styles/themes.css", import.meta.url), "utf8");

/** 抓取 [data-theme="x"] 块里声明的自定义属性，返回 名→值。 */
function themeBlock(css: string, name: string): Record<string, string> {
  const block = css.match(new RegExp(`\\[data-theme="${name}"\\]\\s*\\{([\\s\\S]*?)\\n\\}`));
  if (!block) return {};
  const out: Record<string, string> = {};
  for (const m of block[1].matchAll(/^\s*(--[a-z0-9-]+)\s*:\s*([^;]+);/gm)) {
    out[m[1]] = m[2].trim();
  }
  return out;
}

describe("themes", () => {
  // 主题只允许覆盖颜色类 token；字体、间距、圆角、阴影是全局不变量。
  const colorTokens = () =>
    declaredTokens(tokens).filter((t) => t.startsWith("--color-") || t === "--backdrop");

  it.each(["dark", "arcade"])("theme %s overrides every color token", (name) => {
    const overridden = new Set(Object.keys(themeBlock(themes, name)));
    expect(colorTokens().filter((t) => !overridden.has(t))).toEqual([]);
  });

  // arcade 用 accent-600 而不是设计稿原本的 accent-500 (#ec3013)：后者配白字
  // 只有 4.20:1，达不到 AA。这条断言就是当初发现该问题的地方，别放宽阈值。
  it.each([
    ["beige", "#f3f2f2", "#201e1d"],
    ["dark", "#201e1d", "#f3f2f2"],
    ["arcade", "#dd2b0f", "#ffffff"],
  ])("theme %s meets WCAG AA for body text", (_name, bg, text) => {
    expect(contrastRatio(bg, text)).toBeGreaterThanOrEqual(4.5);
  });

  // 主题块里声明的背景值必须就是上面校验过的那个，否则校验形同虚设。
  it.each([
    ["dark", "#201e1d"],
    ["arcade", "#dd2b0f"],
  ])("theme %s declares the verified background", (name, bg) => {
    expect(themeBlock(themes, name)["--color-bg"]).toBe(bg);
  });
});
