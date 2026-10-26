---
name: pm-requirement-analysis
description: Use when analyzing a newly submitted product requirement in the WeKnora PM workstation — asks at most 3 clarifying questions per turn, then returns structured JSON covering value, related feature impact, risks, suggested priority, suggested status, and a concrete design plan.
metadata:
  tested_against: v0.10
---

# PM 需求分析（pm-requirement-analysis）

交互式技能：服务于「PM 工作站 → 需求池 → 新建需求 → AI 分析」。
先追问澄清，再给结论；**不允许在信息不足时直接下结论**。

## 1. 触发场景

- 用户在需求弹窗中填写初稿后点击「AI 分析」。
- 用户对已有需求点击「重新分析」。

## 2. 输入契约

调用方注入的上下文固定包含以下小节：

| 小节 | 内容 |
|---|---|
| 所属项目 | 项目名称 |
| 需求初稿 | 标题、描述、优先级、来源、提出人 |
| 产品路线图（最新生效快照） | 目标用户、核心价值、首期范围、三级功能清单 |

- 路线图缺失时上下文为「（暂无路线图快照）」，此时必须提醒用户：缺少路线图会降低关联影响判断的准确度。
- 初稿描述过短（少于 20 字）或缺少验收预期时，优先追问。

## 3. 交互规则

1. **每次最多 3 个问题**，按重要性排序，不要连发多轮无关问题。
2. 只追问影响结论的关键信息：目标用户、使用场景、期望结果、与现有功能的边界。
3. 用户回答后可直接给结论；用户表示「不再补充」时，基于现有信息给结论并显式标注假设。
4. 追问轮次建议不超过 3 轮，避免阻塞需求流转。

## 4. 输出契约（强制 JSON）

最终结论必须是**单个 JSON 代码块**，键名固定：

```json
{
  "value": "该需求解决什么问题、对目标用户/业务的价值",
  "related_impact": [
    {"feature": "模块/功能点/操作项", "impact": "新增|变更|移除", "detail": "影响说明"}
  ],
  "risks": [
    {"level": "high|medium|low", "description": "风险描述", "mitigation": "缓解建议"}
  ],
  "suggested_priority": "P0|P1|P2|P3",
  "suggested_status": "pending|confirmed|planned|shelved",
  "design_plan": "具体设计方案：页面/交互/数据/接口层面的落地方式"
}
```

约束：

- `value` 1–3 句，禁止空泛表述（如「提升体验」）。
- `related_impact` 为空时输出 `[]`，不要编造不存在的功能点；只引用路线图快照中出现的节点。
- `risks` 至少 1 条；无显著风险时给 1 条低风险项，不得输出空数组。
- `suggested_status` 默认 `pending`；仅当需求描述完整、边界清晰时才建议 `confirmed`。
- `design_plan` 必须可执行：说明入口、关键交互、数据结构与依赖，不少于 3 句。

## 5. 结论质量要求

| 维度 | 要求 |
|---|---|
| 价值 | 与产品路线图的核心价值对齐，说明受益角色与场景 |
| 关联影响 | 按三级路径定位，说明是新增还是修改既有功能 |
| 风险 | 覆盖范围蔓延、依赖缺失、体验冲突、数据迁移等 |
| 优先级 | 结合首期范围判断；与首期无关的建议降级 |
| 设计方案 | 给出最小可落地版本，并标注可延后项 |

## 6. 沙箱能力边界

- **不得联网**：不得检索外部资料，只依据注入上下文与需求初稿。
- **单次执行不超过 120 秒**；追问与结论分开发送，不要在一次响应中同时追问又下结论。
- **ReAct 不超过 10 轮**：澄清与自检工具调用合计不超过 10 轮。
- 只读分析，不得直接修改需求、功能清单或路线图；写库由调用方完成。

## 7. 失败兜底

| 情况 | 处理 |
|---|---|
| 用户拒绝补充信息 | 基于现有信息输出结论，并在 `value` 末尾注明「基于假设：…」 |
| 路线图缺失 | `related_impact` 置空并在 `value` 中提示补充路线图后重新分析 |
| 需求与已有功能重复 | 在 `related_impact` 标注「已存在」，`suggested_status` 建议 `shelved` 并说明理由 |
| JSON 输出格式错误 | 仅重发 JSON 代码块修正一次；仍失败则输出同结构纯文本并显式说明解析失败 |
