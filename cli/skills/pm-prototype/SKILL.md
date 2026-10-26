---
name: pm-prototype
description: Use when generating or regenerating an interactive HTML prototype from a version PRD in the WeKnora PM workstation — produces a single self-contained HTML file with mock data and no backend dependency.
metadata:
  tested_against: v0.10
---

# PM 原型生成（pm-prototype）

**确定性技能**：给定 PRD 即静默产出 HTML 原型，不向用户提问。
服务于「PM 工作站 → 版本管理 → 生成/重新生成原型」。

## 1. 触发场景

- PRD 生成成功后自动触发（异步任务）。
- 评审驳回后，基于新版 PRD 重新生成原型。

## 2. 输入契约

| 字段 | 说明 |
|---|---|
| `prd_markdown` | 版本 PRD 全文（必填） |
| `version` | 版本号或「草稿」 |
| `project` | 项目名称、简介 |
| `existing_prototype` | 重生成时的上一版原型（可选，用于保持结构延续） |

PRD 缺少第 3 章「功能详述」时不得生成，返回失败原因「PRD 功能详述为空」。

## 3. 生成规则

1. 按 PRD 的「模块 → 功能点 → 操作项」组织导航与页面，模块作为一级导航。
2. 每个功能点至少对应一个可交互页面或面板；操作项体现为按钮、表单、抽屉或弹窗。
3. 全部使用 mock 数据，覆盖：有数据、空态、加载态、错误态。
4. 页面之间可跳转，关键操作可点击并有反馈（toast / 抽屉 / 弹窗），不要求真实持久化。
5. 重生成时保留上一版的导航结构与视觉风格，仅按新 PRD 增删页面。

## 4. 输出契约

- 产出**单个** `.html` 文件，文件名 `原型-{版本号或草稿}.html`。
- 文件必须自包含：CSS / JS 内联，**不引用任何外部 CDN、字体、图片 URL**；图标用内联 SVG 或字符。
- 首屏即进入业务界面，不设置营销落地页。
- 结构约定：

```html
<!doctype html>
<html lang="zh-CN">
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1" />
  <title>{项目名称} 原型-{版本}</title>
  <style>/* 设计令牌 + 布局 + 组件样式 */</style>
</head>
<body>
  <!-- 左侧导航：模块 -->
  <!-- 主区域：功能页面，data-page 标识 -->
  <script>/* 路由切换、mock 数据、交互反馈 */</script>
</body>
</html>
```

- 视觉要求：桌面优先（最小宽度 1280px），中性浅色底，主色单一，圆角与间距统一；
  字体使用系统字体栈（`-apple-system, "Segoe UI", "PingFang SC", "Microsoft YaHei", sans-serif`）。
- 可访问性：交互元素使用 `button` / `a`，表单控件带 `label`，焦点态可见。

## 5. 质量自检

| 检查项 | 通过标准 |
|---|---|
| 覆盖度 | PRD 每个功能点都有对应页面或面板 |
| 可交互 | 导航可切换，主要按钮可点击并有反馈 |
| 自包含 | 无任何外部网络请求（`fetch` / `src="http` / `@import url(` 均为 0） |
| 状态 | 每个列表页至少提供空态；表单提供必填校验提示 |
| 一致性 | 同类组件样式与交互一致，不出现两套按钮风格 |

## 6. 沙箱能力边界

- **不得联网**：不得下载模板、图片、字体或调用任何外部服务。
- **单次执行不超过 120 秒**：页面过多时优先保证每个模块首页完整，次要页面给骨架 + 说明。
- **ReAct 不超过 10 轮**：不做多轮渲染截图校验。
- 只产出 HTML 文件内容，不写知识库、不修改版本状态。

## 7. 失败兜底

| 情况 | 处理 |
|---|---|
| PRD 功能详述为空 | 返回 `{"error": "PRD 功能详述为空"}` |
| PRD 模块过多（>12） | 合并为「其他模块」入口并在页面内注明，不丢功能点 |
| HTML 超出单文件合理体量 | 保留核心模块完整实现，其余模块仅保留导航与占位说明 |
| 重生成时旧原型缺失 | 按新 PRD 从头生成，不假设旧结构 |
| 生成中断 | 输出已完成的 HTML 并在末尾注释标注未完成模块，供重新触发 |
