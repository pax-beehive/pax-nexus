# Wiki 生成指令预设 chips 设计

日期:2026-07-30
状态:设计已定,待写实现计划
前置:`2026-07-30-wiki-generation-settings-design.md`(PR #41,本功能 stacked 其上)

## 1. 目标与动机

自由文本 custom instructions 的空白输入框自由度过大,用户不知道写什么。第一档解法:
在 WikiStatusPage 的 Generation 卡片上提供一组精选风格预设(多选 chips),选中即组合成
指令文本。引导式 LLM 向导(grill-me 式)为第二档,待观察真实使用后再决策——本 spec 不含。

## 2. 核心机制:与现有存储的互通

**存储、API、后端零改动。** chips 只是 `custom_instructions` 文本的一种输入方式。

- 每个预设 = `{ id, label, sentence }`,`sentence` 为固定英文指令句,作为常量数组
  `INSTRUCTION_PRESETS` 放在前端(`WikiStatusPage.tsx` 或就近模块)。
- **保存(组合)**:`custom_instructions` = 选中 chips 的 sentence(按数组固定顺序,
  每句一行)+ 空行 + 自由文本(trim 后非空才拼)。整体 trim 后提交。
- **加载(还原)**:对每个预设做**整句精确匹配**——存储文本中命中该 sentence 则 chip
  点亮,并把该句(及其紧邻换行)从文本中摘除;全部摘除后剩余文本 trim 后回填自由
  文本框。被手工编辑过的句子不再匹配,自然落回自由文本框,内容不丢失。
- 2000 字符(rune)上限对**组合后的整体**生效:剩余字数提示按组合后长度计算;组合
  超限时禁用保存并提示。textarea 自身的 `maxLength` 调整为动态余量或移除(以组合计
  数为准,保留后端校验兜底)。

## 3. 预设清单(初版 6 个)

| id | label | sentence |
| --- | --- | --- |
| tables | Prefer tables | Prefer tables over prose when presenting comparisons, options, or structured data. |
| newcomer | Newcomer-friendly | Write for readers new to the team: briefly explain team-specific terms and acronyms on first use. |
| bullets | Concise bullets | Prefer concise bullet points over long paragraphs. |
| english-terms | Keep English terms | Keep technical terms, code identifiers, and product names in English even when writing in another language. |
| tldr | TL;DR first | Start every page with a one-paragraph TL;DR summary. |
| examples | Include examples | Include concrete examples or code snippets where they clarify a decision or process. |

增删预设 = 改这个数组。旧存量文本中已不在数组里的句子按普通文本回填,不报错。

## 4. UI

整张 Generation 卡片统一为"点选为主"的体验:

- **语言从下拉改为单选 chip 组**(Follow source / 简体中文 / English / Custom…):
  语义与 #41 完全一致——Follow source = 空值;Custom 选中时展示文本输入;加载值不在
  预设内时落入 Custom 并回填(#41 的 custom-fallback 行为保持)。仅交互形态变化,
  `language` 的存储与提交不变。
- **风格预设为多选 chip 组**,排在语言之下,toggle 交互;
- 语言 chip(单选)与风格 chip(多选)用同一套视觉,选中态区分明显;新增
  `.wiki-preset-chips` 一小块 CSS,规格对齐 `.wiki-generation` 现有块;
- 自由文本框标签改为 "Additional instructions",placeholder 提示它与 chips 叠加;
- 剩余字数提示移到卡片底部,反映组合后长度;
- 保存按钮与成功文案不变。

## 5. 测试(dom)

- chips 选中 + 附加文本 → 保存 PUT body 为"句子们 + 空行 + 附加文本"的组合,顺序固定;
- 加载含两句预设句 + 其他文本的存量值 → 对应 chips 点亮,其余文本回填 textarea;
- 加载被手工改过一字的预设句 → chip 不点亮,整句落回 textarea;
- 组合超 2000 字符 → 保存按钮禁用、提示显示;
- 语言 chip 组:选中 简体中文 chip → PUT body language 为 "简体中文";加载非预设语言
  值 → Custom chip 点亮且文本输入回填(#41 行为在新形态下保持);
- 现有 generation 卡片测试(默认态/保存流/自定义语言回退)更新到 chip 交互后全部通过。

## 6. 非目标

- LLM 引导式向导(第二档)、问题生成、答案存储;
- 后端/存储/校验改动;
- chips 的多语言 label(与 UI 现状一致用英文);
- 预设句本身的多语言版本(指令句恒为英文,LLM 按语言设置产出目标语言内容)。
