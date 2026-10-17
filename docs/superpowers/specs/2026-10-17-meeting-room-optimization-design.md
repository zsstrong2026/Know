# 会议室（Meeting Room）模块优化设计

- **日期**：2026-10-17
- **状态**：待审阅
- **作者**：与用户协作 brainstorming 产出
- **前置文档**：[2026-10-13 会议室功能设计](./2026-10-13-meeting-room-design.md)

---

## 0 · 背景与目标

会议室模块已完成首版（M1–M6）：用户可创建会议、拉多个智能体讨论、评审意见、由主持人汇总方案、在侧边抽屉查看历史方案。首版上线后暴露 4 个体验与能力问题：

1. **卡片信息噪音**：会议列表卡片上平铺展示「发言模式 / 发言权路由 / 主持人介入」等技术参数，普通用户看不懂也不关心。
2. **无详情入口**：卡片只有「进入会议」，想看/改会议配置必须进房再找设置，且**参会智能体创建后完全不可变更**（首版刻意定死）。
3. **创建即冷启动**：建完会议是一片空白，用户不知道该说什么、从哪开始，缺少引导。
4. **产物形态单一**：产物只有「方案 markdown」一种，且必须先走「评审 → 汇总」流程才能得到；用户实际需要的是**按需指定形态的产物**（报告 / 表格 / PPT / 网页等），并且一次会议可以产出多个。

**优化目标**

| # | 目标 | 对应问题 |
|---|---|---|
| G1 | 会议卡片只保留「状态标签 + 时间」，移除技术参数 | 1 |
| G2 | 新增会议详情弹窗，可查看并编辑全部会议设置（含参会智能体增删） | 2 |
| G3 | 创建会议后主持人立即开场：总结主题 + 整理建议流程，用户确认后开跑 | 3 |
| G4 | 产物成为一等概念：多产物、多形态、可调用工具箱技能生成；抽屉「卡片列表 → 详情」两态 | 4 |

**非目标**：不改动现有 solution（方案汇总/评审/回滚）的内部机制，只统一展示层（见 §1 决策 B1）。

---

## 1 · 关键决策汇总

| 议题 | 决策 |
|---|---|
| G1 卡片精简 | 卡片页脚**只留**「状态标签 + 时间」；移除发言模式、发言权路由、主持人介入(n) 三个 chip |
| G2 详情入口 | 新增 `MeetingDetailDialog.vue`，卡片加「详情」入口；弹窗是**会议设置总入口**（可编辑标题/描述/参会智能体/讨论模式/发言权路由/主持人介入间隔/共识检测/护栏阈值） |
| G2 参会人可变更 | 新增 `PUT /meetings/:id/participants` 全量替换参会智能体；**主持人不可移除、至少保留 1 个 agent**；归档只读；**讨论进行中 409**；`awaiting_confirmation` 态允许 |
| G3 开场时机 | 创建成功后**异步触发**主持人开场；开场输出 = 主题总结 + 建议流程 |
| G3 开场落库 | 开场消息以 `sender_kind=host` **落库**，`payload={"stage":"opening"}`，可刷新复原 |
| G3 确认交互 | 开场消息下方两个按钮：「开始会议」→ `POST /meetings/:id/confirm`（幂等置 active + 自动 `RunTurn`）；「调整流程」→ 复用 `POST /meetings/:id/messages` 带 `adjust_flow` 标记；用户直接打字 = 确认 + 附带要求 |
| G3 状态建模 | 新增 `MeetingStatusAwaitingConfirmation = "awaiting_confirmation"`（C1） |
| G4 产物执行者 | 产物由**主持人执行**生成（用户从 UI 指定形态与要求） |
| G4 产物形态集合 | 8 种：`report_markdown`(Markdown 报告) / `minutes`(会议纪要) / `todo_list`(待办事项) / `docx`(Word) / `xlsx`(Excel) / `pptx`(PPT) / `html_report`(网页报告) / `chart_image`(图表图片) |
| G4 技术选型 | **A1**：为会议懒创建「后台会话」（sessions 行 + 沙箱 pin，侧栏隐藏）→ 跑 AgentQA（允许工具）→ 文件由采集器按 `(session, message, index)` 落库 → 回写 `MeetingArtifact` |
| G4 展示层 | **B1**：不改 solution 内部机制（`is_solution` / `current_solution_message_id` / `review_state` / `version` / `rollback` 全保留），只在抽屉展示层把 solution 与 generated 产物统一为「产物卡片」 |
| G4 生成接口 | 生成是**长耗时操作**（首次需拉起沙箱）→ 必须 `202 异步 + SSE`，不能沿用现有同步的 `POST /:id/solution` 模式 |
| G4 并发 | **room 级互斥串行**生成，避免采集器 diff 去重互相干扰 |
| G4 本期范围 | 做 `regenerate`；**不做**产物删除 / 分享 / 跨会议产物库 |
| 兼容 | 新列走 GORM AutoMigrate；历史 solution 行 `Kind` 为空按 solution 兜底；保留 `POST /:id/solution` 与 `ExportArtifact` |
| 文档语言 | 中文 |

---

## 2 · 数据模型

> 全部为**增量变更**，通过 GORM AutoMigrate 自动加列；无破坏性 DDL。历史数据靠默认值 + 读取时兜底兼容。

### 2.1 `meeting_rooms`（新增字段）

| 字段 | 类型 | 说明 |
|---|---|---|
| artifact_session_id | varchar(36) | **新增**。懒创建的隐藏后台会话 ID，用于跑 AgentQA 采集产物；空表示尚未创建 |

**变更点**
- `status` 取值范围由 `active / archived` 扩展为 **`active / awaiting_confirmation / archived`**。

### 2.2 `meeting_artifacts`（扩展字段）

现有字段全部保留（`solution_message_id` / `version` / `adopted_count` / `rejected_count` / `exported_format`），新增：

| 字段 | 类型 | 说明 |
|---|---|---|
| kind | varchar(16) | `solution`（原方案汇总）/ `generated`（用户指定形态生成）。**历史行空值按 `solution` 兜底** |
| title | varchar(255) | 产物标题（展示用） |
| file_name | varchar(255) | 文件名（generated 产物用） |
| file_type | varchar(32) | 文件类型/MIME（如 `text/markdown` / `application/vnd.openxmlformats...`） |
| file_size | bigint | 字节数 |
| url | varchar(512) | 产物存储 URL（`provider://path`，对齐 `types.MessageArtifact.URL`） |
| content_hash | varchar(64) | 内容哈希（去重/校验） |
| status | varchar(16) | `generating` / `ready` / `failed` |
| error | text | 生成失败原因 |
| source_message_id | varchar(36) | 生成该产物的会话消息 ID，用于按 `(session, message, index)` 寻址下载 |

**兼容规则**
- 历史行：`kind=""` 视为 `solution`，`status=""` 视为 `ready`，其余新字段为空；读取路径统一做 normalize。
- solution 原有行为（`SolutionMessageID` 指向会议消息、`version` 递增、`rollback` 切 `current_solution_message_id`）**完全不变**。

### 2.3 关联复用（不新增表）

generated 产物的**二进制字节**不落 `meeting_artifacts`，而是复用平台既有链路：

- 会议后台会话（`MeetingRoom.ArtifactSessionID`）的某条 assistant 消息携带 `MessageArtifact`（`internal/types/message.go`），按 `(session_id, message_id, index)` 三元组寻址。
- 下载复用 `internal/handler/session/artifact_download.go` 的能力；`meeting_artifacts` 只存**索引与元数据**（`url` / `source_message_id` / 文件元信息）用于在会议上下文内直接列卡片。

---

## 3 · API 契约

挂在 `/api/v1/meetings`，全部 `tenant_id + created_by` 双重过滤。**归档房间只读**（现有语义保留）；**`awaiting_confirmation` 视为可写态**（见 §4.4）。

### 3.1 新增 / 变更端点

| 方法 | 路径 | 说明 | 权限 |
|---|---|---|---|
| PUT | `/meetings/:id/participants` | **新增**。全量替换参会智能体 `{agent_ids: []}`；主持人不可移除、至少 1 个 agent、讨论中 409 | Viewer+（owner） |
| PATCH | `/meetings/:id` | **变更**。补齐校验并放宽 status 判定（`active` 或 `awaiting_confirmation` 可改） | Viewer+（owner） |
| POST | `/meetings/:id/confirm` | **新增**。确认流程开场：幂等置 `active` + 自动触发首轮 `RunTurn` | Viewer+（owner） |
| POST | `/meetings/:id/artifacts` | **新增**。请求生成产物 `{kind_type, title, requirement}` → **202**，异步执行 | Viewer+（owner） |
| GET | `/meetings/:id/artifacts` | **变更**。返回全部产物（solution + generated），含 `status` / `kind` / 文件元信息 | Viewer+（owner） |
| GET | `/meetings/:id/artifacts/:aid` | **变更**。产物详情（solution 联查带正文；generated 带文件元信息 + 预览 URL） | Viewer+（owner） |
| GET | `/meetings/:id/artifacts/:aid/download` | **新增**。generated 产物下载（流式；solution 沿用 export） | Viewer+（owner） |
| POST | `/meetings/:id/artifacts/:aid/regenerate` | **新增**。按原参数重新生成（**202**） | Viewer+（owner） |
| POST | `/meetings/:id/messages` | **变更**。支持请求体可选 `adjust_flow: true`，用于「调整流程」分支 | Viewer+（owner） |

**保留不动**：`POST /meetings/:id/solution`（同步生成方案，老路径）、`GET /meetings/:id/artifacts/:aid/export`（solution 导出 .md）、`POST /meetings/:id/solution/rollback`、`POST /meetings/:id/archive`、`POST /meetings/:id/interrupt`、`GET /meetings/:id/stream`、评审端点。

### 3.2 生成请求体

```json
{
  "kind_type": "report_markdown | minutes | todo_list | docx | xlsx | pptx | html_report | chart_image",
  "title": "产物标题",
  "requirement": "用户补充要求（自然语言，可空）"
}
```

### 3.3 SSE 事件

现有事件常量（`internal/application/service/meeting_broadcast.go`）：`message.created`、`message.review_updated`、`discussion.turn_started`、`discussion.turn_ended`、`guard.triggered`、`consensus.created`、`solution.generating`、`solution.created`、`ping`。

**新增**：

- `discussion.flow_confirmation` —— 开场消息创建后推送，携带 `{message_id, stage:"opening"}`，前端据此渲染「开始会议 / 调整流程」按钮组
- `artifact.generating` —— 产物进入 `generating`，携带 `{artifact_id, kind_type, title}`，前端插入 loading 占位卡片
- `artifact.created` —— 产物 `ready`，携带产物元信息，卡片就位
- `artifact.failed` —— 产物 `failed`，携带 `{artifact_id, error}`

---

## 4 · 讨论引擎与状态机

### 4.1 房间状态机

```
                创建
                 │
                 ▼
        awaiting_confirmation ──confirm──▶ active ──archive──▶ archived
                 │                            │                  （只读终态）
                 └──────用户直接打字(=确认+要求)──┘
```

- **进入**：`CreateMeeting` 成功后同步置 `awaiting_confirmation` 并返回；**异步**触发开场生成。
- **离开**：`POST /:id/confirm`（幂等：已是 `active` 直接返回）或用户直接在房间里发消息（隐式 confirm + 携带内容开跑）。
- `archived` 仍为终态、只读。

### 4.2 开场（opening）

1. `CreateMeeting` 返回后，service 层异步调用主主持人：输入 = 会议 `Title` / `Description` / 参会智能体名单 / 会议级 KB。
2. 主持人输出 = **主题总结**（对议题的理解与拆解）+ **建议流程**（分段/轮次建议 + 建议重点）。
3. 输出以 `sender_kind=host` **落库**为 `MeetingMessage`，`Payload = {"stage":"opening"}`，并 `Publish(message.created)` + `Publish(discussion.flow_confirmation)`。
4. 失败（LLM 超时/报错）→ 落一条 `sender_kind=system` 的兜底提示，房间**仍停在** `awaiting_confirmation`，用户可手动发消息开跑。

> 开场复用现有 `resolveHostChat`（租户默认模型，纯文本），**不引入沙箱**。

### 4.3 「开始会议 / 调整流程」交互

| 用户动作 | 后端行为 |
|---|---|
| 点「开始会议」 | `POST /:id/confirm`：置 `active` + 自动发起首轮 `RunTurn` |
| 点「调整流程」 | 前端把输入框切到「对流程的修改意见」模式，发送时带 `adjust_flow:true`；后端保留在 `awaiting_confirmation`，用该消息 + 原开场作为上下文让主持人**重出流程**（新的 opening 消息） |
| 直接打字发送 | 等同「确认 + 附带要求」：置 `active`，把该消息作为首轮讨论的用户输入 |

### 4.4 归档守卫放宽（**关键改动**）

现有实现存在硬编码 `!= active` 判定，新增状态后会**误拒所有写操作**，必须同步放宽：

- [meeting_lifecycle.go](file:///d:/知识库/Know/internal/application/service/meeting_lifecycle.go#L14-L20) 的 `ensureRoomActive`：由「非 active 即拒绝」改为 **`active` 或 `awaiting_confirmation` 均可写**（仅 `archived` 拒绝）。
- [meeting.go](file:///d:/知识库/Know/internal/handler/meeting.go) `UpdateMeeting` handler 内的 `room.Status != MeetingStatusActive → 403`：同样放宽为「仅 `archived` 返回 403」。

`solution` 相关写操作（`GenerateSolution` 等）在 `awaiting_confirmation` 下的语义：允许，但通常用户此时还未讨论，无实际内容；不做额外限制。

### 4.5 产物生成链路（A1）

```
用户点「生成产物」→ POST /:id/artifacts (202)
        │
        ▼
   room 级互斥锁（同房间串行，避免采集器 diff 互相干扰）
        │
        ▼
  懒创建会议后台会话 ArtifactSessionID（sessions 行 + 沙箱 pin，侧栏隐藏）
        │  已存在则复用
        ▼
  构造 prompt（产物形态 + 标题 + 用户要求 + 会议上下文/已采纳意见）
        │
        ▼
  AgentQA 执行（允许工具/技能，使用沙箱）
        │
        ▼
  ArtifactCollector 采集沙箱产出文件（按 path+mtime+hash 去重）
        │
        ▼
  写入 MessageArtifact（落在会话消息上）+ 回写 MeetingArtifact 元数据
        │
        ▼
  Publish(artifact.created) / artifact.failed
```

**关键约束**

- 生成走 **202 异步 + SSE**（首次拉起沙箱耗时长，不能同步等待）。
- **room 级互斥**：同房间同时只允许一个生成任务，「生成中」再点生成返回 409 或排队提示。
- 后台会话**在聊天侧栏隐藏**（沿用现有隐藏机制，如 `IsSkillMaintenanceDescription` 同类做法），避免污染用户会话列表。
- 会议内**讨论**仍走无沙箱的 `cloneAgentForMeeting`（不变）；只有**产物生成**这条链路启用沙箱。

### 4.6 产物展示层统一（B1）

- `GET /:id/artifacts` 返回 union：solution 产物（`kind=solution`，正文来自关联消息）+ generated 产物（`kind=generated`，文件元信息）。
- 抽屉统一渲染为**产物卡片**（标题 / 类型图标 / 状态 / 时间），点卡片进详情。
- solution 卡片保留「回滚 / 导出 .md」动作；generated 卡片提供「下载 / 重新生成」。
- `rollback`、`current_solution_message_id`、`review_state` 等机制**零改动**。

---

## 5 · 前端

### 5.1 会议卡片精简（G1）

[MeetingLayoutView.vue](file:///d:/知识库/Know/frontend/src/views/meeting/MeetingLayoutView.vue#L35-L46) 卡片页脚：
- **移除**：发言模式 chip、发言权路由 chip、主持人介入(n) chip。
- **保留**：状态标签 + 时间。
- 状态标签新增映射：`awaiting_confirmation → 「待确认」`（其余 `active → 「进行中」`、`archived → 「已归档」`）。

### 5.2 会议详情弹窗（G2）

新增 `frontend/src/views/meeting/components/MeetingDetailDialog.vue`：

- 入口：卡片上的「详情」按钮（不改变卡片主体「点击进房」行为）。
- 表单字段：标题、描述、参会智能体（增删，复用 `MentionPicker` 交互）、讨论模式、发言权路由、主持人介入间隔、共识检测、护栏阈值。
- 保存：设置类字段走 `PATCH /:id`；参会人变更走 `PUT /:id/participants`（仅当有增删时触发）。
- 只读条件：`archived` 全字段禁编辑；讨论进行中参会人区域禁编辑并提示。

### 5.3 开场与流程确认（G3）

在 `MeetingRoomView.vue` 消息流中，对 `payload.stage === "opening"` 的 host 消息渲染按钮组：
- 「开始会议」→ `POST /:id/confirm`，按钮变灰、等待首轮讨论开始。
- 「调整流程」→ 输入框切到流程修改模式（placeholder 改为「说说你想怎么调整会议流程…」），发送带 `adjust_flow:true`。
- 房间处于 `awaiting_confirmation` 时，顶部显示提示条「会议待确认，请确认流程或直接发言开始」。

### 5.4 产物抽屉重构（G4）

[ArtifactDrawer.vue](file:///d:/知识库/Know/frontend/src/views/meeting/ArtifactDrawer.vue) 重构为**两态**（对齐 [ChatArtifactsDrawer.vue](file:///d:/知识库/Know/frontend/src/views/chat/components/ChatArtifactsDrawer.vue) 的卡片→详情交互）：

- **列表态**：产物卡片列表（形态图标 / 标题 / 状态 / 时间）；顶部「+ 生成产物」入口。
- **详情态**：markdown 预览 / 文件信息 + 下载 / 重新生成 / 返回列表。
- 生成中显示 **loading 占位卡片**（`status=generating`），就位后由 SSE 事件替换为正式卡片。

新增 `frontend/src/views/meeting/components/ArtifactGenerateDialog.vue`：选择形态（Markdown 报告 / 会议纪要 / 待办事项 / Word / Excel / PPT / 网页报告 / 图表图片）+ 标题 + 补充要求 → `POST /:id/artifacts`。

生成入口位置：**房间顶部工具栏 + 抽屉内**（两处均可唤起生成弹窗）。

### 5.5 类型定义

[frontend/src/api/meeting/index.ts](file:///d:/知识库/Know/frontend/src/api/meeting/index.ts)：
- `MeetingRoom.status` 增加 `'awaiting_confirmation'`。
- `MeetingArtifact` 扩展 `kind` / `title` / `file_name` / `file_type` / `file_size` / `url` / `status` / `error` / `source_message_id`，新增 `ArtifactKind` / `ArtifactStatus` / `ArtifactKindType` 联合类型。

---

## 6 · 实施顺序

### M1 · 状态与守卫放宽
新增 `MeetingStatusAwaitingConfirmation`；放宽 `ensureRoomActive` 与 `UpdateMeeting` 的 status 判定为「仅 archived 拒绝」；`CreateMeeting` 置 `awaiting_confirmation`。
**验收**：新建会议状态为 `awaiting_confirmation`；该状态下 PATCH / 发消息 / 评审均返回成功；归档后仍全部 403。

### M2 · 详情弹窗 + 参会人可变更
`PUT /:id/participants`（校验：主持人不可移除、≥1 agent、讨论中 409）；`PATCH /:id` 校验补齐；`MeetingDetailDialog.vue`；卡片加「详情」入口。
**验收**：弹窗能改所有设置并持久化；能增删智能体；讨论进行中改参会人返回 409。

### M3 · 开场 + 流程确认
创建后异步主持人生成开场（落库 + `discussion.flow_confirmation`）；`POST /:id/confirm`；`POST /:id/messages` 支持 `adjust_flow`；前端按钮组与提示条。
**验收**：新建会议后数秒内出现开场消息（含主题总结 + 建议流程）与两个按钮；点「开始会议」自动开跑首轮；点「调整流程」后主持人重出流程；刷新页面开场与按钮仍在。

### M4 · 卡片精简
`MeetingLayoutView.vue` 移除三个 chip；状态标签支持「待确认」。
**验收**：卡片仅显示状态标签 + 时间；三种状态文案正确。

### M5 · 产物数据层 + 生成链路
`MeetingArtifact` 扩展字段 + `MeetingRoom.ArtifactSessionID`；`POST/GET/POST regenerate` 端点；懒创建隐藏后台会话 + 沙箱 pin；room 级互斥；采集器回写；SSE 三事件。
**验收**：能生成一个 word 产物并下载；同一房间并发生成被串行化；生成中列表出现 loading 卡片，完成/失败后状态正确；后台会话不出现在聊天侧栏。

### M6 · 产物抽屉重构 + 收尾
`ArtifactDrawer.vue` 两态重构 + `ArtifactGenerateDialog.vue`；solution 与 generated 统一卡片；回归 solution 回滚/导出；单测 + 集成测试。
**验收**：抽屉列表展示全部产物卡片；点卡片进详情；solution 卡片回滚/导出仍正常；generated 卡片可下载/重新生成。

---

## 7 · 风险与对策

| 风险 | 对策 |
|---|---|
| 新增 `awaiting_confirmation` 导致写操作被误拒 | M1 优先放宽 `ensureRoomActive` 与 handler 判定；M1 验收覆盖该状态下的所有写端点 |
| 首次生成需拉起沙箱，耗时明显长于同步方案生成 | 生成接口一律 **202 + SSE**；生成中 loading 占位卡片；必要时前端超时提示但后台继续 |
| 同房间并发生成 → 采集器 diff 去重互相干扰 | **room 级互斥串行**；生成中再次请求返回 409/排队 |
| 会议后台会话污染用户聊天列表 | 后台会话设隐藏标记（沿用现有隐藏机制），侧栏过滤 |
| 产物元数据双写不一致（`meeting_artifacts` vs 会话消息 `MessageArtifact`） | 以会话消息为字节真相源；`meeting_artifacts` 仅存索引与元数据；回写失败时产物状态置 `failed` 并保留可重试入口 |
| 历史 solution 行缺新字段导致前端渲染异常 | 读取路径统一 normalize（`kind→solution`、`status→ready`）；类型层给默认值 |
| 开场生成失败房间卡在待确认 | 落 system 兜底消息；用户可直接打字隐式确认开跑，不阻塞 |
| 参会人全量替换误删主持人 | 后端强制保留 `kind=host` 记录，忽略请求中的移除意图 |

---

## 8 · 配置

沿用现有会议配置，**无新增环境变量**。

生成的产物形态集合若需可配，作为后续增强（本期硬编码 8 种形态）。

---

## 9 · 测试策略

- **单元**
  - `ensureRoomActive` 三态谓词（active 放行 / awaiting_confirmation 放行 / archived 拒绝）
  - `PUT /:id/participants` 校验矩阵（主持人不可移除 / ≥1 agent / 讨论中 409 / 归档 403）
  - 产物 status 状态机（generating → ready / failed）与历史行 normalize
  - room 级互斥锁串行性
- **集成**
  - 「创建 → 开场 → 开始会议 → 讨论 → 生成 word 产物 → 下载」全链路
  - `awaiting_confirmation` 下 PATCH / 发消息 / 评审均成功；归档后全部 403
  - 「调整流程」分支重出 opening
- **E2E（前端）**
  - 卡片精简后的展示；详情弹窗增删智能体；开场按钮组；产物抽屉卡片→详情→下载

---

## 10 · 不做的事（Out of Scope）

- 产物**删除** / **分享** / **跨会议产物库**
- 合并 solution 与 generated 的**内部机制**（仅统一展示层，B1）
- 产物的在线编辑 / 版本对比（generated 产物）
- 多人在同一会议实时协作
- 参会人「部分更新」（本期为全量替换）
- 开场流程的结构化 schema（本期为自然语言建议流程）
- 生成产物的形态自定义扩展（本期硬编码 8 种）
