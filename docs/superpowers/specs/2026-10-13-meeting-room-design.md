# 会议室（Meeting Room）功能设计

- **日期**：2026-10-13
- **状态**：待审阅
- **作者**：与用户协作 brainstorming 产出

---

## 0 · 背景与目标

WeKnora 当前会话是"单用户 ↔ 单智能体"模式。本设计引入"会议室"：一个用户可以把多个智能体拉进同一间会议室，针对一个主题进行多智能体协作讨论；用户实时评审各智能体意见，由系统主持人汇总成结构化方案。

**核心使用场景**
1. 用户创建会议（填主题、勾选参会智能体、可选会议级知识库）
2. 用户在会议里发消息（广播式：所有智能体都看到）
3. 智能体按自己身份 + 知识库范围给出意见；可互相回应（护栏控制）
4. 用户对每条意见 ✓采纳 / ✗不采纳 / ⏸待定
5. 用户点"汇总方案" → 主持人生成结构化方案（仅基于已采纳）
6. 方案作为高亮消息发在对话框内，可继续追问；历史产物在侧边抽屉里查看与回滚

---

## 1 · 关键决策汇总

| 议题 | 决策 |
|---|---|
| 实现路径 | **A · 独立"会议室"子系统**（不复用 Session） |
| 讨论模式 | **广播式**：所有消息对所有参会者可见（可见性广播）；**发言时机由"智能筛选"决定**，用户 @ 点名优先于筛选结果 |
| 互呛控制 | **护栏**：两个智能体之间连续互呛 3 个来回（即 6 条互相回应消息）→ 主持人强制介入；用户可手动"回到正题" |
| 发言时机 | **可切换**：并发模式 / 轮流模式（默认轮流） |
| 评审方式 | **混合**：消息上即时点 + 讨论自然结束（一轮 RunTurn 无新发言）后常驻 PendingReviewBar 兜底 |
| 方案形态 | **主持人生成结构化文档**（markdown，含采纳/未采纳理由） |
| 方案产出位置 | **聊天消息内**（高亮特殊气泡），可继续追问 |
| 历史产物 | **侧边抽屉**，仅显示本会议；可对比、可回滚 |
| 持久化 | **落库 + 仅自己可看**（owner 隔离） |
| 参会人变更 | **创建时定死**（第一版不支持中途加/踢人） |
| 知识库范围 | **双层并集**（智能体 KB ∪ 会议级 KB）+ 权限校验 |
| 上下文窗口 | **最近 50 条** + 摘要压缩 |
| 上下文共享 | **全员可见**（@仅用于点名，不改变可见性） |
| 互呛判定 | **链式**（A 回 B，B 回 A 算互呛） |
| 智能体筛选 | **智能筛选**（主持人 LLM 判断本轮需要谁发言） |
| @ 交互 | **按钮 + chip**（不用 `@` 字符下拉） |
| 历史消息加载 | **默认 100 条 + 顶部"加载更早"按钮** |
| 方案快照 | 生成瞬间对 adopted/rejected 做快照；之后改评审不影响已生成方案 |
| 文档语言 | **中文** |

---

## 2 · 数据模型

### 2.1 `meeting_rooms`

| 字段 | 类型 | 说明 |
|---|---|---|
| id | uuid (PK) | |
| tenant_id | uint64 | 租户隔离 |
| created_by | string | 创建者（owner 隔离） |
| title | string | 会议主题（必填） |
| description | string | 会议描述（可选） |
| status | enum | `active / archived` |
| broadcast_mode | enum | `sequential / parallel`（默认 `sequential`） |
| guard_max_exchange | int | 护栏轮数阈值（默认 3） |
| knowledge_base_ids | json | 会议级 KB id 列表（可空，与智能体 KB 取并集） |
| current_solution_message_id | uuid | 当前生效方案消息（用于回滚指针） |
| context_summary | text | 历史消息摘要缓存（消息总数 > 50 时使用） |
| created_at / updated_at / deleted_at | | 软删 |

**索引**：`(tenant_id, created_by, status)`、`(tenant_id, created_by, updated_at desc)`

### 2.2 `meeting_participants`

| 字段 | 类型 | 说明 |
|---|---|---|
| id | uuid (PK) | |
| meeting_room_id | uuid (FK) | |
| kind | enum | `host / agent` |
| agent_id | uuid (FK→custom_agents.id) | kind=agent 时必填；host 时为空 |
| display_name | string | 冗余，便于前端展示 |
| avatar | string | 冗余 |
| order_index | int | 轮流模式发言顺序 |
| created_at | | |

**约束**：同一 `meeting_room_id` 下 `kind=host` 唯一（系统自动注入，不可移除）。

### 2.3 `meeting_messages`

| 字段 | 类型 | 说明 |
|---|---|---|
| id | uuid (PK) | |
| meeting_room_id | uuid (FK) | |
| sender_kind | enum | `user / agent / host / system` |
| sender_id | string | user_id 或 agent_id（system 时为空） |
| reply_to_message_id | uuid (FK→self) | 自引用，支撑回应链 |
| mentioned_agent_ids | json | agent_id 数组：本消息明确 @ 了哪些智能体（用户消息可填，触发"必发言"；智能体消息通常为 null） |
| content | text | markdown |
| payload | json | citations / tokens / agent 元信息 |
| review_state | enum | `none / adopted / rejected / pending`（仅 sender_kind=agent 时有意义） |
| is_solution | bool | 是否为方案消息 |
| solution_version | int | is_solution=true 时有效 |
| created_at | | |

**索引**：`(meeting_room_id, created_at)`、`(meeting_room_id, review_state)`、`(meeting_room_id, is_solution)`

### 2.4 `meeting_artifacts`

| 字段 | 类型 | 说明 |
|---|---|---|
| id | uuid (PK) | |
| meeting_room_id | uuid (FK) | |
| solution_message_id | uuid (FK→meeting_messages.id) | 指向方案消息（不重复存正文） |
| version | int | 方案 v1/v2/v3… |
| adopted_count | int | 快照 |
| rejected_count | int | 快照 |
| exported_format | enum | `none / md / docx / pdf`（第一版仅用 md） |
| created_at | | |

**唯一约束**：`(meeting_room_id, version)`

---

## 3 · API 契约

挂在 `/api/v1/meetings`，所有端点 `tenant_id + created_by` 双重过滤（仅自己可见）。

| 方法 | 路径 | 说明 | 权限 |
|---|---|---|---|
| POST | `/meetings` | 创建会议 | Viewer+ |
| GET | `/meetings` | 我的会议列表（分页） | Viewer+ |
| GET | `/meetings/:id` | 会议详情（含 participants + 最近 100 条消息） | Viewer+（owner） |
| PATCH | `/meetings/:id` | 修改（主题/描述/broadcast_mode/guard_max_exchange；**不允许改参会人**） | Viewer+（owner） |
| POST | `/meetings/:id/archive` | 归档 | Viewer+（owner） |
| DELETE | `/meetings/:id` | 删除（软删） | Viewer+（owner） |
| GET | `/meetings/:id/messages?before=&limit=` | 消息游标分页 | Viewer+（owner） |
| POST | `/meetings/:id/messages` | 用户发消息（异步触发讨论，立即 202） | Viewer+（owner） |
| GET | `/meetings/:id/stream` | SSE 实时推送 | Viewer+（owner） |
| POST | `/meetings/:id/messages/:mid/review` | 评审 `{action: adopted/rejected/pending/none}` | Viewer+（owner） |
| POST | `/meetings/:id/interrupt` | 用户触发"回到正题"（中断当前 RunTurn，主持人收敛） | Viewer+（owner） |
| POST | `/meetings/:id/solution` | 触发主持人生成方案 | Viewer+（owner） |
| GET | `/meetings/:id/artifacts` | 产物列表 | Viewer+（owner） |
| GET | `/meetings/:id/artifacts/:aid` | 产物详情（含方案正文，后端 join 取消息） | Viewer+（owner） |
| POST | `/meetings/:id/artifacts/:aid/rollback` | 回滚到指定版本（切换 current_solution_message_id） | Viewer+（owner） |

### SSE 事件

- `message.created` 新消息
- `message.review_updated` 评审状态变更
- `discussion.turn_started` / `discussion.turn_ended` 轮次进度
- `guard.triggered` 护栏触发
- `solution.generating` / `solution.created` 方案生成进度
- `ping` 心跳（每 30s，前端检测断线）

---

## 4 · 讨论引擎（MeetingEngine）

独立服务，组合现有 `AgentQA` 能力，**不修改** `AgentEinoEngine`。

### 4.1 触发流程与并发控制

```
POST /messages
  → MeetingService.AppendUserMessage（落库 + SSE）
  → 立即返回 202 + message_id
  → 异步 goroutine 启动 MeetingEngine.RunTurn(roomID, triggerMsgID)
```

**并发控制**：
- 同一 `meeting_room_id` 在任意时刻**只允许一个 RunTurn 运行**（用 Redis SETNX 锁，key=`meeting:{id}:turn_lock`，TTL 5 分钟）
- 用户连续发多条消息 → 后续 RunTurn **排队**（不并发执行）；排队时前端可看到"讨论进行中，已加入队列"
- `POST /interrupt` 可强制释放锁并终止当前 RunTurn

### 4.2 一轮讨论状态机

```
TurnStart
   ↓
[智能筛选] 主持人 LLM 判断本轮需要哪些 agent 发言
   ├─ 输入：用户最新消息 + mentioned_agent_ids + 各参会 agent 的 display_name/描述
   ├─ 规则：用户 @ 的 agent 必发言；其余由主持人判断
   └─ 输出：本轮发言 agent_id 列表
   ↓
[broadcast_mode=sequential] 按 order_index 依次调用
[broadcast_mode=parallel]   goroutine 并发调用
   ↓
对每个 agent：
  ├─ 组装上下文（见 4.3）
  ├─ 调用 AgentQA 流式生成
  ├─ 流式推送 SSE(message.created)
  └─ 落库（含 mentioned_agent_ids、reply_to_message_id）
   ↓
一轮结束判定（满足任一即结束）：
  ├─ 本轮没有任何 agent 消息 reply_to 其他 agent 消息 → 自然结束
  ├─ 互呛护栏触发（见 4.4）
  └─ 用户 interrupt
   ↓
TurnEnd
```

**关于"多轮互呛"**：单个 RunTurn 内允许 agent 互相回应（生成 reply_to 链）；当两个 agent 之间 reply_to 链长度达到 `guard_max_exchange`（默认 3，即 3 个来回 = 6 条互相回应消息）→ 触发护栏，本轮强制结束。

### 4.3 上下文组装

每个智能体发言前注入：

```
[系统] 会议主题 + 描述
[系统] 你的身份：<agent.system_prompt>
[系统] 你能访问的知识库：<agent.KBs ∪ 会议级KBs>
[历史] 本会议最近 50 条消息（按时间序，含 sender 标注）
[触发] 用户刚发的消息 / 被回应的那条消息
[指令] 基于身份与知识库发言；可引用历史；可赞同/反对其他参会人
```

**摘要压缩**：
- 当本会议消息总数 > 50 时，MeetingEngine 在组装上下文时先调用 LLM 对**第 1 ~ (总数-50) 条**生成摘要
- 摘要缓存到 `meeting_rooms` 表的 `context_summary` 字段（文本），并在新消息增加时增量更新
- 注入给 agent 的"[历史]"部分 = 摘要 + 最近 50 条原文

**常量区分**：
- LLM 上下文窗口：最近 **50 条**（配置 `MEETING_CONTEXT_MAX_MESSAGES`）
- 前端展示窗口：默认加载最近 **100 条**（用户可点"加载更早"翻页）

### 4.4 主持人行为

主持人 = `kind=host` 特殊 participant，固定 SystemPrompt，**复用当前租户默认 LLM**（不绑定 custom_agent）：

```
你是本次会议的主持人。职责：
1. 会议创建时：输出开场白，说明会议主题、参会者、预期产出
2. 护栏触发时（互呛超过阈值/用户点"回到正题"）：
   - 总结目前分歧点
   - 暂停进一步争论
   - 邀请用户裁决或追问
3. 用户点"汇总方案"时：基于已采纳意见生成结构化方案
4. 平时不主动插话；除非被用户@或上述规则触发
```

**触发点**：
- `POST /meetings` 创建会议成功后，**自动生成一条 host 开场白消息**（落库 + SSE 推送）
- 护栏触发 / 用户 interrupt / 用户点"汇总方案" 时分别触发对应 host 消息生成

### 4.5 评审状态机

```
none ─→ adopted
     ─→ rejected
     ─→ pending
     
四态可互切（用户改判）
```

评审动作只影响"哪些内容进入方案"，不影响消息本身。

### 4.6 方案生成

**触发**：`POST /meetings/:id/solution`

**竞态保护**：触发生成时**先快照**当前所有 `adopted/rejected` 状态（存进 artifact），生成过程中用户即使改评审也不影响本次方案。

**版本号规则**：
- 首次生成：solution_version = 1
- 后续每次生成：solution_version = 当前会议已有 artifact 的 max(version) + 1
- `meeting_messages.solution_version` 与 `meeting_artifacts.version` **恒等**（同一次生成的两端点值一致）

**输入**：会议主题/描述 + 快照的 `adopted` 消息 + 快照的 `rejected` 消息

**Prompt 指令**：
```markdown
# 方案标题
## 1. 背景与目标
## 2. 核心决策
## 3. 具体措施（每条注明"来自 XX智能体"）
## 4. 未采纳意见及理由（简要）
## 5. 后续行动项
```

**输出**：
- `sender_kind=host, is_solution=true, solution_version=N+1` 消息
- 同步落 `meeting_artifacts` 一条记录（快照 adopted/rejected 计数 + 指向消息）
- 更新 `meeting_rooms.current_solution_message_id` 指向新方案
- SSE 推 `solution.generating` → `solution.created`

### 4.7 回滚

- 不删除任何方案版本
- 仅切换 `meeting_rooms.current_solution_message_id` 指针
- 端点：`POST /meetings/:id/artifacts/:aid/rollback`
- 用户可反复切换"当前生效方案"

---

## 5 · 前端

### 5.1 路由

```
/meetings             → MeetingListView
/meetings/new         → MeetingCreateView
/meetings/:id         → MeetingRoomView（含抽屉）
```

左侧导航新增"会议室"入口。

### 5.2 三个核心页面

**MeetingListView**：卡片列表 + 顶部"+ 创建会议"

**MeetingCreateView**：主题（必填）/ 描述 / 参会智能体多选 / 会议级 KB 多选 / 广播模式 / 护栏轮数

**MeetingRoomView**：
```
┌────────────────────────────────────────────────┐
│ TopBar (主题 | 参与者 | 设置 | 🛑回到正题 | 📁产物 | 📋汇总方案) │
├────────────────────────────────────────────────┤
│ MessageList (虚拟滚动, 默认 100 条)              │
│  ├─ UserBubble / AgentBubble(带 ReviewBar)        │
│  ├─ HostBubble / SolutionBubble(高亮)             │
│  └─ SystemBubble                                  │
├────────────────────────────────────────────────┤
│ PendingReviewBar (常驻, 可关, 显示未评审数)        │
├────────────────────────────────────────────────┤
│ InputArea (@按钮+chip | 输入框 | 发送)             │
└────────────────────────────────────────────────┘
                ↕️
        ArtifactDrawer (右侧滑出)
        版本列表 / 对比 / 回滚
```

**PendingReviewBar 行为**：
- **常驻底栏**，默认收起，仅显示"还有 N 条意见未评审"
- 点击展开 → 列出所有 `review_state ∈ {none, pending}` 的 agent 消息（按时间倒序）
- 每条提供快捷 ✓/✗ 按钮；点一条 → 滚动定位到对应消息
- 用户可手动关闭（关闭后仅在 RunTurn 自然结束时再次提示）

### 5.3 关键交互

- @：按钮 + chip（不用 `@` 字符下拉）
- 历史：默认 100 条 + 顶部"加载更早"
- 方案：高亮黄底 + 版本 + 已采纳/未采纳计数 + 复制/导出 .md
- 归档：整个页面只读 + 顶部灰条"已归档"
- SSE 断线：指数退避重连（最多 5 次），重连后用游标拉漏掉的消息

### 5.4 状态管理

新增 Pinia store `useMeetingStore`：当前会议 / 参会人 / 消息流 / SSE / 评审状态 / 产物列表。

---

## 6 · 实施顺序

### 6.1 里程碑

### M1 · 数据层 + 最小可用会议
4 张表 migration + Repository + 基础 Service；`/meetings` CRUD + `/messages` + `/stream`；**完整调通 1 个智能体的 AgentQA 流式链路**（含 KB 检索）；前端骨架（含创建会议 + 聊天界面）。
**验收**：
- 能创建会议（含选择 1 个智能体）
- 用户发消息 → 该智能体流式回复（KB 引用正常）
- 刷新页面历史消息完整保留
- 自动生成 host 开场白

### M2 · 多智能体广播 + 轮流/并发 + 智能筛选
`MeetingEngine.RunTurn` 完整状态机；智能筛选（含 @ 优先级）；并发/轮流；并发锁；@ 功能、打字指示器。
**验收**：
- 3 个智能体同会议，用户提问后，主持人筛出的智能体能轮流发言
- 用户 @ 某智能体时该智能体必发言
- 并发模式 / 轮流模式可切换
- 用户连发 2 条消息时第二条排队执行

### M3 · 护栏 + 主持人收敛 + interrupt
互呛计数；强制收敛；SystemBubble；"回到正题"按钮；`POST /interrupt` 端点。
**验收**：两个智能体之间连续互相回应 6 条消息（3 个来回）后，主持人自动发出收敛消息；用户点"回到正题"立即触发收敛。

### M4 · 评审 + 方案生成
评审状态机；评审 API；主持人生成方案（含快照）；ReviewBar、汇总按钮、方案高亮气泡。
**验收**：
- 用户对某条 agent 消息点 ✓ → 状态变 adopted，SSE 推送
- 点"汇总方案" → 主持人生成方案，方案中明确引用所有 adopted 消息（每条注明来源 agent），未采纳部分简要列出理由
- 方案生成后用户改评审 → 已生成方案不变

### M5 · 产物抽屉 + 回滚 + 对比
`meeting_artifacts`；版本管理；`rollback` 端点；抽屉、对比、回滚按钮。
**验收**：
- 能生成 v1/v2/v3，抽屉中可见
- 点 v1 → 回滚 → `current_solution_message_id` 指向 v1
- 选 v1 + v3 → 对比视图正确显示 diff

### M6 · 收尾
归档/删除；导出 .md；权限测试（不同用户互相看不到）；单元测试 + 集成测试；中文文档 `docs/meeting-room.md`。
**验收**：
- 归档会议后所有写操作返回 403
- 用户 A 创建的会议，用户 B 通过 API 任何方式都查不到
- 单测覆盖率 ≥70%，集成测试覆盖 M1-M5 关键路径

---

## 7 · 风险与对策

| 风险 | 对策 |
|---|---|
| 多智能体并发 LLM 超时 | SSE 边生成边推；打字指示器；60s 前端超时 + 可中断 |
| 上下文爆炸（token 膨胀） | 50 条 + 摘要压缩（保留最近 50 条 + 旧消息摘要）；摘要由 MeetingEngine 在组装上下文时生成并缓存 |
| 互呛护栏误判 | 链式判定；3 轮可调；用户手动"回到正题"兜底 |
| 方案质量不稳 | 主持人 prompt 显式列出已采纳消息；后续支持方案编辑 |
| AgentEinoEngine 复用困难 | M1 先最简单调通；M2 再集成完整 AgentQA |
| SSE 断线丢消息 | 心跳 + 重连 + 游标拉漏 |

---

## 8 · 配置

`.env.example` 新增：

```
MEETING_GUARD_MAX_EXCHANGE=3
MEETING_CONTEXT_MAX_MESSAGES=50
MEETING_DEFAULT_BROADCAST_MODE=sequential
```

---

## 9 · 测试策略

- **单元**：4 张表 Repository / MeetingEngine 状态机 / 护栏计数 / 评审状态机
- **集成**：完整流程"创建 → 发消息 → 智能体回复 → 评审 → 方案 → 回滚"
- **E2E**：前端关键路径

---

## 10 · 不做的事（Out of Scope）

- 中途加/踢参会人
- docx / pdf 导出（第一版只 .md）
- 方案在线编辑
- 会议模板（"头脑风暴模板 / 评审模板"等）
- 跨会议室检索
- 多人协作（多人同时进同一会议室）
