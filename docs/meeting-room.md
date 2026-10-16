# 会议室（Meeting Room）使用指南

会议室是 WeKnora 中的多智能体协作子系统：围绕一个主题创建"会议"，拉入多个智能体进行广播式讨论，用户逐条评审各智能体的意见，最终由主持人（系统）生成结构化方案，并沉淀为可回滚、可导出的产物。

## 1. 功能概述

- **广播式讨论**：用户发言后，参会智能体按顺序（sequential）或并行（parallel）模式依次发言，所有人可见全部发言。
- **用户评审**：每条智能体意见可标记"采纳 / 不采纳"，未处理的保持"待评审"。
- **主持人生成方案**：主持人汇总所有已采纳意见，生成包含「背景与目标 / 方案要点 / 风险与待确认事项 / 下一步行动」四小节的结构化方案。
- **产物管理**：每次生成方案都会产生带版本号的产物，支持查看历史、回滚到任意历史版本、导出为 Markdown 文件。
- **护栏机制**：两个智能体反复互相辩驳时，主持人自动打断并把话题拉回正题。

## 2. 核心概念

| 术语 | 说明 |
|---|---|
| 会议（Meeting Room） | 一个独立讨论空间，由创建者全权管理；**仅创建者可见** |
| 主持人（Host） | 系统自动担任的协调者，负责开场、打断互呛、生成方案；不可移除 |
| 参会智能体（Agent） | 创建会议时选择的 CustomAgent，至少 1 个；同一会议内不可重复 |
| 评审状态 | `pending`（待评审）→ `adopted`（采纳）/ `rejected`（不采纳）；采纳后可回设为待评审 |
| 方案（Solution） | 主持人基于已采纳意见生成的 `is_solution` 消息，带版本号 |
| 产物（Artifact） | 方案的版本化记录，含采纳/不采纳数快照，支持导出 |
| 护栏（Guard） | 互呛检测：同一对智能体在无用户介入的情况下连续互答达到阈值即打断 |
| 讨论轮（Turn） | 一次"用户发言触发 → 智能体依次应答 → 轮次结束"的完整过程；同一会议同一时刻最多一轮 |

## 3. 快速上手

1. **创建会议**：侧边栏进入「会议室」→「创建会议」，填写标题与描述，选择 1~N 个智能体，选择讨论模式。
2. **发起讨论**：进入会议房间，输入主题或问题发送。智能体将自动依次发言，SSE 实时推送。
3. **评审意见**：对每条智能体消息点击「采纳」或「不采纳」；可随时改判（回设为待评审）。
4. **生成方案**：确认采纳意见后点击「生成方案」，主持人输出结构化方案（需至少 1 条采纳意见）。
5. **导出方案**：右侧产物抽屉中查看历史方案，点击「导出 .md」下载 `meeting-solution-v{版本号}.md`。

## 4. 讨论模式与护栏

### 讨论模式

| 模式 | 语义 | 适用场景 |
|---|---|---|
| `sequential`（顺序，默认） | 智能体逐个发言，后者可见前者的发言，可相互呼应 | 需要观点交锋与迭代的深度讨论 |
| `parallel`（并行） | 智能体同时独立作答，互不感知 | 需要多视角独立意见的快速收集 |

- 会议级默认值由 `MEETING_DEFAULT_BROADCAST_MODE` 配置；创建会议时可指定，也可在会议详情中修改（PATCH）。

### 护栏（互呛打断）

- 判定：最后一条用户/主持人消息之后，若**同一对智能体**严格交替发言且完整来回数 ≥ `guard_max_exchange`（默认 3），触发打断。
- 触发后：主持人发送护栏消息，把话题拉回用户最初议题；`guard.triggered` 事件广播到前端。
- 阈值可按会议调整（PATCH 的 `guard_max_exchange`，最小 1）；全局默认由 `MEETING_GUARD_MAX_EXCHANGE` 配置。

### 其他保护

- **打断讨论**：讨论进行中可点击「打断」，当前轮次尽快终止。
- **历史窗口**：构造 LLM 上下文时最多携带最近 `MEETING_CONTEXT_MAX_MESSAGES`（默认 50）条消息；更早的内容按需摘要压缩。
- **并发控制**：同一会议同一时刻仅允许一个讨论轮（Redis 分布式锁保证），重复触发不会叠加。

## 5. 归档与删除

- **归档**：会议详情页点击「归档」。归档后会议进入**只读**状态——发言、评审、生成方案、回滚均被拒绝（返回 403「会议已归档，仅可查看」），但**历史浏览与方案导出不受影响**。列表页可通过「全部 / 进行中 / 已归档」筛选。重复归档幂等（仍返回成功，不产生重复系统消息）。
- **删除**：列表页点击「删除」并确认。删除为**软删除**，会议从列表消失、详情返回 404，数据保留在库中（`deleted_at` 非空）以便审计恢复。

## 6. API 一览

所有端点前缀 `/api/v1/meetings`，需携带登录态（JWT）。权限列中：

- `Contributor`：租户内可创建会议的成员
- `Viewer`：租户内任意成员（列表结果按创建者过滤，仅返回本人会议）
- `OwnedOrAdmin`：会议创建者本人或租户管理员

| # | 方法 | 路径 | 权限 | 说明 |
|---|---|---|---|---|
| 1 | POST | `/meetings` | Contributor | 创建会议（标题 + 智能体列表 + 可选模式/护栏参数） |
| 2 | GET | `/meetings` | Viewer | 会议列表（`?status=active\|archived` 过滤，仅本人） |
| 3 | GET | `/meetings/:id` | OwnedOrAdmin | 会议详情（含参会者） |
| 4 | PATCH | `/meetings/:id` | OwnedOrAdmin | 更新标题/描述/广播模式/护栏阈值 |
| 5 | POST | `/meetings/:id/archive` | OwnedOrAdmin | 归档会议（幂等，归档后只读） |
| 6 | DELETE | `/meetings/:id` | OwnedOrAdmin | 删除会议（软删除） |
| 7 | GET | `/meetings/:id/messages` | OwnedOrAdmin | 历史消息（游标分页：`before_id` + `limit`） |
| 8 | POST | `/meetings/:id/messages` | OwnedOrAdmin | 发送用户消息（触发讨论轮；归档拒绝） |
| 9 | POST | `/meetings/:id/interrupt` | OwnedOrAdmin | 打断进行中的讨论轮 |
| 10 | PUT | `/meetings/:id/messages/:mid/review` | OwnedOrAdmin | 评审智能体消息（adopted/rejected/pending） |
| 11 | POST | `/meetings/:id/solution` | OwnedOrAdmin | 生成结构化方案（需 ≥1 条采纳意见） |
| 12 | GET | `/meetings/:id/artifacts` | OwnedOrAdmin | 方案产物列表（含正文，版本升序） |
| 13 | GET | `/meetings/:id/artifacts/:aid/export` | OwnedOrAdmin | 导出方案为 Markdown 文件（归档后可用） |
| 14 | POST | `/meetings/:id/solution/rollback` | OwnedOrAdmin | 回滚方案到指定历史版本 |
| 15 | GET | `/meetings/:id/stream` | OwnedOrAdmin | SSE 事件流订阅 |

## 7. SSE 事件

订阅 `GET /api/v1/meetings/:id/stream`，响应为 `text/event-stream`，每条事件格式：

```json
{ "type": "<事件名>", "data": { ... } }
```

| 事件名 | 触发时机 | data 内容 |
|---|---|---|
| `message.created` | 新消息落库（用户/智能体/主持人/系统） | 消息对象（含 sender、content、review_state 等） |
| `message.review_updated` | 评审状态变更 | 消息对象（含最新 review_state） |
| `discussion.turn_started` | 讨论轮开始 | 轮次上下文（触发消息等） |
| `discussion.turn_ended` | 讨论轮结束 | 轮次收尾信息 |
| `guard.triggered` | 互呛护栏触发 | 主持人护栏消息与互呛双方 |
| `solution.generating` | 方案生成开始 | 提示性事件（前端显示生成中状态） |
| `solution.created` | 方案生成完成 | 方案消息对象（含版本号） |
| `ping` | 心跳保跳 | 空 |

**断线重连**：前端使用 `@microsoft/fetch-event-source` 订阅，断线后自动重连；重连后通过 `GET /messages` 拉取历史补齐缺口，事件流本身不回放。

## 8. 配置项

| 环境变量 | 默认值 | 说明 |
|---|---|---|
| `MEETING_GUARD_MAX_EXCHANGE` | `3` | 全局互呛护栏阈值（完整来回数）；会议可单独覆盖 |
| `MEETING_CONTEXT_MAX_MESSAGES` | `50` | 构造 LLM 上下文时携带的最近消息条数上限 |
| `MEETING_DEFAULT_BROADCAST_MODE` | `sequential` | 新建会议的默认讨论模式 |

以上均可通过环境变量覆盖，未配置时使用默认值；非法值（≤0 / 非法模式）自动回退默认。

## 9. FAQ

**Q：归档后还能导出方案吗？**
能。归档仅冻结"写操作"（发言/评审/生成/回滚），历史浏览与 `导出 .md` 不受影响。

**Q：为什么我看不到同事创建的会议？**
会议室遵循"仅创建者可见"：列表与详情均按 `tenant_id + created_by` 双重过滤，其他人（含同租户）访问返回 404。租户管理员经由管理通道可见。

**Q：方案生成为什么报"没有已采纳的意见"？**
生成方案要求至少 1 条 `adopted` 状态的智能体消息，请先评审采纳；`rejected` 与 `pending` 均不参与。

**Q：方案生成错了怎么办？**
每次生成都是新版本，不会覆盖旧版。在产物抽屉中选择历史版本「回滚」即可把当前方案指针切回去；回滚本身也可再次操作（指向任意历史版本）。

**Q：两个智能体互相抬杠停不下来？**
护栏会在达到阈值（默认 3 个来回）时由主持人自动打断；也可以手动「打断讨论」，或调低该会议的护栏阈值。
