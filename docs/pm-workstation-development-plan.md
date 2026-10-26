# PM 工作站 开发计划

> **给执行者：** 按任务顺序逐条执行。每个任务都以「先写测试 → 确认失败 → 最小实现 → 确认通过」推进。
> 设计与需求依据：[pm-workstation.md](../pm-workstation.md)（方案 v1.3）、[PRD_PM工作站_20261019.md](./PRD_PM工作站_20261019.md)（PRD v1.8）、[pm-workstation-prototype.html](./pm-workstation-prototype.html)（交互真源）。
> **本计划不含 git 提交步骤**——遵循项目约定「未经明确要求不提交」。
> 实现中发现与设计冲突时：**先改设计文档，再改代码**，并在本文件「实现层补充设计」章节留痕。

**目标：** 在 WeKnora 中新增「PM 工作站」模块，打通「需求池 → 功能规划 → 版本 → 功能清单」主链路，并复用现有知识库、会话、异步任务能力承载 PRD / 原型 / 资料的生成与归档。

**范围：** 后端 `/api/v1/pm/*` + 前端 `/platform/pm` + 5 个内嵌 PM 技能。按方案 §10 分三期交付，Task 1–20 为一期，Task 21 为二期，Task 22 为三期。

---

## 1. 架构

### 1.1 分层落点（以仓库现状为准）

方案 §11.1 写的是 `internal/repository/pm_*.go`，**实际仓库的仓储层位于 `internal/application/repository/`**（参见 [meeting.go](../internal/application/repository/meeting.go)）。本计划按真实路径执行。

```
HTTP 请求
  └─ internal/router/routes_pm.go          路由注册 + RBAC 守卫
       └─ internal/handler/pm_*.go         参数绑定、租户/用户上下文、错误映射
            └─ internal/application/service/pm_*.go   业务编排、状态机、外部能力调用
                 ├─ internal/application/repository/pm_*.go   纯数据访问（GORM）
                 └─ 复用：KnowledgeService / KnowledgeBaseService / SessionService / asynq
```

- 模型与常量：`internal/types/pm*.go`
- 接口契约：`internal/types/interfaces/pm.go`
- 依赖注入：`internal/container/container.go`（`dig` + `must(container.Provide(...))`）
- 异步任务：`internal/router/task.go` 与 `internal/router/sync_task.go` **两处都要注册**

### 1.2 与既有模块的复用关系

| 能力 | 复用对象 | 用途 |
|---|---|---|
| 知识库创建 | `KnowledgeBaseService.CreateKnowledgeBase` | 新建项目时动态建库 |
| 文档落库 | `KnowledgeService.CreateKnowledgeFromPassageSync` / `CreateKnowledgeFromManual` | PRD、原型、资料、路线图落库 |
| 会话 | `SessionService.CreateSession` | 每项目常驻 PM 会话、需求分析会话 |
| 异步任务 | asynq + `task_queue` | PRD / 原型 / 资料生成 |
| RBAC | `middleware.RequireOwnershipOrRole` + `CreatorLookup` | 项目归属校验 |
| JSON 字段 | `internal/types/json.go` 的 `types.JSON` | 所有 JSON/JSONB 字段（不引入 `gorm.io/datatypes`） |

### 1.3 关键架构决策（写死，避免实现期反复）

1. **路线图生成不走 asynq 调 LLM**：前端 5 步 AI 引导会话产出结构化草稿 → 第 5 步提交草稿到后端 → 后端做**确定性渲染 / 合并 / 落库**。"生成"接口同步完成，返回 `task_id` 仅为兼容前端轮询（直接置 `succeeded`）。
2. **PRD / 原型 / 资料生成走 asynq**：任务内「结构化上下文组装 + 模板渲染（必成功）+ 可选 LLM 润色（失败降级为模板结果）」。避免沙箱 120s / ReAct 10 轮限制导致整体失败（方案 §12）。
3. **状态枚举入库为英文**，中文只在 UI 映射；避免多语言与数据库排序问题。
4. **主键统一 `VARCHAR(36)` UUID**（与 `meeting_rooms` 一致），方案 §3 中的 `BIGINT` 属于笔误，按实现层修正。
5. **所有表带 `tenant_id BIGINT NOT NULL` 与 `deleted_at` 软删**，方案 §3 未显式列出，属实现层补充。
6. **Lite / SQLite 模式不支持本模块**：`migrations/sqlite/` 无任何 meeting 表，沿用同一惯例，只提供 PostgreSQL 迁移。启动时若检测到 sqlite DSN，PM 路由照常注册但首次访问返回「当前部署模式不支持 PM 工作站」。

---

## 2. 技术栈

| 层 | 技术 |
|---|---|
| 后端 | Go 1.26 · Gin · GORM · PostgreSQL（JSONB）· asynq |
| 测试 | `testing` · `testify` v1.11.1 · `DATA-DOG/go-sqlmock` v1.5.2 · `gorm.io/driver/sqlite` v1.6.0 |
| 前端 | Vue 3 · Vite · TypeScript · TDesign Vue Next · Axios · Mermaid（已有依赖，复用） |
| 前端测试 | `tsx --test`（纯函数单测）+ `vue-tsc --build` 类型检查 |
| 技能 | Markdown + YAML frontmatter，`//go:embed` 内嵌 |

---

## 3. 验证基线（已实测）

| 命令 | 期望 |
|---|---|
| `go build ./internal/...` | 退出码 0 |
| `go test ./internal/types/... -count=1` | `ok` |
| `go test ./internal/application/repository/... -count=1` | 全包 `ok` |
| `go test ./internal/application/service/... -count=1` | 全包 `ok` |
| `go test ./internal/handler/... -count=1` | `ok` |
| `go build ./...` | **可能失败（既有环境问题）**：`cmd/download/duckdb` 因本地 DuckDB CGO 静态库缺失报 `undefined reference`，与本模块无关，不要尝试修复；以 `go build ./internal/... ./cmd/server/...` 作为替代验证 |
| `cd frontend; npm run type-check` | 退出码 0 |
| `cd frontend; npm run test` | 全部通过 |
| `cd frontend; npm run check-i18n` | 退出码 0（五个语言文件键集合一致） |
| `cd frontend; npm run build` | 产出 `dist/`，无告警级错误 |

> 环境注记（2026-10-20 实测）：本机 `CGO_ENABLED=0` 且 PATH 无 `gcc`，任何依赖 `internal/utils` 的包执行 `go build` 都会报 `undefined: pg_query.Parse`。该失败与 PM 模块无关，**不要尝试修复**；此类 Task 的验证改用 `go test` 指定包（测试同样需要 CGO，若失败则以真实 PostgreSQL 冒烟替代），并在交付说明中注明。

---

## 4. 实现层补充设计

以下是方案 §3 未覆盖、但实现必须落定的内容。**实现时以本章为准**。

### 4.1 表结构总览（9 张）

| 表 | 说明 | 对应方案 |
|---|---|---|
| `pm_projects` | 项目 | §3.1 |
| `pm_requirements` | 需求 | §3.2 |
| `pm_versions` | 版本 | §3.3 |
| `pm_version_requirements` | 版本↔需求 | §3.4 |
| `pm_features` | 功能清单三级结构 | §3.5 |
| `pm_feature_iterations` | 功能点迭代历史 | §3.5.1 |
| `pm_materials` | 项目资料 | §3.6 |
| `pm_plan_batches` | 规划批次 | §3.7（+ 4 个补充字段） |
| `pm_tasks` | 异步任务进度 | **新增**（§8.7 有接口无表） |

### 4.2 对方案 §3 的字段补充

| 表 | 补充字段 | 原因 |
|---|---|---|
| 全部表 | `tenant_id BIGINT NOT NULL` | 仓库所有业务表强制多租户隔离 |
| 全部表 | `deleted_at TIMESTAMPTZ` | 与 `meeting_rooms` 一致，统一软删 |
| `pm_projects` | `session_id VARCHAR(36)` | §5.1「每项目常驻 PM 会话」需要持久化指针 |
| `pm_features` | `source VARCHAR(16)` | §6.4「PRD / 路线图双来源」需要区分来源 |
| `pm_plan_batches` | `draft JSONB`、`current_step SMALLINT`、`generated_at`、`snapshot_at` | §6.3 5 步引导需要草稿续接、步骤回显、生成/快照时间 |
| `pm_tasks` | 整表 | §8.7 任务轮询接口缺载体 |

### 4.3 枚举字典（数据库存英文）

**需求状态**（方案 §4.1 八态）

| 英文值 | UI 中文 | 备注 |
|---|---|---|
| `pending_analysis` | 待分析 | 初始态 |
| `analyzing` | 分析中 | |
| `confirmed` | 已确认 | |
| `scheduled` | 已排期 | 进入版本后由版本联动置位 |
| `developing` | 开发中 | 版本进入开发中时联动 |
| `launched` | 已上线 | 版本上线时联动 |
| `rejected` | 已驳回 | 终止态，可重新激活 |
| `closed` | 已关闭 | 终止态，可重新激活 |

**版本状态**（方案 §4.2 六态）

| 英文值 | UI 中文 |
|---|---|
| `planning` | 规划中 |
| `reviewing` | 评审中 |
| `finalized` | 定稿 |
| `developing` | 开发中 |
| `launched` | 已上线 |
| `shelved` | 已搁置 |

**其余枚举**

| 字段 | 取值 |
|---|---|
| `pm_requirements.priority` | `P0` `P1` `P2` `P3` |
| `pm_requirements.source` | `customer` `internal` `data` `competitor` `strategy` `other` |
| `pm_requirements.value_achieved` | `achieved` `partial` `not_achieved`（空串 = 未回填） |
| `pm_versions.review_conclusion` | `pass` `conditional` `reject`（空串 = 未评审） |
| `pm_features.status` | `planned` `launched` |
| `pm_features.source` | `roadmap` `prd` `manual` |
| `pm_feature_iterations.change_type` | `add` `change` `remove` |
| `pm_materials.material_type` | `manual` `brochure` `ppt` |
| `pm_materials.status` | `draft` `published` |
| `pm_plan_batches.plan_type` | `initial` `iteration` |
| `pm_plan_batches.status` | `draft` `active` `archived` |
| `pm_tasks.status` | `pending` `running` `succeeded` `failed` |

### 4.4 迁移文件

- 目录：`migrations/versioned/`（PostgreSQL）
- 序号：当前最新为 `000129_meeting_topic_brief_web_search`，PM 使用 **`000130_create_pm_tables`**
- 必须成对提供 `.up.sql` / `.down.sql`

### 4.5 知识库目录约定与降级

方案 §3.8 要求四个一级目录。仓库知识库目录能力若不支持多级，按方案 §12 的降级方案执行：

| 逻辑目录 | 降级命名前缀 |
|---|---|
| `需求/` | `REQ-{需求标题}` |
| `需求/规划历史/` | `ROADMAP-HIS-{批次名称}` |
| `PRD/` | `PRD-v{版本号}` |
| `原型/` | `PROTO-v{版本号}` |
| `资料/` | `MAT-{资料类型}-{标题}` |

目录服务（Task 6）对外暴露逻辑目录名，内部决定是否降级，业务层不感知。

---

## 5. 文件结构

### 5.1 后端

| 文件 | 职责 | 任务 |
|---|---|---|
| `migrations/versioned/000130_create_pm_tables.up.sql` | 9 张表 + 索引 | T1 |
| `migrations/versioned/000130_create_pm_tables.down.sql` | 逆序 drop | T1 |
| `internal/types/pm.go` | 模型、常量、枚举、状态机纯函数 | T2 |
| `internal/types/pm_test.go` | 状态机与枚举单测 | T2 |
| `internal/types/interfaces/pm.go` | Repository / Service 接口 | T3 |
| `internal/application/repository/pm_project.go` | 项目仓储 | T4 |
| `internal/application/repository/pm_requirement.go` | 需求仓储 | T4 |
| `internal/application/repository/pm_version.go` | 版本 + 版本需求关联仓储 | T4 |
| `internal/application/repository/pm_feature.go` | 功能点 + 迭代历史仓储 | T4 |
| `internal/application/repository/pm_material.go` | 资料仓储 | T4 |
| `internal/application/repository/pm_plan_batch.go` | 规划批次仓储 | T4 |
| `internal/application/repository/pm_task.go` | 异步任务仓储 | T4 |
| `internal/application/service/pm_project.go` | 项目服务 | T5 |
| `internal/application/service/pm_kb_dir.go` | 知识库产物目录服务 | T6 |
| `internal/application/service/pm_roadmap.go` | 功能规划服务（草稿/生成/合并/生效/快照） | T7 |
| `internal/application/service/pm_requirement.go` | 需求服务（状态机 + AI 分析编排） | T8 |
| `internal/application/service/pm_version.go` | 版本服务（生成/评审/定稿/联动） | T9 |
| `internal/application/service/pm_feature.go` | 功能清单服务（双来源 + 迭代历史） | T10 |
| `internal/application/service/pm_task.go` | asynq 任务处理 + 进度轮询 | T11 |
| `internal/application/service/pm_material.go` | 资料服务（T22 实现） | T22 |
| `internal/handler/pm_project.go` | 项目 + 规划批次 handler | T12 |
| `internal/handler/pm_requirement.go` | 需求 handler | T12 |
| `internal/handler/pm_version.go` | 版本 handler | T12 |
| `internal/handler/pm_feature.go` | 功能清单 handler | T12 |
| `internal/handler/pm_task.go` | 任务轮询 handler | T12 |
| `internal/handler/pm_rbac_lookups.go` | `PMProjectCreatorLookup` | T12 |
| `internal/handler/pm_material.go` | 资料 handler | T22 |
| `internal/router/routes_pm.go` | 路由注册 | T12 |
| `internal/router/rbac.go` | 新增 `pmCreator` + `OwnedPMProjectOrAdmin()` | T12 |
| `internal/router/router.go` | 注入 `PMHandler`、调用 `RegisterPMRoutes` | T12 |
| `internal/router/task.go` | asynq mux 注册 | T11 |
| `internal/router/sync_task.go` | 同步执行器注册 | T11 |
| `internal/container/container.go` | DI 注册 | T12 |
| `internal/types/task.go` | 新增 3 个任务类型常量 | T11 |

### 5.2 前端

| 文件 | 职责 | 任务 |
|---|---|---|
| `frontend/src/api/pm/index.ts` | 全部 PM 接口封装 + 类型 | T13 |
| `frontend/src/api/pm/pm.test.ts` | 请求参数/URL 构造单测 | T13 |
| `frontend/src/views/pm/pmStatus.ts` | 状态 → 中文/颜色映射、看板列定义 | T13 |
| `frontend/src/views/pm/pmStatus.test.ts` | 映射完整性单测 | T13 |
| `frontend/src/views/pm/PmLayoutView.vue` | 左侧项目切换 + 右侧 RouterView | T13 |
| `frontend/src/views/pm/PmProjectListView.vue` | 项目列表 + 空态 + 新建/关联 | T14 |
| `frontend/src/views/pm/PmProjectDialog.vue` | 新建 / 关联已存在知识库 | T14 |
| `frontend/src/views/pm/PmRoadmapView.vue` | 批次条 + 路线图渲染 + 入口 | T15 |
| `frontend/src/views/pm/PmRoadmapGuide.vue` | 5 步引导（左对话 / 右草稿 / 底部操作条） | T15 |
| `frontend/src/views/pm/PmPlanHistoryDialog.vue` | 批次历史与快照查看 | T15 |
| `frontend/src/views/pm/PmRequirementBoard.vue` | 需求池列表/看板双视图 | T16 |
| `frontend/src/views/pm/PmRequirementForm.vue` | 两步新建弹窗 | T16 |
| `frontend/src/views/pm/PmRequirementAiDialog.vue` | AI 分析对话 + 结果编辑 | T16 |
| `frontend/src/views/pm/PmRequirementDetail.vue` | 详情抽屉 + 上线验证区 | T16 / T21 |
| `frontend/src/views/pm/PmVersionList.vue` | 版本列表 | T17 |
| `frontend/src/views/pm/PmVersionDetail.vue` | 详情：生成/评审/定稿/上线 | T17 |
| `frontend/src/views/pm/PmFeatureTree.vue` | 功能清单树 + 迭代历史 | T18 |
| `frontend/src/views/pm/PmMaterialList.vue` | 资料列表 | T22 |
| `frontend/src/views/pm/PmMaterialPreview.vue` | 预览 / 发布 | T22 |
| `frontend/src/views/pm/components/PmTaskProgress.vue` | 异步任务进度轮询组件 | T13 |
| `frontend/src/router/index.ts` | `/platform/pm` 路由树 | T13 |
| `frontend/src/stores/menu.ts` | 菜单项 | T13 |
| `frontend/src/i18n/locales/*.ts` | 5 语言键 | T13–T18 |

### 5.3 技能

| 文件 | 任务 |
|---|---|
| `cli/skills/pm-roadmap-planner/SKILL.md` | T19 |
| `cli/skills/pm-requirement-analysis/SKILL.md` | T19 |
| `cli/skills/pm-prd-generator/SKILL.md` | T19 |
| `cli/skills/pm-prototype/SKILL.md` | T19 |
| `cli/skills/pm-material/SKILL.md` | T19 |
| `cli/skills/embed.go` | T19 |

---

## 6. 任务清单

### Task 1：数据库迁移

**Files:**
- Create: `migrations/versioned/000130_create_pm_tables.up.sql`
- Create: `migrations/versioned/000130_create_pm_tables.down.sql`

- [x] **Step 1：先写迁移自检脚本（无数据库依赖）**

在仓库根目录执行以下 PowerShell 脚本，校验 up/down 语句成对且表数量正确：

```powershell
# 校验：up 中 CREATE TABLE 数量为 9，down 中 DROP TABLE 数量为 9
$up = Get-Content migrations/versioned/000130_create_pm_tables.up.sql -Raw
$down = Get-Content migrations/versioned/000130_create_pm_tables.down.sql -Raw
($up | Select-String -Pattern 'CREATE TABLE' -AllMatches).Matches.Count   # 期望 9
($down | Select-String -Pattern 'DROP TABLE' -AllMatches).Matches.Count  # 期望 9
```

- [x] **Step 2：确认失败**

若文件不存在，`Get-Content` 报 `找不到路径`。

- [x] **Step 3：编写迁移**

`000130_create_pm_tables.up.sql`（类型基线对齐 `000115_meeting_room.up.sql`）：

```sql
-- ===== 项目 =====
CREATE TABLE pm_projects (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    name VARCHAR(255) NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    kb_id VARCHAR(36) NOT NULL DEFAULT '',
    session_id VARCHAR(36) NOT NULL DEFAULT '',
    status VARCHAR(16) NOT NULL DEFAULT 'active',
    owner_id VARCHAR(36) NOT NULL DEFAULT '',
    created_by VARCHAR(36) NOT NULL DEFAULT '',
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMP WITH TIME ZONE
);
CREATE INDEX idx_pm_projects_tenant_status ON pm_projects (tenant_id, status, updated_at DESC);
-- 同租户下项目名唯一（软删不计入），支撑「按名称判断项目已存在」
CREATE UNIQUE INDEX uq_pm_projects_tenant_name ON pm_projects (tenant_id, name) WHERE deleted_at IS NULL;

-- ===== 需求 =====
CREATE TABLE pm_requirements (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    project_id VARCHAR(36) NOT NULL,
    title VARCHAR(255) NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    priority VARCHAR(8) NOT NULL DEFAULT 'P1',
    status VARCHAR(32) NOT NULL DEFAULT 'pending_analysis',
    source VARCHAR(32) NOT NULL DEFAULT 'other',
    proposer VARCHAR(128) NOT NULL DEFAULT '',
    ai_analysis JSONB NOT NULL DEFAULT '{}',
    design_plan TEXT NOT NULL DEFAULT '',
    session_id VARCHAR(36) NOT NULL DEFAULT '',
    knowledge_id VARCHAR(36) NOT NULL DEFAULT '',
    actual_launch_date DATE,
    effect_notes TEXT NOT NULL DEFAULT '',
    value_achieved VARCHAR(16) NOT NULL DEFAULT '',
    retro_notes TEXT NOT NULL DEFAULT '',
    value_achieved_at TIMESTAMP WITH TIME ZONE,
    owner_id VARCHAR(36) NOT NULL DEFAULT '',
    created_by VARCHAR(36) NOT NULL DEFAULT '',
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMP WITH TIME ZONE
);
CREATE INDEX idx_pm_requirements_project_status ON pm_requirements (tenant_id, project_id, status);
CREATE INDEX idx_pm_requirements_project_updated ON pm_requirements (tenant_id, project_id, updated_at DESC);

-- ===== 版本 =====
CREATE TABLE pm_versions (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    project_id VARCHAR(36) NOT NULL,
    version_no VARCHAR(32) NOT NULL DEFAULT '',
    name VARCHAR(255) NOT NULL DEFAULT '',
    status VARCHAR(16) NOT NULL DEFAULT 'planning',
    review_round INT NOT NULL DEFAULT 0,
    review_opinions JSONB NOT NULL DEFAULT '[]',
    review_conclusion VARCHAR(16) NOT NULL DEFAULT '',
    reviewers VARCHAR(512) NOT NULL DEFAULT '',
    release_note TEXT NOT NULL DEFAULT '',
    plan_date DATE,
    plan_launch_date DATE,
    finalize_date TIMESTAMP WITH TIME ZONE,
    launch_date TIMESTAMP WITH TIME ZONE,
    owner_id VARCHAR(36) NOT NULL DEFAULT '',
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMP WITH TIME ZONE
);
CREATE INDEX idx_pm_versions_project_status ON pm_versions (tenant_id, project_id, status);
-- 版本号定稿后才生成，空串不参与唯一约束
CREATE UNIQUE INDEX uq_pm_versions_no ON pm_versions (tenant_id, project_id, version_no)
    WHERE version_no <> '' AND deleted_at IS NULL;

-- ===== 版本 ↔ 需求 =====
CREATE TABLE pm_version_requirements (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    version_id VARCHAR(36) NOT NULL,
    requirement_id VARCHAR(36) NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX uq_pm_version_requirements ON pm_version_requirements (version_id, requirement_id);
CREATE INDEX idx_pm_version_requirements_req ON pm_version_requirements (tenant_id, requirement_id);

-- ===== 功能清单（三级：模块 / 功能 / 操作）=====
CREATE TABLE pm_features (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    project_id VARCHAR(36) NOT NULL,
    requirement_id VARCHAR(36) NOT NULL DEFAULT '',
    parent_id VARCHAR(36) NOT NULL DEFAULT '',
    module_name VARCHAR(255) NOT NULL DEFAULT '',
    feature_name VARCHAR(255) NOT NULL DEFAULT '',
    operation_name VARCHAR(255) NOT NULL DEFAULT '',
    source VARCHAR(16) NOT NULL DEFAULT 'manual',
    status VARCHAR(16) NOT NULL DEFAULT 'planned',
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMP WITH TIME ZONE
);
CREATE INDEX idx_pm_features_project ON pm_features (tenant_id, project_id, parent_id);
CREATE INDEX idx_pm_features_requirement ON pm_features (tenant_id, requirement_id);

-- ===== 功能点迭代历史 =====
CREATE TABLE pm_feature_iterations (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    feature_id VARCHAR(36) NOT NULL,
    version_id VARCHAR(36) NOT NULL DEFAULT '',
    change_type VARCHAR(16) NOT NULL,
    change_note TEXT NOT NULL DEFAULT '',
    changed_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_pm_feature_iterations_feature ON pm_feature_iterations (tenant_id, feature_id);
CREATE INDEX idx_pm_feature_iterations_version ON pm_feature_iterations (tenant_id, version_id);

-- ===== 项目资料 =====
CREATE TABLE pm_materials (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    project_id VARCHAR(36) NOT NULL,
    material_type VARCHAR(16) NOT NULL,
    version_id VARCHAR(36) NOT NULL DEFAULT '',
    knowledge_id VARCHAR(36) NOT NULL DEFAULT '',
    title VARCHAR(255) NOT NULL DEFAULT '',
    status VARCHAR(16) NOT NULL DEFAULT 'draft',
    archived BOOLEAN NOT NULL DEFAULT FALSE,
    created_by VARCHAR(36) NOT NULL DEFAULT '',
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMP WITH TIME ZONE
);
CREATE INDEX idx_pm_materials_project ON pm_materials (tenant_id, project_id, material_type);

-- ===== 规划批次 =====
CREATE TABLE pm_plan_batches (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    project_id VARCHAR(36) NOT NULL,
    batch_name VARCHAR(128) NOT NULL,
    plan_type VARCHAR(16) NOT NULL DEFAULT 'initial',
    version_id VARCHAR(36) NOT NULL DEFAULT '',
    change_summary TEXT NOT NULL DEFAULT '',
    status VARCHAR(16) NOT NULL DEFAULT 'draft',
    -- 5 步引导草稿与进度（对方案 §3.7 的补充）
    draft JSONB NOT NULL DEFAULT '{}',
    current_step SMALLINT NOT NULL DEFAULT 1,
    roadmap_knowledge_id VARCHAR(36) NOT NULL DEFAULT '',
    snapshot_knowledge_id VARCHAR(36) NOT NULL DEFAULT '',
    generated_at TIMESTAMP WITH TIME ZONE,
    snapshot_at TIMESTAMP WITH TIME ZONE,
    operator_id VARCHAR(36) NOT NULL DEFAULT '',
    activated_at TIMESTAMP WITH TIME ZONE,
    created_by VARCHAR(36) NOT NULL DEFAULT '',
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMP WITH TIME ZONE
);
CREATE INDEX idx_pm_plan_batches_project ON pm_plan_batches (tenant_id, project_id, created_at DESC);
-- 同一项目同时最多一个生效批次
CREATE UNIQUE INDEX uq_pm_plan_batches_active ON pm_plan_batches (tenant_id, project_id)
    WHERE status = 'active' AND deleted_at IS NULL;

-- ===== 异步任务 =====
CREATE TABLE pm_tasks (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    project_id VARCHAR(36) NOT NULL DEFAULT '',
    task_type VARCHAR(64) NOT NULL,
    status VARCHAR(16) NOT NULL DEFAULT 'pending',
    progress SMALLINT NOT NULL DEFAULT 0,
    payload JSONB NOT NULL DEFAULT '{}',
    result JSONB NOT NULL DEFAULT '{}',
    error_message TEXT NOT NULL DEFAULT '',
    created_by VARCHAR(36) NOT NULL DEFAULT '',
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    finished_at TIMESTAMP WITH TIME ZONE
);
CREATE INDEX idx_pm_tasks_tenant_status ON pm_tasks (tenant_id, status, created_at DESC);
```

`000130_create_pm_tables.down.sql`（逆序 drop）：

```sql
DROP TABLE IF EXISTS pm_tasks;
DROP TABLE IF EXISTS pm_plan_batches;
DROP TABLE IF EXISTS pm_materials;
DROP TABLE IF EXISTS pm_feature_iterations;
DROP TABLE IF EXISTS pm_features;
DROP TABLE IF EXISTS pm_version_requirements;
DROP TABLE IF EXISTS pm_versions;
DROP TABLE IF EXISTS pm_requirements;
DROP TABLE IF EXISTS pm_projects;
```

- [x] **Step 4：确认通过**

```bash
# 校验迁移可被 golang-migrate 正确解析：启动服务后查看迁移日志，无 syntax error
go build ./internal/database/...
```

期望：退出码 0；启动服务（PostgreSQL 模式）后日志出现 `000130_create_pm_tables` 且无报错。

> 实测替代验证（本机 `CGO_ENABLED=0` 且无 gcc，`go build` 会卡在 `internal/utils` 的 `pg_query` 未定义，属既有环境问题）：在临时库 `pm_ddl_check` 中执行 up → 9 表 / 16 索引全部创建，4 个唯一索引（含 3 个部分索引）就位；执行 down → 9 表全部删除；并冒烟确认「软删后同名项目可重建」「同项目第二个 active 批次被拒」。临时库已清理。

---

### Task 2：类型层（模型 / 枚举 / 状态机）

**Files:**
- Create: `internal/types/pm.go`
- Create: `internal/types/pm_test.go`

- [x] **Step 1：先写测试**

```go
package types

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
)

// 需求状态机：合法迁移必须放行，非法迁移必须拒绝
func TestRequirementStatusTransitions(t *testing.T) {
	cases := []struct {
		from, to string
		want     bool
	}{
		{RequirementStatusPendingAnalysis, RequirementStatusAnalyzing, true},
		{RequirementStatusPendingAnalysis, RequirementStatusRejected, true},
		{RequirementStatusAnalyzing, RequirementStatusConfirmed, true},
		{RequirementStatusConfirmed, RequirementStatusScheduled, true},
		{RequirementStatusScheduled, RequirementStatusDeveloping, true},
		{RequirementStatusDeveloping, RequirementStatusLaunched, true},
		// 终止态重新激活
		{RequirementStatusRejected, RequirementStatusPendingAnalysis, true},
		{RequirementStatusClosed, RequirementStatusPendingAnalysis, true},
		// 非法：不能从待分析直接上线
		{RequirementStatusPendingAnalysis, RequirementStatusLaunched, false},
		// 非法：已上线不能回到待分析（必须经 closed）
		{RequirementStatusLaunched, RequirementStatusPendingAnalysis, false},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, CanTransitRequirement(c.from, c.to), "%s -> %s", c.from, c.to)
	}
}

// 版本状态机：定稿是分水岭，定稿后不允许回评审中
func TestVersionStatusTransitions(t *testing.T) {
	assert.True(t, CanTransitVersion(VersionStatusPlanning, VersionStatusReviewing))
	assert.True(t, CanTransitVersion(VersionStatusReviewing, VersionStatusFinalized))
	assert.True(t, CanTransitVersion(VersionStatusFinalized, VersionStatusDeveloping))
	assert.True(t, CanTransitVersion(VersionStatusDeveloping, VersionStatusLaunched))
	assert.True(t, CanTransitVersion(VersionStatusShelved, VersionStatusPlanning))
	assert.False(t, CanTransitVersion(VersionStatusFinalized, VersionStatusReviewing))
	assert.False(t, CanTransitVersion(VersionStatusLaunched, VersionStatusDeveloping))
}

// 枚举校验：非法值必须被拒绝，避免脏数据入库
func TestPMEnumValidation(t *testing.T) {
	assert.True(t, IsValidRequirementPriority("P0"))
	assert.False(t, IsValidRequirementPriority("P9"))
	assert.True(t, IsValidRequirementSource("customer"))
	assert.False(t, IsValidRequirementSource("unknown"))
	assert.True(t, IsValidVersionConclusion("conditional"))
	assert.False(t, IsValidVersionConclusion("passed"))
}

// 版本号生成：定稿时按 主.次.修订 递增
func TestNextVersionNo(t *testing.T) {
	assert.Equal(t, "1.0.0", NextVersionNo("", 0))
	assert.Equal(t, "1.1.0", NextVersionNo("1.0.0", 1))
	assert.Equal(t, "1.2.0", NextVersionNo("1.1.0", 1))
	assert.Equal(t, "1.2.1", NextVersionNo("1.2.0", 2))
}

// JSON 字段：types.JSON 必须能双向序列化并原样回读
func TestPMJSONFieldRoundTrip(t *testing.T) {
	raw := json.RawMessage(`{"value":"用户留存提升","risk":["依赖埋点"]}`)
	f := JSON(raw)
	b, err := f.MarshalJSON()
	assert.NoError(t, err)
	assert.JSONEq(t, string(raw), string(b))
}
```

- [x] **Step 2：确认失败**

```bash
go test ./internal/types/ -run 'TestRequirementStatusTransitions|TestVersionStatusTransitions|TestPMEnumValidation|TestNextVersionNo|TestPMJSONFieldRoundTrip' -count=1
```

期望：编译失败 `undefined: RequirementStatusPendingAnalysis`。

- [x] **Step 3：最小实现**

`internal/types/pm.go` 按以下结构实现（字段与 Task 1 的 DDL 一一对应，JSON 字段统一 `JSON`）：

```go
package types

import "time"

// ---- 常量：需求状态 ----
const (
	RequirementStatusPendingAnalysis = "pending_analysis"
	RequirementStatusAnalyzing       = "analyzing"
	RequirementStatusConfirmed       = "confirmed"
	RequirementStatusScheduled       = "scheduled"
	RequirementStatusDeveloping      = "developing"
	RequirementStatusLaunched        = "launched"
	RequirementStatusRejected        = "rejected"
	RequirementStatusClosed          = "closed"
)

// ---- 常量：版本状态 ----
const (
	VersionStatusPlanning   = "planning"
	VersionStatusReviewing  = "reviewing"
	VersionStatusFinalized  = "finalized"
	VersionStatusDeveloping = "developing"
	VersionStatusLaunched   = "launched"
	VersionStatusShelved    = "shelved"
)

// ---- 模型：一张表一个 struct，GORM 标签显式声明表名与列名 ----
type PMProject struct {
	ID          string     `gorm:"column:id;primaryKey" json:"id"`
	TenantID    int64      `gorm:"column:tenant_id" json:"-"`
	Name        string     `gorm:"column:name" json:"name"`
	Description string     `gorm:"column:description" json:"description"`
	KBID        string     `gorm:"column:kb_id" json:"kb_id"`
	SessionID   string     `gorm:"column:session_id" json:"session_id"`
	Status      string     `gorm:"column:status" json:"status"`
	OwnerID     string     `gorm:"column:owner_id" json:"owner_id"`
	CreatedBy   string     `gorm:"column:created_by" json:"created_by"`
	CreatedAt   time.Time  `gorm:"column:created_at" json:"created_at"`
	UpdatedAt   time.Time  `gorm:"column:updated_at" json:"updated_at"`
	DeletedAt   *time.Time `gorm:"column:deleted_at" json:"-"`
}

func (PMProject) TableName() string { return "pm_projects" }

// PMRequirement / PMVersion / PMVersionRequirement / PMFeature /
// PMFeatureIteration / PMMaterial / PMPlanBatch / PMTask 同风格定义

// 状态机：以「允许迁移集合」表达，便于单测和前端提示复用
var requirementTransitions = map[string]map[string]bool{
	RequirementStatusPendingAnalysis: {
		RequirementStatusAnalyzing: true,
		RequirementStatusConfirmed: true,
		RequirementStatusRejected:  true,
		RequirementStatusClosed:    true,
	},
	RequirementStatusAnalyzing: {
		RequirementStatusConfirmed: true,
		RequirementStatusRejected:  true,
		RequirementStatusClosed:    true,
	},
	RequirementStatusConfirmed: {
		RequirementStatusScheduled: true,
		RequirementStatusDeveloping: true, // 允许跳过排期直接开发（小需求）
		RequirementStatusRejected:  true,
		RequirementStatusClosed:    true,
	},
	RequirementStatusScheduled: {
		RequirementStatusDeveloping: true,
		RequirementStatusConfirmed:  true, // 从版本移出后回退
		RequirementStatusClosed:     true,
	},
	RequirementStatusDeveloping: {
		RequirementStatusLaunched: true,
		RequirementStatusScheduled: true,
	},
	RequirementStatusLaunched: {RequirementStatusClosed: true},
	RequirementStatusRejected: {RequirementStatusPendingAnalysis: true},
	RequirementStatusClosed:   {RequirementStatusPendingAnalysis: true},
}

func CanTransitRequirement(from, to string) bool {
	return requirementTransitions[from][to]
}

var versionTransitions = map[string]map[string]bool{
	VersionStatusPlanning:   {VersionStatusReviewing: true, VersionStatusShelved: true},
	VersionStatusReviewing:  {VersionStatusFinalized: true, VersionStatusPlanning: true, VersionStatusShelved: true},
	VersionStatusFinalized:  {VersionStatusDeveloping: true, VersionStatusShelved: true},
	VersionStatusDeveloping: {VersionStatusLaunched: true},
	VersionStatusShelved:    {VersionStatusPlanning: true},
}

func CanTransitVersion(from, to string) bool {
	return versionTransitions[from][to]
}

// NextVersionNo 生成版本号：major 不变，minor 随迭代 +1，patch 用于同轮重生成
func NextVersionNo(current string, patchStep int) string { /* 解析 X.Y.Z 后按 patchStep 递增 */ }
```

其余字段（`AIAnalysis JSON`、`ReviewOpinions JSON`、`Draft JSON`、`Payload JSON`、`Result JSON`）一律用 `types.JSON`，**不要**引入 `gorm.io/datatypes`。

- [x] **Step 4：确认通过**

```bash
gofmt -w internal/types/pm.go internal/types/pm_test.go
go test ./internal/types/ -run 'TestRequirementStatusTransitions|TestVersionStatusTransitions|TestPMEnumValidation|TestNextVersionNo|TestPMJSONFieldRoundTrip' -count=1
```

期望：`ok  .../internal/types`。

> 实测注记：`internal/types` 依赖 `internal/utils`（pg_query_go），本机无 CGO 环境，已用 `weknora-goverify:1.26` 容器（go1.26.9 + gcc，`CGO_ENABLED=1`）执行同一组测试，5 个测试全部 PASS，输出 `ok github.com/Tencent/WeKnora/internal/types`。

---

### Task 3：接口契约

**Files:**
- Create: `internal/types/interfaces/pm.go`

- [x] **Step 1：先写测试**

接口本身无运行时逻辑，用编译期断言保证实现方与接口一致。在 Task 4 的仓储测试文件顶部加入：

```go
// 编译期断言：实现必须满足接口，防止签名漂移
var _ interfaces.PMProjectRepository = (*pmProjectRepository)(nil)
```

> 说明：断言写在 Task 4 的仓储测试文件顶部，随 Task 4 一并完成。

- [x] **Step 2：确认失败**

```bash
go build ./internal/application/repository/
```

期望：编译失败 `undefined: interfaces.PMProjectRepository`。

> 说明：本步骤依赖 Task 4 的仓储实现引用该接口，故随 Task 4 一并执行；Task 3 先落地接口文件。

- [x] **Step 3：最小实现**

按聚合拆分接口（参照 [meeting.go](../internal/types/interfaces/meeting.go) 的拆分风格），方法首参一律 `ctx context.Context`：

```go
package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// PMProjectRepository 项目仓储
type PMProjectRepository interface {
	Create(ctx context.Context, p *types.PMProject) error
	GetByID(ctx context.Context, tenantID int64, id string) (*types.PMProject, error)
	GetByName(ctx context.Context, tenantID int64, name string) (*types.PMProject, error)
	List(ctx context.Context, tenantID int64, status string) ([]*types.PMProject, error)
	Update(ctx context.Context, p *types.PMProject) error
}

// 其余：PMRequirementRepository / PMVersionRepository /
// PMFeatureRepository / PMMaterialRepository /
// PMPlanBatchRepository / PMTaskRepository 同风格拆分
//
// Service 接口按「业务用例」定义，不暴露仓储细节：
// PMProjectService / PMRoadmapService / PMRequirementService /
// PMVersionService / PMFeatureService / PMMaterialService / PMTaskService
```

错误语义写在注释里：`GetByID` 查不到返回包级 `ErrXxxNotFound`；`Update` 使用乐观校验（带 `tenant_id` 条件）返回影响行数。

> 实测注记：
> - 计划片段中的 `tenantID int64` 为笔误，实际统一用 `uint64`，与 `types.PMProject.TenantID`、`types.TenantIDFromContext` 保持一致。
> - `CreatePMProjectInput` / `PMPayload` / `PMFeatureNode` 等 DTO 尚未定义，服务接口先用基础参数（name/isNew/items 等）表达用例，相应 DTO 在其归属 Task 引入后再收敛签名。
> - `PMKBDirService` 不在 Task 3 的 7 个服务接口清单内，留待 Task 6 定义。
> - 已创建 `internal/types/interfaces/pm.go`：7 个仓储接口 + 7 个服务接口，`go build ./internal/types/interfaces/` 退出码 0（本机 CGO 限制，构建在 `weknora-goverify:1.26` 容器内执行）。

- [x] **Step 4：确认通过**

```bash
go build ./internal/types/interfaces/
```

期望：退出码 0。

---

### Task 4：仓储层

**Files:**
- Create: `internal/application/repository/pm_project.go`
- Create: `internal/application/repository/pm_requirement.go`
- Create: `internal/application/repository/pm_version.go`
- Create: `internal/application/repository/pm_feature.go`
- Create: `internal/application/repository/pm_material.go`
- Create: `internal/application/repository/pm_plan_batch.go`
- Create: `internal/application/repository/pm_task.go`
- Test: 同名 `_test.go`（至少 project / plan_batch / task 三个）

- [x] **Step 1：先写测试**

> 实测注记：仓库现状并非 sqlmock，`task_queue_test.go` 等均使用 `gorm.io/driver/sqlite` 内存库 + 手写 DDL。本 Task 沿用 SQLite 内存库（DSN 唯一后缀 + 单连接），并在 AutoMigrate 后手工补齐 4 个部分唯一索引，更贴近真实约束行为。

以项目仓储为例（sqlmock 范式参照 `task_queue_test.go`）：

```go
package repository

import (
	"context"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/types"
)

// 查不到资源时必须返回包级错误，供中间件判定「放行由 handler 发 404」
func TestPMProjectGetByIDNotFound(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer sqlDB.Close()

	db, err := gorm.Open(postgres.New(postgres.Config{Conn: sqlDB}), &gorm.Config{})
	require.NoError(t, err)

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM "pm_projects" WHERE tenant_id = $1 AND id = $2 AND "pm_projects"."deleted_at" IS NULL`)).
		WithArgs(int64(1), "p-1").
		WillReturnRows(sqlmock.NewRows([]string{"id"}))

	repo := NewPMProjectRepository(db)
	_, err = repo.GetByID(context.Background(), 1, "p-1")
	assert.ErrorIs(t, err, ErrPMProjectNotFound)
}

// 同租户项目名唯一：重复创建必须透传唯一约束错误
func TestPMProjectCreateDuplicateName(t *testing.T) {
	// 期望 INSERT 返回 pq 唯一键冲突，断言 repo 将其转换为 ErrPMProjectNameTaken
}
```

规划批次仓储需额外覆盖一条：`ActivateBatch` 在同一事务内先把旧 `active` 置 `archived`，再置新批次 `active`，断言事务内两次 UPDATE 顺序正确。

- [x] **Step 2：确认失败**

```bash
go test ./internal/application/repository/ -run 'TestPMProject' -count=1
```

期望：编译失败 `undefined: NewPMProjectRepository`。

> 实测注记：测试先行后按预期编译失败，确认用例确实依赖待实现符号。

- [x] **Step 3：最小实现**

统一范式（与 [meeting.go](../internal/application/repository/meeting.go) 一致）：

```go
package repository

import (
	"errors"

	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

var (
	ErrPMProjectNotFound  = errors.New("pm project not found")
	ErrPMProjectNameTaken = errors.New("pm project name already exists")
)

type pmProjectRepository struct {
	db *gorm.DB
}

// 构造函数返回接口，便于 service 层依赖注入与替换
func NewPMProjectRepository(db *gorm.DB) interfaces.PMProjectRepository {
	return &pmProjectRepository{db: db}
}

func (r *pmProjectRepository) GetByID(ctx context.Context, tenantID int64, id string) (*types.PMProject, error) {
	var p types.PMProject
	// 双过滤：租户 + 主键，杜绝跨租户越权读取
	err := r.db.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, id).First(&p).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrPMProjectNotFound
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}
```

要点：
- 列表查询统一 `Order("updated_at DESC")`，不做无限分页（PM 数据量级小）
- 所有写入带 `tenant_id` 条件，防越权
- `pm_plan_batches` 的「同项目唯一 active」靠部分唯一索引兜底，仓储层额外在事务内先归档旧批次

- [x] **Step 4：确认通过**

```bash
gofmt -w internal/application/repository/pm_*.go
go test ./internal/application/repository/ -run 'TestPM' -count=1
go build ./internal/types/interfaces/
```

期望：`ok`；构建退出码 0。

> 实测注记：
> - 新增 `pm_common.go` 承载共用辅助：`pmUpdateByID`（强制 `tenant_id` 条件 + `Select("*")` 支持写零值 + 0 行转 notFound）、`isUniqueViolation`（兼容 PG/SQLite 唯一冲突）。
> - 7 个仓储全部落地，接口参数按实际模型统一为 `tenantID uint64`；额外导出 `ErrPMVersionNoTaken` 用于版本号并发冲突。
> - `GetLatestVersionNo` 在 Go 侧比较三段版本号，避免依赖 `split_part`，SQLite 测试可跑。
> - `ReplaceRequirements` 事务内回填 `tenant_id`、去重后批量插入；`ActivateBatch` 事务内先归档旧 active 再激活目标。
> - 容器内 `go test -run 'TestPM' -count=1`：3 个测试函数全 PASS；`go build ./internal/types/interfaces/ ./internal/application/repository/` 与 `go vet` 均退出码 0。

---

### Task 5：项目服务（动态建库 / 建会话 / 目录初始化）

**Files:**
- Create: `internal/application/service/pm_project.go`
- Test: `internal/application/service/pm_project_test.go`

- [x] **Step 1：先写测试**

用假实现（fake）注入知识库与会话服务，验证三条编排规则：

```go
// 1) is_new=true 时：建库 → 建会话 → 建项目，三者按序且 kb_id/session_id 回填
func TestCreateProjectNewCreatesKBAndSession(t *testing.T) { ... }

// 2) is_new=false 且同名项目已存在：直接返回已有项目，不得重复建库
func TestCreateProjectExistingNameReturnsExisting(t *testing.T) { ... }

// 3) 建库成功但建会话失败：必须回滚（删除已建知识库），避免产生孤儿库
func TestCreateProjectSessionFailureRollsBackKB(t *testing.T) { ... }
```

- [x] **Step 2：确认失败**

```bash
go test ./internal/application/service/ -run 'TestCreateProject' -count=1
```

期望：编译失败 `undefined: NewPMProjectService`。

- [x] **Step 3：最小实现**

```go
type pmProjectService struct {
	projectRepo   interfaces.PMProjectRepository
	kbService     interfaces.KnowledgeBaseService
	sessionSvc    interfaces.SessionService
	kbDirService  interfaces.PMKBDirService
}

// CreateProject 创建或关联项目
// is_new=true：动态建知识库 + 常驻 PM 会话；is_new=false：校验同名项目已存在并直接返回
func (s *pmProjectService) CreateProject(ctx context.Context, tenantID int64, userID string, in types.CreatePMProjectInput) (*types.PMProject, error) {
	if !in.IsNew {
		existing, err := s.projectRepo.GetByName(ctx, tenantID, in.Name)
		if err != nil {
			return nil, err // 包含 ErrPMProjectNotFound，由 handler 映射 404
		}
		return existing, nil
	}
	// 建库（名称 = 项目名）
	kb, err := s.kbService.CreateKnowledgeBase(ctx, &types.KnowledgeBase{Name: in.Name})
	if err != nil {
		return nil, err
	}
	// 建会话失败时回滚知识库，避免孤儿资源
	session, err := s.sessionSvc.CreateSession(ctx, &types.Session{Title: in.Name + " - PM 工作站"})
	if err != nil {
		_ = s.kbService.DeleteKnowledgeBase(ctx, kb.ID)
		return nil, err
	}
	// 目录初始化失败仅记录告警，不阻断项目创建（目录可后续补建）
	_ = s.kbDirService.EnsureProjectDirs(ctx, tenantID, kb.ID)
	p := &types.PMProject{ /* 赋值 kb.ID / session.ID / owner_id=userID / status=active */ }
	if err := s.projectRepo.Create(ctx, p); err != nil {
		_ = s.kbService.DeleteKnowledgeBase(ctx, kb.ID)
		return nil, err
	}
	return p, nil
}

// ArchiveProject 归档 / 恢复：归档为幂等操作，不删除任何知识库内容
func (s *pmProjectService) ArchiveProject(ctx context.Context, tenantID int64, id string, archived bool) error { ... }
```

- [x] **Step 4：确认通过**

```bash
gofmt -w internal/application/service/pm_project.go internal/application/service/pm_project_test.go
go test ./internal/application/service/ -run 'TestCreateProject' -count=1
```

期望：`ok`。

> **实现注记（已实测）**
> - 接口签名沿用 Task 3 定稿的 `CreateProject(ctx, tenantID uint64, userID, name string, isNew bool)`，计划片段里的 `int64 + CreatePMProjectInput` 为早期笔误。
> - 在 `pm_project.go` 内收窄 `pmKBService` / `pmSessionService` 两个依赖，宽接口可直接传入，测试只写 2~3 个方法。
> - `CreateProject` 先向 ctx 注入 `TenantIDContextKey`（下游知识库/会话服务从 ctx 取租户），并对 `is_new=true` 也做同名预检：已存在则直接复用，避免先建库再失败留下孤儿资源；预检仅放行 `ErrPMProjectNotFound`，其他仓储错误原样返回。
> - 建会话失败、项目落库失败均删除已建知识库；目录初始化失败只 `logger.Warnf`。
> - 新增 `types.PMProjectStatusActive/Archived`、`types.PMDir` 与 5 个目录常量（Task 6 使用）。
> - 容器内 `go test ./internal/application/service/ -run 'TestCreateProject' -count=1`：5 个测试全 PASS；`go vet` 与 `go build ./internal/application/service/ ./internal/types/...` 退出码 0。

---

### Task 6：知识库产物目录服务

**Files:**
- Create: `internal/application/service/pm_kb_dir.go`
- Test: `internal/application/service/pm_kb_dir_test.go`

- [x] **Step 1：先写测试**

```go
// 目录路径解析：逻辑目录 → 实际落库标题/前缀，降级模式必须稳定可预测
func TestPMKBDirResolve(t *testing.T) {
	svc := NewPMKBDirService(nil /* 目录能力探测 */)
	assert.Equal(t, "需求", svc.Resolve(PMDirRequirement, ""))
	assert.Equal(t, "REQ-登录优化", svc.Resolve(PMDirRequirement, "登录优化"))
	assert.Equal(t, "ROADMAP-HIS-首期规划", svc.Resolve(PMDirPlanHistory, "首期规划"))
	assert.Equal(t, "PRD-v1.2.0", svc.Resolve(PMDirPRD, "v1.2.0"))
	assert.Equal(t, "PROTO-v1.2.0", svc.Resolve(PMDirPrototype, "v1.2.0"))
	assert.Equal(t, "MAT-brochure-产品宣传册", svc.Resolve(PMDirMaterial, "brochure-产品宣传册"))
}
```

- [x] **Step 2：确认失败**

```bash
go test ./internal/application/service/ -run 'TestPMKBDirResolve' -count=1
```

期望：编译失败 `undefined: NewPMKBDirService`。

- [x] **Step 3：最小实现**

```go
// PMKBDirService 负责 PM 产物在知识库中的目录/前缀约定
// 仓库知识库若不支持多级目录，自动退化为命名前缀（方案 §12）
type PMKBDirService interface {
	EnsureProjectDirs(ctx context.Context, tenantID int64, kbID string) error
	Resolve(dir PMDir, name string) string
}
```

`Resolve` 为纯函数，无副作用，便于单测；`EnsureProjectDirs` 尽力而为，失败只记日志。

- [x] **Step 4：确认通过**

```bash
go test ./internal/application/service/ -run 'TestPMKBDirResolve' -count=1
```

期望：`ok`。

> **实现注记（已实测）**
> - `PMDir` 与 5 个目录常量、`PMKBDirService` 接口已在 Task 5 一并落到 `types` / `types/interfaces`，本 Task 只实现 `pm_kb_dir.go`。
> - 构造函数参数改为可选的 `PMDirCapability`（`EnsureDirs(ctx, tenantID, kbID, dirs)`），传 `nil` 即前缀降级模式；WeKnora 当前知识库无多级目录 API，项目创建时 `EnsureProjectDirs` 为零副作用返回。
> - `Resolve` 的目录名/前缀集中在 `pmDirMeta` 映射表，未知目录原样返回名称；名称首尾空白会被裁剪。
> - 测试覆盖计划中的 6 条断言 + 空白裁剪 + 未知目录，并补 `EnsureProjectDirs` 的 nil 能力与能力透传用例。
> - 容器内 `go test -run 'TestPMKBDir'`：2 个测试 PASS；`go vet ./internal/application/service/` 退出码 0。

---

### Task 7：功能规划服务（批次 / 草稿 / 生成合并 / 生效快照）

**Files:**
- Create: `internal/application/service/pm_roadmap.go`
- Test: `internal/application/service/pm_roadmap_test.go`

- [x] **Step 1：先写测试**

核心是**生成合并规则**，必须做成纯函数并全覆盖：

```go
// 三种规划的合并语义（方案 §6.3）
func TestMergeRoadmap(t *testing.T) {
	existing := []types.PMFeature{
		{ID: "f1", ModuleName: "账号", FeatureName: "登录", OperationName: "短信登录"},
		{ID: "f2", ModuleName: "账号", FeatureName: "登录", OperationName: "密码登录"},
	}
	draft := []types.PMFeature{
		{ModuleName: "账号", FeatureName: "登录", OperationName: "密码登录"}, // 已存在
		{ModuleName: "账号", FeatureName: "登录", OperationName: "扫码登录"}, // 新增
	}

	// 增量规划：保留既有 + 追加新增
	got, err := MergeRoadmap(existing, draft, types.PlanTypeIteration)
	assert.NoError(t, err)
	assert.Len(t, got.Added, 1)
	assert.Equal(t, "扫码登录", got.Added[0].OperationName)
	assert.Empty(t, got.Removed) // 增量不删除

	// 重新规划：以草稿为准，未出现在草稿中的既有项标记移除
	got, err = MergeRoadmap(existing, draft, types.PlanTypeReplan)
	assert.NoError(t, err)
	assert.Len(t, got.Removed, 1) // 短信登录被移除

	// 首期规划：既有必须为空，否则报错，避免误覆盖
	_, err = MergeRoadmap(existing, draft, types.PlanTypeInitial)
	assert.ErrorIs(t, err, ErrRoadmapExistingNotEmpty)
}

// 草案校验：三级结构必填、同一操作不得重复
func TestValidateRoadmapDraft(t *testing.T) {
	ok := []types.PMFeature{{ModuleName: "账号", FeatureName: "登录", OperationName: "短信登录"}}
	assert.NoError(t, ValidateRoadmapDraft(ok))

	missing := []types.PMFeature{{ModuleName: "账号", FeatureName: "", OperationName: "短信登录"}}
	assert.Error(t, ValidateRoadmapDraft(missing))

	dup := append(ok, ok[0])
	assert.ErrorIs(t, ValidateRoadmapDraft(dup), ErrRoadmapDraftDuplicate)
}

// 生效：旧 active 批次转 archived，新批次 active，并写快照指针
func TestActivateBatchArchivesPrevious(t *testing.T) { ... }
```

- [x] **Step 2：确认失败**

```bash
go test ./internal/application/service/ -run 'TestMergeRoadmap|TestValidateRoadmapDraft|TestActivateBatchArchivesPrevious' -count=1
```

期望：编译失败 `undefined: MergeRoadmap`。

- [x] **Step 3：最小实现**

```go
// SaveDraft 按步骤保存引导草稿，支持退出后续接
// draft 整体覆盖，current_step 单调不减（避免用户回看前步时进度倒退）
func (s *pmRoadmapService) SaveDraft(ctx context.Context, tenantID int64, batchID string, draft types.JSON, step int) error

// GenerateRoadmap 生成/合并路线图（同步，确定性）
// 1) 读取批次草稿 → 2) ValidateRoadmapDraft → 3) MergeRoadmap
// 4) 渲染 Markdown（含 Mermaid flowchart）→ 5) CreateKnowledgeFromPassageSync 落库
// 6) sync_features=true 时调用 PMFeatureService.SyncFromRoadmap
func (s *pmRoadmapService) GenerateRoadmap(ctx context.Context, tenantID int64, batchID string, syncFeatures bool) (*types.PMTask, error)

// ActivateBatch 确认生效：事务内归档旧批次 + 置新批次 active + 写 snapshot_knowledge_id
func (s *pmRoadmapService) ActivateBatch(ctx context.Context, tenantID int64, batchID, operatorID string) error
```

约束：
- 路线图**不提供独立编辑入口**，所有变更走引导草稿（方案 §12）
- `GenerateRoadmap` 生成的 Markdown 必须包含：项目认知、首期范围、三级功能清单表、Mermaid 流程图
- Mermaid 只使用 `flowchart TD/LR` 子集，生成后做括号配对与节点名合法性校验，失败则丢弃图示只保留表格

- [x] **Step 4：确认通过**

```bash
go test ./internal/application/service/ -run 'TestMergeRoadmap|TestValidateRoadmapDraft|TestActivateBatchArchivesPrevious' -count=1
```

期望：`ok`。

> **实现注记（已实测）**
> - `types` 补齐规划类型：`PlanBatchTypeReplan = "replan"`，并以 `PlanTypeInitial/Iteration/Replan` 作为合并语义同义别名；新增 `TypePMRoadmapGenerate` 与 `PMRoadmapDraft`（`cognition/features/flows/scope`）。
> - `MergeRoadmap` 返回 `Kept/Added/Removed` 三组：首期规划既有非空直接返回 `ErrRoadmapExistingNotEmpty`；增量规划 `Kept` 预置全部既有项；重新规划以草稿为准计算 `Removed`（仅标记、不物理删除），命中既有时沿用原 `ID`。
> - `ValidateRoadmapDraft` 校验三级名称必填与同一操作重复（`ErrRoadmapDraftInvalid` / `ErrRoadmapDraftDuplicate`），并补 `ErrRoadmapDraftEmpty`。
> - 服务依赖聚合为 `PMRoadmapDeps`（项目/批次/任务仓储、知识库写入窄接口、功能清单服务、目录服务），测试可按需注入。
> - `SaveDraft` 整体覆盖草稿且 `current_step` 单调不减；`GenerateRoadmap` 以生效批次的草稿为合并基线，主文档与批次快照分别写入知识库（`pm-roadmap` / `pm-roadmap-snapshot` channel），返回已完成的 `PMTask` 作为产物载体；功能清单同步失败只告警不回滚。
> - `ActivateBatch` 先写快照指针（缺失时回落到 `roadmap_knowledge_id`）再调用仓储事务切换状态，保证不出现无快照的生效批次；未生成路线图返回 `ErrRoadmapNotGenerated`，已 active 幂等返回。
> - Mermaid 只接受 `flowchart TD/LR`，首行非法、括号不配对或含反引号时丢弃图示只留表格。
> - 容器内 3 个测试 PASS（`TestMergeRoadmap` / `TestValidateRoadmapDraft` / `TestActivateBatchArchivesPrevious`）；`go vet` + `go build` 退出码 0。

---

### Task 8：需求服务（状态机 + AI 分析编排）

**Files:**
- Create: `internal/application/service/pm_requirement.go`
- Test: `internal/application/service/pm_requirement_test.go`

- [x] **Step 1：先写测试**

```go
// 状态变更必须过状态机，非法迁移不得落库
func TestUpdateRequirementStatusRejectsIllegal(t *testing.T) {
	repo := newFakeRequirementRepo(types.RequirementStatusPendingAnalysis)
	svc := NewPMRequirementService(repo, nil, nil, nil)
	err := svc.UpdateStatus(context.Background(), 1, "r-1", types.RequirementStatusLaunched)
	assert.ErrorIs(t, err, ErrIllegalRequirementTransition)
}

// 上线验证回填：只允许在 launched 状态执行，且回填后写 value_achieved_at
func TestLaunchVerificationRequiresLaunched(t *testing.T) { ... }

// 重新激活：仅 rejected / closed 可调用，重置为 pending_analysis 且清空 ai_analysis
func TestReactivateOnlyFromTerminal(t *testing.T) { ... }

// AI 分析编排：把路线图上下文 + 需求初稿拼成 prompt，返回 session_id 供前端 SSE 订阅
func TestStartAnalysisCreatesSessionAndSetsAnalyzing(t *testing.T) { ... }
```

- [x] **Step 2：确认失败**

```bash
go test ./internal/application/service/ -run 'TestUpdateRequirementStatusRejectsIllegal|TestLaunchVerificationRequiresLaunched|TestReactivateOnlyFromTerminal|TestStartAnalysisCreatesSessionAndSetsAnalyzing' -count=1
```

期望：编译失败 `undefined: ErrIllegalRequirementTransition`。

- [x] **Step 3：最小实现**

```go
// UpdateStatus 唯一的状态写入入口，内部统一过 CanTransitRequirement
// 版本联动也走这里：版本定稿 → scheduled，版本开发中 → developing，版本上线 → launched
func (s *pmRequirementService) UpdateStatus(ctx context.Context, tenantID int64, id, to string) error {
	r, err := s.repo.GetByID(ctx, tenantID, id)
	if err != nil { return err }
	if !types.CanTransitRequirement(r.Status, to) {
		return fmt.Errorf("%w: %s -> %s", ErrIllegalRequirementTransition, r.Status, to)
	}
	r.Status = to
	return s.repo.Update(ctx, r)
}

// StartAnalysis 触发 AI 引导分析：
// 1) 状态置 analyzing 2) 建会话 3) 拼上下文（项目路线图最新快照 + 需求初稿）
// 4) 返回 session_id，前端用现有 SSE 通道对话，结束后回写 ai_analysis/design_plan
func (s *pmRequirementService) StartAnalysis(ctx context.Context, tenantID int64, id string) (*types.PMAnalysisSession, error)
```

- [x] **Step 4：确认通过**

```bash
go test ./internal/application/service/ -run 'TestUpdateRequirementStatusRejectsIllegal|TestLaunchVerificationRequiresLaunched|TestReactivateOnlyFromTerminal|TestStartAnalysisCreatesSessionAndSetsAnalyzing' -count=1
```

期望：`ok`。

> **实现注记（已实测）**
> - 构造函数为 `NewPMRequirementService(repo, sessionSvc, batchRepo, projectRepo)`，后三者可按场景注入 `nil`；`StartAnalysis` 缺会话服务时返回 `ErrRequirementSessionUnready`。
> - `UpdateStatus` 与 `StartAnalysis` 共用内部 `transit`，所有状态写入统一过 `types.CanTransitRequirement`，非法迁移返回 `ErrIllegalRequirementTransition` 且不落库；`UpdateRequirement` 强制保留库中状态，确保状态机是唯一写入口。
> - `StartAnalysis` 幂等：已处于 `analyzing` 且有 `session_id` 时直接复用；prompt 由需求初稿 + 生效批次草稿（项目认知/首期范围/三级功能清单）与路线图快照 ID 拼装，写入会话 `Description`，返回 `session_id` 供前端 SSE 订阅。
> - `SaveAnalysis` 仅 `analyzing` 可回写，`ai_analysis` 非空时校验 JSON；`Reactivate` 仅 `rejected/closed`，重置 `pending_analysis` 并清空 `ai_analysis/design_plan/session_id`。
> - `VerifyLaunch` 仅 `launched` 可执行，校验价值达成三枚举，`note` 落 `retro_notes`，同时写 `value_achieved_at`。
> - `CreateRequirement` 固定从 `pending_analysis` 起步，优先级缺省 `P2`、来源缺省 `internal` 并做枚举校验。
> - 容器内 4 个测试 PASS；`go vet` + `go build` 退出码 0。

### Task 9：版本服务（生成 / 评审 / 定稿 / 联动）

**Files:**
- Create: `internal/application/service/pm_version.go`
- Test: `internal/application/service/pm_version_test.go`

- [x] **Step 1：先写测试**

```go
// 定稿前置校验：最近一轮评审结论必须是 pass / conditional
func TestFinalizeRequiresPassConclusion(t *testing.T) {
	// 未评审（review_round=0）→ 拒绝
	// 最近一轮 reject → 拒绝
	// conditional → 通过，版本号生成、finalize_date 写入
}

// 定稿生成版本号：已有历史版本时按 minor 递增
func TestFinalizeAssignsVersionNo(t *testing.T) {
	// 项目已有 1.0.0，新版本定稿 → 1.1.0
	// 同版本重复定稿 → 幂等，不重复递增
}

// 状态联动：版本 → 需求集合状态
func TestVersionStatusCascade(t *testing.T) {
	// finalized → 关联需求 confirmed → scheduled
	// developing → scheduled → developing
	// launched  → developing → launched（并写 requirement.actual_launch_date）
	// shelved   → scheduled → confirmed（回退，不保留排期）
}

// 评审记录：轮次 +1，意见项带处理状态，历史轮次不可改
func TestReviewAppendsRound(t *testing.T) { ... }
```

- [x] **Step 2：确认失败**

```bash
go test ./internal/application/service/ -run 'TestFinalizeRequiresPassConclusion|TestFinalizeAssignsVersionNo|TestVersionStatusCascade|TestReviewAppendsRound' -count=1
```

期望：编译失败 `undefined: NewPMVersionService`。

- [x] **Step 3：最小实现**

```go
// Finalize 定稿：校验最近一轮结论 → 生成 version_no → 状态置 finalized → 联动需求
// 必须幂等：重复调用直接返回已有结果
func (s *pmVersionService) Finalize(ctx context.Context, tenantID int64, id string) (*types.PMVersion, error) {
	v, err := s.repo.GetByID(ctx, tenantID, id)
	if err != nil { return nil, err }
	if v.Status == types.VersionStatusFinalized || v.Status == types.VersionStatusDeveloping ||
		v.Status == types.VersionStatusLaunched {
		return v, nil
	}
	if !isPassConclusion(v.ReviewConclusion) {
		return nil, ErrVersionNotApproved
	}
	latest, err := s.repo.GetLatestVersionNo(ctx, tenantID, v.ProjectID)
	if err != nil { return nil, err }
	v.VersionNo = types.NextVersionNo(latest, 1)
	v.Status = types.VersionStatusFinalized
	v.FinalizeDate = now()
	return v, s.repo.UpdateWithCascade(ctx, v, s.requirementSvc)
}

// UpdateStatus 状态变更统一入口，变更后按级联表同步关联需求
func (s *pmVersionService) UpdateStatus(ctx context.Context, tenantID int64, id, to string) error
```

级联关系（实现时以此表为准，与方案 §4.2 一致）：

| 版本状态变化 | 关联需求状态变化 |
|---|---|
| → `finalized` | `confirmed` → `scheduled` |
| → `developing` | `scheduled` → `developing` |
| → `launched` | `developing` → `launched`，并写 `actual_launch_date` |
| → `shelved` | `scheduled` / `developing` → `confirmed` |

- [x] **Step 4：确认通过**

```bash
go test ./internal/application/service/ -run 'TestFinalizeRequiresPassConclusion|TestFinalizeAssignsVersionNo|TestVersionStatusCascade|TestReviewAppendsRound' -count=1
```

期望：`ok`。

> **实现注记（已实测）**
> - `types.PMVersionReviewRecord`（`round/conclusion/opinions/reviewers/reviewed_at`）新增于 `internal/types/pm.go`，`review_opinions` 按轮次数组累积。
> - `NewPMVersionService(repo, requirementRepo)`：需求仓储可传 `nil`，无关联需求时仅做版本自身流转。
> - `Finalize` 幂等：`finalized/developing/launched` 直接返回；未评审（`review_round=0`）或结论为 `reject` → `ErrVersionNotApproved`；版本号取项目最大已定稿号后 `minor+1`。
> - `UpdateStatus(finalized)` 委托 `Finalize`，确保版本号与结论校验不被绕过；其余迁移统一过 `types.CanTransitVersion`，非法迁移返回 `ErrIllegalVersionTransition`。
> - 级联由 `requirementCascadeTargets` 映射驱动：`finalized/developing/launched/shelved` 四分支；`launched` 同时写需求 `actual_launch_date` 与版本 `launch_date`。
> - `Review` 轮次 +1，历史轮次原样保留；定稿及之后拒绝评审（`ErrVersionReviewClosed`）。
> - `UpdateVersion` 保留库中状态/版本号/评审/定稿/上线字段，状态只能经 `UpdateStatus`/`Finalize` 写入。
> - 容器内 4 个测试 PASS；`go vet` + `go build` 退出码 0。

---

### Task 10：功能清单服务（双来源 + 多对多迭代）

**Files:**
- Create: `internal/application/service/pm_feature.go`
- Test: `internal/application/service/pm_feature_test.go`

- [x] **Step 1：先写测试**

```go
// 路线图同步：按「模块/功能/操作」三级路径做 upsert，重复生成不产生重复节点
func TestSyncFromRoadmapIdempotent(t *testing.T) {
	// 第一次同步 → 新增 N 个节点
	// 第二次同样输入 → 节点数量不变，仅更新名称
}

// PRD 同步：同一功能点在新版本中被修改 → 写 iteration(change) 并更新归属版本
func TestSyncFromPRDRecordsIteration(t *testing.T) {
	// 变更 → pm_feature_iterations 新增 change 记录
	// 新增 → add；从 PRD 消失 → remove
}

// 人工补充：source=manual，可选关联版本 → 同时写 add 迭代
func TestCreateManualFeature(t *testing.T) { ... }

// 迭代历史排序：按版本 plan_date 升序，无版本记录排在最后
func TestListIterationsOrderedByVersionPlanDate(t *testing.T) { ... }
```

- [x] **Step 2：确认失败**

```bash
go test ./internal/application/service/ -run 'TestSyncFromRoadmapIdempotent|TestSyncFromPRDRecordsIteration|TestCreateManualFeature|TestListIterationsOrderedByVersionPlanDate' -count=1
```

期望：编译失败 `undefined: NewPMFeatureService`。

- [x] **Step 3：最小实现**

```go
// SyncFromRoadmap 路线图 → 功能清单（source=roadmap）
// 以三级路径为自然键去重，避免重复生成产生重复节点
func (s *pmFeatureService) SyncFromRoadmap(ctx context.Context, tenantID, projectID string, items []types.PMFeature) error

// SyncFromPRD 版本 PRD → 功能清单（source=prd）
// versionID 参与比对：同名操作归属变化时写 change 迭代
func (s *pmFeatureService) SyncFromPRD(ctx context.Context, tenantID, projectID, versionID string, items []types.PMFeature) error

// CreateManual 人工补充：可同时指定版本，写一条 add 迭代
func (s *pmFeatureService) CreateManual(ctx context.Context, tenantID int64, in types.CreatePMFeatureInput) (*types.PMFeature, error)

// ListTree 返回三级树，节点上携带最新迭代版本号与状态
func (s *pmFeatureService) ListTree(ctx context.Context, tenantID int64, projectID string) ([]*types.PMFeatureNode, error)
```

- [x] **Step 4：确认通过**

```bash
go test ./internal/application/service/ -run 'TestSyncFromRoadmapIdempotent|TestSyncFromPRDRecordsIteration|TestCreateManualFeature|TestListIterationsOrderedByVersionPlanDate' -count=1
```

期望：`ok`。

> **实现注记（已实测）**
> - 构造为 `NewPMFeatureService(repo, versionRepo)`，`versionRepo` 仅用于 `ListTree` 回填版本号/状态，可传 nil。
> - 自然键复用 `roadmapKey`（`模块\x1f功能\x1f操作`），与路线图草稿合并判重口径一致；`ensureNode` 按层级逐级 upsert，命中只校正 `ParentID`，不覆盖来源/状态。
> - 迭代判定：无历史 → `add`；最新归属其它版本 → `change`；同版本重复同步不重复记账。
> - `remove` 仅在「最新迭代属于当前版本且本次未出现」时写入，避免误删其它版本的功能点。
> - `CreateManual` 同样走 `ensureNode` 保证三级挂载；仅叶子节点继承 `RequirementID`，`VersionID` 非空时写一条 `add`。
> - 迭代排序由仓储契约保证（版本计划日升序、无版本最后），服务层透传，`ListIterations` 不做二次排序。
> - `ListTree` 按 `parent_id` 组树，父节点缺失时降级为根；版本已删除时跳过版本回填。
> - 容器内 4 个测试 PASS；`go vet` + `go build` 退出码 0。

---

### Task 11：异步任务（asynq + 进度轮询）

**Files:**
- Modify: `internal/types/task.go`（新增任务类型常量）
- Create: `internal/application/service/pm_task.go`
- Modify: `internal/router/task.go`（asynq mux 注册）
- Modify: `internal/router/sync_task.go`（同步执行器注册）
- Test: `internal/application/service/pm_task_test.go`

- [x] **Step 1：先写测试**

```go
// 任务处理：反序列化 payload → 置 running → 执行 → 置 succeeded，并写 result
func TestProcessPMTaskSucceeds(t *testing.T) { ... }

// 终态幂等：已 succeeded 的任务重复消费直接返回 nil，不重复落库
func TestProcessPMTaskIdempotentOnSucceeded(t *testing.T) { ... }

// 失败：写 error_message 与 finished_at，进度停在失败点，不抛 panic
func TestProcessPMTaskFailureRecordsError(t *testing.T) { ... }

// 进度轮询：按 task_id + tenant_id 查询，跨租户返回 NotFound
func TestGetTaskCrossTenantNotFound(t *testing.T) { ... }

// LLM 润色失败时必须降级为模板结果，任务仍 succeeded
func TestProcessPMTaskFallsBackWhenPolisherFails(t *testing.T) { ... }
```

- [x] **Step 2：确认失败**

```bash
go test ./internal/application/service/ -run 'TestProcessPMTask|TestGetTaskCrossTenantNotFound' -count=1
```

期望：编译失败 `undefined: TypePMPRDGenerate`。

- [x] **Step 3：最小实现**

在 `internal/types/task.go` 常量块追加：

```go
// ---- PM 工作站异步任务 ----
TypePMPRDGenerate      = "pm:prd-generate"       // 版本 PRD + 功能清单
TypePMPrototypeGenerate = "pm:prototype-generate" // 版本原型 HTML
TypePMMaterialGenerate  = "pm:material-generate"  // 项目资料草稿
```

`internal/application/service/pm_task.go`：

```go
// ProcessTask asynq 处理器入口（签名对齐 doc_assistant/generate.go）
func (s *pmTaskService) ProcessTask(ctx context.Context, t *asynq.Task) error {
	var payload types.PMPayload
	if err := json.Unmarshal(t.Payload(), &payload); err != nil {
		return fmt.Errorf("unmarshal pm payload: %w", err)
	}
	// 重建租户上下文：asynq worker 无 HTTP 上下文
	ctx = types.WithTenantID(ctx, payload.TenantID)

	task, err := s.taskRepo.GetByID(ctx, payload.TenantID, payload.TaskID)
	if err != nil { return err }
	// 终态幂等：避免 asynq 重试导致重复生成
	if task.Status == types.PMTaskSucceeded { return nil }

	_ = s.taskRepo.UpdateStatus(ctx, task.ID, types.PMTaskRunning, 10)
	result, genErr := s.dispatch(ctx, task)
	if genErr != nil {
		_ = s.taskRepo.Fail(ctx, task.ID, genErr.Error())
		return genErr
	}
	return s.taskRepo.Succeed(ctx, task.ID, result)
}
```

注册（两处必须都改）：

```go
// internal/router/task.go —— asynq mux
mux.HandleFunc(types.TypePMPRDGenerate, params.PMTaskService.ProcessTask)
mux.HandleFunc(types.TypePMPrototypeGenerate, params.PMTaskService.ProcessTask)
mux.HandleFunc(types.TypePMMaterialGenerate, params.PMTaskService.ProcessTask)

// internal/router/sync_task.go —— 同步执行器
params.Executor.RegisterHandler(types.TypePMPRDGenerate, params.PMTaskService.ProcessTask)
// ... 同上三条
```

任务体内策略（对应架构决策 2）：

```go
// dispatch 内部分派：模板渲染必成功，LLM 润色为可选增强
// 1) 组装结构化上下文（版本需求集合 + 路线图 + 评审意见）
// 2) RenderPRD / RenderPrototype / RenderMaterial（纯模板，无外部依赖）
// 3) 可选：Polisher.Polish(ctx, draft)；失败仅记录日志，返回模板结果
// 4) CreateKnowledgeFromManual 落库，回写 knowledge_id 到 version / material
```

- [x] **Step 4：确认通过**

```bash
gofmt -w internal/types/task.go internal/application/service/pm_task.go
go test ./internal/application/service/ -run 'TestProcessPMTask|TestGetTaskCrossTenantNotFound' -count=1
go build ./internal/router/
```

期望：`ok`；构建退出码 0。

> **实现注记（已实测）**
> - 任务类型常量在 Task 2 已落在 `internal/types/pm.go`（`TypePMPRDGenerate` / `TypePMPrototypeGenerate` / `TypePMMaterialGenerate` / `TypePMRoadmapGenerate`），本 Task 未在 `task.go` 重复声明，否则同包重定义编译失败。
> - `types.PMPayload`（`tenant_id/task_id/target_id/title/sections`）与 `types.PMPayloadSection` 定义于 `pm.go`；worker 无 HTTP 上下文，租户只能从载荷重建（`context.WithValue(TenantIDContextKey)`，仓库无 `WithTenantID` 辅助函数）。
> - `NewPMTaskService(repo, polisher)`：`PMDraftPolisher`（`Polish(ctx, draft) (string, error)`）为可选窄接口，传 `nil` 即关闭润色；`Task 12` 依赖注入时按需提供。
> - `ProcessTask` 仅对 `succeeded` 短路（重试幂等）；`failed` 允许 asynq 重试后重新执行。执行失败先 `Fail` 再返回错误，进度停在 `running=10`。
> - `renderPMDraft` 纯模板渲染（标题必填 + 小节 Markdown），三类任务类型之外返回 `ErrPMTaskTypeUnsupported`，标题为空返回 `ErrPMTaskTitleRequired`；润色失败仅 `logger.Warnf` 降级，结果中 `polished=false`。
> - 两处注册均已落地：`AsynqTaskParams` / `SyncTaskParams` 新增 `PMTaskService` 字段，`RunAsynqServer` 与 `RegisterSyncHandlers` 各注册三条。任务类型未登记 `queueDefinitions`，入队走 `default` 队列（core 池已订阅），无需改队列拓扑。
> - 实测：5 个测试全部 PASS；`go build ./internal/router/` 退出码 0。

---

### Task 12：Handler / 路由 / RBAC / 依赖注入

**Files:**
- Create: `internal/handler/pm_project.go`
- Create: `internal/handler/pm_requirement.go`
- Create: `internal/handler/pm_version.go`
- Create: `internal/handler/pm_feature.go`
- Create: `internal/handler/pm_task.go`
- Create: `internal/handler/pm_rbac_lookups.go`
- Create: `internal/router/routes_pm.go`
- Modify: `internal/router/rbac.go`
- Modify: `internal/router/router.go`
- Modify: `internal/container/container.go`
- Test: `internal/handler/pm_rbac_lookups_test.go`

- [x] **Step 1：先写测试**

```go
// 项目归属查询：项目不存在时返回 ErrResourceNotFound（放行由 handler 发 404）
func TestPMProjectCreatorLookupNotFound(t *testing.T) {
	// repo 返回 ErrPMProjectNotFound
	// 期望 err == middleware.ErrResourceNotFound，且 creatorID 为空
}

// 正常返回 created_by
func TestPMProjectCreatorLookupOK(t *testing.T) { ... }
```

- [x] **Step 2：确认失败**

```bash
go test ./internal/handler/ -run 'TestPMProjectCreatorLookup' -count=1
```

期望：编译失败 `undefined: (*PMHandler).PMProjectCreatorLookup`。

- [x] **Step 3：最小实现**

`internal/handler/pm_rbac_lookups.go`（仿 [rbac_lookups.go](../internal/handler/rbac_lookups.go#L96-L133)）：

```go
// PMProjectCreatorLookup 供 middleware.RequireOwnershipOrRole 使用
// 资源不存在时返回 ErrResourceNotFound，让中间件放行、由 handler 统一发 404
func (h *PMHandler) PMProjectCreatorLookup(c *gin.Context) (string, error) {
	id := c.Param("id")
	tenantID, ok := types.TenantIDFromContext(c.Request.Context())
	if !ok {
		return "", middleware.ErrResourceNotFound
	}
	project, err := h.projectService.GetProject(c.Request.Context(), tenantID, id)
	if err != nil {
		if errors.Is(err, repository.ErrPMProjectNotFound) {
			return "", middleware.ErrResourceNotFound
		}
		return "", err
	}
	return project.CreatedBy, nil
}

// 编译期断言：签名漂移时立即暴露
var _ middleware.CreatorLookup = (*PMHandler)(nil).PMProjectCreatorLookup
```

`internal/router/rbac.go`：

1. `rbacGuards` 结构体新增字段 `pmCreator middleware.CreatorLookup`
2. `newRBACGuards(...)` 末尾新增参数 `pmHandler *handler.PMHandler`，并赋值 `g.pmCreator = pmHandler.PMProjectCreatorLookup`
3. 新增守卫：

```go
// OwnedPMProjectOrAdmin 项目归属校验：管理员或项目创建者可访问
func (g *rbacGuards) OwnedPMProjectOrAdmin() gin.HandlerFunc {
	return middleware.RequireOwnershipOrRole(types.TenantRoleAdmin, g.pmCreator, g.cfg)
}
```

`internal/router/routes_pm.go`（仿 [routes_meeting.go](../internal/router/routes_meeting.go)）：

```go
func RegisterPMRoutes(r *gin.RouterGroup, pmHandler *handler.PMHandler, g *rbacGuards) {
	pm := g.apiKeyGroup(r.Group("/pm"), apiKeyFullAccess())
	{
		// 项目
		pm.POST("/projects", g.Contributor(), pmHandler.CreateProject)
		pm.GET("/projects", g.Viewer(), pmHandler.ListProjects)
		pm.GET("/projects/:id", g.OwnedPMProjectOrAdmin(), pmHandler.GetProject)
		pm.PATCH("/projects/:id", g.OwnedPMProjectOrAdmin(), pmHandler.UpdateProject)
		pm.POST("/projects/:id/archive", g.OwnedPMProjectOrAdmin(), pmHandler.ArchiveProject)

		// 功能规划（批次与路线图）
		pm.GET("/projects/:id/plan-batches", g.OwnedPMProjectOrAdmin(), pmHandler.ListPlanBatches)
		pm.POST("/projects/:id/plan-batches", g.OwnedPMProjectOrAdmin(), pmHandler.CreatePlanBatch)
		pm.POST("/projects/:id/roadmap/guide", g.OwnedPMProjectOrAdmin(), pmHandler.GetRoadmapGuide)
		pm.GET("/plan-batches/:id", g.Viewer(), pmHandler.GetPlanBatch)
		pm.POST("/plan-batches/:id/draft", g.Contributor(), pmHandler.SavePlanDraft)
		pm.POST("/plan-batches/:id/generate", g.Contributor(), pmHandler.GenerateRoadmap)
		pm.POST("/plan-batches/:id/activate", g.Contributor(), pmHandler.ActivatePlanBatch)
		pm.GET("/plan-batches/:id/snapshot", g.Viewer(), pmHandler.GetPlanSnapshot)

		// 需求 / 版本 / 功能清单 / 任务轮询：按方案 §8.3–8.5、§8.7 逐条注册
	}
}
```

`internal/router/router.go`：
- `RouterParams` 新增 `PMHandler *handler.PMHandler`
- `newRBACGuards(...)` 调用处末尾追加 `params.PMHandler`
- `/api/v1` 组内追加 `RegisterPMRoutes(v1, params.PMHandler, rbacGuards)`

`internal/container/container.go`：
- repository 7 个、service 7 个、handler 全部 `must(container.Provide(...))`
- 注意 handler 需聚合为一个 `PMHandler`，避免 `RouterParams` 膨胀

- [x] **Step 4：确认通过**

```bash
gofmt -w internal/handler/pm_*.go internal/router/routes_pm.go internal/router/rbac.go internal/router/router.go
go build ./internal/...
go test ./internal/handler/ -run 'TestPMProjectCreatorLookup' -count=1
```

期望：构建退出码 0；测试 `ok`。

> **实现注记（已实测）**
> - Handler 聚合为单个 `PMHandler`（`pm_project.go` 定义 + `pmClientErrors` 错误聚合 + `pmFail` 统一出口）；`pmFail` 约定：404 哨兵 → 404、业务哨兵 → 400、其余 500 并记日志。
> - PATCH 项目 / 需求 / 版本均为「先 Get 合并指针字段再 Update」；版本 `status` 单独走 `UpdateStatus`（状态机唯一写入口），最后回读返回最新数据（版本号、定稿日）。
> - `ReviewVersion` 若带 `reviewers` 会先落库再评审，使评审记录快照本轮参会人；`GET /plan-batches/:id/snapshot` 只返回快照指针，无快照返回 404 `snapshot not ready`。
> - `POST /projects/:id/roadmap/guide` 由项目常驻 `SessionID` + 批次列表聚合实现（优先 active，其次首项），未新增服务方法。
> - 计划中的「7 个服务」实际注册 6 个：`PMMaterialService` 属 Task 22，仓储已注册但服务与路由未注册，避免未使用依赖。
> - 无服务方法支撑的契约未注册（注释说明）：版本 `generate/regenerate`、功能清单 `PATCH /features/:id` 与 `POST /features/:id/iterations`、版本关联需求集合替换；`CreatePlanBatch` 的 `version_id` 仅保留契约、不落库。
> - dig 注入：`NewPMProjectService` / `NewPMRequirementService` / `NewPMTaskService` / `NewPMRoadmapService` 的构造参数含未导出接口或聚合结构体，统一在 `container.go` 用函数包装适配；`NewPMKBDirService(nil)` 与 `NewPMTaskService(repo, nil)` 表示 KBDir / 润色能力暂不启用。
> - 实测：`gofmt` 无差异；`go build ./internal/...` 退出码 0；`go test ./internal/handler/ -run 'TestPMProjectCreatorLookup' -count=1` 为 `ok`；补充 `go test ./internal/router/ ./internal/handler/ -count=1` 均 `ok`。

---

### Task 13：前端基础设施（API / 路由 / 菜单 / i18n / 布局）

**Files:**
- Create: `frontend/src/api/pm/index.ts`
- Create: `frontend/src/api/pm/pm.test.ts`
- Create: `frontend/src/views/pm/pmStatus.ts`
- Create: `frontend/src/views/pm/pmStatus.test.ts`
- Create: `frontend/src/views/pm/PmLayoutView.vue`
- Create: `frontend/src/views/pm/components/PmTaskProgress.vue`
- Modify: `frontend/src/router/index.ts`
- Modify: `frontend/src/stores/menu.ts`
- Modify: `frontend/src/i18n/locales/{zh-CN,en-US,ko-KR,ja-JP,ru-RU}.ts`

- [x] **Step 1：先写测试**

```ts
// pmStatus.test.ts：状态映射必须覆盖全部枚举值，防止新增状态漏翻译
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { REQUIREMENT_STATUS_MAP, VERSION_STATUS_MAP, REQUIREMENT_BOARD_COLUMNS } from './pmStatus';

test('需求状态映射覆盖八个状态', () => {
  const keys = ['pending_analysis','analyzing','confirmed','scheduled','developing','launched','rejected','closed'];
  for (const k of keys) assert.ok(REQUIREMENT_STATUS_MAP[k], `missing ${k}`);
});

test('看板列不包含终止态', () => {
  const cols = REQUIREMENT_BOARD_COLUMNS.map(c => c.status);
  assert.ok(!cols.includes('rejected'));
  assert.ok(!cols.includes('closed'));
});

test('版本状态映射覆盖六个状态', () => {
  const keys = ['planning','reviewing','finalized','developing','launched','shelved'];
  for (const k of keys) assert.ok(VERSION_STATUS_MAP[k], `missing ${k}`);
});
```

```ts
// pm.test.ts：URL 与查询参数构造
test('需求列表查询参数只拼接非空筛选项', () => {
  assert.equal(
    buildRequirementQuery({ status: 'confirmed', priority: '', source: 'customer' }),
    '?status=confirmed&source=customer',
  );
});
```

- [x] **Step 2：确认失败**

```bash
cd frontend
npm run test -- src/views/pm/pmStatus.test.ts
```

期望：`Cannot find module './pmStatus'`。

- [x] **Step 3：最小实现**

`frontend/src/api/pm/index.ts` 沿用 [meeting/index.ts](../frontend/src/api/meeting/index.ts) 的两条范式：
- 类型定义置于文件顶部，注释标注「与后端 `internal/types/pm.go` 对齐」
- **动态加载 request**：`async function loadRequest() { return import('../../utils/request'); }`，保证 Node 下可单测

```ts
// 任务轮询：前端统一 3s 间隔，终态即停
export async function getPmTask(taskId: string) {
  const { get } = await loadRequest();
  return get<{ data: PmTask }>(`/api/v1/pm/tasks/${taskId}`).then((r) => r.data);
}
```

`PmLayoutView.vue` 结构参照 [MeetingLayoutView.vue](../frontend/src/views/meeting/MeetingLayoutView.vue)：左侧项目切换列表（宽度固定），右侧 `<RouterView />`；无项目时展示空态引导。

路由（`frontend/src/router/index.ts`，紧随 meetings 之后）：

```ts
{
  // PM 工作站：左侧项目切换常驻，右侧为子路由出口
  path: 'pm',
  component: () => import('../views/pm/PmLayoutView.vue'),
  meta: { requiresInit: true, requiresAuth: true },
  children: [
    { path: '', name: 'pmProjectList', component: () => import('../views/pm/PmProjectListView.vue'), meta: { requiresInit: true, requiresAuth: true } },
    { path: ':projectId/roadmap', name: 'pmRoadmap', component: () => import('../views/pm/PmRoadmapView.vue'), meta: { requiresInit: true, requiresAuth: true } },
    { path: ':projectId/requirements', name: 'pmRequirements', component: () => import('../views/pm/PmRequirementBoard.vue'), meta: { requiresInit: true, requiresAuth: true } },
    { path: ':projectId/versions', name: 'pmVersions', component: () => import('../views/pm/PmVersionList.vue'), meta: { requiresInit: true, requiresAuth: true } },
    { path: ':projectId/versions/:versionId', name: 'pmVersionDetail', component: () => import('../views/pm/PmVersionDetail.vue'), meta: { requiresInit: true, requiresAuth: true } },
    { path: ':projectId/features', name: 'pmFeatures', component: () => import('../views/pm/PmFeatureTree.vue'), meta: { requiresInit: true, requiresAuth: true } },
    { path: ':projectId/materials', name: 'pmMaterials', component: () => import('../views/pm/PmMaterialList.vue'), meta: { requiresInit: true, requiresAuth: true } },
  ],
}
```

菜单：`menu.ts` 增加 `menu.pm` 项（icon 复用 TDesign 既有图标，path `/platform/pm`）。

i18n：**五个语言文件同步新增**同一批键，前缀 `pm.`（如 `pm.title`、`pm.projects.create`、`pm.requirement.status.pending_analysis`）。`npm run check-i18n` 会校验键集合一致性。

- [x] **Step 4：确认通过**

```bash
cd frontend
npm run test -- src/views/pm/pmStatus.test.ts src/api/pm/pm.test.ts
npm run type-check
npm run check-i18n
```

期望：测试全通过；类型检查退出码 0；i18n 校验通过。

> **实现注记（已实测）**
> - 语言包实为 6 个（计划只列 5 个）：`en-US`/`zh-CN`/`zh-TW`/`ko-KR`/`ja-JP`/`ru-RU`。本次 `menu.pm` 与 `pm.*` 共 27 个键已同步写入 6 个文件，键集合与 en-US 完全一致（脚本核对 `pmMissing=0`、`pmExtra=0`）。
> - `check-i18n` 仍失败，但与本任务无关：`zh-TW` 自 `d2bc0df6a` 引入后，en-US 后续新增的 `docAssistant`(125)、`meeting`(124)、`agent`(79)、`datasource`(21)、`knowledgeEditor`(6)、`menu`(2)、`knowledgeBase`(1) 共 358 个键从未回填，改动前即为失败状态。未采用 `regenerate-i18n-locales`（会裁剪 en-US 未引用键、用英文回填其他语言并整文件重排），留待专门的 i18n 补齐任务处理。
> - 计划未列出的新增文件：`views/pm/components/PmPlaceholder.vue` 与 7 个占位视图（`PmProjectListView`/`PmRoadmapView`/`PmRequirementBoard`/`PmVersionList`/`PmVersionDetail`/`PmFeatureTree`/`PmMaterialList`）。`vue-tsc --build` 会校验路由 `import()` 的模块存在性，后续 Task 逐个替换为真实页面。
> - 菜单图标复用既有 `artifact` 键（`components/menu.vue` 只映射固定 SVG，无 `pm` 图标资源）。
> - `PmLayoutView.vue`：280px 左侧项目列表 + 本地关键字过滤（不打接口）+ 项目内 5 个导航项；`pmVersionDetail` 归属「版本」高亮；无 `projectId` 时不渲染项目内导航。
> - `PmTaskProgress.vue`：3s 轮询，`succeeded`/`failed` 停止并 emit `done`/`failed`；任务 ID 变更时重置并丢弃过期响应；单次请求失败不终止，下一轮重试。
> - `pmStatus.ts` 的 `labelKey` 会被 i18n 审计计入引用键，因此 14 个状态文案必须进全部语言包。
> - 实测：`npm run test -- src/views/pm/pmStatus.test.ts src/api/pm/pm.test.ts` → 5 pass / 0 fail；`npm run type-check` 退出码 0。

---

### Task 14：项目列表与新建

**Files:**
- Create: `frontend/src/views/pm/PmProjectListView.vue`
- Create: `frontend/src/views/pm/PmProjectDialog.vue`

- [x] **Step 1：先写测试**

组件层无测试框架，测试放在纯逻辑上：项目名与知识库名的联动/校验函数抽到 `pmStatus.ts` 或同级 `pmProjectForm.ts`。

```ts
// 新建项目：名称必填、去首尾空格、长度上限 255
test('validateProjectName', () => {
  assert.equal(validateProjectName('  '), '项目名称不能为空');
  assert.equal(validateProjectName('a'.repeat(256)), '项目名称不能超过 255 个字符');
  assert.equal(validateProjectName('  智能客服  '), '');
});
```

- [x] **Step 2：确认失败**

```bash
npm run test -- src/views/pm/pmProjectForm.test.ts
```

期望：`Cannot find module './pmProjectForm'`。

- [x] **Step 3：最小实现**

- 列表：卡片网格，展示项目名、描述、状态、更新时间，操作「进入 / 归档 / 恢复」
- 新建弹窗：单选项「新建知识库（默认）」/「关联已存在知识库」，后者拉取知识库列表供选择
- 空态：引导创建第一个项目

- [x] **Step 4：确认通过**

```bash
npm run test -- src/views/pm/pmProjectForm.test.ts
npm run type-check
```

期望：通过；退出码 0。

> **实现注记（已实测）**
> - 后端 `CreateProject(ctx, tenantID, userID, name, isNew)` 只有 `name` 与 `is_new`，**没有 `kb_id` 入参**；`is_new=false` 的语义是「按名称关联已存在的同名项目」（不存在返回 404），而非「关联已选知识库」。因此弹窗未实现无契约的「关联已存在知识库」单选项，只保留「新建同名知识库」（`is_new: true`），并用 `pm.projects.kbHint` 说明会自动创建同名知识库。
> - 创建接口的 `description` 在 handler 中接收但服务层未使用（不会落库）。实现为：创建成功后若用户填了描述，再调 `PATCH /projects/:id` 补写，并用返回值回填卡片。
> - 新增 `stores/pmProjects.ts`（Pinia setup store）：`PmLayoutView` 侧边栏与列表页共用同一份 `projects`，新建后 `upsert`、归档/恢复后 `fetchProjects`，避免两侧数据不同步。计划未列出该文件。
> - `pmProjectForm.ts` 的默认文案为中文，仅服务 node 单测；组件调用时传入 i18n 文案（`pm.projects.nameEmpty` / `nameTooLong`），避免测试依赖 i18n 运行时。
> - 6 个语言包各新增 17 个键，脚本核对 `pmMissing=0`、`pmExtra=0`；`zh-TW` 仍存在 358 个历史缺失键，与本任务无关（见 Task 13 注记）。
> - 实测：`npm run test -- src/views/pm/pmProjectForm.test.ts` → 1 pass / 0 fail（实现前为 `ERR_MODULE_NOT_FOUND`）；`npm run type-check` 退出码 0。

---

### Task 15：功能规划视图（路线图 / 批次条 / 5 步引导 / 规划历史）

**Files:**
- Create: `frontend/src/views/pm/PmRoadmapView.vue`
- Create: `frontend/src/views/pm/PmRoadmapGuide.vue`
- Create: `frontend/src/views/pm/PmPlanHistoryDialog.vue`
- Test: `frontend/src/views/pm/pmRoadmapState.test.ts`

- [x] **Step 1：先写测试**

把 5 步引导的状态机抽为纯函数（步骤推进条件、草稿合并）：

```ts
// 步骤推进：当前步必填项未完成时不允许进入下一步
test('canAdvanceStep', () => {
  assert.equal(canAdvanceStep(1, { project_brief: '' }), false);
  assert.equal(canAdvanceStep(1, { project_brief: '面向 B 端的客服知识库' }), true);
  assert.equal(canAdvanceStep(5, { scope: '首期' }), false); // 第 5 步需已生成草稿
});

// 草稿合并：分步保存时只覆盖本步字段，不清空其他步
test('mergeDraft', () => {
  const base = { project_brief: 'A', scope: 'B', features: [] };
  assert.deepEqual(mergeDraft(base, { scope: 'B2' }), { project_brief: 'A', scope: 'B2', features: [] });
});
```

- [x] **Step 2：确认失败**

```bash
npm run test -- src/views/pm/pmRoadmapState.test.ts
```

期望：`Cannot find module './pmRoadmapState'`。

- [x] **Step 3：最小实现**

`PmRoadmapView.vue`：顶部批次条（当前生效批次 + 历史入口 + 「新建规划」按钮），主体为路线图 Markdown（Mermaid 渲染，失败降级为代码块），右侧可折叠三级功能清单摘要。

`PmRoadmapGuide.vue`（交互布局以原型为准）：
- 顶部 5 步步骤条
- 左侧 AI 对话区：消息滚动区 `flex: 1; overflow-y: auto`，**输入框固定在底部**，禁止出现下方空白
- 右侧草稿区：按步骤渲染字段，可编辑
- 底部操作条：单组「上一步 / 下一步 / 完成规划」，**不重复放置生成按钮**
- 每步切换与失焦时调用 `POST /plan-batches/:id/draft`

`PmPlanHistoryDialog.vue`：批次列表（名称/类型/关联版本/变更摘要/操作人/时间/状态），点击查看快照（只读 Markdown）。

- [x] **Step 4：确认通过**

```bash
npm run test -- src/views/pm/pmRoadmapState.test.ts
npm run type-check
```

期望：通过；退出码 0。

> **实现注记（已实测）**
> - 纯函数层拆到计划未列出的 `frontend/src/views/pm/pmRoadmapState.ts`（步骤推进、草稿合并、Mermaid 合法性、三级树聚合、Markdown 重建），`PmRoadmapView` 与 `PmRoadmapGuide` 共用；另新增 `frontend/src/views/pm/components/PmMarkdown.vue` 渲染 Markdown + Mermaid（失败保留源码块）。
> - 批次创建时机前移：后端无重命名/删除接口，点击「开始规划 / 新增规划 / 重新规划」先弹窗收集「批次名称 / 规划类型」，`createPlanBatch` 成功后再进入引导；第 5 步只读展示批次信息与「同步至功能清单」勾选。
> - 引导页无独立路由，作为 `position:absolute; inset:0` 覆盖层渲染在路线图视图内（侧边栏保留）；布局按原型：5 步步骤条 + 左 AI 对话（输入框贴底 `border-top`）+ 右草稿区 + 底部单组「上一步 / 下一步（完成规划）」。
> - 完成规划 = 保存草稿 → `generatePlanBatch` → `activatePlanBatch` → `emit('completed')` → 父组件重拉批次并选中 active。
> - 规划历史快照：后端 `GetPlanSnapshot` 只返回知识库指针，正文用 `buildRoadmapMarkdown(projectName, batch.draft)` 只读重建；仅 `generated_at` 非空批次可查看，其余按钮禁用 + tooltip。
> - 新建规划弹窗不放版本下拉：`CreatePlanBatch` 忽略 `version_id`（`internal/handler/pm_project.go:231-252` 注明「仅作前端契约保留」），避免静默丢弃。
> - 草稿保存：`@blur`、增删行、步骤切换触发，400ms 防抖 + 串行 Promise 队列（`saveChain`）保证最后一次写入最新草稿；卸载时若 dirty 补存一次。
> - 批次条「继续引导」可恢复 `draft` 批次的草稿与 `current_step`；「查看快照」在选中批次已生成时通过 `initialSnapshotId` 直达该批次快照。
> - 主视图布局取舍：开发计划要求「Markdown + 右侧可折叠三级清单」，原型为「清单左 + 流程图右」，实现按开发计划取 Markdown 左 + 320px 可折叠树右；引导页完全按原型布局。
> - 6 个语言包各新增 `pm.roadmap` 完整文案，脚本核对 `pmMissing=0`、`pmExtra=0`；`zh-TW` 仍有 358 个历史缺失键（与本任务无关，见 Task 13 注记），故 `localeKeyAudit.test.ts` 整体仍失败。
> - 实测：`npm run test -- src/views/pm/pmRoadmapState.test.ts` → 4 pass / 0 fail（实现前为 `Cannot find module './pmRoadmapState'`）；`npm run type-check` 退出码 0。

---

### Task 16：需求池视图

**Files:**
- Create: `frontend/src/views/pm/PmRequirementBoard.vue`
- Create: `frontend/src/views/pm/PmRequirementForm.vue`
- Create: `frontend/src/views/pm/PmRequirementAiDialog.vue`
- Create: `frontend/src/views/pm/PmRequirementDetail.vue`
- Test: `frontend/src/views/pm/pmRequirementState.test.ts`

- [x] **Step 1：先写测试**

```ts
// 看板分列：终止态不进入看板，由「已归档」筛选查看
test('groupByStatus', () => {
  const items = [
    { id: '1', status: 'pending_analysis' },
    { id: '2', status: 'rejected' },
  ];
  const cols = groupByStatus(items);
  assert.equal(cols.find(c => c.status === 'pending_analysis')!.items.length, 1);
  assert.equal(cols.find(c => c.status === 'rejected'), undefined);
});

// 两步弹窗：第 1 步未填来源不允许进入第 2 步
test('canEnterStepTwo', () => {
  assert.equal(canEnterStepTwo({ title: '登录优化', source: '' }), false);
  assert.equal(canEnterStepTwo({ title: '登录优化', source: 'customer' }), true);
});
```

- [x] **Step 2：确认失败**

```bash
npm run test -- src/views/pm/pmRequirementState.test.ts
```

期望：`Cannot find module './pmRequirementState'`。

- [x] **Step 3：最小实现**

- 列表视图：TDesign 表格，列含标题/优先级/状态/来源/提出人/价值达成/更新时间，支持筛选与排序
- 看板视图：8 列横向滚动，卡片拖拽仅做状态变更（调用 `PATCH`，非法迁移由后端拒绝并 toast 提示）
- 新建弹窗分两步：第 1 步基础信息（标题/描述/来源/提出人/优先级），第 2 步 AI 分析结果确认（可编辑设计方案）
- AI 分析弹窗：左侧对话（复用现有 SSE 组件），右侧结构化结果（价值/关联影响/风险/建议优先级/设计方案），确认后回写需求
- 详情抽屉：基础信息、AI 分析、设计方案、关联版本、上线验证区（二期开放）

- [x] **Step 4：确认通过**

```bash
npm run test -- src/views/pm/pmRequirementState.test.ts
npm run type-check
```

期望：通过；退出码 0。

> **实现注记（已实测）**
> - 纯函数层拆到计划未列出的 `frontend/src/views/pm/pmRequirementState.ts`（8 态常量、筛选/排序、看板分组、两步校验、分析结果读写与 Markdown 分段解析），供主页面与三个子组件共用；另新增共享字段组件 `frontend/src/views/pm/PmRequirementAnalysisFields.vue`，供表单第 2 步与 AI 弹窗复用，避免 6 个字段重复实现。
> - 页面严格对齐原型：工具栏「＋新建需求 | 列表视图 / 看板视图 | 右侧 5 个筛选 + 排序 + 重置」；列表 10 列（标题/优先级/状态/来源/提出人/描述/版本/价值达成/更新时间/操作）；看板 8 列横向滚动，卡片不重复展示状态，拖拽落列调用 `PATCH /requirements/:id`，非法迁移由后端拒绝后 toast `statusChangeFailed` 并重拉。
> - 契约缺口取舍：后端无需求 DELETE 路由，故不实现删除；`PmRequirement` 无 `version_id`、`PmVersion` 无需求关联，列表「所属版本」统一显示「未排期」且不提供版本筛选；已上线行操作改为「详情 / 上线验证」，开发中行为「详情 / 编辑 / AI 分析」，终止态提供「重新激活」。
> - 分析回写补齐契约：新增 `POST /api/v1/pm/requirements/:id/analysis`（仅 `analyzing` 可写，校验 `ai_analysis` / `design_plan` JSON 合法性），并在 `UpdateRequirement` 的 PATCH 中扩展 `ai_analysis` / `design_plan` 字段；新建第 2 步「完成」与 AI 弹窗确认均走该能力。
> - AI 弹窗复用 `useStream()`：只累积 `response_type === 'answer'` 的正文，流结束后按「## 1. 需求价值」「**风险**：…」等标题解析并仅覆盖非空字段；保存时若建议优先级与当前不同则追加一次 PATCH。
> - 6 个语言包各新增 `pm.requirements` 完整文案，脚本核对 `pmMissing=0`、`pmExtra=0`；`zh-TW` 仍有 358 个历史缺失键（与本任务无关，见 Task 13 注记），故 `localeKeyAudit.test.ts` 整体仍失败。
> - 实测：`npm run test -- src/views/pm/pmRequirementState.test.ts` → 2 pass / 0 fail（实现前为 `Cannot find module './pmRequirementState'`）；`npm run type-check` 退出码 0。

---

### Task 17：版本管理视图

**Files:**
- Create: `frontend/src/views/pm/PmVersionList.vue`
- Create: `frontend/src/views/pm/PmVersionDetail.vue`
- Test: `frontend/src/views/pm/pmVersionState.test.ts`

- [x] **Step 1：先写测试**

```ts
// 7 步流程的可用性判定：定稿前不可开发，未通过评审不可定稿
test('versionActions', () => {
  assert.equal(canFinalize({ status: 'reviewing', review_conclusion: 'reject' }), false);
  assert.equal(canFinalize({ status: 'reviewing', review_conclusion: 'conditional' }), true);
  assert.equal(canDevelop({ status: 'planning' }), false);
  assert.equal(canDevelop({ status: 'finalized' }), true);
});
```

- [x] **Step 2：确认失败**

```bash
npm run test -- src/views/pm/pmVersionState.test.ts
```

期望：`Cannot find module './pmVersionState'`。

- [x] **Step 3：最小实现**

- 列表：版本名/版本号/状态/计划上线日/实际上线日/需求数
- 创建：选择需求集合（仅 `confirmed` 可用）+ 计划上线日
- 详情 7 步：需求集合 → 生成（PRD/功能清单/原型）→ 评审记录 → 重生成 → 定稿 → 开发中 → 上线
- 生成中通过 `PmTaskProgress` 轮询，展示进度与结果链接（跳转知识库文档）
- 评审弹窗：轮次自动 +1、结论（通过/有条件通过/驳回）、参会人、意见项与处理状态

- [x] **Step 4：确认通过**

```bash
npm run test -- src/views/pm/pmVersionState.test.ts
npm run type-check
```

期望：通过；退出码 0。

> **实现注记（已实测）**
> - 纯函数层拆到计划未列出的 `frontend/src/views/pm/pmVersionState.ts`（版本状态机与迁移表、评审轮次/意见解析、待处理意见计数、筛选与排序、7 步操作可用性 `canFinalize` / `canDevelop` / `canLaunch` / `canShelve`、创建表单校验、状态与结论展示映射），供列表页与详情页共用。
> - 交互容器拆为两个组件避免主页面膨胀：`PmVersionDialogs.vue` 统一承载「记录意见 / 定稿 / 生成 / 重新生成 / 搁置」5 个弹窗（生成类弹窗内部渲染 `PmTaskProgress` 轮询任务），`PmVersionRequirementDialogs.vue` 承载「取消需求 / 搁置需求 / 变更需求」3 个弹窗；均以 `open(kind, target)` + `changed` 事件对外。
> - 页面严格对齐原型：列表为工具栏（新建版本 / 关键词 / 版本状态 / 创建时间）+ 9 列表格（版本号/名称/状态/需求数/规划日期/计划上线日/定稿日/上线日/操作），行操作按状态机裁剪；详情为面包屑 + 头部操作组 + 「需求集合已变动」横幅 + 三栏（版本信息 / 需求集合 / 产物）+ 评审记录 + 页脚只读说明。
> - 产物区按知识库文档落地：PRD / 原型支持「查看（跳转知识库文档）/ 下载（blob + a[download]）/ 预览（blob 新窗口，60s 后释放）」三操作，功能清单展示 `synced` 数量；未生成 / 无知识库分别给出空态文案。
> - 后端补齐版本契约：`internal/handler/pm_version.go` 扩展 CRUD 与状态迁移；`internal/application/service/pm_version.go` / `pm_version_requirements.go` / `pm_version_opinions.go` / `pm_version_artifacts.go` 分别实现版本主体、需求关联（取消/搁置/变更 + 需求状态级联）、评审意见（轮次、结论、意见项处理状态）、产物生成与重生成（PRD / 原型写入知识库 + 功能清单增量同步）；新增迁移 `000131_add_pm_version_artifacts`；路由挂在 `/api/v1/pm` 下，沿用 `OwnedPMProjectOrAdmin` / `Contributor` / `Viewer` 权限档位。
> - 评审意见下标：解析时保留后端原始 `index`，避免过滤空项后「意见N」与实际存储错位。
> - 6 个语言包各新增 `pm.version` 完整文案（约 220 个键），脚本核对 `pmMissing=0`、`pmExtra=0`；`zh-TW` 仍有 358 个历史缺失键（与本任务无关，见 Task 13 注记），故 `localeKeyAudit.test.ts` 整体仍失败。
> - 实测：`npm run test -- src/views/pm/pmVersionState.test.ts` → 1 pass / 0 fail（实现前为 `Cannot find module './pmVersionState'`）；`npm run type-check` 退出码 0。后端因本机缺少 gcc / duckdb 预编译产物无法执行 `go build`，仅以 `gofmt -e` 做语法校验并人工核对。

---

### Task 18：功能清单视图

**Files:**
- Create: `frontend/src/views/pm/PmFeatureTree.vue`
- Test: `frontend/src/views/pm/pmFeatureTree.test.ts`

- [x] **Step 1：先写测试**

```ts
// 三级树构建：模块 → 功能 → 操作，空层级自动补「未分类」
test('buildFeatureTree', () => {
  const tree = buildFeatureTree([
    { module_name: '账号', feature_name: '登录', operation_name: '短信登录', status: 'planned' },
    { module_name: '账号', feature_name: '登录', operation_name: '扫码登录', status: 'launched' },
  ]);
  assert.equal(tree.length, 1);
  assert.equal(tree[0].children[0].children.length, 2);
});
```

- [x] **Step 2：确认失败**

```bash
npm run test -- src/views/pm/pmFeatureTree.test.ts
```

期望：`Cannot find module './pmFeatureTree'`。

- [x] **Step 3：最小实现**

- 树形表格：模块/功能/操作三级可展开
- 列：名称、来源（路线图/PRD/人工）、最新迭代版本、状态
- 操作：人工补充功能点（可关联版本）、查看迭代历史抽屉（按版本计划时间升序）
- 顶部来源筛选与「仅看已上线」

- [x] **Step 4：确认通过**

```bash
npm run test -- src/views/pm/pmFeatureTree.test.ts
npm run type-check
npm run build
```

期望：测试通过；构建成功产出 `dist/`。

> **实现注记**
> - 纯函数层 `frontend/src/views/pm/pmFeatureTree.ts`：`normalizeFeatureTree` 直接归一后端 `ListTree` 的 `parent_id` 三级树（模块行/功能行/操作行均为独立记录），再做叶子计数；`filterFeatureTree` 命中子节点时保留祖先链；`summarizeFeatureTree` 统计模块/功能点/条目/已上线；`sortFeatureIterations` 按「版本计划上线日升序 → 变更时间升序」排序，无计划日置后。
> - 组件拆分为 `PmFeatureTree.vue`（树表格 + 筛选 + 统计）与 `PmFeatureDialogs.vue`（新增功能点弹窗 + 迭代历史抽屉），避免单文件过大。
> - 新增功能点一次可填多个操作项（逗号/顿号/换行分隔），前端逐条调用 `POST /api/v1/pm/features`；不填操作项时功能点自身即叶子。
> - 偏离原型 1：「功能描述」列无法落地——`PMFeature` 无 `description` 字段。实际列为：名称（树列）、来源需求、最新迭代版本、来源、状态、操作。
> - 偏离原型 2：「迭代版本」由多选改为单选——后端 `CreatePMFeatureInput` 仅支持单个 `version_id`，避免同一功能点重复建记录导致迭代历史只显示首个版本；弹窗内已加提示文案。
> - 迭代历史接口不返回版本号/版本名，前端用 `listPmVersions(projectId)` 建映射补全，并展示版本状态与计划上线日。
> - 6 个语言包新增 `pm.features` 命名空间（约 70 个键），键结构在 6 语言间完全对齐；`zh-TW` 仍有 358 个历史缺失键（与本任务无关）。
> - 实测：`npm run test -- src/views/pm/pmFeatureTree.test.ts` → 5 pass / 0 fail（实现前为 `Cannot find module './pmFeatureTree'`）；`npm run type-check` 退出码 0；`npm run build` 成功产出 `dist/`（本机首次两次构建因 less worker 超时失败，设置 `NODE_OPTIONS=--max-old-space-size=8192` 后 1m12s 通过，属环境性能抖动）。后端无需改动。

---

### Task 19：PM 技能（5 个 SKILL.md + 内嵌）

**Files:**
- Create: `cli/skills/pm-roadmap-planner/SKILL.md`
- Create: `cli/skills/pm-requirement-analysis/SKILL.md`
- Create: `cli/skills/pm-prd-generator/SKILL.md`
- Create: `cli/skills/pm-prototype/SKILL.md`
- Create: `cli/skills/pm-material/SKILL.md`
- Modify: `cli/skills/embed.go`

- [x] **Step 1：先写测试**

在 `cli/skills/embed_test.go`（若不存在则新建）断言 5 个技能可被枚举：

```go
func TestPMEmbeddedSkills(t *testing.T) {
	names := make(map[string]bool)
	for _, s := range List() {
		names[s.Name] = true
	}
	for _, want := range []string{
		"pm-roadmap-planner", "pm-requirement-analysis",
		"pm-prd-generator", "pm-prototype", "pm-material",
	} {
		assert.True(t, names[want], "missing skill %s", want)
	}
}
```

- [x] **Step 2：确认失败**

```bash
go test ./cli/skills/ -run TestPMEmbeddedSkills -count=1
```

期望：`missing skill pm-roadmap-planner`。

- [x] **Step 3：最小实现**

每个 `SKILL.md` 的 frontmatter 必须与 `/weknora-shared/SKILL.md` 一致：

```yaml
---
name: pm-roadmap-planner
description: Use when planning a product roadmap in WeKnora PM workstation — ...
metadata:
  tested_against: v0.10
---
```

正文需覆盖：触发场景、输入契约、5 步引导流程、输出结构与约束（强制 JSON / Markdown 模板）、沙箱能力边界（明确声明不得联网、单次执行不超 120s、ReAct 不超 10 轮）、失败兜底。

`cli/skills/embed.go:17` 追加：

```go
//go:embed all:weknora-shared all:weknora-rag-search all:frontend-slides
//go:embed all:pm-roadmap-planner all:pm-requirement-analysis all:pm-prd-generator
//go:embed all:pm-prototype all:pm-material
```

- [x] **Step 4：确认通过**

```bash
gofmt -w cli/skills/embed.go cli/skills/embed_test.go
go test ./cli/skills/ -run TestPMEmbeddedSkills -count=1
go build ./cli/...
```

期望：测试 `ok`；构建退出码 0。

> **实现注记**
> - `List()` 实际签名为 `([]Skill, error)`，测试中需 `require.NoError(t, err)` 后再遍历，已按实际签名实现（与本文示例的单返回值写法不同）。
> - `//go:embed` 不支持本文原先的 `\` 续行写法：`gofmt` 会把续行提到指令之前，编译报 `pattern \: invalid pattern syntax`。实际改为连续三条 `//go:embed` 指令，Go 会累加，`gofmt` 与构建均通过。
> - 5 个 `SKILL.md` 均为中文正文 + 英文 `description`（frontmatter 与 `weknora-shared` 一致），正文统一覆盖：触发场景、输入契约、流程、输出契约（JSON/Markdown 模板）、沙箱能力边界（不得联网、单次 ≤120s、ReAct ≤10 轮）、失败兜底。
> - `pm-roadmap-planner` 的草稿 JSON 键名与后端 `types.PMRoadmapDraft` 对齐（`cognition.target_users/core_value/scenarios/background`、`features[].module_name/feature_name/operation_name/source/status`、`flows[].module/mermaid`、`scope[]`）。
> - 实测（`cli/` 目录）：`go test ./skills/ -run TestPMEmbeddedSkills -count=1 -v` → 1 pass；`go build ./...` 与 `go vet ./skills/` 退出码 0。

---

### Task 20：一期端到端联调验收

**Files:**
- 无新增代码；产出验收记录（可写在 PR 描述，不落仓库文档）

- [x] **Step 1：准备验证数据**

启动 PostgreSQL + 服务，准备一个可用租户与一个可用模型。在前端 `/platform/pm` 创建项目「智能客服」，勾选「新建知识库」。

- [x] **Step 2：确认失败**

此时路线图 / 需求 / 版本页应均为空态，说明前端与后端尚未联通。逐项点击，记录每个失败点。

- [x] **Step 3：按主链路走通并修复**

依次验证：

1. 创建项目 → 知识库列表出现同名库，五类目录（或前缀）存在，`pm_projects.session_id` 非空
2. 功能规划：新建首期批次 → 5 步引导 → 保存草稿退出 → 重新进入可续接 → 生成路线图 → 知识库出现路线图文档（含三级清单 + Mermaid）→ 勾选同步功能清单 → 功能清单出现节点 → 确认生效 → 批次条显示 active，历史可查快照
3. 需求池：两步弹窗新建需求 → AI 分析（SSE 对话）→ 确认回写（`ai_analysis` / `design_plan` 非空）→ 看板拖拽改状态 → 非法拖拽被拒绝并提示
4. 版本：创建版本（选已确认需求）→ 生成 PRD + 功能清单 + 原型（异步任务进度可见）→ 记录评审（驳回）→ 定稿被拒绝 → 重生成 → 记录评审（有条件通过）→ 定稿成功（版本号形如 `1.0.0`，需求转 `scheduled`）→ 开发中（需求转 `developing`）→ 上线（需求转 `launched`，`actual_launch_date` 写入）
5. 功能清单：人工补充功能点 + 关联版本 → 迭代历史按版本计划时间升序
6. 权限：用非创建者的普通成员访问项目详情 → 403；管理员访问 → 200

- [x] **Step 4：确认通过**

```bash
go build ./internal/... 
go test ./internal/types/... ./internal/application/repository/... ./internal/application/service/... ./internal/handler/... -count=1
cd frontend; npm run type-check; npm run test; npm run check-i18n; npm run build
```

期望：后端构建退出码 0、全部测试 `ok`；前端四项全通过。上述 6 条主链路全部通过。

> **验收注记（2026-10-21）**
>
> **环境**：验证在容器 `weknora-pm-e2e`（宿主 18080 → 容器 8080）进行，数据库为 `WeKnora-postgres` 的 `WeKnora`，租户 10000。前端四项检查在宿主 `frontend/` 执行。
>
> **自动化验证**：
> - 后端 `go build ./internal/...` 退出码 0；PM 相关包测试全部 `ok`。既有失败仅 `internal/application/service/memory`（缺测试夹具，非 PM 代码引入）。
> - 前端 `type-check`、`build`、PM 定向测试通过。既有失败：全量 `npm run test` 中 skills 页面用例、`check-i18n` 的 zh-TW 缺键，均与 PM 无关。
>
> **六条主链路结果**（均以真实 HTTP 请求验证）：
> 1. 创建项目 → 同名知识库创建成功、五类目录存在、`session_id` 非空。
> 2. 功能规划 → 批次引导、草稿续接、路线图生成（文档 `chunks=1`）、功能清单同步与确认生效（批次 `active`）全部通过。
> 3. 需求池 → 新建 201（`pending_analysis`）、AI 分析返回 `session_id`、回写 `ai_analysis`/`design_plan` 成功、`confirmed` 合法、`confirmed → launched` 被拒绝（400 `illegal status transition`）。
> 4. 版本 → 生成 PRD + 原型（`round=1`）、驳回评审、未通过时定稿 400、重生成 `round=2`、有条件通过、定稿 `1.0.0`（需求转 `scheduled`）、开发中、上线（写入 `launch_date`，需求转 `launched`）。
> 5. 功能清单 → 人工补充三级节点 201、迭代历史返回 `add` 记录（`change_note=人工补充`）、项目功能树可见。
> 6. 权限（真实 JWT）→ 非创建者 viewer：`GET /projects/:id` 403、`GET /projects/:id/requirements` 403、`POST /projects/:id/requirements` 403；admin：前两个 200。测试用户 `pm-e2e-viewer@example.com`（viewer）、`pm-e2e-admin@example.com`（admin）已保留在租户 10000，密码 `PmE2e#2026`，仅用于本地验收。
>
> **联调中修复的缺陷**：
> - PM 自动建库时未写 `embedding_model_id`，导致知识库解析报 `failed to get embedding model: model ID cannot be empty`、`chunks=0`。已为 `PMProjectService` 注入 `ModelService`，建库前取租户默认 Embedding 模型（[pm_project.go](file:///d:/知识库/Know/internal/application/service/pm_project.go)），容器装配见 [container.go](file:///d:/知识库/Know/internal/container/container.go)。存量 E2E 知识库已手工回填 `embedding_model_id` 后重跑生成，新文档 `parse_status=completed`。
>
> **已知非缺陷项**：`DELETE /api/v1/pm/projects/:id` 未注册路由，请求返回 404 属预期（本期不提供删除项目）。

---

### Task 21：二期——上线验证回填

**Files:**
- Modify: `internal/application/service/pm_requirement.go`
- Modify: `internal/handler/pm_requirement.go`
- Modify: `frontend/src/views/pm/PmRequirementDetail.vue`
- Test: `internal/application/service/pm_requirement_test.go`（追加用例）

- [x] **Step 1：先写测试**

```go
// 只有 launched 状态可回填；重复回填覆盖旧值并刷新 value_achieved_at
func TestLaunchVerificationOnlyWhenLaunched(t *testing.T) {
	// status=developing → ErrRequirementNotLaunched
	// status=launched → 成功后 value_achieved/effect_notes/retro_notes 写入
}

// 回填值合法性与必填校验
func TestLaunchVerificationValidation(t *testing.T) {
	// value_achieved 必填且必须是 achieved/partial/not_achieved
	// effect_notes 长度上限 2000
}
```

- [x] **Step 2：确认失败**

```bash
go test ./internal/application/service/ -run 'TestLaunchVerification' -count=1
```

期望：`TestLaunchVerificationOnlyWhenLaunched` 失败（当前实现未校验状态）。

- [x] **Step 3：最小实现**

```go
// VerifyLaunch 上线验证回填：仅 launched 可调用，写入效果数据与复盘
func (s *pmRequirementService) VerifyLaunch(ctx context.Context, tenantID int64, id string, in types.LaunchVerificationInput) (*types.PMRequirement, error) {
	r, err := s.repo.GetByID(ctx, tenantID, id)
	if err != nil { return nil, err }
	if r.Status != types.RequirementStatusLaunched {
		return nil, ErrRequirementNotLaunched
	}
	if !types.IsValidValueAchieved(in.ValueAchieved) {
		return nil, ErrInvalidValueAchieved
	}
	r.ValueAchieved = in.ValueAchieved
	r.EffectNotes = in.EffectNotes
	r.RetroNotes = in.RetroNotes
	r.ValueAchievedAt = now()
	return r, s.repo.Update(ctx, r)
}
```

前端：详情抽屉「上线验证」区可编辑（效果数据 / 价值达成 / 复盘说明），展示回填时间；已回填时显示「已回填」标记与最近更新时间。

- [x] **Step 4：确认通过**

```bash
go test ./internal/application/service/ -run 'TestLaunchVerification' -count=1
cd frontend; npm run type-check
```

期望：`ok`；类型检查退出码 0。

> **实现注记**：
> - 后端新增 `types.LaunchVerificationInput`（`value_achieved` / `effect_notes` / `retro_notes`），`VerifyLaunch` 仅允许 `launched` 状态，重复回填覆盖旧值并刷新 `value_achieved_at`；单字段长度上限 `LaunchVerificationNoteMaxLen = 2000`，超限返回 `ErrRequirementNoteTooLong`（已纳入 handler 400 错误映射）。
> - 测试 `TestLaunchVerificationOnlyWhenLaunched` / `TestLaunchVerificationValidation` 均通过（`ok github.com/Tencent/WeKnora/internal/application/service`）。
> - 前端详情抽屉「上线验证」区提供价值达成单选 + 效果数据/复盘说明两个文本域（各 2000 字），提交后抽屉经父组件 `reload` 刷新；已回填时展示「已回填」标记、价值达成标签与最近回填时间，并分字段展示效果数据与复盘说明。
> - `npm run type-check` 退出码 0；PM 定向单测 `npx tsx --test src/api/pm/pm.test.ts src/views/pm/*.test.ts` 共 18 项全部通过。`npm run check-i18n` 仍因 zh-TW 存量缺键失败（与 PM 无关）。

---

### Task 22：三期——项目资料

**Files:**
- Create: `internal/application/service/pm_material.go`
- Create: `internal/handler/pm_material.go`
- Modify: `internal/types/task.go`（已含 `TypePMMaterialGenerate`，仅确认）
- Modify: `internal/router/routes_pm.go`（资料路由）
- Modify: `internal/container/container.go`
- Create: `frontend/src/views/pm/PmMaterialList.vue`
- Create: `frontend/src/views/pm/PmMaterialPreview.vue`
- Test: `internal/application/service/pm_material_test.go`

- [x] **Step 1：先写测试**

```go
// 发布：draft → published，旧版自动 archived（不删除，保留可追溯）
func TestPublishMaterialArchivesPrevious(t *testing.T) { ... }

// 更新提示：最新版本号 > 资料关联版本号时，列表返回 needs_update=true
func TestMaterialNeedsUpdate(t *testing.T) { ... }

// 生成走异步任务，不得同步阻塞请求
func TestGenerateReturnsTask(t *testing.T) {
	// 调用后立即返回 task_id，material.status 保持 draft
}
```

- [x] **Step 2：确认失败**

```bash
go test ./internal/application/service/ -run 'TestPublishMaterialArchivesPrevious|TestMaterialNeedsUpdate|TestGenerateReturnsTask' -count=1
```

期望：编译失败 `undefined: NewPMMaterialService`。

- [x] **Step 3：最小实现**

```go
// CreateMaterial 创建资料（类型 + 目标版本），初始 draft
// Generate 投递 asynq 任务 TypePMMaterialGenerate，返回 task_id
// Publish 发布：status=published，同类型旧 published 置 archived=true；转正式格式（md→docx/pptx）
// Update  更新到最新版本：以最新已定稿版本重生成草稿，不覆盖已发布版本
func (s *pmMaterialService) Publish(ctx context.Context, tenantID int64, id string) (*types.PMMaterial, error)
```

路由（追加到 `RegisterPMRoutes`）：

```go
pm.GET("/projects/:id/materials", g.OwnedPMProjectOrAdmin(), pmHandler.ListMaterials)
pm.POST("/materials", g.Contributor(), pmHandler.CreateMaterial)
pm.GET("/materials/:id", g.Viewer(), pmHandler.GetMaterial) // 预览页详情（计划外补充）
pm.POST("/materials/:id/generate", g.Contributor(), pmHandler.GenerateMaterial)
pm.POST("/materials/:id/publish", g.Contributor(), pmHandler.PublishMaterial)
pm.POST("/materials/:id/update", g.Contributor(), pmHandler.UpdateMaterial)
```

前端：列表按类型分组（手册/宣传册/PPT），显示关联版本、更新时间与「有新版本」提示；预览页支持发布与查看历史归档。

- [x] **Step 4：确认通过**

```bash
go test ./internal/application/service/ -run 'TestPublishMaterialArchivesPrevious|TestMaterialNeedsUpdate|TestGenerateReturnsTask' -count=1
go build ./internal/...
cd frontend; npm run type-check; npm run check-i18n
```

期望：`ok`；构建与检查全部退出码 0。

> **实现注记（已实测）**
> - 实测：3 个定向测试 PASS（`ok github.com/Tencent/WeKnora/internal/application/service`）；`go build ./internal/...` 退出码 0（本地 Windows 受 duckdb 平台绑定限制，均在 Linux 容器 `weknora-goverify:1.26` 中执行）。
> - 前端 `npm run type-check` 退出码 0；`npm run check-i18n` 仍因 zh-TW 存量缺键（`agent.eval.*`、`docAssistant.*`、`meeting.*` 等，与 PM 无关）失败，已核对失败清单中不含任何 `pm.materials.*` 键，六种语言的资料文案键完全对齐。
> - 前端按原型落地为「单表格 + 资料类型/对应版本筛选」，未做类型分组；列表只展示 `archived=false` 的当前资料，归档记录在预览页「历史归档」区按同项目 + 同类型展示（仓储 `List` 本身不过滤归档，过滤放在前端）。
> - 生成与更新到最新均返回 `PmTask`，前端统一用 `PmTaskProgress` 弹窗轮询，`done/failed` 后刷新数据；发布走 `DialogPlugin.confirm` 二次确认，服务端负责把同类型旧发布版置 `archived=true`。
> - 新建资料仅可选 `finalized / developing / launched` 版本（与后端 `ensureUsableVersion` 对齐，后端仍为最终防线）。
> - 计划外补充 `GET /api/v1/pm/materials/:id`（Viewer 权限）供预览页取详情；新增前端路由 `pmMaterialPreview`。
> - 初版遗留的下载/删除/知识库落库/格式转换已按下方「后端能力缺口修复」补齐；前端下载/删除入口已在下方「前端下载/删除入口（已实测）」中接入。

> **后端能力缺口修复（已实测）**
> - 正文落库：`pm_materials` 新增 `content` 字段（迁移 `000132_add_pm_material_content`）。生成任务成功后由 `PMMaterialContentSink.SaveGeneratedContent` 回写（仅 `draft` 且未归档；回写失败只记日志，不回滚任务）。
> - 发布落库：`Publish` 取项目 `KBID`，以 `CreateKnowledgeFromPassageSync(..., channel="pm-material")` 写入知识库，标题走 `PMKBDirService.Resolve(PMDirMaterial, title)`（`MAT-` 前缀），并回填 `knowledge_id`；正文为空或知识库未装配时返回明确错误，不做伪成功。
> - 下载：`GET /api/v1/pm/materials/:id/download`（Viewer），按类型按需导出 docx（manual/brochure）/pptx（ppt），`Content-Disposition` 附件下载，不落库。
> - 删除：`DELETE /api/v1/pm/materials/:id`（Contributor），仅 `draft` 可删；已发布/已归档返回 `ErrMaterialReadOnly`，保留可追溯。
> - 格式转换：新增 `internal/utils/pmdocs`，仅用标准库 `archive/zip` + `encoding/xml` 手写最小 OOXML（无新增第三方依赖）；`Parse` 识别标题/列表/代码围栏，PPT 按一二级标题分页、单页最多 8 块自动续页。
> - 验证：Linux 容器 `go build ./...` 退出码 0；`go test ./internal/utils/pmdocs/` 与 `go test ./internal/application/service/ -run 'PM|Material'` 全部 `ok`；`gofmt -l` 无差异。

> **前端下载/删除入口（已实测）**
> - API：`frontend/src/api/pm/index.ts` 新增 `PmMaterial.content?`、`downloadPmMaterial(id)`（复用 `getDown` 取 Blob）、`deletePmMaterial(id)`。
> - 纯函数：`pmMaterialState.ts` 新增 `canDownload`（正文非空，归档记录也可下载追溯）、`canDelete`（未归档草稿）、`materialFileName`（`ppt` → `.pptx`，其余 `.docx`，剔除文件系统非法字符）。
> - 落盘：抽取 `frontend/src/utils/downloadFile.ts#downloadBlob`，避免两个页面重复实现 object URL 逻辑。
> - 入口：列表页操作列顺序为「预览 / 下载 / 生成草稿 / 发布 / 删除 / 有新版可用 / 更新到最新版本」，操作列宽度 340 → 420；预览页操作区同样加入下载与删除，删除成功后 `goBack()` 返回列表。删除统一走 `DialogPlugin.confirm`（`theme: 'danger'`）。
> - i18n：六语言补齐 `pm.materials.actions.{download,delete}` 与 `downloadFailed / deleteConfirmTitle / deleteConfirmBody / deleteSuccess / deleteFailed`。
> - 验证：`npx tsx --test src/views/pm/pmMaterialState.test.ts` 3 项 PASS；PM 全部 7 个状态单测 19 项 PASS；`npm run type-check` 退出码 0；`npm run check-i18n` 仍仅报 zh-TW 存量缺键，失败清单不含 `pm.materials.*`。

---

## 7. 里程碑与验收

对应方案 §10：

| 里程碑 | 覆盖任务 | 验收口径 |
|---|---|---|
| **M1 一期：主链路闭环** | Task 1–20 | 项目 → 路线图 → 需求 → 版本 → 功能清单 全链路可在 UI 走通；权限隔离生效；五项验证基线全绿 |
| **M2 二期：上线验证** | Task 21 | 仅 `launched` 需求可回填；回填后详情与列表正确展示价值达成 |
| **M3 三期：项目资料** | Task 22 | 手册/宣传册/PPT 可生成草稿、发布、按最新版本更新；旧版归档可追溯 |

每个里程碑结束执行一次：

```bash
go build ./internal/...
go test ./internal/... -count=1
cd frontend; npm run type-check; npm run test; npm run check-i18n; npm run build
```

> **M3 验证结果（已实测）**
> - 后端：`go build ./internal/...` 退出码 0；`go test ./internal/... -count=1` 退出码 1，唯一 FAIL 为 `internal/application/service/memory`（缺测试夹具，Task 20 已记录为既有基线，与 PM 无关），`internal/types`、`internal/application/repository`、`internal/handler` 等 PM 相关包全部 `ok`。命令在 Linux 容器 `weknora-goverify:1.26` 执行（本地 Windows 受 duckdb 平台绑定限制）。
> - 前端：`npm run type-check` 退出码 0；PM 定向单测 `npx tsx --test src/api/pm/pm.test.ts src/views/pm/*.test.ts` 18 项全部 pass；`npm run build` 退出码 0（`✓ built in 1m 32s`，仅 chunk 体积告警）。
> - `npm run check-i18n` 仍退出码 1：失败清单全部为 zh-TW 存量缺键（`agent.eval.*`、`docAssistant.*`、`meeting.*`、`knowledgeEditor.wikiBrowser.*`、`datasource.topicSearch.*` 等），已过滤确认不含任何 `pm.*` 键。
> - 已知遗留：初版记录的资料生成结果未落库（`knowledge_id` 恒为空）、无下载/删除端点、md→docx/pptx 未接入三项缺口已全部修复（见 Task 22「后端能力缺口修复」）；前端下载/删除入口尚未接入。

---

## 8. 风险与应对

沿用方案 §12，并补充本计划已落实的具体手段：

| 风险 | 本计划中的应对 | 落点 |
|---|---|---|
| 沙箱 120s 超时 / ReAct 10 轮上限 | 生成类任务全部走 asynq，任务体内模板渲染必成功，LLM 仅润色 | Task 11 |
| 技能输出格式漂移 | 引导分析强制 JSON；后端对草稿做 `ValidateRoadmapDraft` 结构校验后才合并 | Task 7 / Task 19 |
| 资料被 AI 静默覆盖 | `draft` → 人工确认 → `published`，旧版 `archived`，无覆盖路径 | Task 22 |
| 路线图无快照被直接改写 | 不提供独立编辑入口；每次生成写批次快照；历史批次只读 | Task 7 |
| Mermaid 渲染兼容性 | 只生成 `flowchart TD/LR` 子集，服务端做语法校验，失败降级为表格 | Task 7 / Task 15 |
| 知识库目录层级受限 | `PMKBDirService.Resolve` 统一降级为命名前缀，业务层不感知 | Task 6 |
| 技能安装网络受限 | 技能随 `//go:embed` 内嵌，不依赖运行时下载 | Task 19 |
| 并发生效两个批次 | 数据库部分唯一索引 + 事务内先归档旧批次 | Task 1 / Task 7 |
| 生成任务重复消费 | 任务终态幂等（`succeeded` 直接返回） | Task 11 |
| 跨租户越权 | 仓储所有查询带 `tenant_id`；路由统一 `OwnedPMProjectOrAdmin` | Task 4 / Task 12 |

---

## 9. 交付检查清单

- [x] 9 张表迁移成对存在，`down` 可完整回滚
  - 证据：`migrations/versioned/000130_create_pm_tables.up.sql` 含 9 条 `CREATE TABLE`，`000130_create_pm_tables.down.sql` 成对 `DROP TABLE`。
- [x] 全部枚举英文入库，前端映射覆盖完整（单测保障）
  - 证据：`internal/types/pm_test.go` 枚举值断言；`frontend/src/api/pm/pm.test.ts`、`frontend/src/views/pm/*.test.ts` 覆盖类型/状态映射。
- [x] 需求与版本状态机的非法迁移均被拒绝且有单测
  - 证据：`TestUpdateRequirementStatusRejectsIllegal`、`TestFinalizeRequiresPassConclusion`、`TestVersionStatusCascade`、`TestReactivateOnlyFromTerminal`。
- [x] 路线图三种规划模式的合并语义有单测，首期规划不得覆盖既有
  - 证据：`TestMergeRoadmap`（`internal/application/service/pm_roadmap_test.go`）。
- [x] 版本定稿幂等，版本号不重复递增
  - 证据：`Finalize` 对已定稿版本直接返回已有结果；`TestFinalizeAssignsVersionNo` 内含重复定稿断言（编号不变、不重复触发）；仓储唯一键 `ErrPMVersionNoTaken` 兜底。
- [x] asynq 任务在两个注册点均已注册
  - 证据：`internal/router/task.go`（Redis/asynq）与 `internal/router/sync_task.go`（Lite）均注册 `types.TypePMMaterialGenerate`；`TypePMPRDGenerate` / `TypePMPrototypeGenerate` 同点位。
- [x] `OwnedPMProjectOrAdmin` 生效：非创建者普通成员 403，管理员放行
  - 证据：`routes_pm.go` 项目级路由统一挂载该守卫；守卫复用 `middleware.RequireOwnershipOrRole`，已有 `TestRequireOwnershipOrRole_AdminBypassesLookup` / `_CreatorAllowed` / `_NonCreatorContributorRejected`。
- [x] 所有 PM 接口跨租户访问返回 404/403，不泄露存在性
  - 证据：PM 仓储查询全部带 `tenant_id`（`pm_common.go`、`pm_project.go`、`pm_requirement.go`、`pm_material.go` 等）；`TestGetTaskCrossTenantNotFound`、`pm_project_test.go` 跨租户 `GetByID` / `Update` 均返 `ErrPMProjectNotFound`。
- [x] 前端六个语言文件键集合一致（`npm run check-i18n`）
  - 说明：实际为 zh-CN / zh-TW / en-US / ja-JP / ko-KR / ru-RU 六个语言文件。PM 新增键已在六语言对齐并逐键核对；`npm run check-i18n` 仍因 zh-TW 存量缺键（非 PM）失败，失败清单过滤后不含任何 `pm.*` 键。
- [x] AI 引导页输入框贴底、底部操作条不重复（对照原型）
  - 证据：`PmRequirementAiDialog.vue` 为 `flex` 纵向布局，消息区 `flex:1 + overflow-y:auto`，`.ai-chat__input` 设 `flex-shrink:0` 贴底；底部仅一组 `.ai-foot` 操作条。`PmRoadmapGuide.vue` 底部仅一组 `.pg-foot`。
- [x] 5 个技能可被 `cli/skills.List()` 枚举
  - 证据：`cli/skills/embed_test.go` 断言包含 `pm-roadmap-planner`、`pm-requirement-analysis`、`pm-prd-generator`、`pm-prototype`、`pm-material`。
- [x] 所有新增 Go 文件通过 `gofmt`，关键逻辑有中文注释
  - 证据：`gofmt -l` 遍历 `internal/**/pm*.go` 无输出（本次顺带修正了 `internal/types/pm.go` 的结构体字段对齐）。
- [x] 未引入新依赖（JSON 字段统一 `types.JSON`）
  - 证据：`git status --short go.mod go.sum frontend/package.json frontend/package-lock.json` 无输出；PM 模型 JSON 字段统一使用 `types.JSON`。

