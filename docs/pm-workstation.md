# PM 工作站 · 细化方案

> 基于 WeKnora 内嵌的产品需求一站式管理模块
> 版本：v1.3（方案评审稿）｜日期：2026-10-20

> **v1.3 修订**（依据原型对齐评审）：功能规划升级为**贯穿项目全生命周期的持续规划**——新增规划批次模型 `pm_plan_batches` 与批次快照目录 `需求/规划历史/`；新增 5 步 AI 引导规划页（项目认知 → 功能清单 → 业务流程 → 首期范围 → 确认生成）与可视化流程图编辑；路线图视图改为"批次条切换 + 规划历史/查看快照"，工具栏统一为"新增规划（主按钮）/ 重新规划 / 规划历史"，不再单设"编辑"入口；功能清单支持由路线图规划同步写入；同步补充规划批次 API、引导页组件、规划技能与 Mermaid 渲染风险。

> **v1.2 修订**（依据交互优化评审）：需求池支持"列表/看板"视图切换（看板按需求状态分列）；新建需求改两步弹窗（表单 → AI 分析弹窗），AI 基于需求现状补充详细情况并输出**具体设计方案**，用户确认后可二次编辑；移除"甘特图"模块（含里程碑，保留版本"计划上线日"作排期字段）；"生产材料"更名"项目资料"（知识库目录同步 `资料/`）。

---

## 1. 项目概述

### 1.1 目标

为产品经理提供**需求一站式管理**能力，覆盖产品的方案规划、需求维护、方案及原型设计、产品版本维护，以及操作手册、宣传册、PPT 的自动更新，形成"需求 → 版本 → 功能清单 → 项目资料"的完整闭环。

### 1.2 核心价值

- **AI 引导需求分析**：新建需求时由智能体引导挖掘真实需求（价值/关联影响/风险），并基于现状给出**具体设计方案**（可二次编辑）
- **PRD/原型自动生成**：版本评审迭代中自动生成 PRD 文档与 HTML 原型，保留每一版历史
- **功能清单自动同步**：PRD 定稿即同步功能清单，上线状态与版本联动，杜绝人工维护漂移
- **项目资料自动更新**：版本上线后提示更新手册/宣传册/PPT，先草稿后定稿，旧版归档可回滚
- **产品路线持续规划**：AI 引导 5 步产出三级功能清单与可视化流程图，支持首期/增量/重新规划三种模式，每次规划形成批次与快照，可持续迭代并回溯
- **需求多视角管理**：需求池支持列表/看板双视图，按状态分列流转，版本排期（计划/实际上线日）统一记录

### 1.3 使用范围

- 单人起步使用（数据模型预留 `owner_id / created_by` 字段，未来可平滑升级到多人）
- 产品经理日常：需求登记与分析、版本规划与评审、文档与资料产出

---

## 2. 形态与总原则

| 决策项 | 结论 |
|---|---|
| 落地形态 | **WeKnora 内嵌新模块**（Vue3 前端 `/platform/pm` 路由 + Go 后端 Gin 路由） |
| 用户体系 | 单人起步，预留负责人字段，暂不建完整 RBAC |
| 数据存储 | **混合**：`pm_*` 业务表（golang-migrate 新迁移）+ 产物文档存关联知识库 |
| 项目↔知识库 | **1:1**，创建项目时动态建库（复用 `CreateKnowledgeBase`） |
| 版本号 | 语义化 `X.Y.Z` |
| 资料输出 | 先草稿（Markdown/HTML）后定稿（docx/pptx），人工确认后替换并归档旧版 |

### 2.1 技术可行性结论（已探查确认）

- 前端扩展模式：参照 `meeting` 模块（[router/index.ts](frontend/src/router/index.ts)、[menu.ts](frontend/src/stores/menu.ts)、[MeetingLayoutView.vue](frontend/src/views/meeting/MeetingLayoutView.vue)）
- 后端扩展模式：参照 `KnowledgeBaseService`（[container.go](internal/container/container.go) 分层注册、[router.go](internal/router/router.go) 路由挂载、[rbac.go](internal/router/rbac.go) 权限声明、[migration.go](internal/database/migration.go) 迁移入口）
- AI 会话通道：`POST /api/v1/sessions` 创建会话 → `POST /api/v1/agent-chat/:session_id` 带 `skill_names` 触发技能（SSE 流式）
- 产物落库：`CreateKnowledgeFromPassageSync`（同步等待索引）/ `CreateKnowledgeFromManual`（Markdown 直写）
- 异步任务：复用 `doc_assistant` 的 asynq 队列模式

---

## 3. 数据模型设计

> 新增 `pm_` 前缀表，通过 golang-migrate 新增迁移文件（[migrations](migrations)）。

### 3.1 pm_projects（项目）

| 字段 | 类型 | 说明 |
|---|---|---|
| id | BIGINT PK | 主键 |
| name | VARCHAR | 项目名称 |
| description | TEXT | 项目描述 |
| kb_id | BIGINT | 关联知识库 ID（1:1，创建项目时动态建库） |
| status | VARCHAR | `active` 活跃 / `archived` 已归档（仅归档，不物理删除） |
| owner_id | BIGINT | 负责人（预留多人升级） |
| created_by / created_at / updated_at | — | 审计字段 |

### 3.2 pm_requirements（需求）

| 字段 | 类型 | 说明 |
|---|---|---|
| id | BIGINT PK | 主键 |
| project_id | BIGINT FK | 所属项目 |
| title | VARCHAR | 需求标题 |
| description | TEXT | 需求描述（初始想法 / 富文本详情） |
| priority | VARCHAR | `P0`~`P3`（P0 紧急 / P1 高 / P2 中 / P3 低） |
| status | VARCHAR | 状态机（见 §4.1） |
| source | VARCHAR | 需求来源：`customer` 客户反馈 / `internal` 内部提出 / `data` 数据分析 / `competitor` 竞品分析 / `strategy` 战略规划 / `other` 其他（默认 `internal`） |
| proposer | VARCHAR | 提出人 |
| ai_analysis | JSON | AI 引导分析结论（价值/关联影响/风险/建议优先级/建议状态） |
| design_plan | TEXT | **具体设计方案**（AI 基于现状生成，可人工二次编辑，≤2000 字） |
| session_id | VARCHAR | 引导分析所用的 PM 会话 ID |
| knowledge_id | BIGINT | 需求详情文档的知识条目 ID（存知识库 `需求/` 目录） |
| actual_launch_date | DATETIME | 实际上线时间（关联版本全部上线时回写） |
| effect_notes | TEXT | 上线效果数据（上线验证回填，非必填，≤1000 字） |
| value_achieved | VARCHAR | 价值达成：`achieved` 达成 / `partial` 部分达成 / `not_achieved` 未达成（上线验证回填） |
| retro_notes | TEXT | 复盘说明（上线验证回填，非必填，≤1000 字） |
| value_achieved_at | DATETIME | 价值达成回填时间 |
| owner_id | BIGINT | 负责人（预留） |
| created_by / created_at / updated_at | — | 审计字段 |

### 3.3 pm_versions（版本）

| 字段 | 类型 | 说明 |
|---|---|---|
| id | BIGINT PK | 主键 |
| project_id | BIGINT FK | 所属项目 |
| version_no | VARCHAR | 语义化版本号 `X.Y.Z`（定稿时生成） |
| name | VARCHAR | 版本名称（可选，如"启明版"） |
| status | VARCHAR | 状态机（见 §4.2） |
| review_round | INT | 评审轮次（每轮评审 +1） |
| review_opinions | JSON | 评审意见（按轮次记录：轮次/意见/功能变更明细/**参会人**/**结论**/意见处理状态与处理说明） |
| review_conclusion | VARCHAR | 最近一轮评审结论：`pass` 通过 / `conditional` 有条件通过 / `reject` 不通过（定稿前置校验） |
| reviewers | VARCHAR | 评审参会人（非必填，逗号分隔） |
| release_note | TEXT | 版本更新内容大纲（定稿时由功能摘要聚合生成） |
| plan_date | DATETIME | 规划日（创建版本时填写） |
| plan_launch_date | DATETIME | **计划上线日**（新建版本时人工填写，须晚于规划日；排期管理基线） |
| finalize_date | DATETIME | 定稿日（定稿时记录） |
| launch_date | DATETIME | 实际/上线日（上线时记录，与计划上线日对比反映延期情况） |
| owner_id | BIGINT | 负责人（预留） |
| created_by / created_at / updated_at | — | 审计字段 |

### 3.4 pm_version_requirements（版本↔需求 多对多）

| 字段 | 说明 |
|---|---|
| version_id | FK → pm_versions |
| requirement_id | FK → pm_requirements |
| created_at | 纳入时间 |

> 一个需求可被拆入多个版本，或评审变更后重新分配；保留历史归属，支持版本统计。
> **状态联动**：版本搁置时，其关联需求若不再被任何"非搁置"版本承接，则状态由 `已排期` 回落为 `已确认`；版本全部上线时，关联需求上行至 `已上线`。

### 3.5 pm_features（功能清单，三级结构）

| 字段 | 类型 | 说明 |
|---|---|---|
| id | BIGINT PK | 主键 |
| project_id | BIGINT FK | 所属项目 |
| module_name | VARCHAR | 一级：模块 |
| feature_name | VARCHAR | 二级：功能点 |
| operation_name | VARCHAR | 三级：操作项 |
| parent_id | BIGINT NULL | 父级节点（三级树形） |
| requirement_id | BIGINT NULL | 来源需求 |
| status | VARCHAR | `planned` 规划 / `launched` 已上线（按**最新迭代版本**状态联动，非手工维护） |
| created_at / updated_at | — | 审计字段 |

> **版本归属为多对多**：一个功能点可随多个迭代版本演进（新增 / 变更 / 移除），关联关系记录于 `pm_feature_iterations`，功能清单界面按最新迭代版本展示状态，并保留完整演进历史。
> **来源**：功能点来源分为"PRD 同步"与"路线图规划同步"两类（由 `pm_feature_iterations.change_note` 与来源标记体现）；路线图规划确认时勾选同步，则按"模块/功能点/操作项"名称匹配写入或更新，初始状态为 `planned`。

### 3.5.1 pm_feature_iterations（功能点↔版本 迭代历史）

| 字段 | 类型 | 说明 |
|---|---|---|
| id | BIGINT PK | 主键 |
| feature_id | BIGINT FK | 功能点（pm_features.id） |
| version_id | BIGINT FK | 迭代版本（pm_versions.id） |
| change_type | VARCHAR | `add` 新增 / `change` 变更 / `remove` 移除 |
| change_note | TEXT | 变更说明 |
| changed_at | DATETIME | 变更操作时间 |

> 排序规则：一个功能点的迭代版本以其**所属版本的规划时间**升序排列；"最新迭代版本"决定功能清单当前状态展示。

### 3.6 pm_materials（项目资料）

| 字段 | 类型 | 说明 |
|---|---|---|
| id | BIGINT PK | 主键 |
| project_id | BIGINT FK | 所属项目 |
| material_type | VARCHAR | `manual` 操作手册 / `brochure` 宣传册 / `ppt` PPT |
| version_id | BIGINT NULL | 对应版本号（版本维度） |
| knowledge_id | BIGINT | 产物知识条目 ID（存知识库 `资料/` 目录） |
| status | VARCHAR | `draft` 草稿 / `published` 已发布 |
| archived | BOOLEAN | 是否归档（替换后旧版归档） |
| created_at / updated_at | — | 审计字段 |

### 3.7 pm_plan_batches（规划批次）

| 字段 | 类型 | 说明 |
|---|---|---|
| id | BIGINT PK | 主键 |
| project_id | BIGINT FK | 所属项目 |
| batch_name | VARCHAR | 批次名称（默认"首期规划"/"{版本号} 规划"/"{YYYY-MM} 迭代规划"，可修改） |
| plan_type | VARCHAR | `initial` 首期规划 / `iteration` 迭代规划 |
| version_id | BIGINT NULL | 关联版本（选填，用于版本规划前的规划） |
| change_summary | TEXT | 变更摘要（新增/调整/移除的模块、功能点、流程节点数量与明细） |
| status | VARCHAR | `draft` 草稿 / `active` 生效中 / `archived` 已归档（同项目同一时间仅一条 `active`） |
| roadmap_knowledge_id | BIGINT | 当前生效路线图文档知识条目 ID（存知识库 `需求/` 目录） |
| snapshot_knowledge_id | BIGINT NULL | 批次快照文档知识条目 ID（存知识库 `需求/规划历史/`） |
| operator_id | BIGINT | 操作人 |
| activated_at | DATETIME | 批次生效时间 |
| created_by / created_at / updated_at | — | 审计字段 |

> **批次流转**：新批次生成时先落 `draft`；确认生成后原 `active` 批次转 `archived`、新批次转 `active`，并写入快照。重新规划覆盖当前批次内容但仍生成新快照，保证历史可回溯。
> **关系约束**：路线图内容只在批次维度变更，不提供绕过批次的直接编辑，避免出现无快照的内容漂移。

### 3.8 知识库目录约定

项目关联知识库内按产物类型建**四个一级目录**（`需求/` 下新增 `规划历史/` 子目录；平台目录能力受限则退化为命名前缀）：

```
需求/   需求/规划历史/   PRD/   原型/   资料/
```

- `需求/`：需求详情与分析文档（pm_requirements.knowledge_id 指向，含设计方案章节）；当前生效路线图 `产品路线图-{项目名称}.md`（pm_plan_batches.roadmap_knowledge_id 指向）
- `需求/规划历史/`：历次规划批次快照 `产品路线图-{项目名称}-{批次名称}.md`（只读，供批次回溯）
- `PRD/`：各版本 PRD（`PRD-v1.2.0.md`，评审迭代产出 PRD-v2/v3…）
- `原型/`：各版本 HTML 原型（`prototype-v1.2.0.html`）
- `资料/`：操作手册 / 宣传册 / PPT 各版本（替换后旧版归档保留）

---

## 4. 状态机设计

### 4.1 需求状态机

```
待分析 ──→ 分析中 ──→ 已确认 ──→ 已排期 ──→ 开发中 ──→ 已上线
   │           │         ↑           ↑
   │           │         └───────────┴── 关联版本全部搁置（回落，见下方规则）
   │           └──→ 已驳回（可重新激活回 待分析）
   └──────────────→ 已关闭（可重新激活回 待分析）
```

- 新建需求进入 `待分析`
- AI 引导分析过程中为 `分析中`
- 分析确认后一键转 `已确认`
- 纳入版本后为 `已排期`（关联版本均未启动开发）
- **关联版本启动开发后为 `开发中`**（8 态中新增的中间态，避免"已排期→已上线"的状态跳变）
- 所属需求**全部关联版本均上线**后为 `已上线`
- **双向联动（版本→需求）**：版本搁置时，需求若不再被任何"非搁置"版本承接，则由 `已排期`/`开发中` 回落至 `已确认`
- `已驳回 / 已关闭` 为终止态，但可重新激活，**保留历史状态记录与 AI 分析意见**

| 状态 | 触发条件 |
|---|---|
| 待分析 | 新建 |
| 分析中 | 启动 AI 引导分析 |
| 已确认 | 分析确认通过 |
| 已排期 | 被纳入版本且关联版本均未启动开发 |
| 开发中 | 关联版本进入"开发中" |
| 已上线 | 全部关联版本均已上线 |
| 已驳回 | 分析结论为不采纳 |
| 已关闭 | 人工关闭 |

### 4.2 版本状态机

```
规划中 ──→ 评审中 ──→ 定稿 ──→ 开发中 ──→ 已上线
   │           │
   │           └──→（评审意见 → 重新生成 PRD/原型 → 回到评审中，可多轮）
   └──────────────→ 已搁置（需求重新分配后可回 规划中）
```

- **定稿为分水岭**：定稿前可反复"评审意见 → 重新生成 PRD/原型"；定稿前置条件为最近一轮评审结论为"通过"或"有条件通过"，定稿后锁定并生成版本号 `X.Y.Z` 与更新内容大纲
- 版本进入 `开发中` → 关联需求状态自动由 `已排期` 变更为 `开发中`
- 版本 `已搁置` → 关联需求按 §4.1 规则回落至 `已确认`
- 版本 `已上线` → 其关联功能清单条目状态联动标记为 `launched`；关联需求**全部关联版本均上线**才置 `已上线`，否则保持 `开发中`

---

## 5. AI 技术通道

### 5.1 会话管理

- 每个项目创建时自动建立**常驻 PM 助手会话**（复用 `CreateSession`）
- 后续所有 AI 操作在该会话内以不同技能触发（`skill_names` 参数注入）
- 会话累积项目背景与历史评审上下文，零成本带入

### 5.2 技能集（自研 5 个轻量技能）

| 技能 | 职责 | 产出 |
|---|---|---|
| `pm-roadmap-planner` | 5 步引导规划产品路线图：项目认知 → 三级功能清单 → 可视化业务流程 → 首期范围 → 确认生成；支持首期/增量/重新规划 | 路线图 Markdown（三级清单 + Mermaid 流程图）、批次变更摘要、可选功能清单结构化数据 |
| `pm-requirement-analysis` | 引导需求分析：基于现状连续追问澄清，输出分析结论与具体设计方案 | JSON：价值 / 关联影响 / 风险 / 建议优先级 / 具体设计方案 |
| `pm-prd-generator` | 按模板生成版本 PRD | PRD Markdown + 功能清单结构化数据（双产物） |
| `pm-prototype` | 基于 PRD 生成交互式 HTML 原型 | HTML 原型文件 |
| `pm-material` | 基于 PRD/功能清单生成操作手册/宣传册/PPT | Markdown/HTML 草稿（待人工确认后转正式格式） |

> 技能内部可复用原型/PPT 的生成逻辑，但编排由 PM 技能控制。`pm-roadmap-planner` 与 `pm-requirement-analysis` 为交互式技能（分步引导 / 连续追问）；`pm-prd-generator`、`pm-prototype`、`pm-material` 面向"给定结构化上下文 → 静默产出 → 落库"的确定性流程。

### 5.3 执行模式

- **异步任务**：复用 doc_assistant 的 asynq 队列模式，前端轮询进度，完成回调落库并提示
- 生成逻辑拆分为"起草 → 转格式"多步（规避沙箱 120s 超时 / ReAct 10 轮上限）
- 需求分析（轻量、交互式）可同步 SSE 流式执行

### 5.4 产物落库

- 生成产物经 `CreateKnowledgeFromPassageSync` / `CreateKnowledgeFromManual` 写入关联知识库对应目录
- `pm_*` 表记录 `knowledge_id + 产物类型 + 版本号`，实现版本追溯与"更新到最新版本"

---

## 6. 功能模块详细设计

### 6.1 创建项目

**流程**：
1. 入口选择"新项目 / 已存在项目"
2. 新项目：填写名称/描述 → 后端调用 `CreateKnowledgeBase` 动态建库 → 创建 `pm_projects` 记录并关联 kb_id → 自动创建常驻 PM 会话
3. 已存在项目：选择既有知识库 → 创建 `pm_projects` 记录关联

**前端页面**：`PmProjectListView.vue`（项目列表/卡片，新建/归档/恢复）

### 6.2 需求池

**视图模式**：支持"列表视图"与"看板视图"切换（默认列表，偏好本地记忆）——
- 列表视图：分组（"全部"+各需求状态）+ 表格展示全部字段；
- 看板视图：按需求状态分为 8 列（待分析/分析中/已确认/已排期/开发中/已上线/已驳回/已关闭），列内卡片展示核心字段（标题/优先级/需求来源/提出人/描述摘要/所属版本/价值达成），支持拖拽跨列流转状态（按 §4.1 状态流转规则校验）。

两种视图共用筛选项（优先级/状态/需求来源/价值达成/提出人/所属版本/搜索）与排序（更新时间、优先级升/降序）。

**详情视图**：完整描述 + **AI 分析意见**（价值/关联影响/风险/建议优先级/建议状态）+ **具体设计方案**（可二次编辑）+ 关联版本 + 需求文档 + **上线验证**（需求已上线后展示：实际上线时间/效果数据/价值达成/复盘说明）。

**新建需求（两步弹窗）**：
1. **第一步·需求表单弹窗**：填写基础信息（标题/初步想法/需求来源/提出人/优先级），保存后创建"待分析"需求，可随时触发 AI 分析；
2. **第二步·AI 分析弹窗**：弹窗含 **AI 对话区** + **分析结果区**——
   - `pm-requirement-analysis` 技能以需求基础信息 + 项目产品路线图 + 关联知识库为上下文，**基于需求现状**连续追问澄清（每次最多 3 问），引导补充需求详细情况；
   - 分析完成输出结构化结果（JSON）：需求价值 / 关联功能影响 / 风险 / 建议优先级 / 建议状态 / **具体设计方案**；
   - 结果自动填充"分析结果区"，**设计方案可二次编辑**；对话记录与分析结果在弹窗内保留，关闭再打开不丢失；
3. 用户确认"生成需求卡片" → 落库生成需求卡片（状态"已确认"）+ 需求详情文档（含设计方案章节）存知识库 `需求/` 目录；
4. 可"跳过 AI 分析"直接生成（状态"待分析"）。

**上线验证回填**：需求关联版本全部上线后，可在需求详情填写"效果数据/价值达成/复盘说明"，价值达成回显于需求列表/看板"价值达成"标签。

**前端页面**：`PmRequirementBoard.vue`（列表/看板双视图）、`PmRequirementDetail.vue`（详情 + 上线验证区 + 设计方案编辑）、`PmRequirementForm.vue`（新建表单弹窗）、`PmRequirementAiDialog.vue`（AI 分析弹窗）

### 6.3 功能规划（产品路线 · 持续规划）

功能规划是**贯穿项目全生命周期**的持续规划过程，不局限于项目立项：项目创建后通过"首期规划"建立产品骨架，推进过程中结合新需求、版本节奏与阶段复盘随时发起"迭代规划"。每次规划形成一条**规划批次**记录，并生成内容快照，支持回溯。

**（1）路线图视图**

- 顶部工具栏：`新增规划`（主按钮，无路线图时文案为"开始规划"）/ `重新规划` / `规划历史`；不提供独立"编辑"入口，内容调整统一走规划引导
- 批次条：横向平铺展示规划批次（当前批次高亮），右侧展示"本次变更"摘要、批次状态标签与"查看快照"入口
- 主体双栏：
  - 左：核心功能清单，三级树（模块 → 功能点 → 操作项），功能点展示功能描述，汇总"模块数 · 功能点数 · 首期项数"
  - 右：核心业务流程，按模块切换查看的可视化流程图（迷你图 + 放大查看）
- 页面数据不做数据权限管理，拥有表单权限即可查看与操作当前项目路线图

**（2）AI 引导规划页（5 步）**

左侧引导对话 + 右侧路线图草稿的双栏布局，顶部 5 步步骤条，底部操作条：

| 步骤 | 名称 | 引导内容 | 产出 |
|---|---|---|---|
| 1 | 项目认知 | 读取项目简介、知识库与现有路线图，输出目标用户/核心价值/主要场景并追问确认；增量规划补充"本次规划背景" | 项目认知信息 |
| 2 | 功能清单 | 按"模块 → 功能点 → 操作项"三级结构生成初版，功能点带功能描述；支持勾选采纳、增删节点、调整层级与排序 | 三级功能清单 |
| 3 | 业务流程 | 为每个核心模块生成流程图（开始/结束、处理节点、判断分支），支持拖拽排序、增删节点、双击改名、添加分支、切换模块 | 可视化流程图 |
| 4 | 首期范围 | 在三级清单上勾选本批次落地范围并标注优先级，未勾选标记为"后续规划" | 首期范围 |
| 5 | 确认生成 | 汇总预览（模块/功能点/流程数、首期占比）+ 批次信息（批次名称/规划类型/关联版本）+ 落库位置，确认生成 | 路线图文档与批次记录 |

**布局约束**：左右双栏等高；左栏纵向弹性布局，对话区自适应填充并可内部滚动，输入框固定贴底且下方不留白；底部仅保留"上一步 / 下一步"与自动保存提示，第 5 步主按钮文案切换为"完成规划"，不设独立完成按钮。

**规划模式**：

| 模式 | 入口 | 预填内容 | 生成方式 |
|---|---|---|---|
| 首期规划 | 开始规划（无路线图） | 无，AI 从项目资料生成初版 | 新建批次 |
| 增量规划 | 新增规划（已有路线图） | 预填当前生效批次全部内容，AI 标注新增/调整建议 | 与当前路线图合并，生成新批次 |
| 重新规划 | 重新规划（当前批次） | 预填当前批次内容 | 覆盖当前批次内容，另存快照 |

**（3）草稿与校验**

- 草稿随步骤实时更新，切换步骤不丢失，当前步骤对应草稿区块高亮；中途退出自动保存，可继续
- 步骤校验：①确认项目认知；②至少 1 个模块且每模块至少 1 个功能点、功能点至少关联 1 个操作项；③每模块至少 1 条流程且节点不少于 2 个；④至少勾选 1 个功能点；未通过则横幅提示并定位问题项

**（4）生成与合并规则**

- 生成《产品路线图》Markdown，写入知识库 `需求/`（`产品路线图-{项目名称}.md`），包含项目认知、三级功能清单、核心业务流程（Mermaid 代码 + 渲染图）、首期范围
- 增量规划合并：新增节点追加；同名节点做字段更新并记录前后差异；移除节点不物理删除，标记"已移除"并保留在批次快照
- 每次生成写批次快照至 `需求/规划历史/产品路线图-{项目名称}-{批次名称}.md`
- 勾选"同步至功能清单"时，按模块/功能点/操作项名称匹配写入或更新功能清单（来源标记"路线图规划"，初始状态 `planned`）

> 路线图同时作为后续需求的 **AI 分析与 PRD 生成的骨架上下文**：需求分析时带入当前生效批次，判断需求在整体产品中的位置与关联影响。

**前端页面**：`PmRoadmapView.vue`（路线图视图：批次条 + 三级清单 + 流程图）、`PmRoadmapGuide.vue`（5 步 AI 引导页）、`PmPlanHistoryDialog.vue`（规划批次列表与快照查看）

### 6.4 功能清单

- 三级结构展示（模块 → 功能点 → 操作项），树形清单
- **自动同步（双来源）**：① PRD 生成时同步导出功能清单结构化数据（模板约定为主 + AI 兜底），后端直接落库，无需二次解析；② 路线图规划确认时勾选"同步至功能清单"，按"模块/功能点/操作项"名称匹配写入或更新（来源标记"路线图规划"）
- **多对多迭代**：一个功能点可归属多个迭代版本，每次演进取"变更类型（新增/变更/移除）+ 变更说明"记录于 `pm_feature_iterations`，形成**迭代历史**可追溯
- **上线联动**：按**最新迭代版本**状态联动标记 `planned`/`launched`，不单独手工维护状态
- 支持人工补充自由描述的功能点（AI 兜底解析）

**前端页面**：`PmFeatureTree.vue`（树形清单 + 来源需求/迭代版本追溯 + 迭代历史抽屉）

### 6.5 版本管理

**流程**：
1. 从需求池归纳选择本版本需求（多对多关联 `pm_version_requirements`），填写**计划上线日**（须晚于规划日）
2. 版本状态 `规划中` → 触发 `pm-prd-generator` 生成 PRD + 功能清单双产物 → 触发 `pm-prototype` 生成 HTML 原型（异步任务）
3. 进入 `评审中`：记录评审意见（按轮次，含功能变更明细）+ **评审结论**（通过/有条件通过/不通过）+ **参会人**，并对每条意见做**处理跟踪**（待处理/已解决/不处理 + 处理说明）
4. 根据评审意见 → **重新生成**新版 PRD/原型（PRD-v2/v3…，保留历史供对照）→ 回到评审中，可多轮
5. 评审通过（最近一轮结论为"通过"或"有条件通过"）→ `定稿`：锁定生成版本号 `X.Y.Z` + 更新内容大纲（关联需求功能摘要聚合）
6. `开发中` → 上线 → `已上线`：记录上线日，关联需求联动 `开发中`→`已上线`，功能清单联动上线，项目资料模块提示更新
7. 搁置：`已搁置` → 关联需求按 §4.1 规则回落 `已确认`，需求可重新分配

**前端页面**：`PmVersionList.vue`（版本列表/状态）、`PmVersionDetail.vue`（需求归纳、计划上线日、PRD/原型产物、评审意见与结论、意见跟踪、重生成、定稿操作）

### 6.6 项目资料

**产物类型**：操作手册（manual）、宣传册（brochure）、PPT（ppt）

**流程**：
1. 创建资料：选择类型 → 标记目标版本号 → `pm-material` 生成草稿（Markdown/HTML）
2. 人工确认 → 发布（转 docx/pptx 正式格式，存知识库 `资料/` 目录）
3. **版本更新提示**：新版本状态变为 `已上线` 后，资料模块提示"有新版可用"
4. 用户点击"更新资料" → 基于最新版 PRD/功能清单重新生成**草稿** → 确认后替换对应资料，**旧版归档保留**（可回滚）

**前端页面**：`PmMaterialList.vue`（资料卡片 + 版本号标记 + 更新提示徽标）、`PmMaterialPreview.vue`（草稿预览/确认发布）

---

## 7. 前端页面结构

```
/platform/pm（PmLayoutView.vue，左侧项目切换 + 右侧 RouterView）
├── 项目列表（PmProjectListView.vue）
├── 需求池（PmRequirementBoard.vue[列表/看板] / PmRequirementDetail.vue / PmRequirementForm.vue / PmRequirementAiDialog.vue）
├── 功能规划（PmRoadmapView.vue / PmRoadmapGuide.vue / PmPlanHistoryDialog.vue）
├── 功能清单（PmFeatureTree.vue）
├── 版本管理（PmVersionList.vue / PmVersionDetail.vue）
└── 项目资料（PmMaterialList.vue / PmMaterialPreview.vue）
```

- 侧边栏菜单：在 [menu.ts](frontend/src/stores/menu.ts) 增加 `MenuItem`（`titleKey/icon/path`）
- 路由：在 [router/index.ts](frontend/src/router/index.ts) `/platform` 下注册子路由
- API 层：仿照 [api/meeting/index.ts](frontend/src/api/meeting/index.ts) 新建 `api/pm/` 模块，复用统一 Axios 实例（自动注入 Authorization/X-Tenant-ID）

---

## 8. 后端 API 清单

> 路由统一挂载 `/api/v1/pm/`，遵循 [router.go](internal/router/router.go) 注册模式与 [rbac.go](internal/router/rbac.go) 权限声明。

### 8.1 项目

| 方法 | 路径 | 说明 |
|---|---|---|
| POST | `/api/v1/pm/projects` | 创建项目（参数：`is_new`，新建则动态建库+建会话） |
| GET | `/api/v1/pm/projects` | 项目列表 |
| GET | `/api/v1/pm/projects/:id` | 项目详情 |
| PATCH | `/api/v1/pm/projects/:id` | 更新项目 |
| POST | `/api/v1/pm/projects/:id/archive` | 归档 / 恢复 |

### 8.2 功能规划（路线图与批次）

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/v1/pm/projects/:id/plan-batches` | 规划批次列表（批次名称/规划类型/关联版本/变更摘要/操作人/时间/状态） |
| POST | `/api/v1/pm/projects/:id/plan-batches` | 创建规划批次（`plan_type` 首期/迭代，`version_id` 选填，初始 `draft`） |
| GET | `/api/v1/pm/plan-batches/:id` | 批次详情（含草稿进度、当前步骤、变更摘要、快照地址） |
| POST | `/api/v1/pm/plan-batches/:id/draft` | 保存引导草稿（按步骤提交，用于退出后续接） |
| POST | `/api/v1/pm/plan-batches/:id/generate` | 生成/合并路线图（异步；参数 `sync_features` 控制是否同步功能清单） |
| POST | `/api/v1/pm/plan-batches/:id/activate` | 确认生效（原 `active` 批次转 `archived`，写批次快照） |
| GET | `/api/v1/pm/plan-batches/:id/snapshot` | 获取批次快照文档内容（只读） |
| POST | `/api/v1/pm/projects/:id/roadmap/guide` | 获取/续接引导会话（返回 `session_id`、当前步骤与草稿） |

### 8.3 需求

| 方法 | 路径 | 说明 |
|---|---|---|
| POST | `/api/v1/pm/projects/:id/requirements` | 新建需求（含来源 + AI 分析 JSON） |
| GET | `/api/v1/pm/projects/:id/requirements` | 需求列表（卡片字段，支持按优先级/状态/来源/价值达成筛选与排序） |
| GET | `/api/v1/pm/requirements/:id` | 需求详情 |
| PATCH | `/api/v1/pm/requirements/:id` | 更新需求（状态/优先级/来源/描述） |
| POST | `/api/v1/pm/requirements/:id/analyze` | 触发 AI 引导分析（返回会话信息，带入路线图上下文） |
| POST | `/api/v1/pm/requirements/:id/reactivate` | 重新激活（驳回/关闭 → 待分析） |
| POST | `/api/v1/pm/requirements/:id/launch-verification` | 上线验证回填（效果数据/价值达成/复盘说明） |

### 8.4 版本

| 方法 | 路径 | 说明 |
|---|---|---|
| POST | `/api/v1/pm/projects/:id/versions` | 创建版本（选择需求集合 + 计划上线日） |
| GET | `/api/v1/pm/projects/:id/versions` | 版本列表（含计划上线日/实际上线日） |
| GET | `/api/v1/pm/versions/:id` | 版本详情（含 PRD/原型产物、评审意见与结论、意见跟踪、参会人） |
| PATCH | `/api/v1/pm/versions/:id` | 更新版本状态 / 需求集合（状态变更触发需求状态联动：开发中/搁置/上线） |
| POST | `/api/v1/pm/versions/:id/generate` | 触发 PRD+功能清单+原型生成（异步任务） |
| POST | `/api/v1/pm/versions/:id/review` | 记录评审意见（轮次+1，含结论/参会人/意见处理状态） |
| POST | `/api/v1/pm/versions/:id/regenerate` | 按最新评审意见重新生成（PRD-v2/v3…） |
| POST | `/api/v1/pm/versions/:id/finalize` | 定稿（校验最近一轮结论为通过/有条件通过，生成版本号+更新内容大纲） |

### 8.5 功能清单

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/v1/pm/projects/:id/features` | 功能清单树（含最新迭代版本状态） |
| POST | `/api/v1/pm/features` | 人工补充功能点（可关联迭代版本） |
| PATCH | `/api/v1/pm/features/:id` | 更新功能点 / 迭代版本归属 |
| GET | `/api/v1/pm/features/:id/iterations` | 功能点迭代历史（按版本规划时间升序） |
| POST | `/api/v1/pm/features/:id/iterations` | 记录一次迭代（变更类型+变更说明） |

### 8.6 项目资料

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/v1/pm/projects/:id/materials` | 资料列表（含版本号、更新提示） |
| POST | `/api/v1/pm/materials` | 创建资料（类型+目标版本） |
| POST | `/api/v1/pm/materials/:id/generate` | 生成草稿（异步任务） |
| POST | `/api/v1/pm/materials/:id/publish` | 确认发布（转正式格式） |
| POST | `/api/v1/pm/materials/:id/update` | 更新到最新版本（重新生成草稿） |

### 8.7 通用异步任务

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/v1/pm/tasks/:task_id` | 异步生成任务进度轮询 |

---

## 9. 技能定义（Skill 清单）

| 技能名 | 触发场景 | 输入 | 输出 |
|---|---|---|---|
| `pm-roadmap-planner` | 产品路线规划（首期/增量/重新规划） | 项目简介 + 知识库资料 + 现有路线图（增量时）+ 关联版本/需求来源 | 三级功能清单 + Mermaid 流程图 + 项目认知 + 首期范围 + 变更摘要（可选同步功能清单结构化数据） |
| `pm-requirement-analysis` | 新建需求引导分析 | 用户初步想法 + 需求来源 + 项目产品路线图上下文 | JSON（价值/关联影响/风险/建议优先级/建议状态/具体设计方案） |
| `pm-prd-generator` | 版本 PRD 生成/重生成 | 版本需求集合 + 产品路线图 + 评审意见（如有） | PRD Markdown + 功能清单结构化数据 |
| `pm-prototype` | 原型生成/重生成 | PRD 文档 | HTML 原型文件 |
| `pm-material` | 资料生成/更新 | 最新版 PRD + 功能清单 + 资料类型 | Markdown/HTML 草稿 |

> 技能安装遵循 WeKnora 技能管理流程（installer agent），SKILL.md 中明确声明沙箱能力边界。

---

## 10. 实现分期

| 阶段 | 内容 | 依赖 |
|---|---|---|
| **一期** | 项目创建（动态建库/建会话）+ **功能规划（持续规划：3 种模式 + 5 步 AI 引导 + 批次与快照）** + 需求池（两步弹窗 + AI 分析含设计方案、列表/看板双视图）+ 版本管理（PRD/原型生成、评审迭代、定稿上线）+ **功能清单同步（PRD / 路线图双来源，多对多迭代）** | 完成"需求 → 版本 → 功能清单"主链路闭环；**路线图前置**为需求分析与 PRD 生成提供上下文 |
| **二期** | 上线验证回填（依赖版本上线数据） | 一期版本管理 |
| **三期** | 项目资料（手册/宣传册/PPT 自动更新） | 一期版本管理 + 二期上线数据 |

---

## 11. 文件落点清单

### 11.1 后端（Go）

| 文件 | 说明 |
|---|---|
| `migrations/versioned/xxxxxx_create_pm_tables.up.sql` | pm_* 表迁移（含 pm_feature_iterations 迭代历史表、pm_plan_batches 规划批次表） |
| `internal/application/service/pm_project.go` | 项目服务（含动态建库/建会话） |
| `internal/application/service/pm_roadmap.go` | 功能规划服务（引导草稿、路线图生成/合并、批次生效与快照） |
| `internal/application/service/pm_requirement.go` | 需求服务（含 AI 分析编排） |
| `internal/application/service/pm_version.go` | 版本服务（生成/评审/定稿） |
| `internal/application/service/pm_feature.go` | 功能清单服务（含迭代历史 pm_feature_iterations） |
| `internal/application/service/pm_material.go` | 项目资料服务（生成/发布/更新） |
| `internal/application/service/pm_task.go` | 异步生成任务（asynq） |
| `internal/handler/pm_*.go` | 各模块 handler |
| `internal/repository/pm_*.go` | 各模块 repository |
| `internal/container/container.go` | 注册新服务/handler/repo |
| `internal/router/router.go` + `rbac.go` | 挂载 `/api/v1/pm/*` 路由与权限 |

### 11.2 前端（Vue3）

| 文件 | 说明 |
|---|---|
| `frontend/src/router/index.ts` | 注册 `/platform/pm` 子路由 |
| `frontend/src/stores/menu.ts` | 增加 PM 工作站菜单项 |
| `frontend/src/api/pm/*.ts` | API 封装模块 |
| `frontend/src/views/pm/PmLayoutView.vue` | 模块布局（左侧项目切换） |
| `frontend/src/views/pm/PmProjectListView.vue` | 项目列表 |
| `frontend/src/views/pm/PmRequirementBoard.vue` | 需求池（列表/看板双视图） |
| `frontend/src/views/pm/PmRequirementForm.vue` | 新建需求表单弹窗 |
| `frontend/src/views/pm/PmRequirementAiDialog.vue` | AI 分析弹窗（对话 + 结果编辑） |
| `frontend/src/views/pm/PmRequirementDetail.vue` | 需求详情 + 上线验证区 + 设计方案编辑 |
| `frontend/src/views/pm/PmRoadmapView.vue` | 功能规划：批次条 + 三级功能清单 + 可视化流程图 |
| `frontend/src/views/pm/PmRoadmapGuide.vue` | AI 引导规划页（5 步步骤条 + 左对话 + 右草稿 + 底部操作条） |
| `frontend/src/views/pm/PmPlanHistoryDialog.vue` | 规划批次列表与快照查看 |
| `frontend/src/views/pm/PmFeatureTree.vue` | 功能清单树 |
| `frontend/src/views/pm/PmVersionList.vue` | 版本列表 |
| `frontend/src/views/pm/PmVersionDetail.vue` | 版本详情（评审/重生成/定稿） |
| `frontend/src/views/pm/PmMaterialList.vue` | 资料列表（更新提示） |
| `frontend/src/views/pm/PmMaterialPreview.vue` | 资料预览/发布 |

### 11.3 技能

| 文件 | 说明 |
|---|---|
| `cli/skills/pm-roadmap-planner/SKILL.md` | 产品路线规划技能（5 步引导，输出三级清单 + Mermaid 流程图） |
| `cli/skills/pm-requirement-analysis/SKILL.md` | 需求引导分析技能 |
| `cli/skills/pm-prd-generator/SKILL.md` | PRD 生成技能 |
| `cli/skills/pm-prototype/SKILL.md` | 原型生成技能 |
| `cli/skills/pm-material/SKILL.md` | 资料生成技能 |

> 内嵌技能流程：技能目录复制到 `cli/skills/`，并在 embed.go 的 `//go:embed` 指令追加 `all:<技能名>`。

---

## 12. 风险与约束

| 风险 | 应对 |
|---|---|
| 沙箱 120s 超时 / ReAct 10 轮上限 | 长任务拆"起草→转格式"多步，走异步队列 |
| 技能输出不可控（格式漂移） | 引导分析强制 JSON 输出；PRD 功能清单章节模板约束 + AI 兜底 |
| 资料被 AI 错误内容静默覆盖 | 先草稿后定稿 + 人工确认 + 旧版归档，杜绝静默替换 |
| 路线图无快照被直接改写 | 路线图不提供独立编辑入口，内容变更统一走规划引导；每次生成写批次快照，历史批次只读 |
| 流程图渲染兼容性（Mermaid 语法差异） | 统一 `flowchart TD/LR` 子集（节点类型 + 判断分支），生成后服务端校验语法；文档同时保存 Mermaid 代码与渲染图片，阅读端不支持 Mermaid 时降级为图片 |
| 知识库目录层级能力受限 | 退化为命名前缀约定（`需求/ 需求/规划历史/ PRD/ 原型/ 资料/` → `REQ-xxx / ROADMAP-HIS-{批次} / PRD-v1.2.0 / ...`） |
| 技能安装网络受限 | 遵循现有技能安装流程（zip 包上传 / installer agent） |
