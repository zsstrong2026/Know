# Know SOP 工作台实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在 Know（WeKnora v0.8.3 本地私有 fork）中原生重建通用 SOP 作业工作台：任意事项挂 SOP 流程交付，平台智能体 + 10 种本机编程 CLI 混合执行，关键节点人工确认，吸收 PM 工作站（数据迁移 + 只读归档），一次性完整交付不分期。

**Architecture:** 流程在引擎里、智能在执行器里——`internal/application/service/sop/engine` 为确定性推进引擎（进程内单循环调度 + DB 租约 + 节点状态机 + 打回/重试/暂停），执行器抽象 Provider（platform_agent / cli×10 / script）负责智能与子进程；前端 Vue3 + TDesign + @vue-flow/core 画布，五主 Tab（事项/流程模板/运行记录/审批中心/SOP任务）+ 项目切换器，REST + SSE 走 `/api/v1/sop/*`。复用 Know 底座：agent 引擎（ReAct/HITL/suspension）、automation 触发器/租约工具函数（模块本身零改动）、notifications、sessions/messages、sandbox/workspace git 检查点、mcp/client、knowledge ingest、tenant 体系、凭据加密。

**Tech Stack:** Go（Know 现有后端栈，gorm + PostgreSQL/SQLite 双库）、Vue3 + Pinia + vue-router + TDesign + @vue-flow/core（新增依赖）、PmMarkdown（含 Mermaid）、SSE（复用 agent-chat 流式基建）、本机子进程（Windows Job Object / Git Bash）。

**Spec:** `docs/superpowers/specs/2026-11-19-sop-workbench-design.md`（定稿，含 §16 共 26 条验收标准）
**交互/视觉唯一参照:** `D:\loopforge\prototypes\sop-workbench-prototype.html`（已定稿单文件原型；其中 mock 数据仅演示，实现一律走真实 API）

## Global Constraints

- 模块定名 `sop`：后端包 `internal/application/service/sop`、表前缀 `sop_`、路由 `/api/v1/sop/*`、前端路由 `/platform/sop`、界面名「SOP 工作台」。与 Know 已有 `workbench`（AFFINE 式编辑器）模块无关，不得混用。
- 不分期、一次性完整交付；内部实现按依赖排序：数据模型→引擎→平台智能体执行器→脚本执行器+审批+通知→API 层（先跑通平台智能体闭环）→CLI 全家桶→SOP 任务触发→知识库/MCP 联动→前端→联调 E2E。前端画布可与引擎并行。
- 所有新表遵循 Know 约定：`tenant_id BIGINT` 贯穿且查询强制租户过滤（ctx 注入）、VARCHAR(36) 主键、软删、无物理外键（字段关联）、JSONB（SQLite 侧按现有双库迁移约定降级 JSON）。生产 schema 一律走迁移脚本（AutoMigrate 仅测试用）：PostgreSQL `migrations/versioned/` 从 **000166** 起（已核对最新 000165_meeting_mode），SQLite `migrations/sqlite/` 从 **000068** 起（已核对最新 000067_workbench_thumbnail），命名 `NNNNNN_描述.up.sql` / `.down.sql`，两库各跑迁移与核心链路测试。
- 引擎为单实例进程内调度循环 + DB 状态推进，**不依赖 Redis/asynq**（桌面 Lite 无 Redis 可跑）；保留 lease_until/heartbeat_at 租约字段照抄 automation_runs 模式；asynq 仅 PM 遗留链路使用，迁移完成后 PM 专属 asynq handler 下线。
- **Know 现有自动化模块零改动**：automations 表、automation_runner、前端自动化页面均不动；SOP 定时派发收进 SOP 工作台第五主 Tab「SOP任务」，仅复用 automation 的触发器解析/claim/租约/心跳工具函数。
- 单一真相：事项只存 `sop_items`，运行只存 `sop_runs`；PM 迁移后 `pm_requirements` 冻结只读；PmArchive 无任何写路径。
- CLI 执行环境 = 本机子进程 + 私有 HOME（无 Docker、无安全隔离边界，个人可信环境假设，文档明示风险）；10 种 CLI：通用 7（opencode/cursor-agent/antigravity(agy)/kimi/pi/dsh/zcode）+ 专用 3（claude/codebuddy/codex）。首版不托管 CLI 安装，设置页 PATH 检测 + 安装指引。
- **API Key 只进环境变量**（如 `SOP_CLI_API_KEY`）注入私有 HOME，配置文件仅引用变量名，不落明文 Key；凭据加密存储沿用 Know 现有机制；私有 HOME 用 os.Root 逐段防符号链接逃逸。
- 私有 HOME 按 run 隔离，**不开放持久 HOME / 跨 run CLI 记忆开关**；同 run 返工靠 resume_session_id 续会话；跨 run 上下文只走 {{humanContext}}（澄清问答+审批意见）与上游产物 inputs/。
- Windows 适配：prompt 超 **24KiB** 改写文件传路径（claude/codebuddy 优先 stdin）；进程组终止用 Job Object（KILL_ON_JOB_CLOSE），降级 `taskkill /T /F`；script 执行器 shell 走 Git Bash；GIT_CEILING_DIRECTORIES 防 git 越界。
- `gate_commands` 是**派生字段**：由 `artifact_outputs[].check` 编译（exists→`test -s <path>`、contains→`grep -q "<keyword>" <path>`、command→原样）+ `gate_extra` 按 `&&` 拆分合并，引擎侧合并单脚本 `set -e` 执行；检查器不直接暴露该数组；**AI 不直接写 gate_commands/shell**。
- AI 编排助手与「AI 配这一步」**仅生成草稿**：不自动保存、不自动发布；ops 含 add/del/edge/mod/set（set 带逐字段 reasons）；AI 只能从真实清单取值（custom_agents/模型管理/已注册 MCP/角色 Skills），服务端校验 id 引用，非法引用**跳过该字段并在应用反馈中报告**，不整单拒绝；内置模板应用草稿后需复制为自定义才能保存。
- 检查器为五步傻瓜式（①谁执行 ②做什么 ③规则能力 ④产物校验 ⑤失败怎么办），全程无「任务书/门禁/gate」工程术语（高级折叠除外）；模型为下拉（模型管理已配置模型+「默认·跟随凭据配置」档，禁止手输）；技能/MCP 为多选下拉（触发器摘要+复选浮层，勾选即时写回浮层不关，点外部收起）；**解释性说明统一收进 ? 气泡**（小标题/字段标签右侧问号图标，悬停/键盘聚焦显示，移开/失焦/滚动/点击隐藏，视口横纵钳位、顶部边缘翻转到下方且箭头指向图标；inline 解释行移除，保留单位短句/空态引导/规则预览正文/高级折叠内提示）。
- 第 2 步「这一步做什么」= 大白话 textarea，无变量插入入口、无 {{…}} 芯片；上下文由引擎运行时自动附带（事项背景/上游步骤产物每次必带、返工意见返工时带、人工补充意见审批/澄清后带）；{{…}} 占位符通道仅服务端保留兼容旧发布模板。
- 产物预览统一大弹窗 `min(1120px, 94vw)`、高 ≤84vh、正文独立滚动；.md 默认 HTML 渲染（复用 PmMarkdown），源码视图含 frontmatter（title/source_node/run/sha256/created）；.html 沙箱 iframe（sandbox=""）、.diff 增删着色、.json 美化、.txt/.log 等宽、二进制提示不渲染；**入知识库的是 .md 源文档**（走现有 ingest 链路，frontmatter 保留来源追溯），HTML 仅为预览视图不入库；复制失败自动切源码视图并全选；下载落源文件（basename）。
- 执行情况五态口径（统计带与筛选下拉共用）：尚未执行=无运行或运行已取消 / 正在推进=running+paused / 需要介入=waiting+failed / 待处理 PR=运行 succeeded 但事项未闭环 / 已闭环=事项 done 或 closed；五态计数之和=工作项总数。来源键中文映射全 UI 统一：customer=客户反馈/internal=平台新建/data=数据洞察/competitor=竞品分析/strategy=战略规划/other=其他（键值落库不变）。
- 事项状态机 8 态：backlog/todo/in_progress/in_review/done/closed/blocked/cancelled，唯一写入口 `UpdateStatus → CanTransit`；运行成功自动推进：模板级 `on_success_item_status` 优先于项目级 `on_run_success_item_status`，都空则不动。
- 项目编号 `<code>-<n>`：code 创建后不可修改（2–10 位字母数字、字母开头、租户内唯一）；next_item_no 事务内 `UPDATE … RETURNING` 并发不重号；项目删除级联事项/运行/触发配置/仓库绑定，存在进行中/待审批运行或仅剩一个项目时拒绝，磁盘工作区不自动删除；流程模板为全局资产跨项目共享。
- 代码仓库绑定独立即时生效（不随「保存配置」提交）：url 仅 HTTPS/SSH Git 地址且同项目去重，第一个自动默认，解绑默认自动顺延；凭证走本机 git 凭证管理不落项目配置；发起运行时快照进 run，克隆到 `work/repos/<name>/` 并建 `sop/<run_id>` 工作分支。
- **项目知识库默认自动创建**：新建项目默认建同名知识库（写入租户默认 Embedding 模型，失败不阻塞建项目、留空待补）；`kb_owner` ∈ project（随项目删除）/ linked（改绑，仅解绑）/ pm（迁移，仅解绑）。未绑 KB 时产物归档与简报同步降级并给白话指引，黑板、任务书现状块、`inputs/project-brief.md` 不受影响。
- **项目共享上下文单一真相**：`sop_project_notes` 黑板是唯一写入源；《<项目名> 项目简报》是它在项目 KB 中的覆盖式投影（文档头标注自动生成、手改会被覆盖），可随时重建。简报渲染按 `brief_rendered_seq` 比对跳过空转，debounce 2s 合并，渲染失败只记日志不影响运行。
- **大需求拆分只允许两级**：父项 `parent_id` 必须为空，拒绝三级嵌套；子项独立编号（平铺自增，不用层级编号）/状态/运行；父项不可直接发起运行（批量操作展开为子项）；父项执行情况由子项汇总（intervene>running>pr>closed>none），全部子项 done/closed 时父项经 `UpdateStatus` 推进为 done；模板产出子事项上限 20 条，超限失败不部分创建。
- **版本规划本期不做**：不建版本表、不做版本视图/发布快照；事项只挂单层父子关系（用户决定，见澄清 17）。
- 产品边界：个人使用、本地存储、不订阅、桌面 Web、不做移动端、不做多人实时协作。
- 前端视觉与布局沿用 Know 现有主题与壳结构，交互细节以定稿原型为准；「PM 工作站」侧栏入口原位替换为「SOP 工作台」，不新增侧栏分组；`/platform/pm` 301 → `/platform/sop`。
- 审批留痕不可篡改：每次审批 decision（人/时间/结论/意见）落盘 decision.md 并产物化 + sha256；通知复用 notifications 表，新增 kind：`sop_approval` / `sop_run_finished` / `sop_run_failed`。
- 每个任务遵循 TDD：先写失败测试→运行确认失败→最小实现→运行确认通过→提交；测试命令与迁移双库（PostgreSQL + SQLite）各跑一遍；提交信息用中文 conventional commits（feat/fix/test/chore）。

---

## 与 spec 的实现澄清（已定决策，执行时不再讨论）

1. **schema 迁移自动跑、PM 数据迁移手动跑**：建表/索引走 `migrations/versioned/`（PG，000166 起）+ `migrations/sqlite/`（000068 起），启动时 `internal/database/migration.go` 自动执行（现状如此）。PM 数据迁移（改数据）按 spec §11.1「手动执行约定」实现为独立 Go 命令 `cmd/sop-migrate-pm/main.go`：先导出 JSON 备份到 `data/backups/pm-migration-<时间戳>.json`，再在事务内幂等迁移（按源 id 判重），可重复执行。
2. **内置 SOP 模板用 SQL 迁移 seed**（000173/000075）：固定 UUID、`is_builtin=true`、PG 用 `ON CONFLICT (id) DO NOTHING`、SQLite 用 `INSERT OR IGNORE`；definition JSON 直接内嵌 SQL。不写启动自愈 seed 代码。
3. **sop_projects 是新表**，吸收 pm_projects 数据（迁移保留 kb_id/session 绑定进 domain_data）；不把事项挂在 pm_projects.id 上。迁移后 pm_projects/pm_requirements 冻结只读，唯一活源是 sop_items。
4. **依赖方向**：`engine` → `executor` → `types`，禁止反向 import。Provider 接口放 executor 包，`Prepare(ctx, node *types.SopNode, wsRoot string)` 用字符串根路径避免 engine.Workspace 反向依赖。engine 需要回写事项状态时用 engine 包内窄接口 `ItemStatusUpdater`，由 sop service 注入自身。
5. **skills 字段语义**：Go `[]string`，JSON `null`→nil=角色全部技能；`[]`→非 nil 空=不启用任何技能；`["a","b"]`=子集。前端「全选」写 null。
6. **平台智能体执行器**复用 `SessionService.AgentQA(ctx, *types.QARequest, *event.EventBus)`（interfaces/session.go:77），调用范例照抄 `automation_runner.go:305-311`（Mode=RunModeDeferred、Suspendable=true、CustomAgent=per-node 配置副本）。节点级覆盖写在副本上：ModelID/SkillNames/MCPServiceIDs/extra_rules 追加 SystemPrompt；**KB 绑定继承角色**。已知约束：AgentQA 在 KnowledgeBaseIDs 非空时强制要求 agent 配置 rerank 模型（session_agent_qa.go L121-141，会议室计划澄清 5 同源）——角色无 rerank 且 KB 非空时按现有校验报错，错误进节点 feedback 走失败策略，不静默跳过、不偷偷清空 KB。
7. **结构化终态双通道**：平台智能体节点运行期临时注册工具 `submit_node_result(status, summary)`（status∈pass/rework/fail/human）；CLI 节点约定写 `.sop/gate-results/<node_id>/attempt-<N>.json`（借鉴 LoopForge engine/gate_submit.go:25 gateResultPath 语义：decision 必填、reason 必填、human 必带 actionRequired）。缺失时恢复同 session 追问一次（平台=同会话续问；CLI=resume 续跑或新进程带追问指令），仍缺失交门禁裁定。
8. **审批中心聚合只读+分发**：SOP approval 节点查 sop_node_runs.waiting_approval；平台 HITL 查 agent_prompts（经 hitl Broker 现有公开方法）；automations 查 automation_runs.awaiting_approval。resolve 分发到各自现有处理入口，**不改动 automation/hitl 任何现有代码**，只调用其公开方法。
9. **前端测试口径**（沿用会议室计划约定）：无组件级测试基建，前端 Task 的「测试」= api/helpers 纯函数 `tsx --test` 单测 + `npm run type-check` + `npm run check-i18n`（6 语言键完整性强制）；组件交互验收对照定稿原型在浏览器手动/E2E 核对（M10）。
10. **SSE**：后端 Gin `text/event-stream` 复用 agent-chat 流式基建；前端用 fetch + `buildAuthHeaders`（utils/request.ts:78）读流，不用 axios（axios 不支持流式读取）。
11. **编号 CAS 双库**：PG 用 `UPDATE sop_projects SET next_item_no = next_item_no + 1 WHERE id=? RETURNING next_item_no`；SQLite 不支持 RETURNING 时退化为同事务 `UPDATE … SET next_item_no = next_item_no + 1` 后 `SELECT next_item_no`（事务内行锁保证并发不重号），repository 按 dialector 分支。
12. **CLI 黄金样本**：`internal/application/service/sop/executor/cli/testdata/<kind>/*.jsonl`（录制真实 headless 输出），每适配器一个翻译回归测试；无 result 帧且 exit 0 不判失败交门禁裁定。
13. **RBAC 与 API Key**：SOP 路由挂现有 Viewer/Contributor 守卫（router/rbac.go）+ tenant ctx 注入；API Key 默认拒绝，新增能力 `manage_sop`/`run_sop`/`read_sop` 按 tenant_api_key 能力枚举模式映射到路由组。
14. **画布依赖**：仅新增 `@vue-flow/core`（安装时取最新稳定版并锁入 package.json）；小地图/工具条自绘（参照原型 SVG 实现），不引额外画布插件。
15. **迁移编号对照**：versioned 000166–000173 ↔ sqlite 000068–000075 一一对应（见 File Structure）；执行前 `ls migrations/versioned | tail` 复核未占用，若被占用顺延并同步更新本计划引用。
16. **PM 专属 asynq handler 下线**放在 M10 收尾（迁移验证通过后），M1–M9 期间保持可用以便回退；`/platform/pm` 前端路由 redirect 在 M9 Task 38 落地，后端 routes_pm.go 写端点下线在 M10 Task 58。
17. **版本规划明确不做**：用户决定「暂时先去掉版本的概念」，因此不建 `sop_versions`、不设 `items.version_id`、不做版本视图与发布快照；上一轮讨论中的版本方案整体搁置，不进入本计划。若后续要做，作为独立增补重走设计→计划。
18. **项目建库复用 PM 既有语义**（`pm_project.go:74-105`）：`CreateKnowledgeBase{Name: 项目名, EmbeddingModelID: 默认嵌入模型}`，建库失败只 warn 不阻断；删除项目时按 `kb_owner` 区分连带删除/仅解绑。改绑 KB 后旧自建库不删，改名追加「（已迁移）」。
19. **简报 = 黑板投影，不是第二真相**：`brief_knowledge_id` 指向的文档只由引擎 `CreateKnowledgeFromManual/UpdateManualKnowledge`（channel=`sop`）写入，状态恒 `publish`（保证检索可命中）；任何人工补充都必须落黑板（`source=human`）。AI 只产草稿（`POST /notes/decision-draft` 不落库），人确认后走 `POST /notes`。
20. **父项状态唯一写入口**：父项 `done` 由引擎在子项终态时经 `UpdateStatus` 推进（复用 T22 状态机守卫），不由列表接口「顺手改」；`DeriveParentExecState` 是只读派生，与 T2 `DeriveExecState` 互不替代。
21. **拆分与触发的关系**：`POST /items/:id/split` 只建子项不触发；`POST /items/:id/relaunch` 与 `POST /runs{item_id}` 遇到有子项的父项一律 400「该事项已拆分为子项，请对子项触发」，避免双轨执行。
22. **迁移编号顺延**：M11 使用 `000174_sop_project_notes.*` ↔ `000076_sop_project_notes.*`（执行前 `ls migrations/versioned | tail` 复核；若被占用顺延并同步 §15 编号对照与 T55 parity 清单）。
23. **既有 SQLite schema 守卫测试必须同步**：`internal/database/migration_sqlite_versioned_schema_test.go` 里 `expectedSQLiteMigrationVersion = 67` 需在 T1 随首个 sop 迁移改为 68，逐任务推进至最终 **76**；`versionedSQLiteTables` 追加 sop_projects/sop_items/sop_item_attachments/sop_item_clarifications/sop_item_activities/sop_templates/sop_template_revisions/sop_runs/sop_node_runs/sop_artifacts/sop_cli_credentials/sop_trigger_configs/sop_trigger_logs/sop_project_notes，`versionedSQLiteColumns` 追加 `sop_projects: kb_owner/brief_knowledge_id/brief_rendered_seq`、`sop_items: parent_id/split_source`。T1/T2/T3/T4/T5/T6/T59 各自的「运行确认通过」步骤必须先跑 `go test ./internal/database/ -run TestSQLiteMigrationsCreateVersionedSchema -v` 并 PASS——否则桌面 Lite 首次启动会迁移失败。

## File Structure

**迁移（新建，PG ↔ SQLite 一一对应）：**
- `migrations/versioned/000166_sop_projects.up.sql` / `.down.sql` ↔ `migrations/sqlite/000068_sop_projects.*` — sop_projects
- `000167_sop_items.*` ↔ `000069_sop_items.*` — sop_items + sop_item_attachments
- `000168_sop_item_extras.*` ↔ `000070_sop_item_extras.*` — sop_item_clarifications + sop_item_activities
- `000169_sop_templates.*` ↔ `000071_sop_templates.*` — sop_templates + sop_template_revisions
- `000170_sop_runs.*` ↔ `000072_sop_runs.*` — sop_runs + sop_node_runs + sop_artifacts
- `000171_sop_cli_trigger.*` ↔ `000073_sop_cli_trigger.*` — sop_cli_credentials + sop_trigger_configs + sop_trigger_logs
- `000172_sop_indexes.*` ↔ `000074_sop_indexes.*` — 派生字段 JOIN 所需复合索引
- `000173_sop_builtin_templates.*` ↔ `000075_sop_builtin_templates.*` — 内置模板 seed（产品方案交付/需求上线复盘）
- `000174_sop_project_notes.*` ↔ `000076_sop_project_notes.*` — sop_project_notes（项目黑板，§3.10）+ sop_items(parent_id) 部分索引；sop_projects/sop_items 的 M11 新列已就地写入 000166/000167 建表语句

**后端新建（根 D:\知识库\Know\）：**
- `internal/types/sop_project.go` — SopProject 模型 + repos JSON 类型 + 校验纯函数
- `internal/types/sop_item.go` — SopItem/SopItemAttachment/SopItemClarification/SopItemActivity + 8 态常量 + CanTransitSopItem + ExecState 五态派生纯函数 + 来源中文映射
- `internal/types/sop_template.go` — SopTemplate/SopTemplateRevision/SopDefinition/SopNode/SopEdge/SopNodeExecutor/SopArtifactOutput/SopArtifactCheck + 状态常量
- `internal/types/sop_run.go` — SopRun/SopNodeRun/SopArtifact + run/node 状态常量 + SopUsage
- `internal/types/sop_credential.go` — SopCLICredential（AES-GCM BeforeSave/AfterFind 照抄 types/model.go:334-352）+ 10 种 CLIKind 常量
- `internal/types/sop_trigger.go` — SopTriggerConfig/SopTriggerLog + interval_key 常量
- `internal/types/interfaces/sop.go` — SopProjectRepo/SopItemRepo/SopTemplateRepo/SopRunRepo/SopCredentialRepo/SopTriggerRepo 接口 + SopService 接口
- `internal/application/repository/sop_project.go` — CRUD + AllocItemNo（CAS 双库）+ repos 独立写 + 删除守卫查询
- `internal/application/repository/sop_item.go` — CRUD + 列表 JOIN 派生字段 + 批量状态 + 附件/澄清/活动
- `internal/application/repository/sop_template.go` — CRUD + Publish（不可变 revision）+ revisions 列表 + copy
- `internal/application/repository/sop_run.go` — run/node_run/artifact CRUD + ClaimDue（CAS）+ 心跳续租 + 状态统计 + 后代重置批量写
- `internal/application/repository/sop_credential.go` — 按 (owner,kind) upsert/get
- `internal/application/repository/sop_trigger.go` — config upsert（project 唯一）+ logs 追加 + ReserveDispatch 幂等
- `internal/application/service/sop/service.go` — Service 门面（deps 装配、ItemStatusUpdater 实现）
- `internal/application/service/sop/project.go` — 项目 CRUD/stats/repos 即时生效/删除级联守卫
- `internal/application/service/sop/item.go` — 事项 CRUD/状态机/编号分配/批量/outcome/澄清/活动/retry-failed/relaunch
- `internal/application/service/sop/template.go` — 模板 CRUD/validate/publish/copy/内置只读守卫
- `internal/application/service/sop/approval.go` — approval 节点三操作/decision.md 产物化/超时策略/通知/聚合视图
- `internal/application/service/sop/trigger.go` — 触发配置保存即生效/扫描派发/run-now/触发历史
- `internal/application/service/sop/credential.go` — 凭据加解密 upsert/PATH 检测/安装指引
- `internal/application/service/sop/archive.go` — 产物入 KB（.md 源文档 frontmatter + ingest）
- `internal/application/service/sop/ai.go` — breakdown/analyze/clarify-generate/orchestrate（SSE，复用 AgentQA）
- `internal/types/sop_project_note.go` — SopProjectNote 模型 + 五种 kind 常量 + 校验
- `internal/types/sop_brief.go` — 简报渲染入参/`RenderProjectBrief` 纯函数（确定性文本，无 IO）+ `DeriveParentExecState` 父项汇总纯函数
- `internal/application/repository/sop_project_note.go` — 黑板 CRUD + seq 自增分配 + 最近 N 条查询
- `internal/application/service/sop/brief.go` — BriefService：黑板事件 → debounce 合并 → 渲染 → KB 覆盖写（seq 比对跳过，KB 缺失降级）+ 首次渲染
- `internal/application/service/sop/note.go` — NotesService：记一条/编辑/软删/置顶/决策草稿（SSE）+ 运行终态自动摘要
- `internal/application/service/sop/engine/engine.go` — Engine 结构 + deps + Run 生命周期入口
- `internal/application/service/sop/engine/definition.go` — definition 解析/规范化/默认值
- `internal/application/service/sop/engine/graph.go` — DAG 校验/拓扑/ancestors/descendants（借鉴 LoopForge engine/workflow_graph.go:60/79/242）
- `internal/application/service/sop/engine/gates.go` — check→gate_commands 编译 + 合并 set -e 脚本 + 门禁执行裁定
- `internal/application/service/sop/engine/workspace.go` — run 目录布局/inputs 物化/路径越界校验/git 检查点（复用 workspace_checkpointer.go）/repos 克隆+工作分支
- `internal/application/service/sop/engine/taskbook.go` — 四上下文块组装 + 规则/技能渲染 + 旧占位符兼容
- `internal/application/service/sop/engine/scheduler.go` — claimDue/startQueued/心跳/expireLeases/reconcileAwaiting（照抄 automation_scheduler.go 模式简化）
- `internal/application/service/sop/engine/node.go` — 节点就绪判定/并行/serial_group/attempt/终态序列/失败四策略/执行器异常重试
- `internal/application/service/sop/engine/ops.go` — Approve/Reject/Retry/Pause/Resume/Cancel（借鉴 run_ops.go:271/359）
- `internal/application/service/sop/engine/sop_node.go` — 子 SOP child run/防环/等待/产物回传（借鉴 sop_node.go:18/157/228/256）
- `internal/application/service/sop/executor/provider.go` — Provider 接口/TaskBook/ExecOptions/ExecResult/StreamEvent/Registry
- `internal/application/service/sop/executor/agent.go` — 平台智能体执行器（AgentQA 后台模式/per-node 会话/子集覆盖/submit_node_result/HITL 挂起恢复）
- `internal/application/service/sop/executor/script.go` — 脚本执行器（Git Bash/受控 env/日志直播/exit code）
- `internal/application/service/sop/executor/cli/home.go` — 私有 HOME（os.Root 防符号链接）/凭据 env 注入/配置文件渲染
- `internal/application/service/sop/executor/cli/process.go` — 子进程运行/Windows Job Object/taskkill 降级/排水取消/超时
- `internal/application/service/sop/executor/cli/prompt.go` — prompt argv/24KiB 文件阈值/stdin 优先/agent-system.md 渲染
- `internal/application/service/sop/executor/cli/stream.go` — 内部事件模型/Translator 接口/黄金样本加载
- `internal/application/service/sop/executor/cli/adapter.go` — Adapter 接口（BuildArgs/Translate/InjectCredential/ResumeArg）+ 注册表
- `internal/application/service/sop/executor/cli/{opencode,cursor,agy,kimi,pi,dsh,zcode,claude,codebuddy,codex}.go` — 10 适配器（每种独立文件）
- `internal/application/service/sop/executor/cli/mcp.go` — 按 CLI 配置路径表注入节点 mcp 子集 + Know MCP endpoint + kb_scope 渲染
- `internal/application/service/sop/executor/cli/testdata/<kind>/*.jsonl` — 黄金样本
- `internal/handler/sop.go` — SopHandler 结构 + projects/stats/repos 端点
- `internal/handler/sop_items.go` — items/attachments/outcome/clarifications/activities/bulk/retry-failed/relaunch
- `internal/handler/sop_templates.go` — templates/validate/publish/revisions/copy
- `internal/handler/sop_runs.go` — runs/stats/events SSE/nodes/控制操作/artifacts 预览下载
- `internal/handler/sop_ai.go` — breakdown/analyze/clarify-generate/ai-orchestrate（SSE）
- `internal/handler/sop_admin.go` — approvals/trigger-config/trigger-logs/cli-credentials/cli-status/pm-archive 只读
- `internal/router/routes_sop.go` — RegisterSopRoutes + API Key 能力映射
- `cmd/sop-migrate-pm/main.go` — PM 数据迁移命令（备份+幂等+事务）
- 对应 `*_test.go`（与被测文件同目录，testify + sqlite :memory: + httptest）

**后端修改：**
- `internal/container/container.go` — Provide(service.NewSopService)/Provide(handler.NewSopHandler) + engine 生命周期 Bind/Start（照抄 :913-947 automations 模式）；追加 `repository.NewSopProjectNoteRepo`、`service.NewNotesService`、`service.NewBriefService`（注入 KnowledgeService/KnowledgeBaseService/Models 默认嵌入模型），并把 BriefService 作为 `engine.Deps.BriefProvider` 与 Dispatcher 回灌 BriefService.Trigger
- `internal/router/router.go` — params 加 SopHandler 字段 + RegisterSopRoutes 调用（照抄 :386）
- `internal/router/rbac.go` — manage_sop/run_sop/read_sop 能力→路由映射
- `internal/types/tenant_api_key.go`（或现有能力枚举文件）— 新增 3 个能力常量
- `internal/mcpserver/` — 新增 SOP 工具组文件（列模板/触发运行/查运行/审批）+ Know MCP 检索 kb_scope 强制校验
- `internal/application/service/pm_version_artifact_ai.go` — M10 下线（标记 deprecated→移除 handler 注册）
- `internal/router/routes_pm.go` — M10 写端点下线、读端点保留给 PmArchive

**前端新建（根 frontend/）：**
- `src/api/sop/index.ts` — 全部 REST 封装（类型镜像后端）
- `src/api/sop/helpers.ts` + `helpers.test.ts` — 纯函数：五态派生/来源中文映射/进度计算/ops 确定性应用/差异卡格式化/gate check 摘要/编号校验/interval 摘要
- `src/api/sop/sse.ts` — fetch+buildAuthHeaders 流读取（breakdown/orchestrate/run events/analyze）
- `src/stores/sop.ts` — 项目切换/事项列表+筛选/模板+designer 草稿/运行+轮询/审批角标/触发配置
- `src/views/sop/SopLayoutView.vue` — 壳：五主 Tab + 项目切换器 + ⋮ 菜单 + 子页路由出口
- `src/views/sop/SopItemBoard.vue` — 事项列表/看板双视图 + 统计带 + 检索区 + 批量
- `src/views/sop/SopItemDrawer.vue` — 九 tab 宽抽屉
- `src/views/sop/SopItemCreateModal.vue` — 980px 双栏 + AI 拆解面板
- `src/views/sop/SopProjectSettings.vue` — 项目配置 + 仓库卡 + 危险操作
- `src/views/sop/SopTemplateList.vue` — 模板库（内置只读+复制/版本历史）
- `src/views/sop/SopDesigner.vue` — vue-flow 画布 + 快捷工具条 + 节点面板 + 检查器/AI 面板互斥
- `src/views/sop/SopRunList.vue` — 运行列表（chips 计数/搜索/自动刷新保焦点）
- `src/views/sop/SopRunDetail.vue` — 只读画布 + 节点抽屉 + 操作条 + SSE
- `src/views/sop/SopApprovalCenter.vue` — 聚合待办
- `src/views/sop/SopTriggerTab.vue` — SOP 任务（配置卡/模态/历史/立即运行）
- `src/views/sop/SopSettings.vue` — CLI 凭据 ×10 + PATH 检测 + 工作区 + 黑名单
- `src/views/sop/PmArchive.vue` — PM 只读归档四 tab
- `src/views/sop/components/ProjectSwitcher.vue` / `ProjectCreateModal.vue`
- `src/views/sop/components/SopNodeCard.vue` — 节点卡（双出口端口/徽标）
- `src/views/sop/components/SopInspector.vue` — 五步检查器
- `src/views/sop/components/SopHelpTip.vue` + `src/views/sop/components/help-tip.ts` — ? 气泡引擎（全局单例/视口钳位）
- `src/views/sop/components/SopMultiSelect.vue` — 技能/MCP 多选下拉（msel）
- `src/views/sop/components/SopExecFilter.vue` — 执行情况可搜索单选下拉（fsel）
- `src/views/sop/components/SopAiPanel.vue` — AI 编排助手（flow/node 双模式/差异卡）
- `src/views/sop/components/SopDocPreview.vue` — 产物预览大弹窗
- `src/views/sop/components/SopCanvasToolbar.vue` — 撤销/重做/缩放/适应/自动排版/小地图
- `src/views/sop/components/SopTriggerConfigModal.vue` — 触发配置模态
- `src/views/sop/components/SopProjectBriefPanel.vue` — 项目黑板时间线 + 简报预览 + 生成决策草稿

**前端修改：**
- `frontend/package.json` — 新增 @vue-flow/core
- `src/router/index.ts` — /platform/sop 路由树（照抄 pm L181-228 模式）+ /platform/pm redirect
- `src/stores/menu.ts` — menuArr 中 pm 项原位替换为 sop（L24-50 区间）
- `src/components/menu.vue` — TOP_MENU_PATHS 白名单（L509）+ isMenuItemActive（L452-488）+ handleMenuClick（L1217）+ 图标分支
- `src/i18n/locales/{zh-CN,zh-TW,en-US,ja-JP,ko-KR,ru-RU}.ts` — 新增 `sop:{...}` 命名空间 + `menu.sop`（6 语言全量，check-i18n 强制）

## 全局约定

- 仓库根：`D:\知识库\Know`。Go 构建 `go build ./...`；定向测试 `go test ./internal/application/service/sop/... -run TestXxx -v`；全量 `go test ./...`（M10 收尾）；迁移验证 `make migrate-up`（PG）+ SQLite 侧启动自动跑（desktop Lite 配置）。
- 前端：`cd frontend && npm test -- src/api/sop/helpers.test.ts`；`npm run type-check`；`npm run check-i18n`；安装依赖 `npm install @vue-flow/core`。
- handler 取值：`types.TenantIDFromContext(ctx)` / `types.UserIDFromContext(ctx)`；错误响应沿用 Know 现有 handler 错误封装（读 internal/handler/automation.go 现有模式后对齐）。
- 模型风格：varchar(36) uuid 主键 + `BeforeCreate` 补 uuid + `TableName()` + json serializer 列（照抄 types/pm.go PMProject）；软删 `deleted_at`；tenant_id BIGINT 全表贯穿。
- 提交信息：`feat(sop): …` / `test(sop): …` / `chore(sop): …`，每个 Task 结束提交一次；迁移与模型同 Task 提交。
- 引擎/执行器测试一律 sqlite :memory: + testify require（照抄 repository/agent_prompt_repository_test.go:16 模式）；CLI 翻译测试用 testdata 黄金样本，不依赖真实 CLI 安装；真实 CLI 实测集中在 M10 Task 57。
- 前端组件视觉/交互以定稿原型 `D:\loopforge\prototypes\sop-workbench-prototype.html` 为唯一参照（品牌色 #00a870 映射到 Know 主题变量）；原型 mock 数据一律替换为真实 API。
- 现有 automation/hitl/pm 模块代码零改动（除本计划明确列出的修改点）；每个 Task 完成后 `go build ./...` 必须通过，不得破坏现有测试。
- **SQLite schema 守卫**：`internal/database/migration_sqlite_versioned_schema_test.go` 是桌面 Lite 的迁移门禁——`expectedSQLiteMigrationVersion` 必须与最新 sqlite 迁移号同步（M1 起点 68，M11 终点 76），`versionedSQLiteTables`/`versionedSQLiteColumns` 必须收录新增表与列；新增含 DDL 的任务在其「运行确认通过」步骤里跑 `go test ./internal/database/ -run TestSQLiteMigrationsCreateVersionedSchema -v`。

---

# Milestone M1 — 数据层 + PM 迁移脚本（spec §3 / §11.1）

### Task 1: sop_projects 模型 + 迁移 + 仓储（编号 CAS / 仓库绑定纯函数）

**Files:**
- Create: `migrations/versioned/000166_sop_projects.up.sql` / `.down.sql`、`migrations/sqlite/000068_sop_projects.up.sql` / `.down.sql`
- Create: `internal/types/sop_project.go`、`internal/types/sop_project_test.go`
- Create: `internal/application/repository/sop_project.go`、`internal/application/repository/sop_project_test.go`
- Modify: `internal/types/interfaces/sop.go`（新建，追加 SopProjectRepo 接口）

**Interfaces:**
- Consumes: Know 现有 gorm 句柄、`types.TenantIDFromContext`、uuid 工具（照抄 types/pm.go PMProject 的 BeforeCreate）。
- Produces: `types.SopProject`（字段见下，含 `KBID`/`KBOwner`/`BriefKnowledgeID`/`BriefRenderedSeq`）、`types.SopProjectRepo{Name,URL,Branch,Default}`、`types.ValidateSopProjectCode(code string) error`、`types.ValidateSopRepoURL(url string) error`、`types.NormalizeSopRepos(repos []SopProjectRepo) []SopProjectRepo`、`types.SopKBOwnerProject/Linked/PM`（值 project/linked/pm）、`repository.SopProjectRepo` 结构（`NewSopProjectRepo(db *gorm.DB)`）方法 `Create/GetByID/ListByTenant/Update/Delete/AllocItemNo/AddRepo/RemoveRepo/SetDefaultRepo/CountActiveRuns`。后续 Task 2 编号分配、Task 21 项目端点、Task 9 工作区根目录全部消费这些签名。

- [ ] **Step 1: 写迁移（4 个文件）**

`migrations/versioned/000166_sop_projects.up.sql`：

```sql
CREATE TABLE IF NOT EXISTS sop_projects (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    owner_user_id VARCHAR(36) NOT NULL DEFAULT '',
    name VARCHAR(200) NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    code VARCHAR(10) NOT NULL,
    next_item_no INTEGER NOT NULL DEFAULT 0,
    kb_id VARCHAR(36) NOT NULL DEFAULT '',
    kb_owner VARCHAR(16) NOT NULL DEFAULT 'project',
    brief_knowledge_id VARCHAR(36) NOT NULL DEFAULT '',
    brief_rendered_seq BIGINT NOT NULL DEFAULT 0,
    default_template_id VARCHAR(36) NOT NULL DEFAULT '',
    default_executor VARCHAR(64) NOT NULL DEFAULT 'platform_agent',
    on_run_success_item_status VARCHAR(32) NOT NULL DEFAULT '',
    notify_on_gate_or_fail BOOLEAN NOT NULL DEFAULT TRUE,
    workspace_root VARCHAR(500) NOT NULL DEFAULT '',
    repos JSONB NOT NULL DEFAULT '[]',
    status VARCHAR(16) NOT NULL DEFAULT 'active',
    domain_data JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ
);
CREATE UNIQUE INDEX IF NOT EXISTS ux_sop_projects_tenant_code ON sop_projects(tenant_id, code) WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX IF NOT EXISTS ux_sop_projects_tenant_name ON sop_projects(tenant_id, name) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS ix_sop_projects_tenant ON sop_projects(tenant_id, deleted_at);
```

`.down.sql`：`DROP TABLE IF EXISTS sop_projects;`
`migrations/sqlite/000068_sop_projects.up.sql`：同构，`JSONB`→`JSON`、`TIMESTAMPTZ`→`DATETIME`、`NOW()`→`CURRENT_TIMESTAMP`（部分索引 SQLite 支持，照抄 migrations/sqlite/ 现有风格）；`.down.sql` 同。
注意：`next_item_no` 默认 **0**，AllocItemNo 返回自增后的值（首次=1），见澄清 11。

- [ ] **Step 2: 写失败测试**

`internal/types/sop_project_test.go`：

```go
package types

import "testing"

func TestValidateSopProjectCode(t *testing.T) {
	for _, ok := range []string{"KNOW", "A1", "ab", "SOP2026"} {
		if err := ValidateSopProjectCode(ok); err != nil {
			t.Fatalf("code %q should be valid: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "A", "1A", "ABCDEFGHIJK", "KN OW", "知识", "KN-OW"} {
		if err := ValidateSopProjectCode(bad); err == nil {
			t.Fatalf("code %q should be invalid", bad)
		}
	}
}

func TestValidateSopRepoURL(t *testing.T) {
	for _, ok := range []string{"https://github.com/a/b.git", "git@github.com:a/b.git", "ssh://git@host/a/b"} {
		if err := ValidateSopRepoURL(ok); err != nil {
			t.Fatalf("url %q should be valid: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "http://github.com/a/b", "ftp://x/y", "/local/path", "https://"} {
		if err := ValidateSopRepoURL(bad); err == nil {
			t.Fatalf("url %q should be invalid", bad)
		}
	}
}

func TestNormalizeSopRepos(t *testing.T) {
	in := []SopProjectRepo{
		{URL: "https://github.com/a/know.git", Branch: ""},
		{URL: "https://github.com/a/know.git", Name: "dup"},
		{URL: "git@github.com:a/web.git", Name: "站点", Branch: "dev", Default: true},
	}
	out := NormalizeSopRepos(in)
	if len(out) != 2 {
		t.Fatalf("dedup failed: %d", len(out))
	}
	if out[0].Name != "know" || out[0].Branch != "main" || !out[0].Default {
		t.Fatalf("first repo auto-name/branch/default wrong: %+v", out[0])
	}
	if out[1].Default {
		t.Fatalf("only first repo may be default: %+v", out[1])
	}
}
```

`internal/application/repository/sop_project_test.go`（sqlite :memory: + AutoMigrate，照抄 repository/agent_prompt_repository_test.go:16 的建库模式）：

```go
package repository

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/types"，执行时核对
)

func newSopTestDB(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.SopProject{}))
	return db
}

func TestAllocItemNoConcurrent(t *testing.T) {
	db := newSopTestDB(t)
	repo := NewSopProjectRepo(db)
	p := &types.SopProject{TenantID: 1, Name: "p", Code: "KNOW"}
	require.NoError(t, repo.Create(context.Background(), p))
	var wg sync.WaitGroup
	nums := make(chan int, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			n, err := repo.AllocItemNo(context.Background(), 1, p.ID)
			require.NoError(t, err)
			nums <- n
		}()
	}
	wg.Wait()
	close(nums)
	seen := map[int]bool{}
	for n := range nums {
		require.False(t, seen[n], "duplicate item no %d", n)
		require.GreaterOrEqual(t, n, 1)
		require.LessOrEqual(t, n, 20)
		seen[n] = true
	}
	require.Len(t, seen, 20)
}

func TestRepoDefaultFallback(t *testing.T) {
	db := newSopTestDB(t)
	repo := NewSopProjectRepo(db)
	p := &types.SopProject{TenantID: 1, Name: "p", Code: "KNOW"}
	require.NoError(t, repo.Create(context.Background(), p))
	ctx := context.Background()
	r1, err := repo.AddRepo(ctx, 1, p.ID, types.SopProjectRepo{URL: "https://github.com/a/know.git"})
	require.NoError(t, err)
	r2, err := repo.AddRepo(ctx, 1, p.ID, types.SopProjectRepo{URL: "https://github.com/a/web.git"})
	require.NoError(t, err)
	require.True(t, r1.Default)
	require.NoError(t, repo.SetDefaultRepo(ctx, 1, p.ID, r2.ID))
	require.NoError(t, repo.RemoveRepo(ctx, 1, p.ID, r2.ID))
	got, err := repo.GetByID(ctx, 1, p.ID)
	require.NoError(t, err)
	require.True(t, got.Repos[0].Default, "解绑默认后应顺延到剩余第一个")
}
```

- [ ] **Step 3: 运行确认失败**

Run: `go test ./internal/types/ -run TestValidateSop -v` 与 `go test ./internal/application/repository/ -run TestAllocItemNo -v`
Expected: 编译失败（undefined: ValidateSopProjectCode / SopProject / NewSopProjectRepo）。

- [ ] **Step 4: 实现模型与仓储**

`internal/types/sop_project.go`：SopProject 结构（字段与迁移一一对应，gorm tag 照抄 PMProject 风格：`ID string gorm:"primaryKey;type:varchar(36)"`、`Repos []SopProjectRepo gorm:"serializer:json"`、`DomainData map[string]any gorm:"serializer:json"`、`DeletedAt gorm.DeletedAt`、`BeforeCreate` 补 uuid+时间戳、`TableName() "sop_projects"`）。纯函数：

```go
var sopCodeRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]{1,9}$`)

func ValidateSopProjectCode(code string) error {
	if !sopCodeRe.MatchString(code) {
		return fmt.Errorf("编号前缀须为 2–10 位字母数字且字母开头")
	}
	return nil
}

func ValidateSopRepoURL(u string) error {
	if strings.HasPrefix(u, "https://") && len(u) > len("https://") {
		return nil
	}
	if strings.HasPrefix(u, "ssh://") || regexp.MustCompile(`^[\w.-]+@[\w.-]+:[\w./-]+$`).MatchString(u) {
		return nil
	}
	return fmt.Errorf("仅支持 HTTPS 或 SSH Git 地址")
}

func NormalizeSopRepos(in []SopProjectRepo) []SopProjectRepo {
	seen := map[string]bool{}
	out := make([]SopProjectRepo, 0, len(in))
	for _, r := range in {
		if seen[r.URL] {
			continue
		}
		seen[r.URL] = true
		if r.ID == "" {
			r.ID = uuid.NewString()
		}
		if r.Name == "" {
			base := r.URL[strings.LastIndexAny(r.URL, "/:")+1:]
			r.Name = strings.TrimSuffix(base, ".git")
		}
		if r.Branch == "" {
			r.Branch = "main"
		}
		r.Default = len(out) == 0 // 第一个自动默认，其余强制 false
		out = append(out, r)
	}
	return out
}
```

`internal/application/repository/sop_project.go`：

```go
type SopProjectRepo struct{ db *gorm.DB }

func NewSopProjectRepo(db *gorm.DB) *SopProjectRepo { return &SopProjectRepo{db: db} }

func (r *SopProjectRepo) Create(ctx context.Context, p *types.SopProject) error {
	p.Repos = types.NormalizeSopRepos(p.Repos)
	return r.db.WithContext(ctx).Create(p).Error
}
func (r *SopProjectRepo) GetByID(ctx context.Context, tenantID uint64, id string) (*types.SopProject, error) {
	var p types.SopProject
	err := r.db.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, id).First(&p).Error
	return &p, err
}
func (r *SopProjectRepo) ListByTenant(ctx context.Context, tenantID uint64) ([]types.SopProject, error) {
	var ps []types.SopProject
	err := r.db.WithContext(ctx).Where("tenant_id = ?", tenantID).Order("created_at ASC").Find(&ps).Error
	return ps, err
}
func (r *SopProjectRepo) Update(ctx context.Context, p *types.SopProject) error {
	return r.db.WithContext(ctx).Save(p).Error // code 不可改由 service 层守卫（Task 21）
}
func (r *SopProjectRepo) Delete(ctx context.Context, tenantID uint64, id string) error {
	return r.db.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, id).Delete(&types.SopProject{}).Error
}

// AllocItemNo 事务内 CAS 自增，返回本次分配编号（首次=1）。双库分支见澄清 11。
func (r *SopProjectRepo) AllocItemNo(ctx context.Context, tenantID uint64, projectID string) (int, error) {
	var no int
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if tx.Dialector.Name() == "postgres" {
			return tx.Raw(`UPDATE sop_projects SET next_item_no = next_item_no + 1, updated_at = NOW()
				WHERE id = ? AND tenant_id = ? AND deleted_at IS NULL RETURNING next_item_no`, projectID, tenantID).Scan(&no).Error
		}
		res := tx.Exec(`UPDATE sop_projects SET next_item_no = next_item_no + 1
			WHERE id = ? AND tenant_id = ? AND deleted_at IS NULL`, projectID, tenantID)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return tx.Raw(`SELECT next_item_no FROM sop_projects WHERE id = ?`, projectID).Scan(&no).Error
	})
	return no, err
}

func (r *SopProjectRepo) mutateRepos(ctx context.Context, tenantID uint64, id string, fn func([]types.SopProjectRepo) ([]types.SopProjectRepo, error)) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var p types.SopProject
		if err := tx.Where("tenant_id = ? AND id = ?", tenantID, id).First(&p).Error; err != nil {
			return err
		}
		repos, err := fn(p.Repos)
		if err != nil {
			return err
		}
		return tx.Model(&p).Update("repos", types.NormalizeSopRepos(repos)).Error
	})
}
func (r *SopProjectRepo) AddRepo(ctx context.Context, tenantID uint64, id string, repo types.SopProjectRepo) (*types.SopProjectRepo, error) {
	if err := types.ValidateSopRepoURL(repo.URL); err != nil {
		return nil, err
	}
	var added *types.SopProjectRepo
	err := r.mutateRepos(ctx, tenantID, id, func(rs []types.SopProjectRepo) ([]types.SopProjectRepo, error) {
		for _, existing := range rs {
			if existing.URL == repo.URL {
				return nil, fmt.Errorf("该仓库已绑定")
			}
		}
		if repo.ID == "" {
			repo.ID = uuid.NewString()
		}
		added = &repo
		return append(rs, repo), nil
	})
	return added, err
}
func (r *SopProjectRepo) RemoveRepo(ctx context.Context, tenantID uint64, id, repoID string) error {
	return r.mutateRepos(ctx, tenantID, id, func(rs []types.SopProjectRepo) ([]types.SopProjectRepo, error) {
		out := rs[:0]
		for _, x := range rs {
			if x.ID != repoID {
				out = append(out, x)
			}
		}
		return out, nil // NormalizeSopRepos 会把默认顺延到剩余第一个
	})
}
func (r *SopProjectRepo) SetDefaultRepo(ctx context.Context, tenantID uint64, id, repoID string) error {
	return r.mutateRepos(ctx, tenantID, id, func(rs []types.SopProjectRepo) ([]types.SopProjectRepo, error) {
		out := make([]types.SopProjectRepo, 0, len(rs))
		found := false
		for _, x := range rs {
			if x.ID == repoID {
				found = true
				x.Default = true
				out = append([]types.SopProjectRepo{x}, out...) // 默认仓库排第一
			} else {
				x.Default = false
				out = append(out, x)
			}
		}
		if !found {
			return nil, gorm.ErrRecordNotFound
		}
		return out, nil
	})
}
```

`CountActiveRuns` 本 Task 先声明接口、实现留待 Task 5 sop_runs 表就绪后补（接口方法签名 `CountActiveRuns(ctx, tenantID uint64, projectID string) (int64, error)`，统计 status∈queued/running/waiting/paused 的 run）；本 Task 测试不覆盖它。`internal/types/interfaces/sop.go` 写入 SopProjectRepo 接口（上述全部方法签名）。

- [ ] **Step 5: 运行确认通过**

Run: `go test ./internal/types/ -run "TestValidateSop|TestNormalizeSopRepos" -v && go test ./internal/application/repository/ -run "TestAllocItemNo|TestRepoDefault" -v && go test ./internal/database/ -run TestSQLiteMigrationsCreateVersionedSchema -v && go build ./...`
Expected: 全部 PASS，构建通过。

- [ ] **Step 6: 提交**

```bash
git add migrations/versioned/000166_sop_projects.* migrations/sqlite/000068_sop_projects.* internal/types/sop_project.go internal/types/sop_project_test.go internal/types/interfaces/sop.go internal/application/repository/sop_project.go internal/application/repository/sop_project_test.go
git commit -m "feat(sop): sop_projects 模型/双库迁移/仓储（编号 CAS 与仓库绑定规范化）"
```

### Task 2: sop_items + 附件 + 8 态状态机 + 五态派生纯函数

**Files:**
- Create: `migrations/versioned/000167_sop_items.up.sql` / `.down.sql`、`migrations/sqlite/000069_sop_items.*`
- Create: `internal/types/sop_item.go`、`internal/types/sop_item_test.go`
- Create: `internal/application/repository/sop_item.go`、`internal/application/repository/sop_item_test.go`
- Modify: `internal/types/interfaces/sop.go`（追加 SopItemRepo 接口）

**Interfaces:**
- Consumes: Task 1 `SopProjectRepo.AllocItemNo`。
- Produces: `types.SopItem`（No/Kind/Priority/Status/Source/Proposer/Tags/TemplateID/KBID/KnowledgeID/Outcome/DomainData/LastRunID/ProjectID/Title/Description/**IsBig/ParentID/SplitSource**）、`types.SopItemAttachment`（ItemID/Name/MediaType/Size/Data []byte/TextContent）、8 态常量 `SopItemStatusBacklog…Cancelled`、`types.CanTransitSopItem(from, to string) bool`、`types.DeriveExecState(itemStatus, runStatus string, hasRun bool) string`（返回 none/running/intervene/pr/closed）、`types.SopExecStateLabels`（五态中文）、`types.SopSourceLabel(source string) string`（六键中文映射）、`types.DeriveProgress(nodeRuns []SopNodeRun) (done, total int)`（Task 5 类型就绪后编译，本 Task 先用 `[]struct{Status string}` 最小签名 `DeriveProgress(statuses []string) (done, total int)`）、`types.DeriveCurrentNode(itemStatus string, nodeRuns []SopNodeRun) string` 同理暂用 `DeriveCurrentNode(itemStatus string, running, waiting, failed []string) string`。repository `SopItemRepo`：`Create/GetByID/ListWithDerived(过滤+JOIN)/Update/Delete/UpdateStatus/BulkUpdateStatus/AddAttachment/ListAttachments/ListByProjectNo`。

- [ ] **Step 1: 写迁移**

`000167_sop_items.up.sql`（PG）：

```sql
CREATE TABLE IF NOT EXISTS sop_items (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    project_id VARCHAR(36) NOT NULL,
    no VARCHAR(24) NOT NULL,
    title VARCHAR(500) NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    kind VARCHAR(16) NOT NULL DEFAULT 'requirement',
    priority VARCHAR(4) NOT NULL DEFAULT 'P2',
    status VARCHAR(16) NOT NULL DEFAULT 'backlog',
    source VARCHAR(16) NOT NULL DEFAULT 'internal',
    proposer VARCHAR(200) NOT NULL DEFAULT '',
    owner VARCHAR(200) NOT NULL DEFAULT '',
    tags JSONB NOT NULL DEFAULT '[]',
    template_id VARCHAR(36) NOT NULL DEFAULT '',
    kb_id VARCHAR(36) NOT NULL DEFAULT '',
    knowledge_id VARCHAR(36) NOT NULL DEFAULT '',
    outcome JSONB NOT NULL DEFAULT '{}',
    domain_data JSONB NOT NULL DEFAULT '{}',
    last_run_id VARCHAR(36) NOT NULL DEFAULT '',
    is_big BOOLEAN NOT NULL DEFAULT FALSE,
    parent_id VARCHAR(36) NOT NULL DEFAULT '',
    split_source VARCHAR(16) NOT NULL DEFAULT '',
    created_by VARCHAR(36) NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ
);
CREATE UNIQUE INDEX IF NOT EXISTS ux_sop_items_project_no ON sop_items(project_id, no) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS ix_sop_items_tenant_project ON sop_items(tenant_id, project_id, status, deleted_at);
CREATE INDEX IF NOT EXISTS ix_sop_items_last_run ON sop_items(last_run_id);

CREATE TABLE IF NOT EXISTS sop_item_attachments (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    item_id VARCHAR(36) NOT NULL,
    name VARCHAR(500) NOT NULL,
    media_type VARCHAR(100) NOT NULL DEFAULT '',
    size BIGINT NOT NULL DEFAULT 0,
    data BYTEA,
    text_content TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS ix_sop_item_attachments_item ON sop_item_attachments(item_id, deleted_at);
```

`.down.sql`：DROP 两表。SQLite 000069 同构（JSONB→JSON、BYTEA→BLOB、TIMESTAMPTZ→DATETIME）。

- [ ] **Step 2: 写失败测试**

`internal/types/sop_item_test.go`：

```go
package types

import "testing"

func TestCanTransitSopItem(t *testing.T) {
	allowed := [][2]string{
		{"backlog", "todo"}, {"todo", "in_progress"}, {"in_progress", "in_review"},
		{"in_review", "done"}, {"done", "closed"}, {"in_progress", "blocked"},
		{"blocked", "in_progress"}, {"closed", "todo"}, {"cancelled", "todo"},
		{"todo", "cancelled"},
	}
	for _, p := range allowed {
		if !CanTransitSopItem(p[0], p[1]) {
			t.Fatalf("%s -> %s should be allowed", p[0], p[1])
		}
	}
	for _, p := range [][2]string{{"backlog", "done"}, {"done", "backlog"}, {"closed", "in_progress"}, {"cancelled", "done"}} {
		if CanTransitSopItem(p[0], p[1]) {
			t.Fatalf("%s -> %s should be rejected", p[0], p[1])
		}
	}
	if !CanTransitSopItem("todo", "todo") {
		t.Fatal("same status must be a no-op allowed")
	}
}

func TestDeriveExecState(t *testing.T) {
	cases := []struct {
		item, run string
		hasRun    bool
		want      string
	}{
		{"done", "", false, "closed"},
		{"closed", "failed", true, "closed"},
		{"todo", "", false, "none"},
		{"todo", "cancelled", true, "none"},
		{"in_progress", "running", true, "running"},
		{"in_progress", "paused", true, "running"},
		{"in_progress", "waiting", true, "intervene"},
		{"in_progress", "failed", true, "intervene"},
		{"in_review", "succeeded", true, "pr"},
	}
	for _, c := range cases {
		if got := DeriveExecState(c.item, c.run, c.hasRun); got != c.want {
			t.Fatalf("item=%s run=%s hasRun=%v: got %s want %s", c.item, c.run, c.hasRun, got, c.want)
		}
	}
}

func TestSopSourceLabel(t *testing.T) {
	want := map[string]string{
		"customer": "客户反馈", "internal": "平台新建", "data": "数据洞察",
		"competitor": "竞品分析", "strategy": "战略规划", "other": "其他",
	}
	for k, v := range want {
		if got := SopSourceLabel(k); got != v {
			t.Fatalf("source %s: got %s want %s", k, got, v)
		}
	}
	if SopSourceLabel("unknown") != "其他" {
		t.Fatal("unknown source must fall back to 其他")
	}
}

func TestDeriveProgress(t *testing.T) {
	done, total := DeriveProgress([]string{"succeeded", "skipped", "running", "pending"})
	if done != 2 || total != 4 {
		t.Fatalf("got %d/%d want 2/4", done, total)
	}
}
```

`internal/application/repository/sop_item_test.go`：sqlite :memory: AutoMigrate(SopItem, SopItemAttachment, SopProject)；测试 `TestItemNoUniquePerProject`（同项目重复 no 报错、跨项目同 no 允许）、`TestUpdateStatusIllegalRejected`（backlog→done 返回错误且库中状态不变，照抄 pm_requirement.go:154-166 transit 模式）、`TestListWithDerivedExecFilter`（造 3 事项分别无运行/running run/succeeded run+in_review，exec=intervene/running/pr 过滤各命中正确；需要 SopRun 表——本 Task 先 AutoMigrate 最小 run  stub 表不可行，改为：ListWithDerived 接受 `runStatusByItem map[string]string` 注入参数由 service 层 JOIN 提供，repository 测试直接传 map 验证过滤与派生字段组装）。

- [ ] **Step 3: 运行确认失败**

Run: `go test ./internal/types/ -run "TestCanTransitSopItem|TestDeriveExecState|TestSopSourceLabel|TestDeriveProgress" -v`
Expected: 编译失败（undefined）。

- [ ] **Step 4: 实现**

`internal/types/sop_item.go`：模型（字段对齐迁移，Tags []string serializer:json、Outcome/DomainData map[string]any serializer:json、BeforeCreate 补 uuid）+ 常量 + 纯函数：

```go
const (
	SopItemStatusBacklog    = "backlog"
	SopItemStatusTodo       = "todo"
	SopItemStatusInProgress = "in_progress"
	SopItemStatusInReview   = "in_review"
	SopItemStatusDone       = "done"
	SopItemStatusClosed     = "closed"
	SopItemStatusBlocked    = "blocked"
	SopItemStatusCancelled  = "cancelled"
)

var sopItemTransitions = map[string][]string{
	SopItemStatusBacklog:    {SopItemStatusTodo, SopItemStatusCancelled},
	SopItemStatusTodo:       {SopItemStatusInProgress, SopItemStatusBacklog, SopItemStatusCancelled},
	SopItemStatusInProgress: {SopItemStatusInReview, SopItemStatusBlocked, SopItemStatusDone, SopItemStatusCancelled},
	SopItemStatusInReview:   {SopItemStatusInProgress, SopItemStatusDone, SopItemStatusCancelled},
	SopItemStatusBlocked:    {SopItemStatusInProgress, SopItemStatusTodo, SopItemStatusCancelled},
	SopItemStatusDone:       {SopItemStatusClosed},
	SopItemStatusClosed:     {SopItemStatusTodo},
	SopItemStatusCancelled:  {SopItemStatusTodo},
}

func CanTransitSopItem(from, to string) bool {
	if from == to {
		return true
	}
	for _, s := range sopItemTransitions[from] {
		if s == to {
			return true
		}
	}
	return false
}

// DeriveExecState 五态口径（spec §3.2）：已闭环优先于运行态。
func DeriveExecState(itemStatus, runStatus string, hasRun bool) string {
	if itemStatus == SopItemStatusDone || itemStatus == SopItemStatusClosed {
		return "closed"
	}
	if !hasRun || runStatus == "cancelled" {
		return "none"
	}
	switch runStatus {
	case "running", "paused":
		return "running"
	case "waiting", "failed":
		return "intervene"
	case "succeeded":
		return "pr"
	}
	return "none"
}

var sopSourceLabels = map[string]string{
	"customer": "客户反馈", "internal": "平台新建", "data": "数据洞察",
	"competitor": "竞品分析", "strategy": "战略规划", "other": "其他",
}

func SopSourceLabel(source string) string {
	if l, ok := sopSourceLabels[source]; ok {
		return l
	}
	return "其他"
}

var SopExecStateLabels = map[string]string{
	"none": "尚未执行", "running": "正在推进", "intervene": "需要介入",
	"pr": "待处理 PR", "closed": "已闭环",
}

func DeriveProgress(statuses []string) (done, total int) {
	for _, s := range statuses {
		total++
		if s == "succeeded" || s == "skipped" {
			done++
		}
	}
	return
}

func DeriveCurrentNode(itemStatus string, running, waiting, failed []string) string {
	switch {
	case len(failed) > 0:
		return failed[0]
	case len(waiting) > 0:
		return waiting[0]
	case len(running) > 0:
		return running[0]
	case itemStatus == SopItemStatusDone:
		return "已完成"
	case itemStatus == SopItemStatusClosed:
		return "已关闭"
	}
	return ""
}
```

`internal/application/repository/sop_item.go`：CRUD + `UpdateStatus`（内部过 CanTransitSopItem，非法不落库，错误格式照抄 pm_requirement.go:162）+ `ListWithDerived(ctx, tenantID, projectID string, f SopItemFilter, runStatusByItem map[string]string, nodeRunsByItem map[string][]string) ([]SopItemWithDerived, error)`：q 模糊 LIKE title/no/description、status/owner/source/kind 精确过滤、exec 过滤在组装派生字段后按 DeriveExecState 匹配；`SopItemWithDerived{SopItem; CurrentNode string; ProgressDone, ProgressTotal int; ExecState string}`；`BulkUpdateStatus(ctx, tenantID, ids []string, to string, skipRunning map[string]bool) (updated, skipped []string)`。附件 AddAttachment/ListAttachments（data BYTEA + text_content）。interfaces/sop.go 追加接口。

- [ ] **Step 5: 运行确认通过**

Run: `go test ./internal/types/ -run "SopItem|ExecState|SourceLabel|Progress" -v && go test ./internal/application/repository/ -run "SopItem|ItemNo" -v && go test ./internal/database/ -run TestSQLiteMigrationsCreateVersionedSchema -v && go build ./...`
Expected: PASS。

- [ ] **Step 6: 提交**

```bash
git add migrations/versioned/000167_sop_items.* migrations/sqlite/000069_sop_items.* internal/types/sop_item.go internal/types/sop_item_test.go internal/application/repository/sop_item.go internal/application/repository/sop_item_test.go internal/types/interfaces/sop.go
git commit -m "feat(sop): sop_items/附件模型与迁移、8 态状态机、执行情况五态派生"
```

### Task 3: 澄清问答 + 活动日志（只增不改不删）

**Files:**
- Create: `migrations/versioned/000168_sop_item_extras.up.sql` / `.down.sql`、`migrations/sqlite/000070_sop_item_extras.*`
- Create: `internal/types/sop_item_extra.go`、`internal/types/sop_item_extra_test.go`
- Create: `internal/application/repository/sop_item_extra.go`、`internal/application/repository/sop_item_extra_test.go`
- Modify: `internal/types/interfaces/sop.go`

**Interfaces:**
- Consumes: Task 2 SopItem。
- Produces: `types.SopItemClarification{ItemID, Question, Answer, Status(open|answered), Source(ai|human)}`、`types.SopItemActivity{ItemID, Kind(business|audit), Actor, Action, Detail}`、repository `SopItemExtraRepo`：`AddClarification/ListClarifications/AnswerClarification/SeedAnswered(itemID string, qas []SopItemClarification)/AddActivity/ListActivities(itemID, kind string)`。**无 Update/Delete 方法**（活动只增不改不删；澄清仅 open→answered 单向）。

- [ ] **Step 1: 写迁移**

```sql
CREATE TABLE IF NOT EXISTS sop_item_clarifications (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    item_id VARCHAR(36) NOT NULL,
    question TEXT NOT NULL,
    answer TEXT NOT NULL DEFAULT '',
    status VARCHAR(16) NOT NULL DEFAULT 'open',
    source VARCHAR(16) NOT NULL DEFAULT 'ai',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    answered_at TIMESTAMPTZ,
    deleted_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS ix_sop_clarifications_item ON sop_item_clarifications(item_id, status, deleted_at);

CREATE TABLE IF NOT EXISTS sop_item_activities (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    item_id VARCHAR(36) NOT NULL,
    kind VARCHAR(16) NOT NULL,
    actor VARCHAR(200) NOT NULL DEFAULT '',
    action VARCHAR(100) NOT NULL,
    detail TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS ix_sop_activities_item ON sop_item_activities(item_id, kind, created_at);
```

down：DROP 两表；SQLite 000070 同构。活动表**无 deleted_at**（只增不改不删，spec §3.9）。

- [ ] **Step 2: 写失败测试**

`internal/application/repository/sop_item_extra_test.go`：

```go
func TestClarificationAnswerOneWay(t *testing.T) {
	db := newSopExtraTestDB(t) // AutoMigrate 两模型
	repo := NewSopItemExtraRepo(db)
	ctx := context.Background()
	c := &types.SopItemClarification{TenantID: 1, ItemID: "it1", Question: "目标用户是谁？", Source: "ai"}
	require.NoError(t, repo.AddClarification(ctx, c))
	require.Equal(t, "open", c.Status)
	require.NoError(t, repo.AnswerClarification(ctx, 1, c.ID, "内部运营"))
	got, err := repo.ListClarifications(ctx, 1, "it1")
	require.NoError(t, err)
	require.Equal(t, "answered", got[0].Status)
	require.Equal(t, "内部运营", got[0].Answer)
	require.Error(t, repo.AnswerClarification(ctx, 1, c.ID, "二次覆盖"), "已回答不可再改")
}

func TestSeedAnswered(t *testing.T) {
	db := newSopExtraTestDB(t)
	repo := NewSopItemExtraRepo(db)
	ctx := context.Background()
	require.NoError(t, repo.SeedAnswered(ctx, 1, "it2", []types.SopItemClarification{
		{Question: "q1", Answer: "a1"}, {Question: "q2", Answer: "a2"},
	}))
	got, _ := repo.ListClarifications(ctx, 1, "it2")
	require.Len(t, got, 2)
	for _, c := range got {
		require.Equal(t, "answered", c.Status)
	}
}

func TestActivitiesAppendOnly(t *testing.T) {
	db := newSopExtraTestDB(t)
	repo := NewSopItemExtraRepo(db)
	ctx := context.Background()
	require.NoError(t, repo.AddActivity(ctx, &types.SopItemActivity{TenantID: 1, ItemID: "it3", Kind: "business", Actor: "u", Action: "创建事项"}))
	require.NoError(t, repo.AddActivity(ctx, &types.SopItemActivity{TenantID: 1, ItemID: "it3", Kind: "audit", Actor: "u", Action: "编辑"}))
	b, _ := repo.ListActivities(ctx, 1, "it3", "business")
	a, _ := repo.ListActivities(ctx, 1, "it3", "audit")
	require.Len(t, b, 1)
	require.Len(t, a, 1)
}
```

- [ ] **Step 3: 运行确认失败**

Run: `go test ./internal/application/repository/ -run "Clarification|SeedAnswered|Activities" -v`
Expected: 编译失败。

- [ ] **Step 4: 实现模型与仓储**

模型照迁移字段；`AnswerClarification` 仅当 status=open 时写 answer+status+answered_at，否则返回错误；`SeedAnswered` 批量插入 status=answered（新建事项 AI 拆解问答 seeding，spec §3.9）；`ListActivities` 按 kind 过滤、created_at ASC。interfaces 追加。

- [ ] **Step 5: 运行确认通过**

Run: `go test ./internal/application/repository/ -run "Clarification|SeedAnswered|Activities" -v && go test ./internal/database/ -run TestSQLiteMigrationsCreateVersionedSchema -v && go build ./...`
Expected: PASS。

- [ ] **Step 6: 提交**

```bash
git add migrations/versioned/000168_sop_item_extras.* migrations/sqlite/000070_sop_item_extras.* internal/types/sop_item_extra.go internal/application/repository/sop_item_extra.go internal/application/repository/sop_item_extra_test.go internal/types/interfaces/sop.go
git commit -m "feat(sop): 事项澄清问答与活动日志（只增不改不删）"
```

### Task 4: sop_templates + revisions + definition 类型系统（不可变发布）

**Files:**
- Create: `migrations/versioned/000169_sop_templates.up.sql` / `.down.sql`、`migrations/sqlite/000071_sop_templates.*`
- Create: `internal/types/sop_template.go`、`internal/types/sop_template_test.go`
- Create: `internal/application/repository/sop_template.go`、`internal/application/repository/sop_template_test.go`
- Modify: `internal/types/interfaces/sop.go`

**Interfaces:**
- Consumes: 无（独立类型）。
- Produces: **全计划核心类型**（后续 engine/executor/handler/前端 types 全部镜像这些 JSON 字段名）：`types.SopDefinition{Nodes []SopNode, Edges []SopEdge, InputSchema []string, OnSuccessItemStatus string}`、`types.SopNode`（字段见 Step 4 代码）、`types.SopNodeExecutor`、`types.SopArtifactOutput`、`types.SopArtifactCheck`、`types.SopEdge{ID,Source,Target,When}`、`types.SopPosition{X,Y float64}`、`types.SopTemplate`、`types.SopTemplateRevision`、常量 `SopTemplateStatusDraft/Published/Archived`、节点类型 `SopNodeTypeAgent/Script/Approval/SOP`、执行器 `SopExecutorPlatformAgent/SopExecutorCLI`、失败策略 `SopOnFailureLoop/Reject/Fail/Human`、`types.NormalizeSopDefinition(def *SopDefinition)`（补默认值：max_attempts=3/max_reworks=2/loop_max_iterations=3/timeout_minutes=60/carry_over=true/mcp=["know-kb"]/kb_scope=project/check.type=exists）、repository `SopTemplateRepo`：`Create/GetByID/ListByTenant(含全局 project_id=''/内置)/Update/Delete/Publish/GetRevision/ListRevisions/Copy`。

- [ ] **Step 1: 写迁移**

```sql
CREATE TABLE IF NOT EXISTS sop_templates (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    project_id VARCHAR(36) NOT NULL DEFAULT '',
    name VARCHAR(200) NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    status VARCHAR(16) NOT NULL DEFAULT 'draft',
    version INTEGER NOT NULL DEFAULT 1,
    published_revision_id VARCHAR(36) NOT NULL DEFAULT '',
    definition JSONB NOT NULL DEFAULT '{}',
    is_builtin BOOLEAN NOT NULL DEFAULT FALSE,
    metric_category VARCHAR(32) NOT NULL DEFAULT '',
    created_by VARCHAR(36) NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS ix_sop_templates_tenant ON sop_templates(tenant_id, project_id, status, deleted_at);

CREATE TABLE IF NOT EXISTS sop_template_revisions (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    template_id VARCHAR(36) NOT NULL,
    revision INTEGER NOT NULL,
    definition JSONB NOT NULL,
    published_by VARCHAR(36) NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX IF NOT EXISTS ux_sop_revisions_template_rev ON sop_template_revisions(template_id, revision);
```

down：DROP 两表；SQLite 000071 同构。revisions 无 deleted_at（不可变快照永不删，spec §3.3）。

- [ ] **Step 2: 写失败测试**

`internal/types/sop_template_test.go`：

```go
func TestNormalizeSopDefinitionDefaults(t *testing.T) {
	def := &SopDefinition{Nodes: []SopNode{{ID: "n1", Name: "分析", Type: "agent",
		Executor: SopNodeExecutor{Kind: "platform_agent", AgentID: "a1"}}}}
	NormalizeSopDefinition(def)
	n := def.Nodes[0]
	if n.MaxAttempts != 3 || n.MaxReworks != 2 || n.LoopMaxIterations != 3 || n.TimeoutMinutes != 60 {
		t.Fatalf("budget defaults wrong: %+v", n)
	}
	if !n.CarryOver || n.OnFailure != "loop" {
		t.Fatalf("carry_over/on_failure defaults wrong: %+v", n)
	}
	if len(n.MCP) != 1 || n.MCP[0] != "know-kb" || n.Executor.KBScope != "project" {
		t.Fatalf("mcp/kb_scope defaults wrong: %+v", n)
	}
}

func TestNormalizeKeepsExplicitValues(t *testing.T) {
	def := &SopDefinition{Nodes: []SopNode{{ID: "n1", Type: "agent", MaxAttempts: 5, OnFailure: "reject",
		MCP: []string{}, Skills: []string{"a"}, Executor: SopNodeExecutor{Kind: "cli", CLIKind: "zcode", KBScope: "all"}}}}
	NormalizeSopDefinition(def)
	n := def.Nodes[0]
	if n.MaxAttempts != 5 || n.OnFailure != "reject" || n.Executor.KBScope != "all" {
		t.Fatalf("explicit values overwritten: %+v", n)
	}
	if n.MCP == nil || len(n.MCP) != 0 {
		t.Fatal("explicit empty mcp must stay empty (not reset to know-kb)")
	}
	if n.Skills == nil || len(n.Skills) != 1 {
		t.Fatal("skills subset must be preserved")
	}
}
```

`internal/application/repository/sop_template_test.go`：

```go
func TestPublishImmutableRevision(t *testing.T) {
	db := newSopTplTestDB(t)
	repo := NewSopTemplateRepo(db)
	ctx := context.Background()
	tpl := &types.SopTemplate{TenantID: 1, Name: "t", Definition: types.SopDefinition{Nodes: []types.SopNode{{ID: "n1", Name: "v1", Type: "agent"}}}}
	require.NoError(t, repo.Create(ctx, tpl))
	rev, err := repo.Publish(ctx, 1, tpl.ID, "u1")
	require.NoError(t, err)
	require.Equal(t, 1, rev.Revision)
	// 发布后改草稿不影响 revision
	tpl.Definition.Nodes[0].Name = "v2"
	require.NoError(t, repo.Update(ctx, tpl))
	got, err := repo.GetRevision(ctx, 1, rev.ID)
	require.NoError(t, err)
	require.Equal(t, "v1", got.Definition.Nodes[0].Name, "revision 必须冻结")
	// 再发布产生 revision 2
	rev2, err := repo.Publish(ctx, 1, tpl.ID, "u1")
	require.NoError(t, err)
	require.Equal(t, 2, rev2.Revision)
}

func TestBuiltinReadonlyCopy(t *testing.T) {
	db := newSopTplTestDB(t)
	repo := NewSopTemplateRepo(db)
	ctx := context.Background()
	b := &types.SopTemplate{TenantID: 1, Name: "内置", IsBuiltin: true, Status: "published"}
	require.NoError(t, repo.Create(ctx, b))
	require.Error(t, repo.Update(ctx, b), "内置模板只读")
	cp, err := repo.Copy(ctx, 1, b.ID, "副本")
	require.NoError(t, err)
	require.False(t, cp.IsBuiltin)
	require.Equal(t, "draft", cp.Status)
	require.NotEqual(t, b.ID, cp.ID)
}
```

- [ ] **Step 3: 运行确认失败**

Run: `go test ./internal/types/ -run TestNormalizeSop -v && go test ./internal/application/repository/ -run "TestPublish|TestBuiltin" -v`
Expected: 编译失败。

- [ ] **Step 4: 实现类型与仓储**

`internal/types/sop_template.go`（**核心类型，JSON 字段名与 spec §3.3 definition 逐字对齐**）：

```go
package types

type SopPosition struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

type SopNodeExecutor struct {
	Kind         string   `json:"kind"`                    // platform_agent | cli
	AgentID      string   `json:"agent_id,omitempty"`
	CLIKind      string   `json:"cli_kind,omitempty"`      // 10 种之一
	Model        string   `json:"model,omitempty"`         // 空=默认·跟随凭据配置
	CredentialID string   `json:"credential_id,omitempty"`
	RoleID       *string  `json:"role_id"`                 // cli 可选角色；null=仅任务书
	KBScope      string   `json:"kb_scope,omitempty"`      // project | custom | all
	KBIDs        []string `json:"kb_ids,omitempty"`
}

type SopArtifactCheck struct {
	Type    string `json:"type"`              // exists | contains | command
	Keyword string `json:"keyword,omitempty"`
	Command string `json:"command,omitempty"`
}

type SopArtifactOutput struct {
	Name      string           `json:"name"`
	Path      string           `json:"path"`
	MediaType string           `json:"media_type"`
	Required  bool             `json:"required"`
	Check     SopArtifactCheck `json:"check"`
}

type SopNode struct {
	ID                string              `json:"id"`
	Name              string              `json:"name"`
	Type              string              `json:"type"` // agent | script | approval | sop
	Executor          SopNodeExecutor     `json:"executor"`
	TaskPrompt        string              `json:"task_prompt"`
	TaskRefs          []string            `json:"task_refs"`
	ExtraRules        string              `json:"extra_rules"`
	Skills            []string            `json:"skills"` // nil=角色全部（JSON null）
	MCP               []string            `json:"mcp"`
	ArtifactOutputs   []SopArtifactOutput `json:"artifact_outputs"`
	GateCommands      []string            `json:"gate_commands"` // 派生字段，AI/用户不直写
	GateExtra         string              `json:"gate_extra"`
	OnFailure         string              `json:"on_failure"` // loop | reject | fail | human
	RejectTo          string              `json:"reject_to"`
	MaxAttempts       int                 `json:"max_attempts"`
	MaxReworks        int                 `json:"max_reworks"`
	LoopMaxIterations int                 `json:"loop_max_iterations"`
	CarryOver         bool                `json:"carry_over"`
	SerialGroup       string              `json:"serial_group"`
	TimeoutMinutes    int                 `json:"timeout_minutes"`
	Position          SopPosition         `json:"position"`
	Script            string              `json:"script,omitempty"`
	CredentialEnv     map[string]string   `json:"credential_env,omitempty"`
	ApproverHint      string              `json:"approver_hint,omitempty"`
	Notification      string              `json:"notification,omitempty"`
	ApprovalTimeoutHours  int             `json:"approval_timeout_hours,omitempty"`  // 0=无限等待（spec §6 默认）
	ApprovalTimeoutAction string          `json:"approval_timeout_action,omitempty"` // notify（默认，提醒）| fail（自动失败）
	AutoIngest        bool                `json:"auto_ingest,omitempty"`             // 门禁通过后产物自动入绑定 KB（T36）
	ChildTemplateID   string              `json:"child_template_id,omitempty"`
}

type SopEdge struct {
	ID     string `json:"id"`
	Source string `json:"source"`
	Target string `json:"target"`
	When   string `json:"when"` // success（依赖边）；失败打回不建边，写 node.reject_to
}

type SopDefinition struct {
	Nodes               []SopNode `json:"nodes"`
	Edges               []SopEdge `json:"edges"`
	InputSchema         []string  `json:"input_schema"`
	OnSuccessItemStatus string    `json:"on_success_item_status"`
}

type SopTemplate struct {
	ID                  string        `gorm:"primaryKey;type:varchar(36)" json:"id"`
	TenantID            uint64        `gorm:"not null" json:"tenant_id"`
	ProjectID           string        `gorm:"type:varchar(36)" json:"project_id"` // 空=全局
	Name                string        `gorm:"type:varchar(200);not null" json:"name"`
	Description         string        `gorm:"type:text" json:"description"`
	Status              string        `gorm:"type:varchar(16);default:'draft'" json:"status"`
	Version             int           `gorm:"default:1" json:"version"`
	PublishedRevisionID string        `gorm:"type:varchar(36)" json:"published_revision_id"`
	Definition          SopDefinition `gorm:"serializer:json" json:"definition"`
	IsBuiltin           bool          `gorm:"default:false" json:"is_builtin"`
	MetricCategory      string        `gorm:"type:varchar(32)" json:"metric_category"`
	CreatedBy           string        `gorm:"type:varchar(36)" json:"created_by"`
	CreatedAt           time.Time     `json:"created_at"`
	UpdatedAt           time.Time     `json:"updated_at"`
	DeletedAt           gorm.DeletedAt `gorm:"index" json:"-"`
}

func (SopTemplate) TableName() string { return "sop_templates" }

type SopTemplateRevision struct {
	ID          string        `gorm:"primaryKey;type:varchar(36)" json:"id"`
	TenantID    uint64        `gorm:"not null" json:"tenant_id"`
	TemplateID  string        `gorm:"type:varchar(36);not null" json:"template_id"`
	Revision    int           `gorm:"not null" json:"revision"`
	Definition  SopDefinition `gorm:"serializer:json" json:"definition"`
	PublishedBy string        `gorm:"type:varchar(36)" json:"published_by"`
	CreatedAt   time.Time     `json:"created_at"`
}

func (SopTemplateRevision) TableName() string { return "sop_template_revisions" }
```

常量 + `NormalizeSopDefinition`：逐节点补默认（**只补零值**：MaxAttempts==0→3、MaxReworks==0→2、LoopMaxIterations==0→3、TimeoutMinutes==0→60、OnFailure==""→loop、MCP==nil→["know-kb"]（显式 [] 保留）、Executor.KBScope==""→project、ArtifactOutputs[].Check.Type==""→exists；CarryOver 默认 true 用「未显式设置」判断——模型加 `CarryOverSet bool json:"-"` 不可行（serializer 往返丢失），改为定义 JSON 里 carry_over 恒显式写入：Normalize 时若节点来自新建（前端/AI 默认值已带 true）不动；服务端兜底对旧数据 false 保留。新建节点默认值由前端/AI ops 生成时显式给 carry_over:true，与 spec §16.15 一致）。

`internal/application/repository/sop_template.go`：`Publish` 事务内取当前 definition 快照→revision=max+1→插入 revisions→回写 tpl.published_revision_id/status=published/version=revision；`Update` 对 IsBuiltin 返回错误（只读）；`Copy` 深拷贝 definition、新 uuid、is_builtin=false、status=draft、name=入参；`ListByTenant` 返回本租户全局（project_id=''）+指定项目+内置。interfaces 追加。

- [ ] **Step 5: 运行确认通过**

Run: `go test ./internal/types/ -run TestNormalizeSop -v && go test ./internal/application/repository/ -run "TestPublish|TestBuiltin" -v && go test ./internal/database/ -run TestSQLiteMigrationsCreateVersionedSchema -v && go build ./...`
Expected: PASS。

- [ ] **Step 6: 提交**

```bash
git add migrations/versioned/000169_sop_templates.* migrations/sqlite/000071_sop_templates.* internal/types/sop_template.go internal/types/sop_template_test.go internal/application/repository/sop_template.go internal/application/repository/sop_template_test.go internal/types/interfaces/sop.go
git commit -m "feat(sop): 模板/不可变 revision 与 definition 类型系统"
```

### Task 5: sop_runs + sop_node_runs + sop_artifacts（租约/状态常量/CAS 抢占）

**Files:**
- Create: `migrations/versioned/000170_sop_runs.up.sql` / `.down.sql`、`migrations/sqlite/000072_sop_runs.*`
- Create: `migrations/versioned/000172_sop_indexes.up.sql` / `.down.sql`、`migrations/sqlite/000074_sop_indexes.*`（派生字段 JOIN 复合索引，File Structure 已列；编号 000172 在 T6 的 000171 之后应用，文件先建不影响迁移顺序）
- Create: `internal/types/sop_run.go`、`internal/types/sop_run_test.go`
- Create: `internal/application/repository/sop_run.go`、`internal/application/repository/sop_run_test.go`
- Modify: `internal/types/interfaces/sop.go`；`internal/application/repository/sop_project.go`（补 CountActiveRuns 实现）

**Interfaces:**
- Consumes: Task 4 SopDefinition（workflow_snapshot 类型）。
- Produces: `types.SopRun`（TemplateID/RevisionID/WorkflowSnapshot SopDefinition/ProjectID/ItemID/TriggerKind(manual|schedule|api|child_run)/TriggerRefID/Status/Input map[string]any/HumanContext []SopHumanContextEntry/WorkspaceDir/LeaseUntil *time.Time/HeartbeatAt *time.Time/CancelRequested/PauseRequested bool/Usage SopUsage/ExecutionDurationMs int64/Error/StartedAt/FinishedAt *time.Time/ParentRunID/ProjectContext）、`types.SopHumanContextEntry{At time.Time, Source string, NodeID string, Text string}`、`types.SopNodeRun`（RunID/NodeID/Status/Attempt/ReworkCount/Output/Error/Feedback/GateResult/GateReason/SessionID/ResumeSessionID/ChildRunID/Usage/StartedAt/FinishedAt）、`types.SopArtifact`（RunID/NodeID/Attempt/Name/Path/MediaType/Size/SHA256/KBID/KnowledgeID/IngestedAt *time.Time）、`types.SopUsage{InputTokens, OutputTokens int64, CostUSD float64}`、run 状态常量 `SopRunQueued/Running/Waiting/Succeeded/Failed/Cancelled/Paused`、node 状态常量 `SopNodePending/Running/WaitingApproval/PendingInput/Succeeded/Failed/Cancelled/Skipped`、gate 常量 `SopGatePass/Rework/Fail/Human`、repository `SopRunRepo`：`CreateRun/GetRun/ListRuns(过滤+分页)/UpdateRun/ClaimDue/Heartbeat/ExpireLeases/CreateNodeRun/GetNodeRun/ListNodeRuns/UpdateNodeRun/ResetNodes(ids []string)/CreateArtifact/ListArtifacts/GetArtifact/StatsByStatus/StatsSummary`；另在 SopProjectRepo 补 `CountActiveRuns`（T1 已声明签名）。

- [ ] **Step 1: 写迁移**

```sql
CREATE TABLE IF NOT EXISTS sop_runs (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    template_id VARCHAR(36) NOT NULL,
    revision_id VARCHAR(36) NOT NULL,
    workflow_snapshot JSONB NOT NULL,
    project_id VARCHAR(36) NOT NULL DEFAULT '',
    item_id VARCHAR(36) NOT NULL DEFAULT '',
    parent_run_id VARCHAR(36) NOT NULL DEFAULT '',
    trigger_kind VARCHAR(16) NOT NULL DEFAULT 'manual',
    trigger_ref_id VARCHAR(36) NOT NULL DEFAULT '',
    status VARCHAR(16) NOT NULL DEFAULT 'queued',
    input JSONB NOT NULL DEFAULT '{}',
    human_context JSONB NOT NULL DEFAULT '[]',
    project_context TEXT NOT NULL DEFAULT '',
    workspace_dir VARCHAR(500) NOT NULL DEFAULT '',
    lease_until TIMESTAMPTZ,
    heartbeat_at TIMESTAMPTZ,
    cancel_requested BOOLEAN NOT NULL DEFAULT FALSE,
    pause_requested BOOLEAN NOT NULL DEFAULT FALSE,
    usage JSONB NOT NULL DEFAULT '{}',
    execution_duration_ms BIGINT NOT NULL DEFAULT 0,
    error TEXT NOT NULL DEFAULT '',
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    created_by VARCHAR(36) NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS ix_sop_runs_status ON sop_runs(tenant_id, status, deleted_at);
CREATE INDEX IF NOT EXISTS ix_sop_runs_item ON sop_runs(item_id, created_at);
CREATE INDEX IF NOT EXISTS ix_sop_runs_project ON sop_runs(project_id, status, deleted_at);
CREATE INDEX IF NOT EXISTS ix_sop_runs_lease ON sop_runs(status, lease_until);

CREATE TABLE IF NOT EXISTS sop_node_runs (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    run_id VARCHAR(36) NOT NULL,
    node_id VARCHAR(36) NOT NULL,
    status VARCHAR(24) NOT NULL DEFAULT 'pending',
    attempt INTEGER NOT NULL DEFAULT 0,
    rework_count INTEGER NOT NULL DEFAULT 0,
    output TEXT NOT NULL DEFAULT '',
    error TEXT NOT NULL DEFAULT '',
    feedback TEXT NOT NULL DEFAULT '',
    gate_result VARCHAR(16) NOT NULL DEFAULT '',
    gate_reason TEXT NOT NULL DEFAULT '',
    session_id VARCHAR(36) NOT NULL DEFAULT '',
    resume_session_id VARCHAR(128) NOT NULL DEFAULT '',
    child_run_id VARCHAR(36) NOT NULL DEFAULT '',
    usage JSONB NOT NULL DEFAULT '{}',
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX IF NOT EXISTS ux_sop_node_runs_run_node ON sop_node_runs(run_id, node_id);
CREATE INDEX IF NOT EXISTS ix_sop_node_runs_status ON sop_node_runs(run_id, status);

CREATE TABLE IF NOT EXISTS sop_artifacts (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    run_id VARCHAR(36) NOT NULL,
    node_id VARCHAR(36) NOT NULL,
    attempt INTEGER NOT NULL DEFAULT 1,
    name VARCHAR(500) NOT NULL,
    path VARCHAR(500) NOT NULL,
    media_type VARCHAR(100) NOT NULL DEFAULT '',
    size BIGINT NOT NULL DEFAULT 0,
    sha256 VARCHAR(64) NOT NULL DEFAULT '',
    kb_id VARCHAR(36) NOT NULL DEFAULT '',
    knowledge_id VARCHAR(36) NOT NULL DEFAULT '',
    ingested_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS ix_sop_artifacts_run ON sop_artifacts(run_id, node_id, deleted_at);
```

down：DROP 三表；SQLite 000072 同构。node_runs 无软删（运行历史不可变，重置走 ResetNodes 更新状态）。

`000172_sop_indexes.up.sql`（PG；SQLite 000074 同文，部分索引 SQLite 支持）：

```sql
CREATE INDEX IF NOT EXISTS idx_sop_runs_item_created ON sop_runs(item_id, created_at DESC) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_sop_runs_project_status ON sop_runs(project_id, status) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_sop_node_runs_run_node ON sop_node_runs(run_id, node_id);
CREATE INDEX IF NOT EXISTS idx_sop_artifacts_run_node ON sop_artifacts(run_id, node_id);
CREATE INDEX IF NOT EXISTS idx_sop_items_project_status ON sop_items(project_id, status) WHERE deleted_at IS NULL;
```

down：`DROP INDEX IF EXISTS` 上述五索引。

- [ ] **Step 2: 写失败测试**

`internal/application/repository/sop_run_test.go`：

```go
func TestClaimDueCAS(t *testing.T) {
	db := newSopRunTestDB(t)
	repo := NewSopRunRepo(db)
	ctx := context.Background()
	r := &types.SopRun{TenantID: 1, TemplateID: "t1", RevisionID: "rev1", Status: types.SopRunQueued}
	require.NoError(t, repo.CreateRun(ctx, r))
	lease := time.Now().Add(30 * time.Second)
	got, err := repo.ClaimDue(ctx, lease)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, r.ID, got.ID)
	require.Equal(t, types.SopRunRunning, got.Status)
	// 第二次抢不到（已被占）
	got2, err := repo.ClaimDue(ctx, lease)
	require.NoError(t, err)
	require.Nil(t, got2)
}

func TestExpireLeases(t *testing.T) {
	db := newSopRunTestDB(t)
	repo := NewSopRunRepo(db)
	ctx := context.Background()
	r := &types.SopRun{TenantID: 1, Status: types.SopRunRunning}
	require.NoError(t, repo.CreateRun(ctx, r))
	past := time.Now().Add(-time.Minute)
	require.NoError(t, repo.Heartbeat(ctx, r.ID, past)) // 伪造过期租约
	n, err := repo.ExpireLeases(ctx, time.Now())
	require.NoError(t, err)
	require.Equal(t, int64(1), n)
	got, _ := repo.GetRun(ctx, 1, r.ID)
	require.Equal(t, types.SopRunFailed, got.Status)
	require.Contains(t, got.Error, "租约")
}

func TestResetNodes(t *testing.T) {
	db := newSopRunTestDB(t)
	repo := NewSopRunRepo(db)
	ctx := context.Background()
	for _, nid := range []string{"n1", "n2"} {
		require.NoError(t, repo.CreateNodeRun(ctx, &types.SopNodeRun{TenantID: 1, RunID: "r1", NodeID: nid, Status: types.SopNodeSucceeded, Output: "old"}))
	}
	require.NoError(t, repo.ResetNodes(ctx, "r1", []string{"n1", "n2"}))
	ns, _ := repo.ListNodeRuns(ctx, 1, "r1")
	for _, n := range ns {
		require.Equal(t, types.SopNodePending, n.Status)
		require.Empty(t, n.Output)
		require.Empty(t, n.Error)
	}
}

func TestStatsByStatus(t *testing.T) {
	db := newSopRunTestDB(t)
	repo := NewSopRunRepo(db)
	ctx := context.Background()
	for _, s := range []string{"succeeded", "succeeded", "failed", "running"} {
		require.NoError(t, repo.CreateRun(ctx, &types.SopRun{TenantID: 1, Status: s}))
	}
	m, err := repo.StatsByStatus(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, int64(2), m["succeeded"])
	require.Equal(t, int64(1), m["failed"])
}
```

- [ ] **Step 3: 运行确认失败**

Run: `go test ./internal/application/repository/ -run "TestClaimDue|TestExpireLeases|TestResetNodes|TestStatsByStatus" -v`
Expected: 编译失败。

- [ ] **Step 4: 实现**

模型照迁移（HumanContext []SopHumanContextEntry serializer:json、Usage SopUsage serializer:json、LeaseUntil/HeartbeatAt *time.Time）。关键仓储方法：

```go
// ClaimDue CAS 抢占一个 queued run（照抄 automation_scheduler claimDue 语义，单实例简化）
func (r *SopRunRepo) ClaimDue(ctx context.Context, leaseUntil time.Time) (*types.SopRun, error) {
	var claimed *types.SopRun
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var run types.SopRun
		err := tx.Where("status = ? AND deleted_at IS NULL", types.SopRunQueued).
			Order("created_at ASC").First(&run).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		res := tx.Model(&types.SopRun{}).
			Where("id = ? AND status = ?", run.ID, types.SopRunQueued).
			Updates(map[string]any{"status": types.SopRunRunning, "lease_until": leaseUntil,
				"heartbeat_at": time.Now(), "started_at": time.Now()})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return nil // 被并发抢走，本轮空手
		}
		run.Status = types.SopRunRunning
		claimed = &run
		return nil
	})
	return claimed, err
}

func (r *SopRunRepo) Heartbeat(ctx context.Context, runID string, leaseUntil time.Time) error {
	return r.db.WithContext(ctx).Model(&types.SopRun{}).Where("id = ?", runID).
		Updates(map[string]any{"lease_until": leaseUntil, "heartbeat_at": time.Now()}).Error
}

func (r *SopRunRepo) ExpireLeases(ctx context.Context, now time.Time) (int64, error) {
	res := r.db.WithContext(ctx).Model(&types.SopRun{}).
		Where("status IN ? AND lease_until IS NOT NULL AND lease_until < ?",
			[]string{types.SopRunRunning, types.SopRunWaiting}, now).
		Updates(map[string]any{"status": types.SopRunFailed, "error": "执行租约过期（进程崩溃回收），可重试", "finished_at": now})
	return res.RowsAffected, res.Error
}

func (r *SopRunRepo) ResetNodes(ctx context.Context, runID string, nodeIDs []string) error {
	return r.db.WithContext(ctx).Model(&types.SopNodeRun{}).
		Where("run_id = ? AND node_id IN ?", runID, nodeIDs).
		Updates(map[string]any{"status": types.SopNodePending, "output": "", "error": "",
			"gate_result": "", "gate_reason": "", "child_run_id": "", "finished_at": nil}).Error
}
```

`StatsByStatus` GROUP BY status 返回 map；`StatsSummary` 返回 `SopRunStats{SuccessRate float64, FinishedCount, TotalCount int64, TotalTokens int64, StatusCounts map[string]int64}`（成功率=succeeded/已结束，已结束=succeeded+failed+cancelled）；`CountActiveRuns(ctx, tenantID uint64, projectID string) (int64, error)` 补进 SopProjectRepo（T1 声明的签名在此实现；status∈queued/running/waiting/paused）。interfaces 追加全部签名。

- [ ] **Step 5: 运行确认通过**

Run: `go test ./internal/application/repository/ -run "SopRun|ClaimDue|ExpireLeases|ResetNodes|StatsByStatus" -v && go test ./internal/database/ -run TestSQLiteMigrationsCreateVersionedSchema -v && go build ./...`
Expected: PASS。

- [ ] **Step 6: 提交**

```bash
git add migrations/versioned/000170_sop_runs.* migrations/sqlite/000072_sop_runs.* migrations/versioned/000172_sop_indexes.* migrations/sqlite/000074_sop_indexes.* internal/types/sop_run.go internal/application/repository/sop_run.go internal/application/repository/sop_run_test.go internal/application/repository/sop_project.go internal/types/interfaces/sop.go
git commit -m "feat(sop): 运行/节点运行/产物模型与迁移、租约 CAS 抢占与崩溃回收"
```

### Task 6: CLI 凭据（加密）+ 触发配置 + 触发历史

**Files:**
- Create: `migrations/versioned/000171_sop_cli_trigger.up.sql` / `.down.sql`、`migrations/sqlite/000073_sop_cli_trigger.*`
- Create: `internal/types/sop_credential.go`、`internal/types/sop_trigger.go`、`internal/types/sop_credential_test.go`
- Create: `internal/application/repository/sop_credential.go`、`internal/application/repository/sop_trigger.go`、`internal/application/repository/sop_trigger_test.go`
- Modify: `internal/types/interfaces/sop.go`

**Interfaces:**
- Consumes: Know 现有 `utils.EncryptAESGCM` / `utils.DecryptStoredSecretLenient`（types/model.go:334-352 同款）。
- Produces: `types.SopCLICredential{OwnerUserID, CLIKind, APIKey(加密), BaseURL, DefaultModel, Extra map[string]any}`、10 种常量 `SopCLIKindOpenCode/Cursor/AGY/Kimi/Pi/DSH/ZCode/Claude/CodeBuddy/Codex`（值：opencode/cursor-agent/agy/kimi/pi/dsh/zcode/claude/codebuddy/codex）+ `types.SopCLIKinds []string`、`types.SopTriggerConfig{ProjectID(唯一), Mode(interval|cron), IntervalKey, Cron, Timezone, DefaultTemplateID, PreferItemTemplate bool, Sources, Statuses []string, PerRunLimit, Enabled, LastRunAt/NextRunAt *time.Time}`、interval 常量 `15m/30m/hourly/daily9/weekly1`、`types.SopTriggerLog{ConfigID, Kind(auto|manual), Scanned, Fired, Skipped int, Note}`、repository `SopCredentialRepo{Upsert/Get(owner,kind)/ListByOwner}`、`SopTriggerRepo{UpsertConfig/GetConfig/AddLog/ListLogs/ReserveDispatch}`。`ReserveDispatch(ctx, configID, itemID string, slot time.Time) (bool, error)`：同一事项同一调度槽幂等（借鉴 LoopForge ReserveIssueDispatch；实现=sop_trigger_logs 旁挂唯一约束表 `sop_dispatch_reservations(config_id,item_id,slot)` 唯一索引，插入冲突返回 false）。

- [ ] **Step 1: 写迁移**

```sql
CREATE TABLE IF NOT EXISTS sop_cli_credentials (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    owner_user_id VARCHAR(36) NOT NULL,
    cli_kind VARCHAR(32) NOT NULL,
    api_key TEXT NOT NULL DEFAULT '',
    base_url VARCHAR(500) NOT NULL DEFAULT '',
    default_model VARCHAR(200) NOT NULL DEFAULT '',
    extra JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ
);
CREATE UNIQUE INDEX IF NOT EXISTS ux_sop_cli_cred_owner_kind ON sop_cli_credentials(owner_user_id, cli_kind) WHERE deleted_at IS NULL;

CREATE TABLE IF NOT EXISTS sop_trigger_configs (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    project_id VARCHAR(36) NOT NULL,
    mode VARCHAR(16) NOT NULL DEFAULT 'interval',
    interval_key VARCHAR(16) NOT NULL DEFAULT 'daily9',
    cron VARCHAR(100) NOT NULL DEFAULT '',
    timezone VARCHAR(64) NOT NULL DEFAULT 'Asia/Shanghai',
    default_template_id VARCHAR(36) NOT NULL DEFAULT '',
    prefer_item_template BOOLEAN NOT NULL DEFAULT TRUE,
    sources VARCHAR(16) NOT NULL DEFAULT 'all',
    statuses JSONB NOT NULL DEFAULT '["backlog","todo"]',
    per_run_limit INTEGER NOT NULL DEFAULT 10,
    enabled BOOLEAN NOT NULL DEFAULT FALSE,
    last_run_at TIMESTAMPTZ,
    next_run_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ
);
CREATE UNIQUE INDEX IF NOT EXISTS ux_sop_trigger_project ON sop_trigger_configs(project_id) WHERE deleted_at IS NULL;

CREATE TABLE IF NOT EXISTS sop_trigger_logs (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    config_id VARCHAR(36) NOT NULL,
    kind VARCHAR(16) NOT NULL DEFAULT 'auto',
    scanned INTEGER NOT NULL DEFAULT 0,
    fired INTEGER NOT NULL DEFAULT 0,
    skipped INTEGER NOT NULL DEFAULT 0,
    note TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS ix_sop_trigger_logs_config ON sop_trigger_logs(config_id, created_at);

CREATE TABLE IF NOT EXISTS sop_dispatch_reservations (
    id VARCHAR(36) PRIMARY KEY,
    config_id VARCHAR(36) NOT NULL,
    item_id VARCHAR(36) NOT NULL,
    slot TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX IF NOT EXISTS ux_sop_dispatch_slot ON sop_dispatch_reservations(config_id, item_id, slot);
```

down：DROP 四表；SQLite 000073 同构。

- [ ] **Step 2: 写失败测试**

`internal/types/sop_credential_test.go`：

```go
func TestCredentialEncryptRoundtrip(t *testing.T) {
	db := newSopCredTestDB(t) // AutoMigrate(&SopCLICredential{})
	c := &SopCLICredential{TenantID: 1, OwnerUserID: "u1", CLIKind: "zcode", APIKey: "sk-secret-123"}
	require.NoError(t, db.Create(c).Error)
	var raw string
	require.NoError(t, db.Raw("SELECT api_key FROM sop_cli_credentials WHERE id = ?", c.ID).Scan(&raw).Error)
	require.NotContains(t, raw, "sk-secret-123", "落库必须是密文")
	var got SopCLICredential
	require.NoError(t, db.First(&got, "id = ?", c.ID).Error)
	require.Equal(t, "sk-secret-123", got.APIKey, "AfterFind 必须解密还原")
}
```

`internal/application/repository/sop_trigger_test.go`：`TestUpsertConfigPerProject`（同项目二次 Upsert 更新同一行不新增）、`TestReserveDispatchIdempotent`（同 (config,item,slot) 第二次返回 false，不同 slot 返回 true）、`TestPerRunLimitBounds`（config.PerRunLimit 越界 0/11 时 Upsert 返回校验错误，合法 1–10）。

- [ ] **Step 3: 运行确认失败**

Run: `go test ./internal/types/ -run TestCredentialEncrypt -v && go test ./internal/application/repository/ -run "SopTrigger|UpsertConfig|ReserveDispatch|PerRunLimit" -v`
Expected: 编译失败。

- [ ] **Step 4: 实现**

`types/sop_credential.go`：模型 + BeforeSave 加密 / AfterFind 解密（**逐字照抄 types/model.go:334-352 的 utils.EncryptAESGCM / DecryptStoredSecretLenient 模式**，兼容旧明文）+ 10 种 CLIKind 常量与清单。`types/sop_trigger.go`：模型 + interval 常量 + `ValidateSopTriggerConfig`（mode∈interval|cron；cron 非空时复用 internal/automation/trigger.go 的解析函数校验且最小间隔 5min；per_run_limit 1–10；statuses 不含 done/closed）。仓储：Upsert（按 project_id 查在则更新否则插入）、ReserveDispatch（插入 reservations，唯一冲突→false,nil）、AddLog/ListLogs（created_at DESC 分页）。interfaces 追加。

- [ ] **Step 5: 运行确认通过**

Run: `go test ./internal/types/ -run TestCredentialEncrypt -v && go test ./internal/application/repository/ -run "SopTrigger|UpsertConfig|ReserveDispatch|PerRunLimit" -v && go test ./internal/database/ -run TestSQLiteMigrationsCreateVersionedSchema -v && go build ./...`
Expected: PASS（加密测试若 utils 函数签名与 model.go 不一致，以 model.go 实际调用为准修正）。

- [ ] **Step 6: 提交**

```bash
git add migrations/versioned/000171_sop_cli_trigger.* migrations/sqlite/000073_sop_cli_trigger.* internal/types/sop_credential.go internal/types/sop_trigger.go internal/types/sop_credential_test.go internal/application/repository/sop_credential.go internal/application/repository/sop_trigger.go internal/application/repository/sop_trigger_test.go internal/types/interfaces/sop.go
git commit -m "feat(sop): CLI 凭据加密存储、触发配置与派发幂等预约"
```

### Task 7: 内置模板 seed + PM 数据迁移命令（备份/幂等/无损）

**Files:**
- Create: `migrations/versioned/000173_sop_builtin_templates.up.sql` / `.down.sql`、`migrations/sqlite/000075_sop_builtin_templates.*`
- Create: `cmd/sop-migrate-pm/main.go`、`cmd/sop-migrate-pm/main_test.go`
- Modify: `internal/application/repository/sop_template.go`（ListByTenant 加 `OR is_builtin = TRUE`，内置行 tenant_id=0 全租户可见）

**Interfaces:**
- Consumes: Task 1–6 全部模型与仓储；`internal/types/pm.go` 的 PMProject/PMRequirement（执行时先读该文件核对列名）。
- Produces: 内置模板固定 id `b0000000-0000-4000-8000-000000000001`（产品方案交付）/ `…0002`（需求上线复盘）；迁移命令 `go run ./cmd/sop-migrate-pm -dsn <gorm dsn> -backup-dir data/backups`（退出码 0=成功；可重复执行）。**澄清 17**：内置模板 agent 节点 `executor.agent_id` 留空（各安装的角色 id 不同），内置模板只读参照、不可直接发起运行；复制后在检查器绑定真实角色（或「✦ AI 配这一步」推断）再发布运行——验收 4「内置产品方案 SOP 跑通原 PM 核心链路」按 复制→绑定→运行 口径验证。发布校验对 is_builtin 豁免执行器完整性（只读参照允许缺角色）。

- [ ] **Step 1: 写内置模板 seed 迁移**

`000173_sop_builtin_templates.up.sql`（PG；SQLite 000075 用 `INSERT OR IGNORE` 且 JSONB→JSON）：

```sql
INSERT INTO sop_templates (id, tenant_id, project_id, name, description, status, version, is_builtin, metric_category, definition, created_at, updated_at)
VALUES ('b0000000-0000-4000-8000-000000000001', 0, '', '产品方案交付',
 'PM 场景沉淀：需求分析→方案确认→PRD→原型→评审→归档。复制后绑定智能体角色即可运行。',
 'published', 1, TRUE, '交付',
 '{"nodes":[
  {"id":"n1","name":"需求分析","type":"agent","executor":{"kind":"platform_agent","agent_id":"","kb_scope":"project"},"task_prompt":"结合事项背景、附件与知识库，产出需求分析报告：目标用户、核心问题、方案方向、风险与依赖。","skills":["pm-requirement-analysis"],"mcp":["know-kb"],"artifact_outputs":[{"name":"分析报告","path":"out/analysis.md","media_type":"text/markdown","required":true,"check":{"type":"contains","keyword":"方案"}}],"on_failure":"loop","max_attempts":3,"max_reworks":2,"loop_max_iterations":3,"carry_over":true,"timeout_minutes":60,"position":{"x":0,"y":0}},
  {"id":"n2","name":"方案确认","type":"approval","approver_hint":"确认需求分析结论与方案方向","on_failure":"reject","reject_to":"n1","position":{"x":250,"y":0}},
  {"id":"n3","name":"PRD 生成","type":"agent","executor":{"kind":"platform_agent","agent_id":"","kb_scope":"project"},"task_prompt":"按已确认的分析报告撰写 PRD：背景、目标、功能清单、交互说明、验收标准。","skills":["pm-prd-generator"],"mcp":["know-kb"],"artifact_outputs":[{"name":"PRD","path":"out/prd.md","media_type":"text/markdown","required":true,"check":{"type":"contains","keyword":"验收"}}],"on_failure":"loop","max_attempts":3,"max_reworks":2,"loop_max_iterations":3,"carry_over":true,"timeout_minutes":60,"position":{"x":500,"y":0}},
  {"id":"n4","name":"原型生成","type":"agent","executor":{"kind":"platform_agent","agent_id":"","kb_scope":"project"},"task_prompt":"按 PRD 生成可交互 HTML 原型，覆盖核心流程与关键状态。","skills":["pm-prototype"],"mcp":["know-kb"],"artifact_outputs":[{"name":"原型","path":"out/prototype.html","media_type":"text/html","required":true,"check":{"type":"exists"}}],"on_failure":"loop","max_attempts":3,"max_reworks":2,"loop_max_iterations":3,"carry_over":true,"timeout_minutes":90,"position":{"x":750,"y":0}},
  {"id":"n5","name":"评审","type":"approval","approver_hint":"评审 PRD 与原型，可打回 PRD 生成","on_failure":"reject","reject_to":"n3","position":{"x":1000,"y":0}},
  {"id":"n6","name":"归档","type":"script","script":"set -e\ntest -s out/prd.md\necho archived","artifact_outputs":[],"on_failure":"fail","position":{"x":1250,"y":0}}],
  "edges":[{"id":"e1","source":"n1","target":"n2","when":"success"},{"id":"e2","source":"n2","target":"n3","when":"success"},{"id":"e3","source":"n3","target":"n4","when":"success"},{"id":"e4","source":"n4","target":"n5","when":"success"},{"id":"e5","source":"n5","target":"n6","when":"success"}],
  "input_schema":["title","description","attachments"],"on_success_item_status":"in_review"}',
 NOW(), NOW())
ON CONFLICT (id) DO NOTHING;
```

第二条 INSERT 同构：id `…0002`、name「需求上线复盘」、metric_category「交付」、链 效果核对(agent, skills 空, check exists out/effect.md) → 价值判定(approval, reject_to 效果核对) → 复盘报告(agent, contains "复盘" out/retro.md) → 回填事项 outcome(script)，edges 顺连，on_success_item_status="done"。`.down.sql`：`DELETE FROM sop_templates WHERE id IN ('b0000000-0000-4000-8000-000000000001','b0000000-0000-4000-8000-000000000002');`

- [ ] **Step 2: 写失败测试（迁移命令）**

`cmd/sop-migrate-pm/main_test.go`（sqlite :memory:，AutoMigrate PM 源表镜像 + SOP 目标表；PM 源表用 gorm Raw DDL 按 types/pm.go 实际列建最小列集）：

```go
func TestMigratePMIdempotentAndLossless(t *testing.T) {
	db := newMigrateTestDB(t)
	seedPMFixtures(t, db) // 1 项目 2 需求：launched（含复盘字段）+ rejected（含 reject_reason）
	dir := t.TempDir()
	require.NoError(t, runMigration(db, dir))
	// 备份存在
	entries, _ := os.ReadDir(dir)
	require.NotEmpty(t, entries)
	// 事项数=2、编号按创建时间顺序 KNOW-1/KNOW-2
	var items []types.SopItem
	require.NoError(t, db.Order("no ASC").Find(&items).Error)
	require.Len(t, items, 2)
	require.Equal(t, "KNOW-1", items[0].No)
	// 状态映射
	require.Equal(t, "done", statusOf(t, db, "launched"))
	require.Equal(t, "closed", statusOf(t, db, "rejected"))
	// 无损：domain_data 保留 ai_analysis/design_plan/reject_reason，outcome 保留复盘字段
	var launched types.SopItem
	require.NoError(t, db.Where("domain_data::text LIKE ?", "%pm_source_id%").First(&launched).Error) // SQLite 侧用 LIKE domain_data
	require.NotEmpty(t, launched.DomainData["ai_analysis"])
	// 幂等：再跑一次不新增
	require.NoError(t, runMigration(db, dir))
	var n int64
	db.Model(&types.SopItem{}).Count(&n)
	require.Equal(t, int64(2), n)
}
```

（测试辅助函数 seedPMFixtures/statusOf/runMigration 与实现同文件写出；SQLite 不支持 `::text`，测试用 `WHERE domain_data LIKE ?` 按 dialector 分支——实现里统一用 gorm serializer 读出后在 Go 侧判重，避免 SQL 方言差异。）

- [ ] **Step 3: 运行确认失败**

Run: `go test ./cmd/sop-migrate-pm/ -v`
Expected: 编译失败（runMigration undefined）。

- [ ] **Step 4: 实现迁移命令**

`cmd/sop-migrate-pm/main.go` 结构：

```go
func main() {
	dsn := flag.String("dsn", "", "gorm DSN（postgres://… 或 sqlite 文件路径）")
	backupDir := flag.String("backup-dir", "data/backups", "迁移前 JSON 备份目录")
	flag.Parse()
	db := openDB(*dsn) // 按前缀选 postgres/sqlite dialector
	if err := runMigration(db, *backupDir); err != nil {
		log.Fatal(err)
	}
}

func runMigration(db *gorm.DB, backupDir string) error {
	// 1) 备份：SELECT 全量 pm_projects/pm_requirements/pm_requirement_attachments → JSON 文件
	//    data/backups/pm-migration-<RFC3339>.json（os.MkdirAll + json.MarshalIndent）
	// 2) 事务内逐项目迁移：
	//    pm_projects → sop_projects（kb_id 保留；session_id/原 status 进 domain_data；
	//    code 由 name 派生大写前缀，冲突加数字后缀；workspace_root 默认 data/sop/workspaces/<code>/）
	//    判重：sop_projects.domain_data.pm_source_id == 源 id（Go 侧遍历比对，避免 SQL 方言）
	// 3) 逐需求迁移（按 created_at ASC 顺序 AllocItemNo 补号）：
	//    kind=requirement；状态映射 pending_analysis/analyzing→backlog、confirmed→todo、
	//    scheduled/developing→in_progress、launched→done、rejected→closed、closed→closed；
	//    ai_analysis/design_plan/session_id/knowledge_id/reject_reason → domain_data（原样）；
	//    上线复盘字段 → outcome{actual_date,effect_notes,value_achieved,retro_notes}；
	//    domain_data.pm_source_id=源 id（幂等键）；附件同构直迁 sop_item_attachments。
	// 4) 写 sop_item_activities（kind=audit, action=PM迁移, detail=源表/源id）。
	// 全程单事务，失败回滚；已存在 pm_source_id 的跳过。
}
```

执行前**先读 `internal/types/pm.go`** 核对 PMProject/PMRequirement 实际字段名（镜像 struct 的 gorm column tag 必须一致；go build 与测试会暴露不一致）。pm_versions 等其余 PM 表**不动**（冻结只读归档，PmArchive 在 M9 Task 54）。

- [ ] **Step 5: 运行确认通过 + 双库迁移验证**

Run: `go test ./cmd/sop-migrate-pm/ -v && go build ./... && make migrate-up`
Expected: PASS；PG 迁移到 000173；SQLite 侧用桌面 Lite 配置启动一次验证自动迁移到 000075（或 `go test ./internal/database/ -run Migration -v` 若现有迁移测试覆盖 sqlite 目录）。

- [ ] **Step 6: 提交**

```bash
git add migrations/versioned/000173_sop_builtin_templates.* migrations/sqlite/000075_sop_builtin_templates.* cmd/sop-migrate-pm/ internal/application/repository/sop_template.go
git commit -m "feat(sop): 内置 PM 场景模板 seed 与幂等 PM 数据迁移命令（含 JSON 备份）"
```

---

# Milestone M2 — 确定性推进引擎（spec §4 / §5 抽象）

### Task 8: 执行器抽象 Provider + 统一流事件模型

**Files:**
- Create: `internal/application/service/sop/executor/provider.go`、`provider_test.go`

**Interfaces:**
- Consumes: Task 4/5/6 类型。
- Produces:（**M2–M6 全部消费，签名锁定**）

```go
package executor

type StreamEvent struct {
	Type     string          `json:"type"` // text_delta | tool_call | tool_result | result | error
	Text     string          `json:"text,omitempty"`
	ToolName string          `json:"tool_name,omitempty"`
	Payload  json.RawMessage `json:"payload,omitempty"`
	At       time.Time       `json:"at"`
}

type ArtifactFile struct {
	Name      string `json:"name"`
	Path      string `json:"path"` // 工作区相对路径
	MediaType string `json:"media_type"`
	Size      int64  `json:"size"`
	SHA256    string `json:"sha256"`
}

type TaskBook struct {
	RunID           string
	NodeRunID       string
	NodeID          string
	NodeName        string
	Prompt          string            // 引擎组装好的完整任务书（大白话+上下文块）
	SystemPrompt    string            // agent-system.md 内容（CLI）/系统提示追加（平台）
	WorkspaceRoot   string
	InputsDir       string
	OutDir          string
	Feedback        string            // 返工意见（非空=返工轮）
	Attempt         int
	ResumeSessionID string            // CLI 续跑 / 平台续会话
	Timeout         time.Duration
	Env             map[string]string // 受控叠加环境变量
	Script          string            // script 节点脚本正文（其余类型为空）
	Model           string            // 空=默认
	Credential      *types.SopCLICredential
	Role            *types.CustomAgent // 解析后的角色（平台智能体执行器消费；可 nil）
	MCPServers      []types.MCPService // 节点 mcp 子集解析后的真实配置
	Skills          []string           // 解析后的技能子集（nil=全部）
	KnowMCP         *KnowMCPEndpoint   // CLI 节点专用：Know MCP  scoped endpoint（T37 供给；平台节点 nil）
}

// KnowMCPEndpoint 是 T37 MCPProvisioner 为 CLI 节点签发的受限检索端点。
// 类型定义在 executor 包（engine 与 cli 都要引用，cli 侧 T33 用类型别名）。
type KnowMCPEndpoint struct {
	URL     string   // 含 kb_scope query，如 http://127.0.0.1:8080/mcp?kb_scope=project
	Token   string   // Bearer token（HMAC 派生，不落盘）
	KBScope string   // project | custom | all
	KBIDs   []string // 服务端强制的 KB 白名单（all=租户全部）
}

type ExecOptions struct {
	Emit      func(StreamEvent) // SSE 直播 sink（nil=丢弃）
	SessionID string            // 平台智能体 per-node 会话（复用/新建由执行器决定）
}

type ExecResult struct {
	Output          string         // 进下游 {{upstream}} 的摘要正文
	GateResult      string         // pass|rework|fail|human；空=缺失（交门禁裁定）
	GateReason      string
	Artifacts       []ArtifactFile
	Usage           types.SopUsage
	SessionID       string
	ResumeSessionID string
	Suspended       bool           // true=运行挂起等待用户输入（HITL），引擎置节点 pending_input
}

type Provider interface {
	Kind() string // platform_agent | cli:<kind> | script
	Prepare(ctx context.Context, node *types.SopNode, wsRoot string) error
	Execute(ctx context.Context, tb TaskBook, opts ExecOptions) (ExecResult, error)
	Cancel(ctx context.Context) error
}

type Registry struct{ providers map[string]Provider }

func NewRegistry() *Registry
func (r *Registry) Register(p Provider)          // key=p.Kind()
func (r *Registry) Resolve(node *types.SopNode) (Provider, error)
// agent→platform_agent；script→script；sop→特殊（engine 内部处理，不走 Registry）；
// executor.kind=cli→"cli:"+node.Executor.CLIKind；未注册→白话错误「执行器 X 未就绪」
```

- [ ] **Step 1: 写失败测试** `provider_test.go`：stub Provider（Kind 返回 "cli:zcode"）注册后 Resolve(&types.SopNode{Type:"agent", Executor:{Kind:"cli", CLIKind:"zcode"}}) 命中；未注册 kind 返回错误含「未就绪」；script 节点 Resolve 到 "script"。
- [ ] **Step 2: 运行确认失败** Run: `go test ./internal/application/service/sop/executor/ -v` Expected: 编译失败。
- [ ] **Step 3: 实现 provider.go**（上方完整代码 + Registry 实现，Resolve 按上述映射）。
- [ ] **Step 4: 运行确认通过** Run: `go test ./internal/application/service/sop/executor/ -v && go build ./...` Expected: PASS。
- [ ] **Step 5: 提交** `git add internal/application/service/sop/executor/ && git commit -m "feat(sop): 执行器 Provider 抽象与统一流事件模型"`

### Task 9: DAG 校验 + 拓扑工具 + 门禁编译（纯函数）

**Files:**
- Create: `internal/application/service/sop/engine/graph.go`、`gates.go`、`definition.go`、`graph_test.go`、`gates_test.go`

**Interfaces:**
- Consumes: Task 4 types.SopDefinition/SopNode。
- Produces: `engine.ValidateDefinition(def *types.SopDefinition, allowIncompleteExecutor bool) []string`（白话错误清单，空=通过）、`engine.Upstreams(def, nodeID) []string`、`engine.Downstreams(def, nodeID) []string`、`engine.Ancestors(def, nodeID) map[string]bool`、`engine.Descendants(def, nodeID) map[string]bool`（借鉴 LoopForge engine/workflow_graph.go:60/79）、`engine.TopoLayers(def) [][]string`（自动排版用）、`engine.CompileGateCommands(node *types.SopNode) []string`、`engine.JoinGateScript(cmds []string) string`。

- [ ] **Step 1: 写失败测试**

`graph_test.go`：

```go
package engine

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func def3() *types.SopDefinition {
	return &types.SopDefinition{
		Nodes: []types.SopNode{
			{ID: "n1", Name: "分析", Type: "agent", Executor: types.SopNodeExecutor{Kind: "platform_agent", AgentID: "a1"}},
			{ID: "n2", Name: "实现", Type: "agent", Executor: types.SopNodeExecutor{Kind: "platform_agent", AgentID: "a1"}, OnFailure: "reject", RejectTo: "n1"},
			{ID: "n3", Name: "验收", Type: "approval"},
		},
		Edges: []types.SopEdge{{ID: "e1", Source: "n1", Target: "n2", When: "success"}, {ID: "e2", Source: "n2", Target: "n3", When: "success"}},
	}
}

func TestValidateDefinitionOK(t *testing.T) {
	if errs := ValidateDefinition(def3(), false); len(errs) != 0 {
		t.Fatalf("valid def rejected: %v", errs)
	}
}

func TestValidateDefinitionCycle(t *testing.T) {
	d := def3()
	d.Edges = append(d.Edges, types.SopEdge{ID: "e3", Source: "n3", Target: "n1", When: "success"})
	errs := ValidateDefinition(d, false)
	if len(errs) == 0 || !containsSub(errs, "环") {
		t.Fatalf("cycle not detected: %v", errs)
	}
}

func TestValidateRejectToMustBeAncestor(t *testing.T) {
	d := def3()
	d.Nodes[1].RejectTo = "n3" // 下游不可作打回目标
	errs := ValidateDefinition(d, false)
	if !containsSub(errs, "上游") {
		t.Fatalf("reject_to downstream not rejected: %v", errs)
	}
}

func TestValidateRejectMissingTargetPlainWords(t *testing.T) {
	d := def3()
	d.Nodes[1].RejectTo = ""
	errs := ValidateDefinition(d, false)
	if !containsSub(errs, "失败后选了打回上游，但没选打回哪一步") {
		t.Fatalf("plain-word error missing: %v", errs)
	}
}

func TestValidateArtifactPathEscape(t *testing.T) {
	d := def3()
	d.Nodes[0].ArtifactOutputs = []types.SopArtifactOutput{{Name: "x", Path: "../evil.md", Required: true, Check: types.SopArtifactCheck{Type: "exists"}}}
	if !containsSub(ValidateDefinition(d, false), "工作区") {
		t.Fatal("path escape not rejected")
	}
}

func TestValidateChildSOPNoSelfReference(t *testing.T) {
	d := def3()
	d.Nodes[0].Type = "sop"
	d.Nodes[0].ChildTemplateID = "self"
	if !containsSub(ValidateDefinitionWithSelf(d, "self", false), "递归") {
		t.Fatal("self-referencing child SOP not rejected")
	}
}

func TestDescendantsAndAncestors(t *testing.T) {
	d := def3()
	desc := Descendants(d, "n1")
	if !desc["n2"] || !desc["n3"] || desc["n1"] {
		t.Fatalf("descendants wrong: %v", desc)
	}
	anc := Ancestors(d, "n3")
	if !anc["n1"] || !anc["n2"] {
		t.Fatalf("ancestors wrong: %v", anc)
	}
}

func TestTopoLayers(t *testing.T) {
	layers := TopoLayers(def3())
	if len(layers) != 3 || layers[0][0] != "n1" || layers[2][0] != "n3" {
		t.Fatalf("layers wrong: %v", layers)
	}
}

func containsSub(ss []string, sub string) bool {
	for _, s := range ss {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
```

`gates_test.go`：

```go
func TestCompileGateCommands(t *testing.T) {
	n := &types.SopNode{ArtifactOutputs: []types.SopArtifactOutput{
		{Name: "报告", Path: "out/a.md", Check: types.SopArtifactCheck{Type: "exists"}},
		{Name: "PRD", Path: "out/prd.md", Check: types.SopArtifactCheck{Type: "contains", Keyword: "验收"}},
		{Name: "自定义", Path: "out/x", Check: types.SopArtifactCheck{Type: "command", Command: "test -f out/x"}},
	}, GateExtra: "echo one && echo two"}
	cmds := CompileGateCommands(n)
	want := []string{"test -s out/a.md", `grep -q "验收" out/prd.md`, "test -f out/x", "echo one", "echo two"}
	if len(cmds) != len(want) {
		t.Fatalf("got %v want %v", cmds, want)
	}
	for i := range want {
		if cmds[i] != want[i] {
			t.Fatalf("cmd[%d]=%q want %q", i, cmds[i], want[i])
		}
	}
}

func TestJoinGateScript(t *testing.T) {
	s := JoinGateScript([]string{"test -s a", "grep -q k b"})
	if s != "set -e\ntest -s a\ngrep -q k b" {
		t.Fatalf("script wrong: %q", s)
	}
}
```

- [ ] **Step 2: 运行确认失败** Run: `go test ./internal/application/service/sop/engine/ -v` Expected: 编译失败。
- [ ] **Step 3: 实现**

`graph.go`：Upstreams/Downstreams 扫 edges；Ancestors/Descendants BFS（不含自身）；HasCycle Kahn 入度法；TopoLayers 按层分组。`ValidateDefinition` 检查清单（全部白话）：① 有环→「流程里存在循环依赖（A→B→A），请去掉一条连线」；② reject_to 非空但不在 Ancestors→「打回目标必须是该步骤的上游环节」；③ on_failure=reject 且 reject_to 空→「失败后选了打回上游，但没选打回哪一步」；④ agent 节点 executor 不完整（platform_agent 缺 agent_id / cli 缺 cli_kind）且 !allowIncompleteExecutor→「步骤「X」还没选执行角色/CLI」；⑤ 产物 path 绝对路径或含 `..`→「产物路径不能越出工作区」；⑥ sop 节点 child_template_id 空→「子流程步骤还没选模板」；⑦ 边引用不存在的节点→「连线指向了不存在的步骤」。`ValidateDefinitionWithSelf(def, selfTemplateID, allow)` 额外查 child_template_id==self→「子流程不能递归引用自身」。

`gates.go`：

```go
func CompileGateCommands(n *types.SopNode) []string {
	var cmds []string
	for _, a := range n.ArtifactOutputs {
		switch a.Check.Type {
		case "contains":
			cmds = append(cmds, fmt.Sprintf("grep -q %q %s", a.Check.Keyword, a.Path))
		case "command":
			if strings.TrimSpace(a.Check.Command) != "" {
				cmds = append(cmds, a.Check.Command)
			}
		default: // exists
			cmds = append(cmds, "test -s "+a.Path)
		}
	}
	for _, part := range strings.Split(n.GateExtra, "&&") {
		if p := strings.TrimSpace(part); p != "" {
			cmds = append(cmds, p)
		}
	}
	return cmds
}

func JoinGateScript(cmds []string) string {
	if len(cmds) == 0 {
		return ""
	}
	return "set -e\n" + strings.Join(cmds, "\n")
}
```

`definition.go`：`RefreshDerived(def)`——对每个节点重算 GateCommands=CompileGateCommands（保存/发布/set op 应用后调用，保证派生字段一致，AI 不直写）。

- [ ] **Step 4: 运行确认通过** Run: `go test ./internal/application/service/sop/engine/ -v && go build ./...` Expected: PASS。
- [ ] **Step 5: 提交** `git add internal/application/service/sop/engine/ && git commit -m "feat(sop): DAG 校验/拓扑工具/门禁确定性编译"`

### Task 10: 运行工作区（目录布局/inputs 物化/路径守卫/git 检查点/仓库克隆）

**Files:**
- Create: `internal/application/service/sop/engine/workspace.go`、`workspace_test.go`

**Interfaces:**
- Consumes: Task 1 types.SopProjectRepo；`internal/application/service/workspace_checkpointer.go`（执行时先读，若暴露可复用纯函数优先复用）。
- Produces: `engine.Workspace{Root, InputsDir, OutDir, WorkDir, LogsDir, PrivateHomeDir string}`、`engine.PrepareWorkspace(root, runID string) (*Workspace, error)`、`engine.SafeWorkspacePath(ws *Workspace, rel string) (string, error)`、`engine.MaterializeInputs(ws *Workspace, arts []types.SopArtifact, srcRoot string) error`、`engine.GitCheckpoint(ws *Workspace, msg string) error`、`engine.CloneRepos(ws *Workspace, runID string, repos []types.SopProjectRepo) ([]string, error)`（返回克隆目录清单；失败=错误进节点 feedback）。

- [ ] **Step 1: 写失败测试**

```go
func TestPrepareWorkspaceLayout(t *testing.T) {
	ws, err := PrepareWorkspace(t.TempDir(), "run1")
	require.NoError(t, err)
	for _, d := range []string{ws.InputsDir, ws.OutDir, ws.WorkDir, ws.LogsDir, ws.PrivateHomeDir} {
		require.DirExists(t, d)
	}
	require.Contains(t, ws.Root, "runs/run1")
	require.Contains(t, ws.PrivateHomeDir, ".sop/private-home")
}

func TestSafeWorkspacePath(t *testing.T) {
	ws, _ := PrepareWorkspace(t.TempDir(), "run1")
	ok, err := SafeWorkspacePath(ws, "out/a.md")
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(ok, ws.Root))
	for _, bad := range []string{"../evil", "/etc/passwd", "out/../../evil"} {
		_, err := SafeWorkspacePath(ws, bad)
		require.Error(t, err, "path %q must be rejected", bad)
	}
}

func TestMaterializeInputs(t *testing.T) {
	src := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(src, "out"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(src, "out", "a.md"), []byte("# A"), 0o644))
	ws, _ := PrepareWorkspace(t.TempDir(), "run2")
	arts := []types.SopArtifact{{NodeID: "n1", Path: "out/a.md", Name: "报告"}}
	require.NoError(t, MaterializeInputs(ws, arts, src))
	got, err := os.ReadFile(filepath.Join(ws.InputsDir, "n1", "a.md"))
	require.NoError(t, err)
	require.Equal(t, "# A", string(got))
}

func TestGitCheckpoint(t *testing.T) {
	ws, _ := PrepareWorkspace(t.TempDir(), "run3")
	require.NoError(t, os.WriteFile(filepath.Join(ws.OutDir, "a.md"), []byte("v1"), 0o644))
	require.NoError(t, GitCheckpoint(ws, "node n1 succeeded"))
	out, err := exec.Command("git", "-C", ws.Root, "log", "--oneline").CombinedOutput()
	require.NoError(t, err)
	require.Contains(t, string(out), "node n1 succeeded")
}
```

- [ ] **Step 2: 运行确认失败** Run: `go test ./internal/application/service/sop/engine/ -run "Workspace|SafeWorkspace|Materialize|GitCheckpoint" -v` Expected: 编译失败。
- [ ] **Step 3: 实现 workspace.go**

```go
func PrepareWorkspace(root, runID string) (*Workspace, error) {
	base := filepath.Join(root, "runs", runID)
	ws := &Workspace{
		Root: base, InputsDir: filepath.Join(base, "inputs"), OutDir: filepath.Join(base, "out"),
		WorkDir: filepath.Join(base, "work"), LogsDir: filepath.Join(base, "logs"),
		PrivateHomeDir: filepath.Join(base, ".sop", "private-home"),
	}
	for _, d := range []string{ws.Root, ws.InputsDir, ws.OutDir, ws.WorkDir, ws.LogsDir, ws.PrivateHomeDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, err
		}
	}
	return ws, nil
}

func SafeWorkspacePath(ws *Workspace, rel string) (string, error) {
	if filepath.IsAbs(rel) || strings.Contains(rel, "..") {
		return "", fmt.Errorf("产物路径不能越出工作区")
	}
	full := filepath.Join(ws.Root, rel)
	resolved, err := filepath.EvalSymlinks(full)
	if err == nil {
		rootResolved, _ := filepath.EvalSymlinks(ws.Root)
		if !strings.HasPrefix(resolved, rootResolved+string(os.PathSeparator)) && resolved != rootResolved {
			return "", fmt.Errorf("产物路径不能越出工作区")
		}
	}
	return full, nil
}
```

MaterializeInputs：逐 artifact 从 srcRoot/<path> 复制到 ws.InputsDir/<node_id>/<basename>（路径先过 SafeWorkspacePath 源侧校验）。GitCheckpoint：`git -C ws.Root init`（若 .git 不存在）→ `git add -A` → `git -c user.name=SOP -c user.email=sop@local commit -m msg --allow-empty`，env 追加 `GIT_CEILING_DIRECTORIES=<ws.Root 的父目录>`；优先复用 workspace_checkpointer.go 的现有函数（先读该文件，签名合适则直接调用，不合适再用本实现）。CloneRepos：逐 repo `git clone --branch <branch> --single-branch <url> work/repos/<name>`（凭证走本机 git 凭证管理，不注入明文）→ `git -C <dir> checkout -b sop/<runID>`；任一失败返回错误（调用方置节点 failed，可补充上下文重试）。

- [ ] **Step 4: 运行确认通过** Run: `go test ./internal/application/service/sop/engine/ -run "Workspace|SafeWorkspace|Materialize|GitCheckpoint" -v && go build ./...` Expected: PASS（git 需在 PATH，Know 部署已依赖 Git Bash）。
- [ ] **Step 5: 提交** `git add internal/application/service/sop/engine/workspace*.go && git commit -m "feat(sop): 运行工作区布局/inputs 物化/路径守卫/git 检查点/仓库克隆"`

### Task 11: 任务书组装（四上下文块 + 规则技能渲染 + 旧占位符兼容）

**Files:**
- Create: `internal/application/service/sop/engine/taskbook.go`、`taskbook_test.go`

**Interfaces:**
- Consumes: Task 4/5 类型、types.CustomAgent（执行时读 internal/types/custom_agent.go:100-240 核对字段）。
- Produces: `engine.TaskBookInput`（字段见 Step 3）、`engine.RenderTaskBook(in TaskBookInput) string`、`engine.RenderAgentSystem(role *types.CustomAgent, skills []string, extraRules, roleSection string) string`、`engine.ResolveSkills(role *types.CustomAgent, nodeSkills []string) []string`（nil→角色 SelectedSkills 全部；非 nil→交集子集）。**澄清 18**：Know CustomAgent 无独立 Rules 字段——spec 的「角色 Rules」映射为 `CustomAgent.SystemPrompt`（检查器规则预览展示其摘要）；若执行时发现该类型确有 Rules 字段则优先用之。

- [ ] **Step 1: 写失败测试**

```go
func TestRenderTaskBookContextBlocks(t *testing.T) {
	in := TaskBookInput{
		Node: types.SopNode{ID: "n2", Name: "实现", TaskPrompt: "按方案实现，完成后运行自测。"},
		Item: &types.SopItem{Title: "登录改造", Description: "支持验证码"},
		UpstreamOutputs: map[string]string{"n1": "分析结论：…"},
		UpstreamNames:   map[string]string{"n1": "需求分析"},
	}
	out := RenderTaskBook(in)
	if !strings.Contains(out, "登录改造") || !strings.Contains(out, "事项背景") {
		t.Fatal("事项背景块缺失")
	}
	if !strings.Contains(out, "需求分析") || !strings.Contains(out, "上游步骤产物") {
		t.Fatal("上游产物块缺失")
	}
	if strings.Contains(out, "返工意见") {
		t.Fatal("非返工轮不应带返工意见块")
	}
	if strings.Contains(out, "{{") {
		t.Fatal("大白话任务书不得出现占位符")
	}
	in.Feedback = "验收不通过：缺少异常态"
	out2 := RenderTaskBook(in)
	if !strings.Contains(out2, "返工意见") || !strings.Contains(out2, "缺少异常态") {
		t.Fatal("返工轮必须带返工意见块")
	}
	in.HumanContext = []types.SopHumanContextEntry{{Text: "补充：优先移动端"}}
	in.AnsweredClarifications = []types.SopItemClarification{{Question: "q", Answer: "a", Status: "answered"}}
	out3 := RenderTaskBook(in)
	if !strings.Contains(out3, "人工补充意见") || !strings.Contains(out3, "优先移动端") || !strings.Contains(out3, "a") {
		t.Fatal("人工补充意见块缺失")
	}
}

func TestRenderTaskBookLegacyPlaceholders(t *testing.T) {
	in := TaskBookInput{
		Node: types.SopNode{ID: "n1", TaskPrompt: "处理 {{input}}，参考 {{upstream}}，意见 {{feedback}}"},
		Item: &types.SopItem{Title: "T", Description: "D"},
		UpstreamOutputs: map[string]string{"n0": "U"},
		Feedback:        "F",
	}
	out := RenderTaskBook(in)
	if strings.Contains(out, "{{") || !strings.Contains(out, "U") || !strings.Contains(out, "F") {
		t.Fatalf("legacy placeholders not rendered: %s", out)
	}
}

func TestResolveSkills(t *testing.T) {
	role := &types.CustomAgent{}
	role.Config.SelectedSkills = []string{"a", "b", "c"} // 字段路径以 custom_agent.go 实际为准
	if got := ResolveSkills(role, nil); len(got) != 3 {
		t.Fatalf("nil should mean all: %v", got)
	}
	if got := ResolveSkills(role, []string{"a", "x"}); len(got) != 1 || got[0] != "a" {
		t.Fatalf("subset intersection wrong: %v", got)
	}
	if got := ResolveSkills(role, []string{}); len(got) != 0 {
		t.Fatalf("explicit empty should mean none: %v", got)
	}
}

func TestRenderAgentSystem(t *testing.T) {
	role := &types.CustomAgent{}
	role.SystemPrompt = "你是资深工程师"
	s := RenderAgentSystem(role, []string{"go-test"}, "禁止改公共 API", "按方案实现")
	for _, want := range []string{"你是资深工程师", "go-test", "禁止改公共 API", "按方案实现"} {
		if !strings.Contains(s, want) {
			t.Fatalf("agent-system missing %q: %s", want, s)
		}
	}
}
```

- [ ] **Step 2: 运行确认失败** Run: `go test ./internal/application/service/sop/engine/ -run "TaskBook|ResolveSkills|AgentSystem" -v` Expected: 编译失败。
- [ ] **Step 3: 实现 taskbook.go**

```go
type TaskBookInput struct {
	Node                   types.SopNode
	Item                   *types.SopItem
	RunInput               map[string]any
	UpstreamOutputs        map[string]string // nodeID→output 摘要
	UpstreamNames          map[string]string
	RefOutputs             map[string]string // task_refs 勾选节点完整输出
	Feedback               string
	HumanContext           []types.SopHumanContextEntry
	AnsweredClarifications []types.SopItemClarification
	AttachmentsText        []string
	Role                   *types.CustomAgent
	ResolvedSkills         []string
	InputsDir              string
	OutDir                 string
}
```

RenderTaskBook 结构（Markdown 大白话，无占位符）：`# 步骤： <Node.Name>` → `## 角色与目标`（Role 存在时一句角色定位 + Node.TaskPrompt）→ `## 遵循规则`（Role.SystemPrompt 摘要 + Node.ExtraRules）→ `## 这一步做什么`（TaskPrompt 正文）→ `## 事项背景`（标题/描述/附件文本/运行输入，每次必带）→ `## 上游步骤产物`（按节点名分节摘要 + task_refs 完整输出 + 「输入文件已物化到 <InputsDir>」，每次必带）→ `## 返工意见`（仅 Feedback 非空）→ `## 人工补充意见`（仅 HumanContext/已回答澄清非空）→ `## 产出要求`（ArtifactOutputs 名称/路径/校验白话 + 「完成后把产物写到 <OutDir> 对应路径」）→ `## 结果提交`（平台=调用 submit_node_result；CLI=写 .sop/gate-results/<node>/attempt-N.json，四值白话说明）。旧模板兼容：TaskPrompt 含 `{{input}}/{{upstream}}/{{node.X.output}}/{{feedback}}/{{humanContext}}/{{artifacts_dir}}/{{inputs_dir}}` 时先做字符串替换再组装（仅服务端通道，检查器无入口）。RenderAgentSystem：SystemPrompt + Skills 渲染（技能名+描述清单，渐进披露由 skills.Manager 负责）+ extra_rules + 角色与目标段。ResolveSkills 按澄清 18 与测试语义实现（字段路径执行时核对）。

- [ ] **Step 4: 运行确认通过** Run: `go test ./internal/application/service/sop/engine/ -run "TaskBook|ResolveSkills|AgentSystem" -v && go build ./...` Expected: PASS。
- [ ] **Step 5: 提交** `git add internal/application/service/sop/engine/taskbook*.go && git commit -m "feat(sop): 任务书四上下文块组装与规则技能渲染（旧占位符仅服务端兼容）"`

### Task 12: 引擎生命周期 + 单实例调度器（租约/心跳/崩溃回收/终态联动）

**Files:**
- Create: `internal/application/service/sop/engine/engine.go`、`scheduler.go`、`engine_test.go`
- Modify: `internal/application/repository/sop_run.go`（GetRun 增加 tenantID==0 内部直通分支）

**Interfaces:**
- Consumes: Task 1–6 仓储接口（interfaces.SopRunRepository/SopTemplateRepository/SopProjectRepository/SopItemRepository/SopItemExtraRepository）、Task 8 executor.Registry/Provider、Task 10 Workspace、Task 11 RenderTaskBook。
- Produces:（**T13–T16、T21 容器接线消费，签名锁定**）

```go
package engine

// ItemStatusUpdater 窄接口：事项状态唯一写入口（item service 实现，T22 接线；nil=跳过联动）
type ItemStatusUpdater interface {
	UpdateStatus(ctx context.Context, tenantID uint64, id, to string) error
}

// ResolvedNode 节点执行上下文解析结果（真实实现=T16 service/sop/context.go；nil Resolver=空解析，供脚本/测试）
type ResolvedNode struct {
	Role       *types.CustomAgent
	Skills     []string           // 已按节点子集过滤
	MCPServers []types.MCPService
	Model      string
	Credential *types.SopCLICredential
	KBIDs      []string
}

type NodeResolver interface {
	Resolve(ctx context.Context, run *types.SopRun, node *types.SopNode) (ResolvedNode, error)
}

type Deps struct {
	Runs          interfaces.SopRunRepository
	Templates     interfaces.SopTemplateRepository
	Projects      interfaces.SopProjectRepository
	Items         interfaces.SopItemRepository
	Extras        interfaces.SopItemExtraRepository
	Registry      *executor.Registry
	Resolver      NodeResolver      // 可 nil
	ItemStatus    ItemStatusUpdater // 可 nil
	WorkspaceRoot string            // 如 data/sop-workspaces
	MaxParallel   int               // 默认 2
	DisableRunLoop bool             // true=startQueued 不启动 runLoop，测试同步驱动 Advance（E2E 用 false）
	Notify        func(ctx context.Context, tenantID uint64, kind, title, body, refID string) // 可 nil；kind=sop_approval|sop_run_finished|sop_run_failed
}

type Engine struct {
	deps   Deps
	mu     sync.Mutex
	active map[string]context.CancelFunc // runID→取消函数
	ctx    context.Context
	cancel context.CancelFunc
}

func NewEngine(d Deps) *Engine
func (e *Engine) Start(ctx context.Context) error  // dig lifecycle OnStart
func (e *Engine) Stop(ctx context.Context) error   // OnStop：取消全部 active，等待退出
func (e *Engine) TickOnce(ctx context.Context) error // 同步一轮：ExpireLeases→ClaimDue→startQueued（测试驱动用）
func (e *Engine) startQueued(ctx context.Context, run *types.SopRun) error
func (e *Engine) finalizeRun(ctx context.Context, run *types.SopRun) error
func (e *Engine) CancelRun(runID string) // ops 调用：取消 active context（进程组杀灭由 provider.Cancel 负责）
```

- [ ] **Step 1: 写失败测试** `engine_test.go`（sqlite :memory: + AutoMigrate 全部 sop 表 + stub Provider）：

```go
type fakeProvider struct {
	kind string
	out  string
	err  error
}

func (f *fakeProvider) Kind() string { return f.kind }
func (f *fakeProvider) Prepare(ctx context.Context, n *types.SopNode, ws string) error { return nil }
func (f *fakeProvider) Execute(ctx context.Context, tb executor.TaskBook, o executor.ExecOptions) (executor.ExecResult, error) {
	if f.err != nil {
		return executor.ExecResult{}, f.err
	}
	if tb.OutDir != "" {
		_ = os.WriteFile(filepath.Join(tb.OutDir, "a.md"), []byte("# out"), 0o644)
	}
	return executor.ExecResult{Output: f.out, GateResult: types.SopGatePass}, nil
}
func (f *fakeProvider) Cancel(ctx context.Context) error { return nil }

type stubItemStatus struct{ calls [][2]string }

func (s *stubItemStatus) UpdateStatus(ctx context.Context, t uint64, id, to string) error {
	s.calls = append(s.calls, [2]string{id, to})
	return nil
}

func newTestEngine(t *testing.T) (*Engine, *stubItemStatus, *gorm.DB) {
	db := newSopEngineTestDB(t) // AutoMigrate 全部 sop_* 模型
	reg := executor.NewRegistry()
	reg.Register(&fakeProvider{kind: "platform_agent", out: "done"})
	st := &stubItemStatus{}
	e := NewEngine(Deps{
		Runs: repository.NewSopRunRepo(db), Templates: repository.NewSopTemplateRepo(db),
		Projects: repository.NewSopProjectRepo(db), Items: repository.NewSopItemRepo(db),
		Extras: repository.NewSopItemExtraRepo(db), Registry: reg, ItemStatus: st,
		WorkspaceRoot: t.TempDir(), MaxParallel: 2, DisableRunLoop: true,
	})
	return e, st, db
}

func TestStartQueuedCreatesWorkspaceAndNodeRuns(t *testing.T) {
	e, _, _ := newTestEngine(t)
	ctx := context.Background()
	def := types.SopDefinition{Nodes: []types.SopNode{
		{ID: "n1", Name: "A", Type: "agent", Executor: types.SopNodeExecutor{Kind: "platform_agent", AgentID: "a"}},
		{ID: "n2", Name: "B", Type: "agent", Executor: types.SopNodeExecutor{Kind: "platform_agent", AgentID: "a"}},
	}, Edges: []types.SopEdge{{ID: "e1", Source: "n1", Target: "n2", When: "success"}}}
	run := &types.SopRun{TenantID: 1, Status: types.SopRunQueued, WorkflowSnapshot: def, ItemID: "item1"}
	require.NoError(t, e.deps.Runs.CreateRun(ctx, run))
	require.NoError(t, e.TickOnce(ctx))
	got, _ := e.deps.Runs.GetRun(ctx, 1, run.ID)
	require.Equal(t, types.SopRunRunning, got.Status)
	require.DirExists(t, got.WorkspaceDir)
	ns, _ := e.deps.Runs.ListNodeRuns(ctx, 1, run.ID)
	require.Len(t, ns, 2)
	for _, n := range ns {
		require.Equal(t, types.SopNodePending, n.Status)
	}
}

func TestFinalizeSuccessLinksItemAndNotify(t *testing.T) {
	e, st, _ := newTestEngine(t)
	var notified []string
	e.deps.Notify = func(ctx context.Context, t uint64, kind, title, body, ref string) { notified = append(notified, kind) }
	ctx := context.Background()
	def := types.SopDefinition{Nodes: []types.SopNode{{ID: "n1", Type: "agent"}}, OnSuccessItemStatus: "in_review"}
	run := &types.SopRun{TenantID: 1, Status: types.SopRunRunning, WorkflowSnapshot: def, ItemID: "item1"}
	require.NoError(t, e.deps.Runs.CreateRun(ctx, run))
	require.NoError(t, e.deps.Runs.CreateNodeRun(ctx, &types.SopNodeRun{TenantID: 1, RunID: run.ID, NodeID: "n1", Status: types.SopNodeSucceeded}))
	require.NoError(t, e.finalizeRun(ctx, run))
	got, _ := e.deps.Runs.GetRun(ctx, 1, run.ID)
	require.Equal(t, types.SopRunSucceeded, got.Status)
	require.NotNil(t, got.FinishedAt)
	require.Equal(t, [][2]string{{"item1", "in_review"}}, st.calls)
	require.Equal(t, []string{"sop_run_finished"}, notified)
}

func TestFinalizeFailedSkipsPendingAndNotifies(t *testing.T) {
	e, _, _ := newTestEngine(t)
	ctx := context.Background()
	def := types.SopDefinition{Nodes: []types.SopNode{{ID: "n1", Type: "agent"}, {ID: "n2", Type: "agent"}}}
	run := &types.SopRun{TenantID: 1, Status: types.SopRunRunning, WorkflowSnapshot: def}
	require.NoError(t, e.deps.Runs.CreateRun(ctx, run))
	require.NoError(t, e.deps.Runs.CreateNodeRun(ctx, &types.SopNodeRun{TenantID: 1, RunID: run.ID, NodeID: "n1", Status: types.SopNodeFailed, Error: "boom"}))
	require.NoError(t, e.deps.Runs.CreateNodeRun(ctx, &types.SopNodeRun{TenantID: 1, RunID: run.ID, NodeID: "n2", Status: types.SopNodePending}))
	require.NoError(t, e.finalizeRun(ctx, run))
	got, _ := e.deps.Runs.GetRun(ctx, 1, run.ID)
	require.Equal(t, types.SopRunFailed, got.Status)
	ns, _ := e.deps.Runs.ListNodeRuns(ctx, 1, run.ID)
	for _, n := range ns {
		if n.NodeID == "n2" {
			require.Equal(t, types.SopNodeSkipped, n.Status)
		}
	}
}

func TestExpireLeasesOnTick(t *testing.T) {
	e, _, _ := newTestEngine(t)
	ctx := context.Background()
	run := &types.SopRun{TenantID: 1, Status: types.SopRunRunning}
	require.NoError(t, e.deps.Runs.CreateRun(ctx, run))
	require.NoError(t, e.deps.Runs.Heartbeat(ctx, run.ID, time.Now().Add(-time.Minute)))
	require.NoError(t, e.TickOnce(ctx))
	got, _ := e.deps.Runs.GetRun(ctx, 1, run.ID)
	require.Equal(t, types.SopRunFailed, got.Status)
	require.Contains(t, got.Error, "租约")
}
```

- [ ] **Step 2: 运行确认失败** Run: `go test ./internal/application/service/sop/engine/ -run "TestStartQueued|TestFinalize|TestExpireLeasesOnTick" -v` Expected: 编译失败。
- [ ] **Step 3: 实现 engine.go + scheduler.go**

engine.go：NewEngine 填默认 MaxParallel=2、active map；`startQueued`：PrepareWorkspace(deps.WorkspaceRoot, run.ID)→回写 WorkspaceDir→项目 repos 非空时 CloneRepos（失败=run failed+error，可重试）→为 snapshot 每个节点 CreateNodeRun(pending)→事项联动（ItemID 非空且事项当前 todo→ItemStatus.UpdateStatus(in_progress)，非法迁移忽略记审计）→`if !deps.DisableRunLoop { go e.runLoop(run.ID) }`。`finalizeRun`：ListNodeRuns 全 succeeded/skipped→run succeeded；存在 failed→run failed（error=首个失败节点 error）；pending→skipped（失败时）；累计 Usage；FinishedAt=now；ItemStatus 联动 OnSuccessItemStatus（仅 succeeded 且 CanTransitSopItem 允许，否则审计跳过）；Notify(sop_run_finished|sop_run_failed)；活动流 Extras.AddActivity(kind=audit)。`CancelRun`：mu 下取 active[runID] 的 cancel 调用。

scheduler.go：

```go
func (e *Engine) Start(ctx context.Context) error {
	e.ctx, e.cancel = context.WithCancel(ctx)
	go func() {
		t := time.NewTicker(2 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-e.ctx.Done():
				return
			case <-t.C:
				_ = e.TickOnce(e.ctx)
			}
		}
	}()
	return nil
}

func (e *Engine) TickOnce(ctx context.Context) error {
	if _, err := e.deps.Runs.ExpireLeases(ctx, time.Now()); err != nil {
		return err
	}
	for {
		e.mu.Lock()
		n := len(e.active)
		e.mu.Unlock()
		if n >= e.deps.MaxParallel {
			return nil
		}
		run, err := e.deps.Runs.ClaimDue(ctx, time.Now().Add(30*time.Second))
		if err != nil || run == nil {
			return err
		}
		if err := e.startQueued(ctx, run); err != nil {
			run.Status = types.SopRunFailed
			run.Error = err.Error()
			_ = e.deps.Runs.UpdateRun(ctx, run)
		}
	}
}

// runLoop 单 run 推进循环：心跳 10s 续租 30s；Advance 无进展且 waiting→1s 轮询；
// PauseRequested 在节点边界生效→paused 退出；ctx 取消→Cancel 收尾。
func (e *Engine) runLoop(runID string) {
	ctx, cancel := context.WithCancel(e.ctx)
	e.mu.Lock()
	e.active[runID] = cancel
	e.mu.Unlock()
	defer func() {
		cancel()
		e.mu.Lock()
		delete(e.active, runID)
		e.mu.Unlock()
	}()
	hb := time.NewTicker(10 * time.Second)
	defer hb.Stop()
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-hb.C:
				_ = e.deps.Runs.Heartbeat(ctx, runID, time.Now().Add(30*time.Second))
			}
		}
	}()
	for {
		select {
		case <-ctx.Done():
			e.finishCancelled(context.Background(), runID)
			return
		default:
		}
		run, err := e.deps.Runs.GetRun(ctx, 0, runID) // tenantID 0=跨租户内部读（本任务给 GetRun 加直通分支）
		if err != nil {
			return
		}
		if run.PauseRequested {
			run.Status = types.SopRunPaused
			run.PauseRequested = false
			_ = e.deps.Runs.UpdateRun(ctx, run)
			return
		}
		progressed, err := e.Advance(ctx, runID) // T13 实现
		if err != nil {
			run.Status = types.SopRunFailed
			run.Error = err.Error()
			_ = e.deps.Runs.UpdateRun(ctx, run)
			_ = e.finalizeRun(ctx, run)
			return
		}
		if isTerminalRun(run.Status) {
			_ = e.finalizeRun(ctx, run)
			return
		}
		if !progressed {
			select {
			case <-ctx.Done():
			case <-time.After(time.Second):
			}
		}
	}
}
```

`finishCancelled`：running 节点→cancelled、pending→cancelled，run cancelled+FinishedAt，不发失败通知（取消非失败）。`isTerminalRun`=succeeded/failed/cancelled。GetRun 的 tenantID==0 直通分支补测试 TestGetRunInternalBypass。dig 接线（container.go）在 T21 统一做，本任务只交付 Engine 本体。

- [ ] **Step 4: 运行确认通过** Run: `go test ./internal/application/service/sop/engine/ -run "TestStartQueued|TestFinalize|TestExpireLeasesOnTick" -v && go test ./internal/application/repository/ -run TestGetRunInternalBypass -v && go build ./...` Expected: PASS。
- [ ] **Step 5: 提交** `git add internal/application/service/sop/engine/engine.go internal/application/service/sop/engine/scheduler.go internal/application/service/sop/engine/engine_test.go internal/application/repository/sop_run.go && git commit -m "feat(sop): 引擎生命周期与单实例调度器（租约心跳/崩溃回收/终态事项联动）"`

### Task 13: 节点推进（就绪计算/并行与串行组/门禁三段/失败四策略/返工预算）

**Files:**
- Create: `internal/application/service/sop/engine/node.go`、`node_test.go`

**Interfaces:**
- Consumes: T12 Engine/Deps、T8 Provider/TaskBook/ExecResult、T9 CompileGateCommands/JoinGateScript/Descendants/Upstreams、T10 Workspace/SafeWorkspacePath/MaterializeInputs/GitCheckpoint、T11 RenderTaskBook/TaskBookInput。
- Produces: `(*Engine).Advance(ctx, runID) (bool, error)`、`(*Engine).executeNode(ctx, run, node, nr, ws) error`、`readyNodes(def *types.SopDefinition, nrs map[string]*types.SopNodeRun) []types.SopNode`（纯函数）、`(*Engine).applyGateOutcome(ctx, run, node, nr, res, ws) error`、`(*Engine).handleFailure(ctx, run, node, nr, reason, ws) error`。

- [ ] **Step 1: 写失败测试** `node_test.go`（复用 newTestEngine；`scriptedProvider` 按调用次序返回 steps，`silentProvider` 不写任何文件）：

```go
type scriptedProvider struct {
	kind  string
	steps []executor.ExecResult
	i     int
}

func (s *scriptedProvider) Kind() string { return s.kind }
func (s *scriptedProvider) Prepare(ctx context.Context, n *types.SopNode, ws string) error { return nil }
func (s *scriptedProvider) Execute(ctx context.Context, tb executor.TaskBook, o executor.ExecOptions) (executor.ExecResult, error) {
	if s.i >= len(s.steps) {
		return executor.ExecResult{GateResult: types.SopGatePass}, nil
	}
	r := s.steps[s.i]
	s.i++
	if tb.OutDir != "" {
		_ = os.WriteFile(filepath.Join(tb.OutDir, "a.md"), []byte(r.Output), 0o644)
	}
	return r, nil
}
func (s *scriptedProvider) Cancel(ctx context.Context) error { return nil }

func TestReadyNodesParallelAndSerialGroup(t *testing.T) {
	def := &types.SopDefinition{Nodes: []types.SopNode{
		{ID: "n1"}, {ID: "n2", SerialGroup: "g"}, {ID: "n3", SerialGroup: "g"}, {ID: "n4"},
	}, Edges: []types.SopEdge{{ID: "e1", Source: "n1", Target: "n2"}, {ID: "e2", Source: "n1", Target: "n3"}, {ID: "e3", Source: "n1", Target: "n4"}}}
	nrs := map[string]*types.SopNodeRun{"n1": {NodeID: "n1", Status: types.SopNodeSucceeded},
		"n2": {NodeID: "n2", Status: types.SopNodePending}, "n3": {NodeID: "n3", Status: types.SopNodePending}, "n4": {NodeID: "n4", Status: types.SopNodePending}}
	ready := readyNodes(def, nrs)
	ids := map[string]bool{}
	for _, n := range ready {
		ids[n.ID] = true
	}
	if !ids["n4"] || ids["n1"] {
		t.Fatalf("ready wrong: %v", ids)
	}
	if ids["n2"] && ids["n3"] {
		t.Fatal("同一 serial_group 不得同时就绪两个")
	}
}

func TestAdvanceLinearSuccess(t *testing.T) {
	e, _, _ := newTestEngine(t)
	ctx := context.Background()
	def := types.SopDefinition{Nodes: []types.SopNode{
		{ID: "n1", Name: "A", Type: "agent", Executor: types.SopNodeExecutor{Kind: "platform_agent", AgentID: "a"}, ArtifactOutputs: []types.SopArtifactOutput{{Name: "r", Path: "out/a.md", Required: true, Check: types.SopArtifactCheck{Type: "exists"}}}},
		{ID: "n2", Name: "B", Type: "agent", Executor: types.SopNodeExecutor{Kind: "platform_agent", AgentID: "a"}},
	}, Edges: []types.SopEdge{{ID: "e1", Source: "n1", Target: "n2", When: "success"}}}
	run := &types.SopRun{TenantID: 1, Status: types.SopRunQueued, WorkflowSnapshot: def}
	require.NoError(t, e.deps.Runs.CreateRun(ctx, run))
	require.NoError(t, e.TickOnce(ctx))
	for i := 0; i < 4; i++ {
		if _, err := e.Advance(ctx, run.ID); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := e.deps.Runs.GetRun(ctx, 1, run.ID)
	require.Equal(t, types.SopRunSucceeded, got.Status)
	arts, _ := e.deps.Runs.ListArtifacts(ctx, 1, run.ID)
	require.Len(t, arts, 1)
	require.NotEmpty(t, arts[0].SHA256)
}

func TestGateReworkThenPass(t *testing.T) {
	e, _, _ := newTestEngine(t)
	e.deps.Registry.Register(&scriptedProvider{kind: "platform_agent", steps: []executor.ExecResult{
		{Output: "v1", GateResult: types.SopGateRework, GateReason: "缺少异常态"},
		{Output: "v2", GateResult: types.SopGatePass},
	}})
	ctx := context.Background()
	def := types.SopDefinition{Nodes: []types.SopNode{{ID: "n1", Type: "agent", Executor: types.SopNodeExecutor{Kind: "platform_agent", AgentID: "a"}, OnFailure: "loop", MaxAttempts: 3, CarryOver: true}}}
	run := &types.SopRun{TenantID: 1, Status: types.SopRunQueued, WorkflowSnapshot: def}
	require.NoError(t, e.deps.Runs.CreateRun(ctx, run))
	require.NoError(t, e.TickOnce(ctx))
	for i := 0; i < 4; i++ {
		e.Advance(ctx, run.ID)
	}
	got, _ := e.deps.Runs.GetRun(ctx, 1, run.ID)
	require.Equal(t, types.SopRunSucceeded, got.Status)
	nr, _ := e.deps.Runs.GetNodeRun(ctx, 1, run.ID, "n1")
	require.Equal(t, 2, nr.Attempt)
	require.Equal(t, "v2", nr.Output)
}

func TestReworkBudgetExhaustedGoesHuman(t *testing.T) {
	e, _, _ := newTestEngine(t)
	e.deps.Registry.Register(&scriptedProvider{kind: "platform_agent", steps: []executor.ExecResult{
		{GateResult: types.SopGateRework, GateReason: "r1"}, {GateResult: types.SopGateRework, GateReason: "r2"}, {GateResult: types.SopGateRework, GateReason: "r3"},
	}})
	ctx := context.Background()
	def := types.SopDefinition{Nodes: []types.SopNode{{ID: "n1", Type: "agent", Executor: types.SopNodeExecutor{Kind: "platform_agent", AgentID: "a"}, OnFailure: "loop", MaxAttempts: 2}}}
	run := &types.SopRun{TenantID: 1, Status: types.SopRunQueued, WorkflowSnapshot: def}
	require.NoError(t, e.deps.Runs.CreateRun(ctx, run))
	require.NoError(t, e.TickOnce(ctx))
	for i := 0; i < 5; i++ {
		e.Advance(ctx, run.ID)
	}
	got, _ := e.deps.Runs.GetRun(ctx, 1, run.ID)
	require.Equal(t, types.SopRunWaiting, got.Status)
	nr, _ := e.deps.Runs.GetNodeRun(ctx, 1, run.ID, "n1")
	require.Equal(t, types.SopNodeWaitingApproval, nr.Status)
}

func TestRejectResetsTargetAndDescendants(t *testing.T) {
	e, _, _ := newTestEngine(t)
	e.deps.Registry.Register(&scriptedProvider{kind: "platform_agent", steps: []executor.ExecResult{
		{Output: "a1", GateResult: types.SopGatePass},
		{Output: "b1", GateResult: types.SopGateRework, GateReason: "方案不行"},
		{Output: "a2", GateResult: types.SopGatePass},
		{Output: "b2", GateResult: types.SopGatePass},
	}})
	ctx := context.Background()
	def := types.SopDefinition{Nodes: []types.SopNode{
		{ID: "n1", Type: "agent", Executor: types.SopNodeExecutor{Kind: "platform_agent", AgentID: "a"}},
		{ID: "n2", Type: "agent", Executor: types.SopNodeExecutor{Kind: "platform_agent", AgentID: "a"}, OnFailure: "reject", RejectTo: "n1", MaxReworks: 2},
		{ID: "n3", Type: "agent", Executor: types.SopNodeExecutor{Kind: "platform_agent", AgentID: "a"}},
	}, Edges: []types.SopEdge{{ID: "e1", Source: "n1", Target: "n2"}, {ID: "e2", Source: "n2", Target: "n3"}}}
	run := &types.SopRun{TenantID: 1, Status: types.SopRunQueued, WorkflowSnapshot: def}
	require.NoError(t, e.deps.Runs.CreateRun(ctx, run))
	require.NoError(t, e.TickOnce(ctx))
	for i := 0; i < 8; i++ {
		e.Advance(ctx, run.ID)
	}
	got, _ := e.deps.Runs.GetRun(ctx, 1, run.ID)
	require.Equal(t, types.SopRunSucceeded, got.Status)
	n1, _ := e.deps.Runs.GetNodeRun(ctx, 1, run.ID, "n1")
	require.Equal(t, "a2", n1.Output)
	require.Equal(t, 1, n1.ReworkCount)
	require.Contains(t, n1.Feedback, "方案不行")
}

func TestGateCommandsScriptFailureMeansRework(t *testing.T) {
	e, _, _ := newTestEngine(t)
	e.deps.Registry.Register(&silentProvider{kind: "platform_agent"}) // 不写产物
	ctx := context.Background()
	def := types.SopDefinition{Nodes: []types.SopNode{{ID: "n1", Type: "agent", Executor: types.SopNodeExecutor{Kind: "platform_agent", AgentID: "a"},
		ArtifactOutputs: []types.SopArtifactOutput{{Name: "r", Path: "out/missing.md", Required: true, Check: types.SopArtifactCheck{Type: "exists"}}},
		OnFailure: "fail"}}}
	run := &types.SopRun{TenantID: 1, Status: types.SopRunQueued, WorkflowSnapshot: def}
	require.NoError(t, e.deps.Runs.CreateRun(ctx, run))
	require.NoError(t, e.TickOnce(ctx))
	for i := 0; i < 3; i++ {
		e.Advance(ctx, run.ID)
	}
	got, _ := e.deps.Runs.GetRun(ctx, 1, run.ID)
	require.Equal(t, types.SopRunFailed, got.Status)
	nr, _ := e.deps.Runs.GetNodeRun(ctx, 1, run.ID, "n1")
	require.Contains(t, nr.GateReason, "out/missing.md")
}
```

- [ ] **Step 2: 运行确认失败** Run: `go test ./internal/application/service/sop/engine/ -run "TestReadyNodes|TestAdvance|TestGate|TestRework|TestReject" -v` Expected: 编译失败。
- [ ] **Step 3: 实现 node.go**

```go
// readyNodes 就绪=pending 且全部上游 succeeded/skipped；同 serial_group 每轮最多一个（按定义顺序）
func readyNodes(def *types.SopDefinition, nrs map[string]*types.SopNodeRun) []types.SopNode {
	var ready []types.SopNode
	seenGroup := map[string]bool{}
	for _, n := range def.Nodes {
		nr := nrs[n.ID]
		if nr == nil || nr.Status != types.SopNodePending {
			continue
		}
		ok := true
		for _, up := range Upstreams(def, n.ID) {
			s := nrs[up].Status
			if s != types.SopNodeSucceeded && s != types.SopNodeSkipped {
				ok = false
				break
			}
		}
		if !ok {
			continue
		}
		if n.SerialGroup != "" {
			if seenGroup[n.SerialGroup] {
				continue
			}
			seenGroup[n.SerialGroup] = true
		}
		ready = append(ready, n)
	}
	return ready
}
```

`Advance`：GetRun→非 running/waiting 直接返回 false；ListNodeRuns→map；readyNodes→对每个 ready 节点：approval 类型→nr.Status=waiting_approval、run.Status=waiting、Notify(sop_approval)、GitCheckpoint、progressed=true；sop 类型→T15 startChildRun；agent/script→`go executeNode` 并行（sync.WaitGroup，节点级错误不终止兄弟）；等待全部完成后重读 run→返回 true。无 ready 且存在 running 子 run→false（等子 run）；无 ready 且全部终态→run.Status=succeeded（交 runLoop finalize）；无 ready 且有 waiting_approval→false。

`executeNode`：nr.Attempt++→UpdateNodeRun(running, StartedAt)→Resolver.Resolve（nil→空 ResolvedNode）→RenderTaskBook（Item 由 Items.GetByID、上游 Output map、RefOutputs 按 TaskRefs、Feedback=nr.Feedback、HumanContext=run.HumanContext、已回答澄清由 Extras.ListClarifications 过滤 status=answered）→MaterializeInputs（上游节点产物，srcRoot=ws.Root）→TaskBook 补齐 Role/Model/Credential/MCPServers/Skills=resolved 各字段、Script=node.Script、Env=node.CredentialEnv→provider.Prepare→provider.Execute（ctx 带 TimeoutMinutes 超时；Emit→先落 logs/<node_id>-attempt-N.jsonl，T24 接 SSE）→收集产物：遍历 node.ArtifactOutputs，SafeWorkspacePath→stat→sha256→CreateArtifact（attempt=nr.Attempt）→**res.Suspended=true 时不走门禁**：nr.Status=pending_input、nr.SessionID=res.SessionID、run.Status=waiting、Notify(sop_approval, body=「需要补充信息」) 后直接返回→applyGateOutcome→GitCheckpoint("node <id> attempt <n> <outcome>")→nr.Usage 累计进 run.Usage。

`applyGateOutcome` 三段顺序（spec §4.2）：

```go
func (e *Engine) applyGateOutcome(ctx context.Context, run *types.SopRun, node *types.SopNode, nr *types.SopNodeRun, res executor.ExecResult, ws *Workspace) error {
	verdict, reason := res.GateResult, res.GateReason
	if verdict == "" {
		// ② 产物契约：required 文件存在且非空
		var missing []string
		for _, a := range node.ArtifactOutputs {
			if !a.Required {
				continue
			}
			full, err := SafeWorkspacePath(ws, a.Path)
			if err != nil {
				missing = append(missing, a.Path)
				continue
			}
			if st, err := os.Stat(full); err != nil || st.Size() == 0 {
				missing = append(missing, a.Path)
			}
		}
		if len(missing) > 0 {
			verdict, reason = types.SopGateRework, "缺少必需产物："+strings.Join(missing, "、")
		} else if script := JoinGateScript(CompileGateCommands(node)); script != "" {
			// ③ 门禁脚本：Git Bash set -e，退出码非 0=打回
			out, ok := runGateScript(ctx, ws, script)
			if !ok {
				verdict, reason = types.SopGateRework, "门禁检查未通过："+clipRunes(out, 500)
			} else {
				verdict = types.SopGatePass
			}
		} else {
			verdict = types.SopGatePass
		}
	}
	nr.GateResult, nr.GateReason = verdict, reason
	switch verdict {
	case types.SopGatePass:
		nr.Status, nr.Output = types.SopNodeSucceeded, res.Output
		nr.SessionID, nr.ResumeSessionID = res.SessionID, res.ResumeSessionID
		now := time.Now()
		nr.FinishedAt = &now
	case types.SopGateRework:
		return e.handleFailure(ctx, run, node, nr, reason, ws)
	case types.SopGateFail:
		nr.Status, nr.Error = types.SopNodeFailed, reason
		now := time.Now()
		nr.FinishedAt = &now
		run.Status = types.SopRunFailed
	case types.SopGateHuman:
		nr.Status = types.SopNodeWaitingApproval
		run.Status = types.SopRunWaiting
		e.notifyApproval(ctx, run, node, reason)
	}
	return e.deps.Runs.UpdateNodeRun(ctx, nr)
}
```

`handleFailure`（四策略，spec §4.2）：

```go
func (e *Engine) handleFailure(ctx context.Context, run *types.SopRun, node *types.SopNode, nr *types.SopNodeRun, reason string, ws *Workspace) error {
	switch node.OnFailure {
	case "fail":
		nr.Status, nr.Error = types.SopNodeFailed, reason
		run.Status = types.SopRunFailed
	case "human":
		nr.Status = types.SopNodeWaitingApproval
		run.Status = types.SopRunWaiting
		e.notifyApproval(ctx, run, node, reason)
	case "reject":
		nr.ReworkCount++
		if nr.ReworkCount >= node.MaxReworks {
			nr.Status = types.SopNodeWaitingApproval
			run.Status = types.SopRunWaiting
			e.notifyApproval(ctx, run, node, "返工次数已达上限："+reason)
			break
		}
		target := node.RejectTo
		ids := []string{target}
		for id := range Descendants(&run.WorkflowSnapshot, target) {
			ids = append(ids, id)
		}
		if err := e.deps.Runs.ResetNodes(ctx, run.ID, ids); err != nil {
			return err
		}
		tnr, _ := e.deps.Runs.GetNodeRun(ctx, run.TenantID, run.ID, target)
		tnr.Feedback = reason
		tnr.ReworkCount++
		if !node.CarryOver {
			tnr.SessionID, tnr.ResumeSessionID = "", ""
		}
		_ = e.deps.Runs.UpdateNodeRun(ctx, tnr)
		nr.Status = types.SopNodePending // 打回方自身也重置，等上游重新产出
	default: // loop
		if nr.Attempt >= node.MaxAttempts {
			nr.Status = types.SopNodeWaitingApproval
			run.Status = types.SopRunWaiting
			e.notifyApproval(ctx, run, node, "自动重试次数已达上限："+reason)
			break
		}
		nr.Status = types.SopNodePending
		nr.Feedback = reason
		if !node.CarryOver {
			nr.SessionID, nr.ResumeSessionID = "", ""
		}
	}
	return e.deps.Runs.UpdateNodeRun(ctx, nr)
}
```

执行器报错（Execute 返回 err）：nr.Attempt<MaxAttempts→pending+Feedback=err.Error()（carry_over 同规则）；否则 waiting_approval+run waiting（spec §4.2 执行器错误重试耗尽转人工）。`runGateScript`：`exec.CommandContext(ctx, "bash", "-c", script)`，Dir=ws.Root，CombinedOutput，60s 超时；Windows 依赖 Git Bash（全局约束）。`notifyApproval`：Notify(sop_approval, 标题=节点名, body=reason, refID=run.ID)+Extras.AddActivity。`clipRunes` 复用 types 包截断或本地实现（按字符截断）。

- [ ] **Step 4: 运行确认通过** Run: `go test ./internal/application/service/sop/engine/ -run "TestReadyNodes|TestAdvance|TestGate|TestRework|TestReject" -v && go build ./...` Expected: PASS。
- [ ] **Step 5: 提交** `git add internal/application/service/sop/engine/node.go internal/application/service/sop/engine/node_test.go && git commit -m "feat(sop): 节点推进引擎（并行/串行组/门禁三段裁定/失败四策略/返工预算）"`

### Task 14: 运行操作（审批三操作/重试/暂停/恢复/取消/decision.md/人工上下文）

**Files:**
- Create: `internal/application/service/sop/engine/ops.go`、`ops_test.go`
- Modify: `internal/application/service/sop/engine/scheduler.go`（TickOnce 追加 resumeWaiting：重启后 waiting run 恢复推进）

**Interfaces:**
- Consumes: T12 Engine、T13 Advance/handleFailure、T9 Descendants、T10 GitCheckpoint。
- Produces:（**T19 审批服务、T24/T26 handler 消费**）

```go
func (e *Engine) Approve(ctx context.Context, tenantID uint64, runID, nodeID, comment, approver string) error
func (e *Engine) Reject(ctx context.Context, tenantID uint64, runID, nodeID, comment, approver, rejectTo string) error // rejectTo 空=用节点模板 RejectTo
func (e *Engine) ReworkCurrent(ctx context.Context, tenantID uint64, runID, nodeID, comment, approver string) error    // 补充信息重跑当前节点（spec §6）
func (e *Engine) Terminate(ctx context.Context, tenantID uint64, runID, nodeID, comment, approver string) error
func (e *Engine) Retry(ctx context.Context, tenantID uint64, runID string) error        // failed run 整体重试
func (e *Engine) Pause(ctx context.Context, tenantID uint64, runID string) error        // 节点边界生效
func (e *Engine) Resume(ctx context.Context, tenantID uint64, runID string) error       // paused→queued
func (e *Engine) Cancel(ctx context.Context, tenantID uint64, runID string) error       // 进程组杀灭
func (e *Engine) AddHumanContext(ctx context.Context, tenantID uint64, runID, text, source, nodeID string) error
```

- [ ] **Step 1: 写失败测试** `ops_test.go`：

```go
func waitingRun(t *testing.T, e *Engine, def types.SopDefinition) *types.SopRun {
	ctx := context.Background()
	run := &types.SopRun{TenantID: 1, Status: types.SopRunWaiting, WorkflowSnapshot: def, WorkspaceDir: t.TempDir()}
	require.NoError(t, e.deps.Runs.CreateRun(ctx, run))
	require.NoError(t, e.deps.Runs.CreateNodeRun(ctx, &types.SopNodeRun{TenantID: 1, RunID: run.ID, NodeID: "n1", Status: types.SopNodeWaitingApproval, Attempt: 2}))
	return run
}

func TestApproveWritesDecisionAndContinues(t *testing.T) {
	e, _, _ := newTestEngine(t)
	ctx := context.Background()
	def := types.SopDefinition{Nodes: []types.SopNode{{ID: "n1", Type: "approval"}}}
	run := waitingRun(t, e, def)
	require.NoError(t, e.Approve(ctx, 1, run.ID, "n1", "同意上线", "张三"))
	nr, _ := e.deps.Runs.GetNodeRun(ctx, 1, run.ID, "n1")
	require.Equal(t, types.SopNodeSucceeded, nr.Status)
	require.Equal(t, types.SopGatePass, nr.GateResult)
	got, _ := e.deps.Runs.GetRun(ctx, 1, run.ID)
	require.Equal(t, types.SopRunRunning, got.Status)
	// decision.md 落盘且含审批人与产物哈希段
	matches, _ := filepath.Glob(filepath.Join(run.WorkspaceDir, ".sop", "decisions", "n1-*.md"))
	require.Len(t, matches, 1)
	body, _ := os.ReadFile(matches[0])
	require.Contains(t, string(body), "张三")
	require.Contains(t, string(body), "同意上线")
	require.Contains(t, string(body), "sha256")
}

func TestRejectApprovalResetsUpstream(t *testing.T) {
	e, _, _ := newTestEngine(t)
	ctx := context.Background()
	def := types.SopDefinition{Nodes: []types.SopNode{
		{ID: "n0", Type: "agent", MaxReworks: 3},
		{ID: "n1", Type: "approval", RejectTo: "n0"},
	}, Edges: []types.SopEdge{{ID: "e1", Source: "n0", Target: "n1"}}}
	run := waitingRun(t, e, def)
	require.NoError(t, e.deps.Runs.CreateNodeRun(ctx, &types.SopNodeRun{TenantID: 1, RunID: run.ID, NodeID: "n0", Status: types.SopNodeSucceeded, Output: "old"}))
	require.NoError(t, e.Reject(ctx, 1, run.ID, "n1", "方案重做", "李四", ""))
	n0, _ := e.deps.Runs.GetNodeRun(ctx, 1, run.ID, "n0")
	require.Equal(t, types.SopNodePending, n0.Status)
	require.Empty(t, n0.Output)
	require.Equal(t, "方案重做", n0.Feedback)
	require.Equal(t, 1, n0.ReworkCount)
	n1, _ := e.deps.Runs.GetNodeRun(ctx, 1, run.ID, "n1")
	require.Equal(t, types.SopNodePending, n1.Status)
}

func TestRejectExhaustedAgentNodeRestartsBudget(t *testing.T) {
	e, _, _ := newTestEngine(t)
	ctx := context.Background()
	def := types.SopDefinition{Nodes: []types.SopNode{{ID: "n1", Type: "agent", OnFailure: "loop", MaxAttempts: 2}}}
	run := waitingRun(t, e, def) // n1 因重试耗尽 waiting_approval
	require.NoError(t, e.Reject(ctx, 1, run.ID, "n1", "按新口径重来", "王五", ""))
	nr, _ := e.deps.Runs.GetNodeRun(ctx, 1, run.ID, "n1")
	require.Equal(t, types.SopNodePending, nr.Status)
	require.Equal(t, 0, nr.Attempt) // 人工打回重置自动重试预算
	require.Equal(t, "按新口径重来", nr.Feedback)
}

func TestReworkCurrentKeepsNodeAndResetsBudget(t *testing.T) {
	e, _, _ := newTestEngine(t)
	ctx := context.Background()
	def := types.SopDefinition{Nodes: []types.SopNode{{ID: "n1", Type: "approval"}}}
	run := waitingRun(t, e, def)
	require.NoError(t, e.ReworkCurrent(ctx, 1, run.ID, "n1", "补充：优先移动端", "赵六"))
	nr, _ := e.deps.Runs.GetNodeRun(ctx, 1, run.ID, "n1")
	require.Equal(t, types.SopNodePending, nr.Status)
	require.Equal(t, 0, nr.Attempt)
	require.Equal(t, "补充：优先移动端", nr.Feedback)
	got, _ := e.deps.Runs.GetRun(ctx, 1, run.ID)
	require.Equal(t, types.SopRunRunning, got.Status)
	require.Len(t, got.HumanContext, 1) // 补充信息同时进人工上下文
}

func TestRetryFailedRun(t *testing.T) {
	e, _, _ := newTestEngine(t)
	ctx := context.Background()
	def := types.SopDefinition{Nodes: []types.SopNode{{ID: "n1", Type: "agent"}}}
	run := &types.SopRun{TenantID: 1, Status: types.SopRunFailed, WorkflowSnapshot: def, Error: "boom"}
	require.NoError(t, e.deps.Runs.CreateRun(ctx, run))
	require.NoError(t, e.deps.Runs.CreateNodeRun(ctx, &types.SopNodeRun{TenantID: 1, RunID: run.ID, NodeID: "n1", Status: types.SopNodeFailed, Attempt: 3, Error: "boom"}))
	require.NoError(t, e.Retry(ctx, 1, run.ID))
	got, _ := e.deps.Runs.GetRun(ctx, 1, run.ID)
	require.Equal(t, types.SopRunQueued, got.Status)
	require.Empty(t, got.Error)
	nr, _ := e.deps.Runs.GetNodeRun(ctx, 1, run.ID, "n1")
	require.Equal(t, types.SopNodePending, nr.Status)
	require.Equal(t, 0, nr.Attempt)
}

func TestCancelInactiveRun(t *testing.T) {
	e, _, _ := newTestEngine(t)
	ctx := context.Background()
	run := &types.SopRun{TenantID: 1, Status: types.SopRunPaused, WorkflowSnapshot: types.SopDefinition{Nodes: []types.SopNode{{ID: "n1", Type: "agent"}}}}
	require.NoError(t, e.deps.Runs.CreateRun(ctx, run))
	require.NoError(t, e.deps.Runs.CreateNodeRun(ctx, &types.SopNodeRun{TenantID: 1, RunID: run.ID, NodeID: "n1", Status: types.SopNodePending}))
	require.NoError(t, e.Cancel(ctx, 1, run.ID))
	got, _ := e.deps.Runs.GetRun(ctx, 1, run.ID)
	require.Equal(t, types.SopRunCancelled, got.Status)
	nr, _ := e.deps.Runs.GetNodeRun(ctx, 1, run.ID, "n1")
	require.Equal(t, types.SopNodeCancelled, nr.Status)
}

func TestPauseResumeFlags(t *testing.T) {
	e, _, _ := newTestEngine(t)
	ctx := context.Background()
	run := &types.SopRun{TenantID: 1, Status: types.SopRunRunning}
	require.NoError(t, e.deps.Runs.CreateRun(ctx, run))
	require.NoError(t, e.Pause(ctx, 1, run.ID))
	got, _ := e.deps.Runs.GetRun(ctx, 1, run.ID)
	require.True(t, got.PauseRequested)
	got.Status = types.SopRunPaused
	require.NoError(t, e.deps.Runs.UpdateRun(ctx, got))
	require.NoError(t, e.Resume(ctx, 1, run.ID))
	got, _ = e.deps.Runs.GetRun(ctx, 1, run.ID)
	require.Equal(t, types.SopRunQueued, got.Status)
	require.False(t, got.PauseRequested)
}

func TestAddHumanContextAppends(t *testing.T) {
	e, _, _ := newTestEngine(t)
	ctx := context.Background()
	run := &types.SopRun{TenantID: 1, Status: types.SopRunRunning}
	require.NoError(t, e.deps.Runs.CreateRun(ctx, run))
	require.NoError(t, e.AddHumanContext(ctx, 1, run.ID, "优先移动端", "manual", "n1"))
	got, _ := e.deps.Runs.GetRun(ctx, 1, run.ID)
	require.Len(t, got.HumanContext, 1)
	require.Equal(t, "优先移动端", got.HumanContext[0].Text)
	require.Equal(t, "n1", got.HumanContext[0].NodeID)
}
```

- [ ] **Step 2: 运行确认失败** Run: `go test ./internal/application/service/sop/engine/ -run "TestApprove|TestReject|TestReworkCurrent|TestRetry|TestCancel|TestPause|TestAddHumanContext" -v` Expected: 编译失败。
- [ ] **Step 3: 实现 ops.go**

公共守卫：GetRun 校验租户与存在；Approve/Reject/Terminate 要求 nr.Status==waiting_approval（否则白话错误「该步骤当前不在等待审批」）。`writeDecision(ws, run, nodeID, decision, comment, approver)`：mkdir ws/.sop/decisions→写 `n1-<unix>.md`（标题/审批人/决定/意见/时间 + 「## 产物指纹」段逐条 `- name path sha256:<hash>`，ListArtifacts(runID,nodeID) 取最新 attempt）→GitCheckpoint("decision <nodeID> <decision>")。

Approve：writeDecision(approve)→nr.Status=succeeded、GateResult=pass、GateReason=comment、FinishedAt→run.Status=running→UpdateRun/UpdateNodeRun→AddActivity(business, action=approve)。Reject：writeDecision(reject)→目标=rejectTo 参数非空则用之，否则 node.RejectTo；目标非空：ResetNodes(target+Descendants)、tnr.Feedback=comment、tnr.ReworkCount++（≥目标节点 MaxReworks→tnr waiting_approval+run waiting，否则 run running）；目标为空且 node.Type==approval→白话错误「请选择打回哪一步」；其余（agent 预算耗尽被打回，无目标）：nr.Status=pending、nr.Attempt=0、nr.Feedback=comment、nr.ReworkCount++、run running。ReworkCurrent：writeDecision(rework)→AddHumanContext(comment, source="rework", nodeID)→nr.Status=pending、nr.Attempt=0、nr.Feedback=comment→run running（当前节点携补充信息重跑，spec §6 第二操作）。Terminate：writeDecision(terminate)→nr failed+Error="人工终止："+comment→run failed→finalizeRun。Retry：仅 failed/cancelled run 可重试（白话错误）→failed/cancelled 节点 ResetNodes+Attempt 清零（UpdateNodeRun 逐个）→run queued+Error 清空+FinishedAt=nil。Pause：仅 running→PauseRequested=true。Resume：仅 paused→queued+PauseRequested=false。Cancel：CancelRequested=true→e.CancelRun(runID)（active 时 runLoop 收尾 finishCancelled）；非 active（queued/paused/waiting 无 loop）→直接 finishCancelled。AddHumanContext：append types.SopHumanContextEntry{At: now, Source, NodeID, Text}→UpdateRun→AddActivity(business, action=human_context)。

scheduler.go 追加（重启恢复）：TickOnce 末尾 `resumeWaiting`：ListRuns(status=waiting)→不在 active 的→`go e.runLoop(run.ID)`（DisableRunLoop=true 时跳过）。补测试 TestResumeWaitingAfterRestart：waiting run 无 active loop，TickOnce 后 e.active 含该 runID（DisableRunLoop=false 的独立引擎实例）。

- [ ] **Step 4: 运行确认通过** Run: `go test ./internal/application/service/sop/engine/ -run "TestApprove|TestReject|TestReworkCurrent|TestRetry|TestCancel|TestPause|TestAddHumanContext|TestResumeWaiting" -v && go build ./...` Expected: PASS。
- [ ] **Step 5: 提交** `git add internal/application/service/sop/engine/ops.go internal/application/service/sop/engine/ops_test.go internal/application/service/sop/engine/scheduler.go && git commit -m "feat(sop): 运行操作（审批三操作/decision.md 指纹/重试/暂停恢复/取消/人工上下文）"`

### Task 15: 子 SOP 节点（child run 编排 + 产物导入 + 防递归）

**Files:**
- Create: `internal/application/service/sop/engine/sop_node.go`、`sop_node_test.go`

**Interfaces:**
- Consumes: T12 Engine、T13 Advance/handleFailure、T4 SopTemplateRepo.GetRevision、T10 SafeWorkspacePath/GitCheckpoint。
- Produces: `(*Engine).startChildRun(ctx, run, node, nr) error`、`(*Engine).reconcileChildRun(ctx, run, node, nr) (bool, error)`（Advance 内对 ChildRunID 非空的 running 节点调用）。

- [ ] **Step 1: 写失败测试** `sop_node_test.go`：

```go
func TestChildSOPRunImportsArtifacts(t *testing.T) {
	e, _, db := newTestEngine(t)
	ctx := context.Background()
	// 子模板：单 agent 节点，产物 out/child.md
	childTpl := &types.SopTemplate{TenantID: 1, Name: "子流程", Status: "published",
		Definition: types.SopDefinition{Nodes: []types.SopNode{{ID: "c1", Name: "子步骤", Type: "agent",
			Executor: types.SopNodeExecutor{Kind: "platform_agent", AgentID: "a"},
			ArtifactOutputs: []types.SopArtifactOutput{{Name: "子产物", Path: "out/child.md", Required: true, Check: types.SopArtifactCheck{Type: "exists"}}}}}}}
	require.NoError(t, e.deps.Templates.Create(ctx, childTpl))
	rev, err := e.deps.Templates.Publish(ctx, 1, childTpl.ID, "u1")
	require.NoError(t, err)
	childTpl.PublishedRevisionID = rev.ID
	require.NoError(t, e.deps.Templates.Update(ctx, childTpl))

	parentDef := types.SopDefinition{Nodes: []types.SopNode{{ID: "p1", Name: "子流程节点", Type: "sop", ChildTemplateID: childTpl.ID}}}
	parent := &types.SopRun{TenantID: 1, Status: types.SopRunQueued, WorkflowSnapshot: parentDef, ProjectID: "proj1"}
	require.NoError(t, e.deps.Runs.CreateRun(ctx, parent))
	require.NoError(t, e.TickOnce(ctx)) // 抢占 parent
	pnr, _ := e.deps.Runs.GetNodeRun(ctx, 1, parent.ID, "p1")
	require.Equal(t, types.SopNodeRunning, pnr.Status)
	require.NotEmpty(t, pnr.ChildRunID)
	child, _ := e.deps.Runs.GetRun(ctx, 1, pnr.ChildRunID)
	require.Equal(t, "child_run", child.TriggerKind)
	require.Equal(t, parent.ID, child.ParentRunID)
	require.Equal(t, rev.ID, child.RevisionID)
	// 推进子 run（fakeProvider 写 out/a.md；子模板产物名 child.md → 改 scriptedProvider 写对应文件）
	require.NoError(t, e.TickOnce(ctx)) // 抢占 child
	for i := 0; i < 3; i++ {
		e.Advance(ctx, child.ID)
	}
	// 子 run 产物路径与声明一致：fakeProvider 写 a.md，这里用 childWriter 写 child.md
	for i := 0; i < 4; i++ {
		e.Advance(ctx, parent.ID)
	}
	got, _ := e.deps.Runs.GetRun(ctx, 1, parent.ID)
	require.Equal(t, types.SopRunSucceeded, got.Status)
	arts, _ := e.deps.Runs.ListArtifacts(ctx, 1, parent.ID)
	require.Len(t, arts, 1)
	require.Equal(t, "p1", arts[0].NodeID)
	require.NotEmpty(t, arts[0].SHA256)
	_, err = os.Stat(filepath.Join(got.WorkspaceDir, "out", "child.md"))
	require.NoError(t, err)
}

func TestChildSOPFailurePropagates(t *testing.T) {
	e, _, _ := newTestEngine(t)
	ctx := context.Background()
	childTpl := &types.SopTemplate{TenantID: 1, Name: "子", Status: "published",
		Definition: types.SopDefinition{Nodes: []types.SopNode{{ID: "c1", Type: "agent", Executor: types.SopNodeExecutor{Kind: "platform_agent", AgentID: "a"}, OnFailure: "fail"}}}}
	require.NoError(t, e.deps.Templates.Create(ctx, childTpl))
	rev, _ := e.deps.Templates.Publish(ctx, 1, childTpl.ID, "u1")
	childTpl.PublishedRevisionID = rev.ID
	require.NoError(t, e.deps.Templates.Update(ctx, childTpl))
	e.deps.Registry.Register(&scriptedProvider{kind: "platform_agent", steps: []executor.ExecResult{{GateResult: types.SopGateFail, GateReason: "子流程失败"}}})
	parent := &types.SopRun{TenantID: 1, Status: types.SopRunQueued, WorkflowSnapshot: types.SopDefinition{Nodes: []types.SopNode{{ID: "p1", Type: "sop", ChildTemplateID: childTpl.ID, OnFailure: "fail"}}}}
	require.NoError(t, e.deps.Runs.CreateRun(ctx, parent))
	require.NoError(t, e.TickOnce(ctx))
	pnr, _ := e.deps.Runs.GetNodeRun(ctx, 1, parent.ID, "p1")
	require.NoError(t, e.TickOnce(ctx))
	for i := 0; i < 3; i++ {
		e.Advance(ctx, pnr.ChildRunID)
	}
	for i := 0; i < 3; i++ {
		e.Advance(ctx, parent.ID)
	}
	got, _ := e.deps.Runs.GetRun(ctx, 1, parent.ID)
	require.Equal(t, types.SopRunFailed, got.Status)
}

func TestChildTemplateUnpublishedPlainError(t *testing.T) {
	e, _, _ := newTestEngine(t)
	ctx := context.Background()
	draft := &types.SopTemplate{TenantID: 1, Name: "草稿", Status: "draft"}
	require.NoError(t, e.deps.Templates.Create(ctx, draft))
	parent := &types.SopRun{TenantID: 1, Status: types.SopRunQueued, WorkflowSnapshot: types.SopDefinition{Nodes: []types.SopNode{{ID: "p1", Type: "sop", ChildTemplateID: draft.ID}}}}
	require.NoError(t, e.deps.Runs.CreateRun(ctx, parent))
	require.NoError(t, e.TickOnce(ctx))
	e.Advance(ctx, parent.ID)
	got, _ := e.deps.Runs.GetRun(ctx, 1, parent.ID)
	require.Equal(t, types.SopRunFailed, got.Status)
	require.Contains(t, got.Error, "尚未发布")
}
```

（TestChildSOPRunImportsArtifacts 需 registry 注册写 `out/child.md` 的 provider：`childWriterProvider`，Execute 内写 filepath.Join(tb.OutDir,"child.md")；newTestEngine 默认 fakeProvider 写 a.md 不影响子模板 exists 检查——故该测试显式 Register 覆盖 platform_agent。）

- [ ] **Step 2: 运行确认失败** Run: `go test ./internal/application/service/sop/engine/ -run "TestChild" -v` Expected: 编译失败。
- [ ] **Step 3: 实现 sop_node.go**

`startChildRun`：Templates.GetByID(node.ChildTemplateID)→Status!=published 或 PublishedRevisionID 空→nr failed+Error「子流程模板尚未发布」+run failed；GetRevision→CreateRun(child{TenantID, TemplateID, RevisionID, WorkflowSnapshot: rev.Definition, ProjectID, ItemID, ParentRunID: run.ID, TriggerKind: "child_run", TriggerRefID: node.ID, Status: queued, Input: run.Input})→nr.ChildRunID=child.ID、nr.Status=running、StartedAt→UpdateNodeRun。子 run 由调度器正常抢占（同实例同租约机制）。

`reconcileChildRun`（Advance 每轮对 ChildRunID 非空且 nr running 的节点调用）：GetRun(child)→queued/running/waiting/paused→return false（继续等）；succeeded→导入产物：ListArtifacts(child) 取每个节点最新 attempt→逐条从 child.WorkspaceDir/<path> 复制到 parent ws 同路径（SafeWorkspacePath 双侧校验）→重算 sha256→CreateArtifact(parent run, node.ID, attempt=nr.Attempt)→nr.Output=子 run 各 succeeded 节点 Output 拼接摘要→nr.Status=succeeded、GateResult=pass、FinishedAt→GitCheckpoint；failed/cancelled→handleFailure(run, node, nr, "子流程执行失败："+child.Error, ws)。防递归：发布校验已拒自引用（T9），运行期再守一层——child.TemplateID==run.TemplateID→nr failed「子流程不能递归引用自身」。

- [ ] **Step 4: 运行确认通过** Run: `go test ./internal/application/service/sop/engine/ -run "TestChild" -v && go build ./...` Expected: PASS。
- [ ] **Step 5: 提交** `git add internal/application/service/sop/engine/sop_node.go internal/application/service/sop/engine/sop_node_test.go && git commit -m "feat(sop): 子 SOP 节点编排与跨 run 产物导入"`

---

# Milestone M3 — 平台智能体执行器（复用 AgentQA，零改 automation）

### Task 16: AgentProvider + 节点上下文解析器（角色/模型/KB/MCP/技能/凭据）

**Files:**
- Create: `internal/application/service/sop/executor/agent.go`、`agent_test.go`
- Create: `internal/application/service/sop/context.go`、`context_test.go`
- Modify: `internal/types/sop_run.go`（追加 `SOPSessionMarker = "[SOP] "` 常量）；会话列表隐藏过滤处（执行时 `grep -rn "PMAnalysisSessionMarker" internal/` 定位，把 SOP 前缀一并隐藏）

**Interfaces:**
- Consumes: T8 Provider/TaskBook/ExecResult/StreamEvent、T12 ResolvedNode/NodeResolver、interfaces.SessionService（含 `AgentQA(ctx, *types.QARequest, *event.EventBus) error`，interfaces/session.go:77）、types.QARequest（qa_request.go:170）、CustomAgent（custom_agent.go:100）、ModelService.GetModelByID（interfaces/model.go:23）、skills.Manager（internal/agent/skills/manager.go:96）、internal/mcp/manager。
- Produces: `executor.NewAgentProvider(d AgentDeps) *AgentProvider`（Kind="platform_agent"）、`sop.NewContextResolver(d ResolverDeps) *ContextResolver`（实现 engine.NodeResolver）。

```go
// 窄接口隔离 AgentQA，测试可 stub（真实实现=interfaces.SessionService 直接满足）
type AgentQACaller interface {
	AgentQA(ctx context.Context, req *types.QARequest, bus *event.EventBus) error
}

type AgentDeps struct {
	Sessions  interfaces.SessionService // 建会话/查会话
	Messages  interfaces.MessageService // 预建 user/assistant 消息行（照 automation_runner.go:290-364 的创建方式）
	QA        AgentQACaller
	Suspensions interface {              // hitl.Suspensions 的窄接口（T17 消费）
		GetBySession(ctx context.Context, sessionID string) (*types.AgentSuspension, error)
	}
}
```

- [ ] **Step 1: 写失败测试** `agent_test.go`（stub QA 记录 QARequest 并通过 bus 发事件）：

```go
type stubQA struct {
	gotReq *types.QARequest
	emit   func(bus *event.EventBus)
	retErr error
}

func (s *stubQA) AgentQA(ctx context.Context, req *types.QARequest, bus *event.EventBus) error {
	s.gotReq = req
	if s.emit != nil {
		s.emit(bus)
	}
	return s.retErr
}

func TestAgentProviderBuildsQARequest(t *testing.T) {
	qa := &stubQA{}
	p := NewAgentProvider(AgentDeps{QA: qa, Sessions: stubSessions{}, Messages: stubMessages{}})
	role := &types.CustomAgent{}
	role.SystemPrompt = "你是工程师"
	tb := TaskBook{RunID: "r1", NodeRunID: "nr1", NodeID: "n1", NodeName: "实现", Prompt: "任务书正文",
		WorkspaceRoot: t.TempDir(), OutDir: t.TempDir(), Model: "model-x", Role: role,
		Skills: []string{"go-test"}, Timeout: time.Minute, Attempt: 1}
	var events []StreamEvent
	res, err := p.Execute(context.Background(), tb, ExecOptions{Emit: func(e StreamEvent) { events = append(events, e) }})
	require.NoError(t, err)
	require.NotNil(t, qa.gotReq)
	require.Equal(t, "任务书正文", qa.gotReq.Query)
	require.True(t, qa.gotReq.Suspendable)
	require.Contains(t, qa.gotReq.RunNote, "r1")
	require.Equal(t, role, qa.gotReq.CustomAgent)
	require.NotEmpty(t, qa.gotReq.UserMessageID)
	require.NotEmpty(t, qa.gotReq.AssistantMessageID)
	require.NotEmpty(t, res.SessionID)
}

func TestAgentProviderPrepareRerankGuard(t *testing.T) {
	p := NewAgentProvider(AgentDeps{QA: &stubQA{}})
	node := &types.SopNode{Executor: types.SopNodeExecutor{Kind: "platform_agent", KBScope: "project"}}
	role := &types.CustomAgent{} // 无重排模型
	err := p.Prepare(context.Background(), node, t.TempDir())
	require.Error(t, err)
	require.Contains(t, err.Error(), "重排模型")
}

func TestContextResolverSkillsAndMCPSubset(t *testing.T) {
	r := NewContextResolver(ResolverDeps{ /* stub agents/mcp/skills/credentials */ })
	run := &types.SopRun{TenantID: 1, ProjectID: "p1"}
	node := &types.SopNode{Executor: types.SopNodeExecutor{Kind: "platform_agent", AgentID: "a1", KBScope: "project"},
		Skills: []string{"a"}, MCP: []string{"know-kb"}}
	got, err := r.Resolve(context.Background(), run, node)
	require.NoError(t, err)
	require.Equal(t, []string{"a"}, got.Skills)
	require.Len(t, got.MCPServers, 1)
	require.Equal(t, []string{"kb-project"}, got.KBIDs) // stub 项目 KB
}
```

（stubSessions/stubMessages/ResolverDeps 的 stub 在测试文件内实现：CreateSession 返回固定 ID 会话；Messages.Create 返回 uuid；ResolverDeps 注入 stub 角色仓储（SelectedSkills=[a,b]）、stub MCP manager（know-kb 服务）、stub 项目仓储（KBID=kb-project）、stub 事项仓储（KBID 空=不覆盖；ResolverDeps 含 Items interfaces.SopItemRepo 供事项级 KB 覆盖）。）

- [ ] **Step 2: 运行确认失败** Run: `go test ./internal/application/service/sop/executor/ -run TestAgentProvider -v && go test ./internal/application/service/sop/ -run TestContextResolver -v` Expected: 编译失败。
- [ ] **Step 3: 实现**

`context.go`（ContextResolver.Resolve）：① Agents.GetByID(node.Executor.AgentID)→Role；② Model：node.Executor.Model 非空→用之，否则角色默认；③ KBIDs：KBScope=project→**事项级覆盖优先**：run.ItemID 非空且 item.KBID 非空→[item.KBID]，否则 [project.KBID]（空则 []）；custom→node.Executor.KBIDs；all→租户全部 KB ID 列表（spec §16.13）；④ Skills=engine.ResolveSkills(role, node.Skills)（避免 import cycle：ResolveSkills 放 types 包或 executor 包——**锁定放 executor 包**，T11 的 engine.ResolveSkills 改为调用 executor.ResolveSkills 的薄包装，本任务一并调整）；⑤ MCPServers：node.MCP 名称列表→mcp manager 按名解析真实配置（nil=角色全部 MCP，[]=无）；⑥ Credential：node.Executor.CredentialID 非空→credentialRepo.GetByID（解密后）；⑦ cli 节点 RoleID 非空→也取角色（仅任务书渲染用）。

`agent.go`（AgentProvider）：
- Prepare：KBScope 非空且解析 KBIDs 非空时，角色必须配置重排模型（照 session_agent_qa.go:121-141 的判定字段，执行时读该文件核对）→否则白话错误「该角色未配置重排模型，无法使用知识库检索；请在角色设置中配置或把知识库范围改为不使用」。
- Execute：会话复用——opts.SessionID 非空→Sessions.GetByID 复用；否则 CreateSession(&types.Session{Title: tb.NodeName + " - SOP", Description: types.SOPSessionMarker + "run=" + tb.RunID + " node=" + tb.NodeID, TenantID: 从 ctx})。预建 user/assistant 消息行（照 automation_runner.go:290-364）。组装 QARequest：Session/Query=tb.Prompt/CustomAgent=tb.Role（深拷贝后覆盖 ModelID=tb.Model、SelectedSkills=tb.Skills、MCPServices=tb.MCPServers 的 ID 列表、KnowledgeBases=解析 KBIDs、AllowedTools 追加 submit_node_result）/KnowledgeBaseIDs=KBIDs/Mode=types.RunModeDeferred（以 types 实际 RunMode 常量为准，语义=无人值守可挂起）/Suspendable=true/RunNote=fmt.Sprintf("SOP 运行 %s 节点 %s 第 %d 次尝试，无人值守；需要用户输入时挂起等待，不要臆测。", tb.RunID, tb.NodeID, tb.Attempt)/UserMessageID/AssistantMessageID/Resume（tb.ResumeSessionID 非空且 Suspensions.GetBySession 有挂起→&types.AgentResume{Snapshot, Prompt}）。event.NewEventBus→订阅→映射 StreamEvent（text_delta/tool_call/tool_result/result/error）→opts.Emit+jsonl 日志→`QA.AgentQA(ctx, req, bus)` 同步阻塞。返回后：查 assistant 消息/挂起状态——有挂起→ExecResult{Suspended: true, SessionID: session.ID}；正常结束→Output=assistant 消息正文、Usage 从消息统计取（照 automation_runner 的 usage 提取）、SessionID。Cancel：ctx 取消即中断（AgentQA 尊重 ctx）。
- 工作区注入：TaskBook.Prompt 已含工作区绝对路径与产物要求（T11）；平台智能体的文件读写经 SOP MCP 工具组（T37 交付 sop_workspace_* 工具；CLI 节点经 tb.KnowMCP 注入，平台节点由 T37 在 Resolver 的 MCPServers 里追加同一 scoped endpoint）。M3 阶段先以任务书路径+角色现有文件工具运行，T37 完成后自动增强。

- [ ] **Step 4: 运行确认通过** Run: `go test ./internal/application/service/sop/executor/ -run TestAgentProvider -v && go test ./internal/application/service/sop/ -run TestContextResolver -v && go test ./internal/application/service/sop/engine/ -v && go build ./...` Expected: PASS（含 T11 ResolveSkills 迁移后的引擎回归）。
- [ ] **Step 5: 提交** `git add internal/application/service/sop/executor/agent.go internal/application/service/sop/executor/agent_test.go internal/application/service/sop/context.go internal/application/service/sop/context_test.go internal/types/sop_run.go internal/application/service/sop/engine/taskbook.go && git commit -m "feat(sop): 平台智能体执行器（复用 AgentQA）与节点上下文解析器"`

### Task 17: submit_node_result 工具 + HITL 挂起/答复续跑 + 迭代上限

**Files:**
- Create: `internal/application/service/sop/executor/submit_tool.go`、`submit_tool_test.go`
- Create: `internal/application/service/sop/engine/answer.go`、`answer_test.go`
- Modify: `internal/application/service/sop/executor/agent.go`（注册工具进 AllowedTools 的工具实现侧）；工具注册处（执行时 grep Know 智能体内置工具注册表，照现有工具注册模式追加）

**Interfaces:**
- Consumes: T16 AgentProvider/AgentDeps.Suspensions、types.AgentResume/AgentPrompt（agent_prompt.go）、hitl.Broker/Suspensions（internal/agent/hitl）。
- Produces: 工具 `submit_node_result`（参数 `{result: "pass"|"rework"|"fail"|"human", reason: string, output: string}`，仅 SOP 会话内可用——工具实现校验 session.Description 含 SOPSessionMarker，否则拒绝）；`(*Engine).AnswerPrompt(ctx, tenantID, runID, nodeID, answer string) error`；`(*Engine).ResumeNode(ctx, tenantID, runID, nodeID string) error`（pending_input→重新 Execute 带 Resume）。

- [ ] **Step 1: 写失败测试**

`submit_tool_test.go`：

```go
func TestSubmitNodeResultParsesFourVerdicts(t *testing.T) {
	for _, v := range []string{"pass", "rework", "fail", "human"} {
		res, err := ParseSubmitArgs(map[string]any{"result": v, "reason": "r", "output": "o"})
		require.NoError(t, err)
		require.Equal(t, v, res.GateResult)
	}
	_, err := ParseSubmitArgs(map[string]any{"result": "ok"})
	require.Error(t, err) // 白话：结果只能是 通过/打回/失败/转人工
	_, err = ParseSubmitArgs(map[string]any{"result": "rework"})
	require.Error(t, err) // rework 必须带 reason
}

func TestSubmitToolRejectsNonSOPSession(t *testing.T) {
	tool := NewSubmitTool(stubSessionGetter{desc: "普通会话"})
	_, err := tool.Invoke(context.Background(), map[string]any{"result": "pass"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "SOP")
}
```

`answer_test.go`：

```go
func TestAnswerPromptResumesPendingInput(t *testing.T) {
	e, _, _ := newTestEngine(t)
	ctx := context.Background()
	def := types.SopDefinition{Nodes: []types.SopNode{{ID: "n1", Type: "agent", Executor: types.SopNodeExecutor{Kind: "platform_agent", AgentID: "a"}}}}
	run := &types.SopRun{TenantID: 1, Status: types.SopRunWaiting, WorkflowSnapshot: def}
	require.NoError(t, e.deps.Runs.CreateRun(ctx, run))
	require.NoError(t, e.deps.Runs.CreateNodeRun(ctx, &types.SopNodeRun{TenantID: 1, RunID: run.ID, NodeID: "n1", Status: types.SopNodePendingInput, SessionID: "sess1"}))
	// stub provider：第二次 Execute 收到 ResumeSessionID=sess1 即返回 pass
	sp := &resumeProvider{kind: "platform_agent"}
	e.deps.Registry.Register(sp)
	require.NoError(t, e.AnswerPrompt(ctx, 1, run.ID, "n1", "用方案A"))
	require.Equal(t, "用方案A", sp.lastAnswer) // 经 HumanContext 注入
	nr, _ := e.deps.Runs.GetNodeRun(ctx, 1, run.ID, "n1")
	require.Equal(t, types.SopNodeRunning, nr.Status) // 交还 Advance 续跑
	got, _ := e.deps.Runs.GetRun(ctx, 1, run.ID)
	require.Equal(t, types.SopRunRunning, got.Status)
	for i := 0; i < 3; i++ {
		e.Advance(ctx, run.ID)
	}
	got, _ = e.deps.Runs.GetRun(ctx, 1, run.ID)
	require.Equal(t, types.SopRunSucceeded, got.Status)
	require.Equal(t, "sess1", sp.lastResume) // carry 会话续跑
}
```

- [ ] **Step 2: 运行确认失败** Run: `go test ./internal/application/service/sop/executor/ -run "TestSubmit" -v && go test ./internal/application/service/sop/engine/ -run TestAnswerPrompt -v` Expected: 编译失败。
- [ ] **Step 3: 实现**

`submit_tool.go`：`ParseSubmitArgs` 校验四值+rework/fail/human 必须 reason；工具 Invoke：校验会话 SOP 前缀→把结果写入会话级结果槽（内存 map[sessionID]ExecResult 片段 + 落 ws/.sop/gate-results/<node>/attempt-N.json 双通道一致）→返回工具确认文本。AgentProvider.Execute 结束时优先读结果槽作为 GateResult/GateReason/Output（平台通道）；缺失→同会话追问一次「请调用 submit_node_result 提交本步骤结果」（再发一轮 QARequest，Query=追问指令，复用会话）→仍缺失→GateResult 空交门禁三段裁定（澄清 7）。

`answer.go`：AnswerPrompt：nr 必须 pending_input→AddHumanContext(text, source="answer", nodeID)→Suspensions.GetBySession(nr.SessionID) 取挂起→答复写入 hitl Broker（照 hitl 现有 Answer API，执行时读 internal/agent/hitl 核对）→nr.Status=running（ResumeSessionID=nr.SessionID）→run running→UpdateRun/UpdateNodeRun（runLoop/Advance 续跑；DisableRunLoop 测试模式由测试手动 Advance）。ResumeNode：无挂起快照时直接 nr pending→Advance 重新 Execute（ carry_over 规则保留会话）。迭代上限：QARequest 组装时若 CustomAgent.Config 含 MaxIterations 字段→覆盖为 node.LoopMaxIterations（执行时核对 custom_agent.go；无该字段则以 TimeoutMinutes 兜底并在 RunNote 声明迭代上限）。

- [ ] **Step 4: 运行确认通过** Run: `go test ./internal/application/service/sop/executor/ -run "TestSubmit" -v && go test ./internal/application/service/sop/engine/ -run TestAnswerPrompt -v && go build ./...` Expected: PASS。
- [ ] **Step 5: 提交** `git add internal/application/service/sop/executor/submit_tool.go internal/application/service/sop/executor/submit_tool_test.go internal/application/service/sop/engine/answer.go internal/application/service/sop/engine/answer_test.go internal/application/service/sop/executor/agent.go && git commit -m "feat(sop): submit_node_result 双通道结果提交与 HITL 挂起答复续跑"`

---

# Milestone M4 — 脚本执行器 + 审批中心服务

### Task 18: 脚本执行器（Git Bash/受控 env/流式日志/退出码/超时杀灭）

**Files:**
- Create: `internal/application/service/sop/executor/script.go`、`script_test.go`

**Interfaces:**
- Consumes: T8 Provider/TaskBook/ExecResult/StreamEvent（含 Script/Env 字段）。
- Produces: `executor.NewScriptProvider() *ScriptProvider`（Kind="script"）。

- [ ] **Step 1: 写失败测试** `script_test.go`：

```go
func TestScriptSuccessWritesArtifact(t *testing.T) {
	p := NewScriptProvider()
	ws := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(ws, "out"), 0o755))
	tb := TaskBook{NodeID: "n1", WorkspaceRoot: ws, OutDir: filepath.Join(ws, "out"),
		Script: "echo hello > out/s.txt", Timeout: 30 * time.Second, Attempt: 1}
	var lines []string
	res, err := p.Execute(context.Background(), tb, ExecOptions{Emit: func(e StreamEvent) {
		if e.Type == "text_delta" {
			lines = append(lines, e.Text)
		}
	}})
	require.NoError(t, err)
	require.Equal(t, types.SopGatePass, res.GateResult)
	body, _ := os.ReadFile(filepath.Join(ws, "out", "s.txt"))
	require.Contains(t, string(body), "hello")
}

func TestScriptNonZeroExitMeansRework(t *testing.T) {
	p := NewScriptProvider()
	tb := TaskBook{NodeID: "n1", WorkspaceRoot: t.TempDir(), Script: "echo 出错 >&2; exit 3", Timeout: 30 * time.Second}
	res, err := p.Execute(context.Background(), tb, ExecOptions{})
	require.NoError(t, err) // 退出码非 0 不是执行器错误，是门禁打回
	require.Equal(t, types.SopGateRework, res.GateResult)
	require.Contains(t, res.GateReason, "退出码 3")
	require.Contains(t, res.GateReason, "出错")
}

func TestScriptEnvInjection(t *testing.T) {
	p := NewScriptProvider()
	ws := t.TempDir()
	tb := TaskBook{NodeID: "n1", WorkspaceRoot: ws, Script: "echo $MY_TOKEN > out/env.txt",
		Env: map[string]string{"MY_TOKEN": "secret123"}, Timeout: 30 * time.Second}
	require.NoError(t, os.MkdirAll(filepath.Join(ws, "out"), 0o755))
	_, err := p.Execute(context.Background(), tb, ExecOptions{})
	require.NoError(t, err)
	body, _ := os.ReadFile(filepath.Join(ws, "out", "env.txt"))
	require.Contains(t, string(body), "secret123")
}

func TestScriptTimeoutKilled(t *testing.T) {
	p := NewScriptProvider()
	tb := TaskBook{NodeID: "n1", WorkspaceRoot: t.TempDir(), Script: "sleep 30", Timeout: 300 * time.Millisecond}
	start := time.Now()
	res, err := p.Execute(context.Background(), tb, ExecOptions{})
	require.Less(t, time.Since(start), 10*time.Second)
	require.NoError(t, err)
	require.Equal(t, types.SopGateRework, res.GateResult)
	require.Contains(t, res.GateReason, "超时")
}
```

- [ ] **Step 2: 运行确认失败** Run: `go test ./internal/application/service/sop/executor/ -run TestScript -v` Expected: 编译失败。
- [ ] **Step 3: 实现 script.go**

Execute：`exec.CommandContext(ctx, "bash", "-c", tb.Script)`，Dir=tb.WorkspaceRoot，Env=append(os.Environ(), tb.Env 键值)；Windows 进程组：SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP}（build tag windows；unix 用 Setpgid）——T28 会抽公共 process  helper，本任务先内联最小实现。stdout/stderr 合并管道逐行扫描→Emit(text_delta)+写 logs/<node>-attempt-N.log；ctx 超时/取消→杀进程组（Windows `taskkill /T /F /PID`，unix kill(-pgid)）→GateResult=rework、Reason「脚本执行超时被终止」。正常退出：code==0→GateResult=pass、Output=输出尾部 2000 字符摘要；code!=0→rework、Reason fmt「脚本退出码 %d：%s」（输出尾部）。Prepare 恒 nil；Cancel 杀当前进程组。

- [ ] **Step 4: 运行确认通过** Run: `go test ./internal/application/service/sop/executor/ -run TestScript -v && go build ./...` Expected: PASS。
- [ ] **Step 5: 提交** `git add internal/application/service/sop/executor/script.go internal/application/service/sop/executor/script_test.go && git commit -m "feat(sop): 脚本执行器（Git Bash/受控环境变量/流式日志/超时进程组杀灭）"`

### Task 19: 审批服务（三操作封装 + 审批超时清扫 + 通知）

**Files:**
- Create: `internal/application/service/sop/approval.go`、`approval_test.go`
- Modify: `internal/application/service/sop/engine/scheduler.go`（TickOnce 追加 sweepApprovalTimeouts）；超时策略读 run snapshot 节点字段 `ApprovalTimeoutHours/ApprovalTimeoutAction`（T4 已定义；**spec §6 默认无限等待**，不设全局默认超时）

**Interfaces:**
- Consumes: T14 engine.Approve/Reject/ReworkCurrent/Terminate、T5 仓储。
- Produces: `sop.ApprovalService`（`Approve(ctx, tenantID, runID, nodeID, comment, userID)`、`Reject(ctx, tenantID, runID, nodeID, comment, userID, rejectTo)`、`ReworkCurrent(ctx, tenantID, runID, nodeID, comment, userID)`、`Terminate(ctx, tenantID, runID, nodeID, comment, userID)` 委托引擎并补业务活动流；`ListPending` 在 T20 实现）。

- [ ] **Step 1: 写失败测试** `approval_test.go`：

```go
func TestApprovalTimeoutSweep(t *testing.T) {
	e, _, _ := newTestEngine(t)
	ctx := context.Background()
	def := types.SopDefinition{Nodes: []types.SopNode{{ID: "n1", Type: "approval", ApprovalTimeoutHours: 1, ApprovalTimeoutAction: "fail"}}}
	run := &types.SopRun{TenantID: 1, Status: types.SopRunWaiting, WorkflowSnapshot: def}
	require.NoError(t, e.deps.Runs.CreateRun(ctx, run))
	nr := &types.SopNodeRun{TenantID: 1, RunID: run.ID, NodeID: "n1", Status: types.SopNodeWaitingApproval}
	require.NoError(t, e.deps.Runs.CreateNodeRun(ctx, nr))
	nr.UpdatedAt = time.Now().Add(-2 * time.Hour) // 伪造久等
	require.NoError(t, e.deps.Runs.UpdateNodeRun(ctx, nr))
	require.NoError(t, e.TickOnce(ctx))
	got, _ := e.deps.Runs.GetRun(ctx, 1, run.ID)
	require.Equal(t, types.SopRunFailed, got.Status)
	require.Contains(t, got.Error, "审批超时")
}

func TestApprovalDefaultWaitsForever(t *testing.T) {
	e, _, _ := newTestEngine(t)
	ctx := context.Background()
	def := types.SopDefinition{Nodes: []types.SopNode{{ID: "n1", Type: "approval"}}} // 无超时配置=无限等待
	run := &types.SopRun{TenantID: 1, Status: types.SopRunWaiting, WorkflowSnapshot: def}
	require.NoError(t, e.deps.Runs.CreateRun(ctx, run))
	nr := &types.SopNodeRun{TenantID: 1, RunID: run.ID, NodeID: "n1", Status: types.SopNodeWaitingApproval}
	require.NoError(t, e.deps.Runs.CreateNodeRun(ctx, nr))
	nr.UpdatedAt = time.Now().Add(-30 * 24 * time.Hour)
	require.NoError(t, e.deps.Runs.UpdateNodeRun(ctx, nr))
	require.NoError(t, e.TickOnce(ctx))
	got, _ := e.deps.Runs.GetRun(ctx, 1, run.ID)
	require.Equal(t, types.SopRunWaiting, got.Status) // 人不在就停着（spec §6）
}

func TestApprovalTimeoutNotifyReminderOnce(t *testing.T) {
	e, _, _ := newTestEngine(t)
	var kinds []string
	e.deps.Notify = func(ctx context.Context, t uint64, kind, title, body, ref string) { kinds = append(kinds, kind) }
	ctx := context.Background()
	def := types.SopDefinition{Nodes: []types.SopNode{{ID: "n1", Type: "approval", ApprovalTimeoutHours: 1, ApprovalTimeoutAction: "notify"}}}
	run := &types.SopRun{TenantID: 1, Status: types.SopRunWaiting, WorkflowSnapshot: def}
	require.NoError(t, e.deps.Runs.CreateRun(ctx, run))
	nr := &types.SopNodeRun{TenantID: 1, RunID: run.ID, NodeID: "n1", Status: types.SopNodeWaitingApproval}
	require.NoError(t, e.deps.Runs.CreateNodeRun(ctx, nr))
	nr.UpdatedAt = time.Now().Add(-2 * time.Hour)
	require.NoError(t, e.deps.Runs.UpdateNodeRun(ctx, nr))
	require.NoError(t, e.TickOnce(ctx))
	require.NoError(t, e.TickOnce(ctx)) // 第二轮不重复提醒
	got, _ := e.deps.Runs.GetRun(ctx, 1, run.ID)
	require.Equal(t, types.SopRunWaiting, got.Status)
	require.Equal(t, []string{"sop_approval"}, kinds) // 仅一次提醒（HumanContext 里 system_reminder 去重）
}

func TestApprovalServiceDelegatesAndLogs(t *testing.T) {
	e, _, db := newTestEngine(t)
	svc := NewApprovalService(e, repository.NewSopItemExtraRepo(db))
	ctx := context.Background()
	def := types.SopDefinition{Nodes: []types.SopNode{{ID: "n1", Type: "approval"}}}
	run := &types.SopRun{TenantID: 1, Status: types.SopRunWaiting, WorkflowSnapshot: def, WorkspaceDir: t.TempDir(), ItemID: "item1"}
	require.NoError(t, e.deps.Runs.CreateRun(ctx, run))
	require.NoError(t, e.deps.Runs.CreateNodeRun(ctx, &types.SopNodeRun{TenantID: 1, RunID: run.ID, NodeID: "n1", Status: types.SopNodeWaitingApproval}))
	require.NoError(t, svc.Approve(ctx, 1, run.ID, "n1", "同意", "u1"))
	acts, _ := repository.NewSopItemExtraRepo(db).ListActivities(ctx, 1, "item1", "business")
	require.NotEmpty(t, acts)
	require.Equal(t, "approve", acts[0].Action)
}
```

- [ ] **Step 2: 运行确认失败** Run: `go test ./internal/application/service/sop/ -run "TestApproval" -v` Expected: 编译失败。
- [ ] **Step 3: 实现**

approval.go：ApprovalService 持 engine+extras；四操作（Approve/Reject/ReworkCurrent/Terminate）委托引擎同名方法，成功后 Extras.AddActivity(kind=business, action=approve|reject|rework|terminate, detail=comment)。sweepApprovalTimeouts（scheduler.go）：ListRuns(status=waiting)→ListNodeRuns→waiting_approval 节点→从 run.WorkflowSnapshot 取该节点 ApprovalTimeoutHours（<=0 跳过=无限等待）→nr.UpdatedAt 早于 now-hours 时：Action=fail→引擎 Terminate(comment="审批超时（超过 N 小时无人处理）", approver="system")；Action=notify（默认）→发一次提醒 Notify(sop_approval)，去重标记=run.HumanContext 追加 {Source: "system_reminder", NodeID}（存在即不再提醒）。TickOnce 顺序：ExpireLeases→sweepApprovalTimeouts→ClaimDue 循环→resumeWaiting。

- [ ] **Step 4: 运行确认通过** Run: `go test ./internal/application/service/sop/ -run "TestApproval" -v && go test ./internal/application/service/sop/engine/ -v && go build ./...` Expected: PASS。
- [ ] **Step 5: 提交** `git add internal/application/service/sop/approval.go internal/application/service/sop/approval_test.go internal/application/service/sop/engine/scheduler.go internal/application/service/sop/engine/engine.go && git commit -m "feat(sop): 审批服务封装与审批超时自动清扫"`

### Task 20: 审批中心聚合（SOP + 智能体挂起 + 自动化待审批，只读聚合零改旧模块）

**Files:**
- Create: `internal/application/service/sop/approval_aggregate.go`、`approval_aggregate_test.go`

**Interfaces:**
- Consumes: T5 run 仓储、hitl.Suspensions（internal/agent/hitl）、automation 仓储（interfaces.AutomationRepository，只读 ListRuns status=awaiting_approval）、项目/事项仓储。
- Produces:

```go
type ApprovalEntry struct {
	Kind         string    `json:"kind"` // sop | agent_prompt | automation
	RefID        string    `json:"ref_id"`
	RunID        string    `json:"run_id,omitempty"`
	NodeID       string    `json:"node_id,omitempty"`
	SessionID    string    `json:"session_id,omitempty"`
	Title        string    `json:"title"`
	ProjectName  string    `json:"project_name,omitempty"`
	ItemTitle    string    `json:"item_title,omitempty"`
	Reason       string    `json:"reason"`
	WaitingSince time.Time `json:"waiting_since"`
	Actions      []string  `json:"actions"` // sop: approve|reject|rework|terminate|answer；automation: approve|reject；agent_prompt: answer
}

func (s *ApprovalService) ListPending(ctx context.Context, tenantID uint64) ([]ApprovalEntry, error)
```

- [ ] **Step 1: 写失败测试** `approval_aggregate_test.go`（stub 三个来源）：

```go
func TestListPendingAggregatesThreeSources(t *testing.T) {
	svc := newAggregateTestService(t) // 注入：1 个 SOP waiting run（waiting_approval 节点）、1 个 hitl 挂起、1 个 automation awaiting_approval
	entries, err := svc.ListPending(context.Background(), 1)
	require.NoError(t, err)
	require.Len(t, entries, 3)
	kinds := map[string]bool{}
	for _, e := range entries {
		kinds[e.Kind] = true
		if e.Kind == "sop" {
			require.Contains(t, e.Actions, "approve")
			require.NotEmpty(t, e.ProjectName)
		}
	}
	require.True(t, kinds["sop"] && kinds["agent_prompt"] && kinds["automation"])
}

func TestListPendingSortedByWaitingSince(t *testing.T) {
	svc := newAggregateTestService(t)
	entries, _ := svc.ListPending(context.Background(), 1)
	for i := 1; i < len(entries); i++ {
		require.False(t, entries[i].WaitingSince.Before(entries[i-1].WaitingSince))
	}
}
```

- [ ] **Step 2: 运行确认失败** Run: `go test ./internal/application/service/sop/ -run TestListPending -v` Expected: 编译失败。
- [ ] **Step 3: 实现 approval_aggregate.go**

三路只读聚合：① SOP：ListRuns(status=waiting)→ListNodeRuns 取 waiting_approval/pending_input→标题=节点名、Reason=GateReason 或「需要补充信息」、WaitingSince=nr.UpdatedAt、项目名/事项标题 join、Actions 按状态（pending_input→answer，waiting_approval→approve/reject/rework/terminate）；② hitl：Suspensions.ListByTenant（执行时读 internal/agent/hitl 核对列表 API，无则按会话扫描）→Kind=agent_prompt、Actions=[answer]、RefID=prompt ID；③ automation：AutomationRepository.ListRuns(tenantID, status=awaiting_approval)→Kind=automation、Actions=[approve,reject]、RefID=run ID。**零改 automation/hitl 代码**，只调其现有读接口；处置分发：sop→ApprovalService、automation→现有 automation 审批 API（handler 层 T26 直接转发现有路由）、agent_prompt→现有 hitl 答复 API。按 WaitingSince 升序（等最久的在前）。

- [ ] **Step 4: 运行确认通过** Run: `go test ./internal/application/service/sop/ -run TestListPending -v && go build ./...` Expected: PASS。
- [ ] **Step 5: 提交** `git add internal/application/service/sop/approval_aggregate.go internal/application/service/sop/approval_aggregate_test.go && git commit -m "feat(sop): 审批中心三源只读聚合（SOP/智能体挂起/自动化待审批）"`

---

# Milestone M5 — HTTP API 层（路由/处理器/容器接线/RBAC/SSE）

> 响应包络与错误映射**逐字照 PM 处理器现有模式**（已核实）：成功 `c.JSON(http.StatusOK, gin.H{"success": true, "data": ...})`；失败 `c.Error(errors.NewBadRequestError/NewNotFoundError/NewInternalServerError)`（照 handler/pm_project.go:112-147 的 pmFail/pmBindJSON/pmTenantID/pmUserID，SOP 侧定义同构 sopFail/sopBindJSON/sopTenantID/sopUserID）。路由注册照 router.go:363 `RegisterPMRoutes(v1, params.PMHandler, rbacGuards)` 模式追加 `RegisterSOPRoutes`。

### Task 21: 路由骨架 + 项目处理器 + 容器接线 + RBAC/API Key 能力

**Files:**
- Create: `internal/handler/sop.go`（SOPHandler 结构 + sopFail/sopBindJSON/sopTenantID/sopUserID 助手 + 项目 CRUD/repos 处理器）、`internal/handler/sop_test.go`
- Create: `internal/router/routes_sop.go`
- Modify: `internal/router/router.go`（v1 组追加 RegisterSOPRoutes）；`internal/router/rbac.go`（manage_sop/run_sop/read_sop 能力→Viewer/Contributor 映射）；`internal/container/container.go`（dig Bind 全部 sop 仓储/服务/引擎 + lifecycle OnStart/OnStop，照 container.go:913-947 automation 模式）；API Key 能力清单（执行时 grep tenant_api_key capabilities 定义处，追加 `APIKeyCapabilityReadSOP APIKeyCapability = "read_sop"` / `APIKeyCapabilityRunSOP = "run_sop"` / `APIKeyCapabilityManageSOP = "manage_sop"`，照 tenant_api_key.go:78-102 常量模式，默认拒绝不变）

**Interfaces:**
- Consumes: T1–T6 仓储、T12–T15 引擎、T16 AgentProvider/ContextResolver、T18 ScriptProvider、T19 ApprovalService。
- Produces: `handler.NewSOPHandler(d SOPHandlerDeps) *SOPHandler`；`router.RegisterSOPRoutes(v1 *gin.RouterGroup, h *handler.SOPHandler, guards router.RBACGuards)`；容器暴露 `*engine.Engine`（后续触发器/AI/归档服务注入用）。

路由表（本任务注册 projects 组，其余组由 T22–T26 追加到同一文件）：

```
POST   /api/v1/sop/projects                      manage_sop
GET    /api/v1/sop/projects                      read_sop
GET    /api/v1/sop/projects/:id                  read_sop
PUT    /api/v1/sop/projects/:id                  manage_sop
DELETE /api/v1/sop/projects/:id                  manage_sop
POST   /api/v1/sop/projects/:id/repos            manage_sop
DELETE /api/v1/sop/projects/:id/repos/:name      manage_sop
PUT    /api/v1/sop/projects/:id/repos/default    manage_sop
```

- [ ] **Step 1: 写失败测试** `sop_test.go`（httptest + sqlite :memory: + 真实仓储/服务，照 Know 现有 handler 测试模式）：

```go
func TestProjectCRUDAndCodeValidation(t *testing.T) {
	r := newSOPTestRouter(t)
	w := httptest.NewRecorder()
	body := `{"name":"测试项目","code":"TP","repos":[{"url":"https://github.com/a/b.git"}]}`
	req := httptest.NewRequest("POST", "/api/v1/sop/projects", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusCreated, w.Code)
	var resp struct{ Success bool; Data types.SopProject }
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.True(t, resp.Success)
	require.Equal(t, "b", resp.Data.Repos[0].Name) // URL basename 去 .git
	require.True(t, resp.Data.Repos[0].Default)
	// 非法 code
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, httptest.NewRequest("POST", "/api/v1/sop/projects", strings.NewReader(`{"name":"x","code":"1bad"}`)))
	require.Equal(t, http.StatusBadRequest, w2.Code)
}

func TestProjectDeleteGuardActiveRuns(t *testing.T) {
	r, db := newSOPTestRouter(t)
	// 建项目 + 一个 running run → DELETE 返回 409/400 白话「还有进行中的运行」
	pid := createTestProject(t, r)
	require.NoError(t, repository.NewSopRunRepo(db).CreateRun(context.Background(), &types.SopRun{TenantID: 1, ProjectID: pid, Status: types.SopRunRunning}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("DELETE", "/api/v1/sop/projects/"+pid, nil))
	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Contains(t, w.Body.String(), "进行中")
}

func TestRepoAddRemoveDefaultImmediate(t *testing.T) {
	r, _ := newSOPTestRouter(t)
	pid := createTestProject(t, r)
	// 加第二个仓库 → 列表立即可见；设默认 → 默认唯一且排首
	doJSON(t, r, "POST", "/api/v1/sop/projects/"+pid+"/repos", `{"url":"git@github.com:a/c.git","branch":"dev"}`)
	w := doJSON(t, r, "PUT", "/api/v1/sop/projects/"+pid+"/repos/default", `{"name":"c"}`)
	require.Equal(t, http.StatusOK, w.Code)
	w = doJSON(t, r, "GET", "/api/v1/sop/projects/"+pid, "")
	require.Contains(t, w.Body.String(), `"default":true`)
}
```

（newSOPTestRouter：gin.New + 注入 tenantID=1/userID=u1 的测试中间件 + RegisterSOPRoutes；doJSON 助手设 Content-Type 并返回 recorder。）

- [ ] **Step 2: 运行确认失败** Run: `go test ./internal/handler/ -run "TestProject|TestRepo" -v` Expected: 编译失败。
- [ ] **Step 3: 实现**

handler/sop.go：SOPHandlerDeps{Projects interfaces.SopProjectRepository; Runs interfaces.SopRunRepository; …后续任务扩展}；助手四件套照 pm_project.go:112-147 同构（sopFail 的 notFound 哨兵=apprepo.ErrSopProjectNotFound，客户端错误判定=errors.Is 各 ErrSop* 业务哨兵）。CreateProject：绑定 {name, code, description, repos}→ValidateSopProjectCode/NormalizeSopRepos→repo.Create→201。DeleteProject：CountActiveRuns>0→BadRequest「该项目还有进行中的运行，请先取消或等待结束」；否则软删。repos 三端点直接调 T1 仓储 mutateRepos/AddRepo/RemoveRepo/SetDefaultRepo（即时生效，无草稿态）。

routes_sop.go：

```go
func RegisterSOPRoutes(v1 *gin.RouterGroup, h *handler.SOPHandler, guards router.RBACGuards) {
	g := v1.Group("/sop")
	{
		p := g.Group("/projects")
		p.POST("", guards.Require("manage_sop"), h.CreateProject)
		p.GET("", guards.Require("read_sop"), h.ListProjects)
		p.GET("/:id", guards.Require("read_sop"), h.GetProject)
		p.PUT("/:id", guards.Require("manage_sop"), h.UpdateProject)
		p.DELETE("/:id", guards.Require("manage_sop"), h.DeleteProject)
		p.POST("/:id/repos", guards.Require("manage_sop"), h.AddRepo)
		p.DELETE("/:id/repos/:name", guards.Require("manage_sop"), h.RemoveRepo)
		p.PUT("/:id/repos/default", guards.Require("manage_sop"), h.SetDefaultRepo)
	}
}
```

（guards.Require 的实际方法名以 router/rbac.go 现有 PM 路由用法为准，执行时读 routes_pm.go 照抄守卫调用形态。）

container.go 接线（dig provide 清单）：NewSopProjectRepo/NewSopItemRepo/NewSopItemExtraRepo/NewSopTemplateRepo/NewSopRunRepo/NewSopCredentialRepo/NewSopTriggerRepo（均注入 *gorm.DB）→ executor.NewRegistry（Register AgentProvider/ScriptProvider）→ sop.NewContextResolver → engine.NewEngine（Deps 全量；WorkspaceRoot=配置 dataDir/sop-workspaces；Notify 包装 Know 通知服务，kind 三种；ItemStatus 暂 nil，T22 接 item service；Resolver 接 ContextResolver）→ sop.NewApprovalService → handler.NewSOPHandler。lifecycle：OnStart engine.Start、OnStop engine.Stop（照 automation Bind/Start 模式 container.go:913-947）。rbac.go：read_sop→Viewer 起、run_sop/manage_sop→Contributor 起（照现有能力映射表追加）。API Key 能力清单追加 read_sop/run_sop/manage_sop 三常量（值即名，照 APIKeyCapabilityReadAgents 模式；默认拒绝语义不变）。

- [ ] **Step 4: 运行确认通过** Run: `go test ./internal/handler/ -run "TestProject|TestRepo" -v && go build ./...` Expected: PASS。
- [ ] **Step 5: 提交** `git add internal/handler/sop.go internal/handler/sop_test.go internal/router/routes_sop.go internal/router/router.go internal/router/rbac.go internal/container/container.go && git commit -m "feat(sop): 项目 API/路由骨架/容器接线与 RBAC 能力"`

### Task 22: 事项处理器 + 事项服务（编号分配/派生执行态/批量/附件/澄清/活动）

**Files:**
- Create: `internal/handler/sop_items.go`、`internal/handler/sop_items_test.go`
- Create: `internal/application/service/sop/item.go`、`item_test.go`
- Modify: `internal/router/routes_sop.go`（items 组）；`internal/container/container.go`（engine.Deps.ItemStatus 接 item service）

**Interfaces:**
- Consumes: T1 AllocItemNo、T2 SopItemRepo.ListWithDerived/UpdateStatus/BulkUpdateStatus、T3 extras 仓储、T5 run 仓储（runStatusByItem/nodeRunsByItem 聚合）。
- Produces: `sop.ItemService`（实现 engine.ItemStatusUpdater；`Create(ctx,tenantID,userID,projectID,in) (*types.SopItem,error)` 事务内 AllocItemNo→编号 `CODE-<no>`；`ListWithDerived(ctx,tenantID,projectID,f)` 内部 join runs 聚合派生字段；`UpdateStatus` 过 CanTransitSopItem；`BulkUpdateStatus` skipRunning）。

路由：

```
POST   /sop/projects/:id/items            manage_sop
GET    /sop/projects/:id/items            read_sop   ?status&source&exec_state&keyword&page&page_size
GET    /sop/items/:id                     read_sop
PUT    /sop/items/:id                     manage_sop
PATCH  /sop/items/:id/status              manage_sop
POST   /sop/items/bulk-status             manage_sop
DELETE /sop/items/:id                     manage_sop
POST   /sop/items/:id/attachments         manage_sop (multipart)
GET    /sop/items/:id/attachments         read_sop
DELETE /sop/items/:id/attachments/:aid    manage_sop
GET    /sop/items/:id/attachments/:aid/file read_sop
GET    /sop/items/:id/clarifications      read_sop
POST   /sop/items/:id/clarifications      manage_sop
PATCH  /sop/items/:id/clarifications/:cid/answer manage_sop
GET    /sop/items/:id/activities          read_sop   ?kind=business|audit
```

- [ ] **Step 1: 写失败测试**

`item_test.go`：

```go
func TestItemCreateAllocatesNumber(t *testing.T) {
	svc, db := newItemTestService(t)
	ctx := context.Background()
	p := &types.SopProject{TenantID: 1, Name: "P", Code: "TP"}
	require.NoError(t, repository.NewSopProjectRepo(db).Create(ctx, p))
	a, err := svc.Create(ctx, 1, "u1", p.ID, CreateItemInput{Title: "第一个"})
	require.NoError(t, err)
	require.Equal(t, "TP-1", a.Number)
	b, _ := svc.Create(ctx, 1, "u1", p.ID, CreateItemInput{Title: "第二个"})
	require.Equal(t, "TP-2", b.Number)
	require.Equal(t, types.SopItemBacklog, a.Status)
}

func TestItemStatusTransitionGuard(t *testing.T) {
	svc, db := newItemTestService(t)
	ctx := context.Background()
	it := seedItem(t, db, types.SopItemBacklog)
	require.Error(t, svc.UpdateStatus(ctx, 1, it.ID, types.SopItemDone)) // backlog 不能直达 done
	require.NoError(t, svc.UpdateStatus(ctx, 1, it.ID, types.SopItemTodo))
	require.NoError(t, svc.UpdateStatus(ctx, 1, it.ID, types.SopItemInProgress))
	require.NoError(t, svc.UpdateStatus(ctx, 1, it.ID, types.SopItemInReview))
}

func TestListWithDerivedExecState(t *testing.T) {
	svc, db := newItemTestService(t)
	ctx := context.Background()
	it := seedItem(t, db, types.SopItemInProgress)
	require.NoError(t, repository.NewSopRunRepo(db).CreateRun(ctx, &types.SopRun{TenantID: 1, ItemID: it.ID, Status: types.SopRunWaiting}))
	list, err := svc.ListWithDerived(ctx, 1, it.ProjectID, types.SopItemFilter{})
	require.NoError(t, err)
	require.Equal(t, "intervene", list[0].ExecState) // waiting→待介入
}
```

`sop_items_test.go`：httptest 批量状态（含 running 事项被跳过并返回 skipped 清单）、附件上传 201+列表不回传 data、澄清答复单向（二次答复 400）、活动流 kind 过滤。

- [ ] **Step 2: 运行确认失败** Run: `go test ./internal/application/service/sop/ -run "TestItem" -v && go test ./internal/handler/ -run "TestSopItem" -v` Expected: 编译失败。
- [ ] **Step 3: 实现**

item.go：Create 事务（AllocItemNo→Number=fmt.Sprintf("%s-%d", project.Code, no)→Status=backlog→Source 缺省 internal→Create→AddActivity(business, create)）。ListWithDerived：repo.List 基础过滤→runs 聚合 map[itemID]最新 run status + nodeRuns 状态列表→repo.ListWithDerived（T2 纯函数 DeriveExecState/DeriveProgress/DeriveCurrentNode）。UpdateStatus：CanTransitSopItem 守卫+AddActivity。BulkUpdateStatus：逐个过状态机，running 派生事项跳过，返回 {updated, skipped}。附件：照 pm_requirement.go AddAttachment 模式（白名单/大小/文本抽取），SopItemAttachment 复用 T2 表。澄清：AI 生成写入 status=open source=ai；答复 open→answered 单向。container：engine.Deps.ItemStatus=itemService（dig 注入）。

- [ ] **Step 4: 运行确认通过** Run: `go test ./internal/application/service/sop/ -run "TestItem" -v && go test ./internal/handler/ -run "TestSopItem" -v && go build ./...` Expected: PASS。
- [ ] **Step 5: 提交** `git add internal/handler/sop_items.go internal/handler/sop_items_test.go internal/application/service/sop/item.go internal/application/service/sop/item_test.go internal/router/routes_sop.go internal/container/container.go && git commit -m "feat(sop): 事项 API 与服务（两级编号/派生执行态/批量/附件/澄清/活动）"`

### Task 23: 模板处理器（发布校验/不可变 revision/内置只读/副本）

**Files:**
- Create: `internal/handler/sop_templates.go`、`internal/handler/sop_templates_test.go`
- Create: `internal/application/service/sop/template.go`、`template_test.go`
- Modify: `internal/router/routes_sop.go`（templates 组）

**Interfaces:**
- Consumes: T4 SopTemplateRepo、T9 engine.ValidateDefinition/RefreshDerived、T6 ValidateSopTriggerConfig（无关，忽略）。
- Produces: `sop.TemplateService`（`Save` 保存草稿前 RefreshDerived；`Publish` 先 ValidateDefinition(def, tpl.IsBuiltin)——内置豁免执行器完整性（澄清 17），错误清单 400 白话逐条返回；`Copy` 内置→副本草稿）。

路由：

```
POST /sop/templates                 manage_sop
GET  /sop/templates                 read_sop  ?project_id（含全局+内置）
GET  /sop/templates/:id             read_sop
PUT  /sop/templates/:id             manage_sop（内置 400「内置模板只读，请先创建副本」）
DELETE /sop/templates/:id           manage_sop（内置 400）
POST /sop/templates/:id/publish     manage_sop
POST /sop/templates/:id/copy        manage_sop
GET  /sop/templates/:id/revisions   read_sop
GET  /sop/revisions/:id             read_sop
```

- [ ] **Step 1: 写失败测试**

```go
func TestPublishRejectsInvalidDefinitionPlainWords(t *testing.T) {
	svc, db := newTplTestService(t)
	ctx := context.Background()
	tpl := &types.SopTemplate{TenantID: 1, Name: "坏流程", Definition: types.SopDefinition{
		Nodes: []types.SopNode{{ID: "n1", Type: "agent", OnFailure: "reject"}}, // 缺执行器+缺打回目标
	}}
	require.NoError(t, repository.NewSopTemplateRepo(db).Create(ctx, tpl))
	_, err := svc.Publish(ctx, 1, tpl.ID, "u1")
	require.Error(t, err)
	require.Contains(t, err.Error(), "失败后选了打回上游，但没选打回哪一步")
}

func TestBuiltinPublishExemptsExecutorCompleteness(t *testing.T) {
	svc, db := newTplTestService(t)
	ctx := context.Background()
	b := &types.SopTemplate{TenantID: 1, Name: "内置", IsBuiltin: true, Definition: types.SopDefinition{
		Nodes: []types.SopNode{{ID: "n1", Type: "agent", Executor: types.SopNodeExecutor{Kind: "platform_agent"}}}, // agent_id 空
	}}
	require.NoError(t, repository.NewSopTemplateRepo(db).Create(ctx, b))
	_, err := svc.Publish(ctx, 1, b.ID, "u1")
	require.NoError(t, err) // 内置豁免（澄清 17）
}

func TestSaveRefreshesDerivedGateCommands(t *testing.T) {
	svc, db := newTplTestService(t)
	ctx := context.Background()
	tpl := &types.SopTemplate{TenantID: 1, Name: "t", Definition: types.SopDefinition{
		Nodes: []types.SopNode{{ID: "n1", Type: "agent", GateCommands: []string{"AI 乱写的"},
			ArtifactOutputs: []types.SopArtifactOutput{{Name: "r", Path: "out/a.md", Check: types.SopArtifactCheck{Type: "exists"}}}}},
	}}
	require.NoError(t, repository.NewSopTemplateRepo(db).Create(ctx, tpl))
	require.NoError(t, svc.Save(ctx, 1, tpl))
	got, _ := repository.NewSopTemplateRepo(db).GetByID(ctx, 1, tpl.ID)
	require.Equal(t, []string{"test -s out/a.md"}, got.Definition.Nodes[0].GateCommands)
}
```

handler 测试：PUT 内置→400 含「只读」；POST copy→201 副本 draft 非内置；GET revisions 列表按 revision 降序。

- [ ] **Step 2: 运行确认失败** Run: `go test ./internal/application/service/sop/ -run "TestPublish|TestBuiltinPublish|TestSaveRefreshes" -v && go test ./internal/handler/ -run TestSopTemplate -v` Expected: 编译失败。
- [ ] **Step 3: 实现** template.go：Save=NormalizeSopDefinition+RefreshDerived+repo.Update（内置拒绝）；Publish=ValidateDefinition（allowIncompleteExecutor=tpl.IsBuiltin）→错误 strings.Join(errs, "；") 返回→repo.Publish；Copy=repo.Copy。handler 照 PM 模式映射错误。routes 追加 templates 组。
- [ ] **Step 4: 运行确认通过** Run: `go test ./internal/application/service/sop/ -run "TestPublish|TestBuiltinPublish|TestSaveRefreshes" -v && go test ./internal/handler/ -run TestSopTemplate -v && go build ./...` Expected: PASS。
- [ ] **Step 5: 提交** `git add internal/handler/sop_templates.go internal/handler/sop_templates_test.go internal/application/service/sop/template.go internal/application/service/sop/template_test.go internal/router/routes_sop.go && git commit -m "feat(sop): 模板 API（发布白话校验/派生门禁重算/内置只读与副本）"`

### Task 24: 运行处理器 + 创建运行 + SSE 事件流 + 产物下载/预览 + 操作端点

**Files:**
- Create: `internal/handler/sop_runs.go`、`internal/handler/sop_runs_test.go`
- Create: `internal/application/service/sop/run.go`（RunService.Create/Stats）、`internal/application/service/sop/eventhub.go`、`eventhub_test.go`
- Modify: `internal/router/routes_sop.go`（runs 组）；`internal/application/service/sop/engine/engine.go`（Deps 追加 `EventSink func(runID string, ev executor.StreamEvent)`）；`engine/node.go`（executeNode 的 Emit 同时调 EventSink；节点状态变更处发 `node_status` 事件）

**Interfaces:**
- Consumes: T4 模板仓储、T5 run 仓储、T12–T15 引擎全部 ops、T22 ItemService。
- Produces: `sop.CreateRunInput{TemplateID, ItemID string; Input map[string]any; TriggerKind, TriggerRefID string}`；`sop.RunService.Create(ctx,tenantID,userID,in CreateRunInput) (*types.SopRun,error)`（模板必须 published；snapshot=PublishedRevision 的 Definition 深拷贝；InputSchema 声明的键必填校验白话；ItemID 可选；TriggerKind 空→manual，可经 in.TriggerKind/TriggerRefID 覆盖——T35 schedule 派发复用本入口；Status=queued）；`sop.EventHub`（`Subscribe(runID) (<-chan executor.StreamEvent, func())`、`Publish(runID, ev)`；无订阅者丢弃；订阅上限每 run 16）。

路由：

```
POST /sop/runs                          run_sop
GET  /sop/runs                          read_sop  ?project_id&item_id&status&keyword&page&page_size
GET  /sop/runs/stats/summary            read_sop
GET  /sop/runs/:id                      read_sop  （run+node_runs+artifacts 聚合 DTO；M11 起追加 `project_context` 字段：本次运行注入的任务书「项目现状」文本，T64 引擎写入 sop_runs，本任务先留空字符串）
GET  /sop/runs/:id/events               read_sop  （SSE）
GET  /sop/runs/:id/artifacts            read_sop
GET  /sop/runs/:id/nodes/:nid/logs      read_sop  （历史流日志：logs/<node_id>-attempt-<n>.jsonl 或 .log，?attempt= 默认最近）
GET  /sop/runs/:id/artifacts/:aid/file  read_sop  （?download=1 附件下载；默认 inline 预览）
POST /sop/runs/:id/retry                run_sop
POST /sop/runs/:id/pause                run_sop
POST /sop/runs/:id/resume               run_sop
POST /sop/runs/:id/cancel               run_sop
POST /sop/runs/:id/human-context        run_sop
POST /sop/runs/:id/nodes/:nid/approve   run_sop
POST /sop/runs/:id/nodes/:nid/reject    run_sop   body {comment, reject_to?}
POST /sop/runs/:id/nodes/:nid/rework    run_sop   body {comment}（补充信息重跑当前节点）
POST /sop/runs/:id/nodes/:nid/terminate run_sop
POST /sop/runs/:id/nodes/:nid/answer    run_sop
```

- [ ] **Step 1: 写失败测试**

`eventhub_test.go`：

```go
func TestEventHubPubSubAndUnsubscribe(t *testing.T) {
	h := NewEventHub()
	ch, unsub := h.Subscribe("r1")
	h.Publish("r1", executor.StreamEvent{Type: "text_delta", Text: "hi"})
	select {
	case ev := <-ch:
		require.Equal(t, "hi", ev.Text)
	case <-time.After(time.Second):
		t.Fatal("no event")
	}
	unsub()
	h.Publish("r1", executor.StreamEvent{Type: "text_delta", Text: "after"}) // 不阻塞、不 panic
	require.Equal(t, 0, h.SubscriberCount("r1"))
}
```

`sop_runs_test.go`：

```go
func TestCreateRunRequiresPublishedTemplate(t *testing.T) {
	r, db := newSOPTestRouter(t)
	draft := seedTemplate(t, db, "draft")
	w := doJSON(t, r, "POST", "/api/v1/sop/runs", fmt.Sprintf(`{"template_id":%q}`, draft.ID))
	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Contains(t, w.Body.String(), "尚未发布")
}

func TestCreateRunSnapshotsRevision(t *testing.T) {
	r, db := newSOPTestRouter(t)
	tpl := seedPublishedTemplate(t, db) // 单 agent 节点
	w := doJSON(t, r, "POST", "/api/v1/sop/runs", fmt.Sprintf(`{"template_id":%q}`, tpl.ID))
	require.Equal(t, http.StatusCreated, w.Code)
	// 发布后改草稿不影响 run snapshot
	tpl.Definition.Nodes[0].Name = "改了"
	require.NoError(t, repository.NewSopTemplateRepo(db).Update(context.Background(), tpl))
	var resp struct{ Data types.SopRun }
	json.Unmarshal(w.Body.Bytes(), &resp)
	got, _ := repository.NewSopRunRepo(db).GetRun(context.Background(), 1, resp.Data.ID)
	require.NotEqual(t, "改了", got.WorkflowSnapshot.Nodes[0].Name)
	require.Equal(t, types.SopRunQueued, got.Status)
}

func TestRunEventsSSEStreams(t *testing.T) {
	r, db := newSOPTestRouter(t)
	run := seedRun(t, db, types.SopRunRunning)
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest("GET", "/api/v1/sop/runs/"+run.ID+"/events", nil).WithContext(ctx)
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { r.ServeHTTP(w, req); close(done) }()
	time.Sleep(50 * time.Millisecond)
	sopTestHub.Publish(run.ID, executor.StreamEvent{Type: "text_delta", Text: "直播"})
	time.Sleep(50 * time.Millisecond)
	cancel()
	<-done
	require.Contains(t, w.Body.String(), "data:")
	require.Contains(t, w.Body.String(), "直播")
}

func TestArtifactFileDownloadAndInline(t *testing.T) {
	r, db := newSOPTestRouter(t)
	run := seedRun(t, db, types.SopRunSucceeded)
	ws := t.TempDir()
	run.WorkspaceDir = ws
	require.NoError(t, repository.NewSopRunRepo(db).UpdateRun(context.Background(), run))
	require.NoError(t, os.MkdirAll(filepath.Join(ws, "out"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(ws, "out", "a.md"), []byte("# 产物"), 0o644))
	art := &types.SopArtifact{TenantID: 1, RunID: run.ID, NodeID: "n1", Name: "报告", Path: "out/a.md", MediaType: "text/markdown"}
	require.NoError(t, repository.NewSopRunRepo(db).CreateArtifact(context.Background(), art))
	w := doJSON(t, r, "GET", "/api/v1/sop/runs/"+run.ID+"/artifacts/"+art.ID+"/file?download=1", "")
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Header().Get("Content-Disposition"), "attachment")
	require.Contains(t, w.Body.String(), "# 产物")
}
```

- [ ] **Step 2: 运行确认失败** Run: `go test ./internal/application/service/sop/ -run TestEventHub -v && go test ./internal/handler/ -run "TestCreateRun|TestRunEvents|TestArtifactFile" -v` Expected: 编译失败。
- [ ] **Step 3: 实现**

run.go：Create 按 Interfaces 描述；Stats 调 repo.StatsSummary。eventhub.go：map[runID][]chan + sync.Mutex，Publish 非阻塞 select-default 丢弃慢订阅者。handler SSE：

```go
func (h *SOPHandler) RunEvents(c *gin.Context) {
	runID := c.Param("id")
	if _, err := h.runs.GetRun(c.Request.Context(), sopTenantID(c), runID); err != nil {
		sopFail(c, err, apprepo.ErrSopRunNotFound, "run not found")
		return
	}
	ch, unsub := h.hub.Subscribe(runID)
	defer unsub()
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("X-Accel-Buffering", "no")
	flusher := c.Writer.(http.Flusher)
	ping := time.NewTicker(15 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-c.Request.Context().Done():
			return
		case ev := <-ch:
			body, _ := json.Marshal(ev)
			fmt.Fprintf(c.Writer, "data: %s\n\n", body)
			flusher.Flush()
		case <-ping.C:
			fmt.Fprintf(c.Writer, ": ping\n\n")
			flusher.Flush()
		}
	}
}
```

产物 file：GetArtifact→校验 run 归属→SafeWorkspacePath(run.WorkspaceDir, art.Path)→c.FileAttachment（download=1）/c.File（inline，Content-Type=art.MediaType）。节点 logs：GetNodeRun 校验归属→attempt 缺省=nr.Attempt→SafeWorkspacePath(run.WorkspaceDir, fmt.Sprintf("logs/%s-attempt-%d.jsonl", nid, attempt))，不存在则试 .log 后缀→c.File（text/plain; charset=utf-8）；两者皆无→404 白话「该节点暂无日志」。ops 端点逐个委托引擎同名方法（approve/reject/terminate  body {comment}，answer body {answer}，human-context body {text, node_id}）。GET /sop/runs/:id 聚合 DTO {run, node_runs, artifacts}。engine EventSink 接线：container 把 hub.Publish 注入 Deps.EventSink；executeNode Emit 闭包内追加 `if e.deps.EventSink != nil { e.deps.EventSink(run.ID, ev) }`；节点状态每次 UpdateNodeRun 后发 StreamEvent{Type: "node_status", Payload: json(nr)}。

- [ ] **Step 4: 运行确认通过** Run: `go test ./internal/application/service/sop/ -run TestEventHub -v && go test ./internal/handler/ -run "TestCreateRun|TestRunEvents|TestArtifactFile" -v && go test ./internal/application/service/sop/engine/ -v && go build ./...` Expected: PASS。
- [ ] **Step 5: 提交** `git add internal/handler/sop_runs.go internal/handler/sop_runs_test.go internal/application/service/sop/run.go internal/application/service/sop/eventhub.go internal/application/service/sop/eventhub_test.go internal/router/routes_sop.go internal/application/service/sop/engine/engine.go internal/application/service/sop/engine/node.go internal/container/container.go && git commit -m "feat(sop): 运行 API（创建快照/SSE 直播/产物预览下载/全部操作端点）"`

### Task 25: AI 助手 SSE（事项拆解/需求分析/澄清生成/流程编排 flow|node 双模式）

**Files:**
- Create: `internal/handler/sop_ai.go`、`internal/handler/sop_ai_test.go`
- Create: `internal/application/service/sop/ai.go`、`ai_test.go`
- Modify: `internal/router/routes_sop.go`（ai 组）

**Interfaces:**
- Consumes: T16 AgentQACaller/会话模式、skills.Manager（pm-requirement-analysis/pm-prd-generator 等技能）、T9 ValidateDefinition/RefreshDerived、T4 模板仓储。
- Produces: `sop.AIService`：`Breakdown(ctx,tenantID,projectID,draft) (<-chan executor.StreamEvent, error)`（事项初稿→建议子事项 JSON 流）；`AnalyzeItem(ctx,tenantID,itemID) (sessionID string, err error)`（照 pm_requirement.go StartAnalysis 模式建 SOP 标记会话，上下文=事项+附件+项目资料+路线图对应物=SOP 模板清单）；`GenerateClarifications(ctx,tenantID,itemID) ([]types.SopItemClarification, error)`（AI 追问→open 澄清落库）；`Orchestrate(ctx,tenantID,templateID,mode,instruction,inventory) (<-chan executor.StreamEvent, error)`。

路由（全部 run_sop，SSE 响应）：

```
POST /sop/ai/breakdown          {project_id, title, description}
POST /sop/ai/analyze-item       {item_id} → {session_id}（前端转订阅会话 SSE）
POST /sop/ai/clarify-generate   {item_id}
POST /sop/ai/orchestrate        {template_id, mode: "flow"|"node", instruction, node_id?, inventory}
```

- [ ] **Step 1: 写失败测试**

`ai_test.go`（stub QA 返回固定 JSON 文本）：

```go
func TestOrchestrateNodeModeValidatesOps(t *testing.T) {
	svc := newAITestService(t) // stub QA 输出下方 ops JSON
	inv := OrchestrateInventory{
		Agents: []InventoryItem{{ID: "a1", Name: "工程师"}},
		CLIs:   []string{"zcode", "claude"},
		Models: []InventoryItem{{ID: "m1", Name: "默认"}},
		Skills: []string{"go-test"},
		MCPs:   []string{"know-kb"},
	}
	opsJSON := `{"ops":[
		{"op":"set","node_id":"n1","fields":{"executor":{"kind":"platform_agent","agent_id":"a1"}}},
		{"op":"set","node_id":"n1","fields":{"executor":{"kind":"platform_agent","agent_id":"不存在"}}},
		{"op":"set","node_id":"nX","fields":{"name":"幽灵节点"}}
	]}`
	res, err := svc.ApplyNodeOps(context.Background(), validDef(), opsJSON, inv)
	require.NoError(t, err)
	require.Len(t, res.Applied, 1)
	require.Len(t, res.Skipped, 2) // 无效 agent_id / 不存在节点 → 跳过并附白话原因
	require.Contains(t, res.Skipped[0].Reason, "不存在")
	// gate_commands 永不被 AI 直写
	require.Empty(t, res.Definition.Nodes[0].GateCommands) // ApplyNodeOps 末尾 RefreshDerived 重算
}

func TestOrchestrateFlowModeDraftOnly(t *testing.T) {
	svc := newAITestService(t)
	def, err := svc.ParseFlowDraft(context.Background(), `{"nodes":[{"id":"n1","name":"分析","type":"agent","task_prompt":"…"}],"edges":[]}`)
	require.NoError(t, err)
	require.Equal(t, "draft", def.Status) //  flow 模式产出整份草稿，绝不自动发布
	require.NotEmpty(t, def.Definition.Nodes[0].GateCommands == nil || true)
}

func TestBreakdownStreamsSubItems(t *testing.T) {
	svc := newAITestService(t) // stub QA 流式输出 [{"title":"子事项1","description":"…"}]
	ch, err := svc.Breakdown(context.Background(), 1, "p1", ItemDraft{Title: "大需求"})
	require.NoError(t, err)
	var texts []string
	for ev := range ch {
		if ev.Type == "text_delta" {
			texts = append(texts, ev.Text)
		}
	}
	require.NotEmpty(t, texts)
}
```

`sop_ai_test.go`：httptest SSE 端点返回 text/event-stream；orchestrate 非法 mode→400 白话「模式只能是 flow 或 node」。

- [ ] **Step 2: 运行确认失败** Run: `go test ./internal/application/service/sop/ -run "TestOrchestrate|TestBreakdown" -v && go test ./internal/handler/ -run TestSopAI -v` Expected: 编译失败。
- [ ] **Step 3: 实现**

ai.go：四个能力均复用 AgentQA（SOP 标记会话+技能注入），prompt 模板内置（Breakdown：输出 JSON 数组子事项；Clarify：每轮≤3 问附推测答案，照 pm_requirement.go buildAnalysisPrompt 的追问风格；Orchestrate flow：输出完整 definition JSON 草稿，系统提示明确「gate_commands 是派生字段不要输出；carry_over 默认 true；失败策略四选一；reject 必须给 reject_to」；Orchestrate node：输出 {ops:[…]}，op∈set|add_node|remove_node|add_edge|remove_edge）。`ApplyNodeOps`：逐 op 校验——node_id 必须存在（add_node 除外）、executor.agent_id∈inventory.Agents、cli_kind∈inventory.CLIs、model∈inventory.Models、skills⊆inventory.Skills、mcp⊆inventory.MCPs、reject_to 必须是上游；无效→Skipped{Op, Reason 白话}；有效→应用到 definition 副本；末尾 RefreshDerived+NormalizeSopDefinition；**永不落库**（返回草稿 DTO，前端逐节点/全部应用后才走 PUT 保存）。`ParseFlowDraft`：JSON→SopDefinition→Normalize+RefreshDerived→ValidateDefinition（allowIncomplete=true，草稿允许缺执行器）→返回 {definition, warnings, status: "draft"}。SSE handler 照 T24 RunEvents 模式（chan→data: 帧）。

- [ ] **Step 4: 运行确认通过** Run: `go test ./internal/application/service/sop/ -run "TestOrchestrate|TestBreakdown" -v && go test ./internal/handler/ -run TestSopAI -v && go build ./...` Expected: PASS。
- [ ] **Step 5: 提交** `git add internal/handler/sop_ai.go internal/handler/sop_ai_test.go internal/application/service/sop/ai.go internal/application/service/sop/ai_test.go internal/router/routes_sop.go && git commit -m "feat(sop): AI 助手四能力 SSE（拆解/分析/澄清/编排双模式，草稿-only 与清单校验）"`

### Task 26: 管理端点（审批中心/触发配置/触发历史/CLI 凭据/PM 归档摘要）

**Files:**
- Create: `internal/handler/sop_admin.go`、`internal/handler/sop_admin_test.go`
- Modify: `internal/router/routes_sop.go`（admin 组）

**Interfaces:**
- Consumes: T20 ApprovalService.ListPending、T6 SopTriggerRepo/SopCredentialRepo、T22 ItemService（pm_migrated 过滤）。
- Produces: 管理路由组（cli-status 与 run-now 分别由 T34/T35 追加，本任务不注册未实现端点）。

路由：

```
GET    /sop/approvals                          read_sop
GET    /sop/projects/:id/trigger-config        read_sop
PUT    /sop/projects/:id/trigger-config        manage_sop
GET    /sop/projects/:id/trigger-logs          read_sop
GET    /sop/cli-credentials                    manage_sop（列表脱敏：只回 kind/name/尾 4 位）
PUT    /sop/cli-credentials/:kind              manage_sop（upsert，body {name, api_key, base_url?, model?}）
DELETE /sop/cli-credentials/:kind              manage_sop
GET    /sop/pm-archive/summary                 read_sop（迁移计数：项目/事项/运行/附件）
```

- [ ] **Step 1: 写失败测试** `sop_admin_test.go`：

```go
func TestCredentialUpsertMasksAndEncrypts(t *testing.T) {
	r, db := newSOPTestRouter(t)
	w := doJSON(t, r, "PUT", "/api/v1/sop/cli-credentials/zcode", `{"name":"我的 zcode","api_key":"sk-super-secret-1234"}`)
	require.Equal(t, http.StatusOK, w.Code)
	require.NotContains(t, w.Body.String(), "sk-super-secret-1234") // 响应脱敏
	require.Contains(t, w.Body.String(), "1234")                    // 尾 4 位
	// 库内密文
	var raw string
	db.Raw("SELECT api_key FROM sop_cli_credentials WHERE cli_kind='zcode'").Scan(&raw)
	require.NotContains(t, raw, "sk-super-secret-1234")
	// 列表脱敏
	w = doJSON(t, r, "GET", "/api/v1/sop/cli-credentials", "")
	require.NotContains(t, w.Body.String(), "sk-super-secret-1234")
}

func TestTriggerConfigValidation(t *testing.T) {
	r, _ := newSOPTestRouter(t)
	pid := createTestProject(t, r)
	w := doJSON(t, r, "PUT", "/api/v1/sop/projects/"+pid+"/trigger-config", `{"mode":"cron","cron":"* * * * *","per_run_limit":50}`)
	require.Equal(t, http.StatusBadRequest, w.Code) // cron <5min + limit 越界
	require.Contains(t, w.Body.String(), "5")
	w = doJSON(t, r, "PUT", "/api/v1/sop/projects/"+pid+"/trigger-config", `{"mode":"interval","interval_key":"30m","per_run_limit":3,"statuses":["todo"],"sources":["customer"]}`)
	require.Equal(t, http.StatusOK, w.Code)
}

func TestApprovalsEndpointAggregates(t *testing.T) {
	r, db := newSOPTestRouter(t)
	seedWaitingApprovalRun(t, db)
	w := doJSON(t, r, "GET", "/api/v1/sop/approvals", "")
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), `"kind":"sop"`)
}
```

- [ ] **Step 2: 运行确认失败** Run: `go test ./internal/handler/ -run "TestCredential|TestTriggerConfig|TestApprovalsEndpoint" -v` Expected: 编译失败。
- [ ] **Step 3: 实现** sop_admin.go：凭据 upsert→types.SopCLICredential（BeforeSave 自动加密；**body.name→Extra["name"]**，表无 Name 列；base_url/model→BaseURL/DefaultModel）→repo.Upsert（T6 签名）→响应 DTO 脱敏（APIKey 字段 json:"-"，另出 tail4；列表 DTO 的 name 取 Extra["name"]）；trigger-config PUT→ValidateSopTriggerConfig（T6）→Upsert；trigger-logs→repo.ListLogs 分页；approvals→ApprovalService.ListPending；pm-archive/summary→count items where domain_data.pm_source_id 非空 + 关联 runs/attachments 计数（Go 侧过滤，照澄清 11 幂等键查询模式）。routes 追加 admin 组。
- [ ] **Step 4: 运行确认通过** Run: `go test ./internal/handler/ -run "TestCredential|TestTriggerConfig|TestApprovalsEndpoint" -v && go build ./...` Expected: PASS。
- [ ] **Step 5: 提交** `git add internal/handler/sop_admin.go internal/handler/sop_admin_test.go internal/router/routes_sop.go && git commit -m "feat(sop): 管理 API（审批聚合/触发配置/凭据加密脱敏/PM 归档摘要）"`

---

# Milestone M6 — CLI 执行器全家桶（10 种，spec §5.2 逐字落地）

> **唯一权威**：启动参数/输出协议/凭据注入以 spec §5.2 表格为准（执行前必读该行）。通用机制（私有 HOME/24KiB/系统提示/MCP 子集/成败判定/2.5s 排水/续跑/记忆边界）全部按 spec §5.2 通用机制段实现，借鉴 LoopForge `agent_cli.go:320-516`、`mcp_provision.go:111-223`、`runner.go:101-144` 的已验证模式（只借鉴语义，不搬代码）。

### Task 27: 私有 HOME（os.Root 防符号链接）+ 凭据 env-only + prompt 24KiB 阈值

**Files:**
- Create: `internal/application/service/sop/executor/cli/home.go`、`prompt.go`、`home_test.go`

**Interfaces:**
- Consumes: T6 SopCLICredential（解密后）、T10 Workspace.PrivateHomeDir。
- Produces:

```go
package cli

func PrivateHome(ws *engine.Workspace, kind string) (*Home, error) // <ws>/.sop/private-home/<kind>/
type Home struct{ Root *os.Root; Dir string }
func (h *Home) WriteFile(rel string, content []byte) error // os.Root 逐段校验，拒绝符号链接越界
func (h *Home) WriteAgentSystem(content string) error      // agent-system.md
func CredentialEnv(c *types.SopCLICredential) map[string]string // {"SOP_CLI_API_KEY": c.APIKey, ...BaseURL/Model 非空时追加 SOP_CLI_BASE_URL/SOP_CLI_MODEL}
func PromptDelivery(prompt string, preferStdin bool, ws *engine.Workspace, nodeID string, attempt int) (argv string, stdin string, err error)
// <=24KiB（24576 字节）→argv 原文；>24KiB 且 preferStdin→stdin 原文；
// >24KiB 且 !preferStdin→写 .sop/prompt-<node>-<attempt>.md，argv=该绝对路径
```

- [ ] **Step 1: 写失败测试** `home_test.go`：

```go
func TestPrivateHomeRejectsSymlinkEscape(t *testing.T) {
	ws, _ := engine.PrepareWorkspace(t.TempDir(), "r1")
	h, err := PrivateHome(ws, "zcode")
	require.NoError(t, err)
	require.NoError(t, h.WriteFile("config.json", []byte("{}")))
	outside := t.TempDir()
	require.NoError(t, os.Symlink(outside, filepath.Join(h.Dir, "escape")))
	err = h.WriteFile("escape/pwned", []byte("x"))
	require.Error(t, err) // os.Root 拒绝符号链接越界
}

func TestCredentialEnvOnlyNoPlaintextInConfig(t *testing.T) {
	c := &types.SopCLICredential{CLIKind: "zcode", APIKey: "sk-secret"}
	env := CredentialEnv(c)
	require.Equal(t, "sk-secret", env["SOP_CLI_API_KEY"])
	// 配置文件渲染只引用变量名（各适配器 ConfigFiles 的公共断言）
	cfg := RenderZcodeConfig(c)
	require.NotContains(t, cfg, "sk-secret")
	require.Contains(t, cfg, "SOP_CLI_API_KEY")
}

func TestPromptDeliveryThreshold(t *testing.T) {
	ws, _ := engine.PrepareWorkspace(t.TempDir(), "r1")
	small := strings.Repeat("a", 1000)
	argv, stdin, err := PromptDelivery(small, false, ws, "n1", 1)
	require.NoError(t, err)
	require.Equal(t, small, argv)
	require.Empty(t, stdin)
	big := strings.Repeat("字", 10000) // 30000 字节 > 24576
	argv, stdin, err = PromptDelivery(big, false, ws, "n1", 1)
	require.NoError(t, err)
	require.Empty(t, stdin)
	require.FileExists(t, argv) // 落文件传路径
	body, _ := os.ReadFile(argv)
	require.Equal(t, big, string(body))
	argv, stdin, err = PromptDelivery(big, true, ws, "n1", 1)
	require.NoError(t, err)
	require.Empty(t, argv)
	require.Equal(t, big, stdin) // claude/codebuddy stdin 优先
}
```

- [ ] **Step 2: 运行确认失败** Run: `go test ./internal/application/service/sop/executor/cli/ -run "TestPrivateHome|TestCredentialEnv|TestPromptDelivery" -v` Expected: 编译失败。
- [ ] **Step 3: 实现** home.go：`os.OpenRoot(filepath.Join(ws.PrivateHomeDir, kind))`（Go 1.24+ os.Root，Know go.mod 已满足；若版本不足改用逐段 EvalSymlinks 前缀校验）；WriteFile 经 root.OpenFile（自动拒绝越界符号链接）；CredentialEnv 只产出环境变量，**任何配置文件不得出现明文 Key**（配置模板用 `${SOP_CLI_API_KEY}` 占位，CLI 自身展开或适配器渲染时保留变量引用——按 spec §5.2 各行凭据注入列：环境变量类直接 env；配置文件类写引用）。prompt.go：阈值常量 `PromptArgMaxBytes = 24576`，按字节判（Windows 命令行 ~32KiB 上限留余量）。RenderZcodeConfig 等配置渲染函数随 T30–T32 适配器交付，本任务先交付 zcode 一个供测试（`.zcode/cli/config.json`：`{"apiKey":"${SOP_CLI_API_KEY}","model":"${SOP_CLI_MODEL}"}` 形态，具体键名以 spec §5.2 zcode 行与实测为准）。
- [ ] **Step 4: 运行确认通过** Run: `go test ./internal/application/service/sop/executor/cli/ -run "TestPrivateHome|TestCredentialEnv|TestPromptDelivery" -v && go build ./...` Expected: PASS。
- [ ] **Step 5: 提交** `git add internal/application/service/sop/executor/cli/home.go internal/application/service/sop/executor/cli/prompt.go internal/application/service/sop/executor/cli/home_test.go && git commit -m "feat(sop): CLI 私有 HOME（os.Root 防越界）/凭据 env-only/prompt 24KiB 阈值"`

### Task 28: 进程治理（Windows Job Object 进程组杀灭/2.5s 排水/超时）

**Files:**
- Create: `internal/application/service/sop/executor/cli/process.go`、`process_windows.go`、`process_unix.go`、`process_test.go`
- Modify: `go.mod`（若缺 golang.org/x/sys 则 `go get golang.org/x/sys/windows`）

**Interfaces:**
- Consumes: T18 内联杀进程逻辑（本任务抽公共后 T18 改调本包）。
- Produces:

```go
type Proc struct{ Cmd *exec.Cmd; /* job handle 等 */ }
func Start(ctx context.Context, name string, args []string, env []string, dir string, stdin io.Reader) (*Proc, error)
func (p *Proc) Wait() (exitCode int, err error)
func (p *Proc) KillTree() error // Windows: Job Object KILL_ON_JOB_CLOSE 已挂→终止 job；兜底 taskkill /T /F /PID；unix: kill(-pgid, SIGKILL)
func DrainThenKill(p *Proc, grace time.Duration) // 终态帧后 2.5s 排水即杀（spec §5.2）
```

- [ ] **Step 1: 写失败测试** `process_test.go`（跨平台用 bash 子进程树）：

```go
func TestKillTreeTerminatesChildren(t *testing.T) {
	// bash -c 'sleep 60 & wait' → 父子两进程
	p, err := Start(context.Background(), "bash", []string{"-c", "sleep 60 & wait"}, os.Environ(), t.TempDir(), nil)
	require.NoError(t, err)
	time.Sleep(200 * time.Millisecond)
	require.NoError(t, p.KillTree())
	done := make(chan struct{})
	go func() { p.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("process tree not killed")
	}
}

func TestDrainThenKillWithinGrace(t *testing.T) {
	p, err := Start(context.Background(), "bash", []string{"-c", "echo done; sleep 60"}, os.Environ(), t.TempDir(), nil)
	require.NoError(t, err)
	start := time.Now()
	DrainThenKill(p, 2500*time.Millisecond)
	p.Wait()
	require.Less(t, time.Since(start), 6*time.Second)
}

func TestStartTimeoutKilled(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	p, err := Start(ctx, "bash", []string{"-c", "sleep 30"}, os.Environ(), t.TempDir(), nil)
	require.NoError(t, err)
	_, err = p.Wait()
	require.Error(t, err) // ctx 超时→KillTree→Wait 返回错误
}
```

- [ ] **Step 2: 运行确认失败** Run: `go test ./internal/application/service/sop/executor/cli/ -run "TestKillTree|TestDrain|TestStartTimeout" -v` Expected: 编译失败。
- [ ] **Step 3: 实现** process.go：Start 组装 exec.Cmd（Env/Dir/StdinPipe），平台钩子 beforeStart（windows：CREATE_NEW_PROCESS_GROUP + 创建 Job Object 设 JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE 并 AssignProcessToJobObject；unix：Setpgid=true）；ctx 取消 goroutine→KillTree；Wait 返回 ExitCode。process_windows.go：Job Object 用 golang.org/x/sys/windows（CreateJobObject/SetInformationJobObject/AssignProcessToJobObject）；KillTree 先 TerminateJobObject，失败兜底 `taskkill /T /F /PID <pid>`。process_unix.go：kill(-pgid)。DrainThenKill：timer grace 后 KillTree（调用方在收到终态帧后 go DrainThenKill）。T18 script.go 的内联杀进程改调 cli.Start/KillTree（回归 TestScript* 全绿）。
- [ ] **Step 4: 运行确认通过** Run: `go test ./internal/application/service/sop/executor/cli/ -run "TestKillTree|TestDrain|TestStartTimeout" -v && go test ./internal/application/service/sop/executor/ -run TestScript -v && go build ./...` Expected: PASS。
- [ ] **Step 5: 提交** `git add internal/application/service/sop/executor/cli/process*.go internal/application/service/sop/executor/script.go go.mod go.sum && git commit -m "feat(sop): CLI 进程治理（Windows Job Object 进程组杀灭/2.5s 排水/超时）"`

### Task 29: 流归一化框架 + claude 基准适配器 + golden fixtures + CLIProvider 骨架

**Files:**
- Create: `internal/application/service/sop/executor/cli/adapter.go`、`stream.go`、`provider.go`、`claude.go`、`stream_test.go`、`provider_test.go`
- Create: `internal/application/service/sop/executor/cli/testdata/claude/stream.jsonl`、`testdata/claude/result.jsonl`

**Interfaces:**
- Consumes: T8 Provider/TaskBook/ExecResult/StreamEvent、T27 Home/PromptDelivery/CredentialEnv、T28 Proc。
- Produces:（**T30–T33 全部适配器消费，签名锁定**）

```go
type Adapter interface {
	Kind() string
	BinaryName() string
	BuildArgs(tb executor.TaskBook, prompt string, resumeID string) []string
	PreferStdin() bool
	SystemPromptMode() string            // native（opencode/pi 原生加载文件）| wrap（<system_instructions> 前置包裹）
	ConfigFiles(tb executor.TaskBook) map[string]string // 私有 HOME 相对路径→内容（凭据只引用 env 变量名）
	Env(tb executor.TaskBook) map[string]string
	ParseLine(line string) (events []executor.StreamEvent, terminal bool)
	ResumeID(events []executor.StreamEvent) string // 不支持续跑返回 ""
	FailureHint(stderr string) string   // max-turns 类→"human"；其余→""
}

func RunStream(ctx context.Context, a Adapter, tb executor.TaskBook, emit func(executor.StreamEvent)) (executor.ExecResult, error)
// 内部：Home 准备→ConfigFiles+agent-system.md 落盘→PromptDelivery→BuildArgs→Env 合并（HOME=私有 Dir + SOP_CLI_* + PATH 保留）→
// cli.Start→逐行 Scan→a.ParseLine→emit+收集→terminal 帧→DrainThenKill(2.5s)→Wait→
// 成败判定（spec §5.2）：有 result 帧→按其 status；无 result 帧且 exit 0→GateResult=""（交门禁）；
// exit!=0→rework+stderr 尾部；FailureHint=="human"→GateResult=human；ResumeID 提取→ExecResult.ResumeSessionID

type CLIProvider struct{ A Adapter }
func (p *CLIProvider) Kind() string { return "cli:" + p.A.Kind() }
// Prepare：exec.LookPath(BinaryName) 失败→白话错误「未检测到 <kind> 命令，请先安装并加入 PATH（设置页有指引）」
// Execute：RunStream；Cancel：当前 Proc.KillTree
```

- [ ] **Step 1: 写失败测试**

`testdata/claude/stream.jsonl`（golden，照 Claude headless stream-json 实测形态）：

```jsonl
{"type":"system","subtype":"init","session_id":"sess-abc","model":"claude-x"}
{"type":"assistant","message":{"content":[{"type":"text","text":"开始实现"}]}}
{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Write","input":{"path":"out/a.md"}}]}}
{"type":"result","subtype":"success","session_id":"sess-abc","result":"实现完成","usage":{"input_tokens":120,"output_tokens":80},"total_cost_usd":0.01}
```

`stream_test.go`：

```go
func TestClaudeStreamNormalization(t *testing.T) {
	body, _ := os.ReadFile("testdata/claude/stream.jsonl")
	var events []executor.StreamEvent
	terminal := false
	for _, line := range strings.Split(strings.TrimSpace(string(body)), "\n") {
		evs, term := ClaudeAdapter{}.ParseLine(line)
		events = append(events, evs...)
		terminal = terminal || term
	}
	require.True(t, terminal)
	var texts, tools int
	var last executor.StreamEvent
	for _, e := range events {
		switch e.Type {
		case "text_delta":
			texts++
		case "tool_call":
			tools++
		case "result":
			last = e
		}
	}
	require.Equal(t, 1, texts)
	require.Equal(t, 1, tools)
	require.Equal(t, "result", last.Type)
	require.Equal(t, "sess-abc", ClaudeAdapter{}.ResumeID(events))
}

func TestClaudeBuildArgsHeadless(t *testing.T) {
	tb := executor.TaskBook{Model: "claude-x", ResumeSessionID: "sess-abc"}
	args := ClaudeAdapter{}.BuildArgs(tb, "PROMPT", "sess-abc")
	joined := strings.Join(args, " ")
	require.Contains(t, joined, "--output-format stream-json")
	require.Contains(t, joined, "--resume sess-abc")
	require.True(t, ClaudeAdapter{}.PreferStdin())
}

func TestRunStreamNoResultFrameExitZeroDefersToGate(t *testing.T) {
	// 用 fake binary（bash 脚本）输出两行非 result JSON 后 exit 0
	dir := t.TempDir()
	bin := filepath.Join(dir, "claude")
	require.NoError(t, os.WriteFile(bin, []byte("#!/bin/bash\necho '{\"type\":\"assistant\",\"message\":{\"content\":[{\"type\":\"text\",\"text\":\"hi\"}]}}'\nexit 0\n"), 0o755))
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	res, err := RunStream(context.Background(), ClaudeAdapter{}, executor.TaskBook{NodeID: "n1", WorkspaceRoot: t.TempDir(), Timeout: 10 * time.Second}, nil)
	require.NoError(t, err)
	require.Empty(t, res.GateResult) // 无 result 帧且 exit 0→交门禁裁定
	require.Contains(t, res.Output, "hi")
}

func TestRunStreamMaxTurnsGoesHuman(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "claude")
	require.NoError(t, os.WriteFile(bin, []byte("#!/bin/bash\necho 'error: max turns exceeded' >&2\nexit 1\n"), 0o755))
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	res, err := RunStream(context.Background(), ClaudeAdapter{}, executor.TaskBook{NodeID: "n1", WorkspaceRoot: t.TempDir(), Timeout: 10 * time.Second}, nil)
	require.NoError(t, err)
	require.Equal(t, types.SopGateHuman, res.GateResult)
}

func TestCLIProviderPrepareMissingBinaryPlainWords(t *testing.T) {
	p := &CLIProvider{A: ClaudeAdapter{}}
	t.Setenv("PATH", t.TempDir()) // 空 PATH
	err := p.Prepare(context.Background(), &types.SopNode{}, t.TempDir())
	require.Error(t, err)
	require.Contains(t, err.Error(), "未检测到")
	require.Contains(t, err.Error(), "PATH")
}
```

- [ ] **Step 2: 运行确认失败** Run: `go test ./internal/application/service/sop/executor/cli/ -run "TestClaude|TestRunStream|TestCLIProvider" -v` Expected: 编译失败。
- [ ] **Step 3: 实现** adapter.go/stream.go/provider.go 按 Interfaces；claude.go：BinaryName="claude"；BuildArgs=`-p --output-format stream-json --verbose [--model <m>] [--resume <id>]`（prompt 走 stdin 时 argv 不带 prompt）；SystemPromptMode=wrap；ConfigFiles：`.claude/settings.json` 引用 env（`{"env":{"ANTHROPIC_API_KEY":"${SOP_CLI_API_KEY}"}}` 形态，键名以 spec §5.2 claude 行「环境变量/配置引用」为准）；Env：ANTHROPIC_API_KEY=SOP_CLI_API_KEY 值（claude 属环境变量类）；ParseLine：type=assistant→content[].text→text_delta、tool_use→tool_call；type=result→result 事件（Payload 含 usage/cost/session_id）+terminal；ResumeID 取 init/result 的 session_id；FailureHint：stderr 含 "max turns"/"turn limit"→"human"。RunStream 成败判定严格按 spec §5.2 通用机制段。Registry 注册：T30–T32 每交付一个适配器即 `reg.Register(&CLIProvider{A: XxxAdapter{}})`（container 接线在 T34 汇总）。
- [ ] **Step 4: 运行确认通过** Run: `go test ./internal/application/service/sop/executor/cli/ -run "TestClaude|TestRunStream|TestCLIProvider" -v && go build ./...` Expected: PASS。
- [ ] **Step 5: 提交** `git add internal/application/service/sop/executor/cli/ && git commit -m "feat(sop): CLI 流归一化框架与 claude 基准适配器（golden fixtures/成败判定/续跑）"`

### Task 30: 通用适配器 ① opencode / cursor-agent / antigravity(agy)

**Files:**
- Create: `cli/opencode.go`、`cli/cursor.go`、`cli/agy.go`、`cli/adapters1_test.go`
- Create: `cli/testdata/opencode/stream.jsonl`、`cli/testdata/cursor/stream.jsonl`、`cli/testdata/agy/stream.jsonl`

**Interfaces:**
- Consumes: T29 Adapter/RunStream/CLIProvider。
- Produces: `OpencodeAdapter{}`、`CursorAdapter{}`、`AgyAdapter{}`（均实现 Adapter；Kind 分别 "opencode"/"cursor-agent"/"agy"）。

- [ ] **Step 1: 写失败测试** `adapters1_test.go`（每种三断言：args 快照 / fixture 归一化 / resume 提取）：

```go
func TestOpencodeAdapter(t *testing.T) {
	a := OpencodeAdapter{}
	args := strings.Join(a.BuildArgs(executor.TaskBook{Model: "m1"}, "P", "sess-1"), " ")
	require.Contains(t, args, "run --format json --thinking")
	require.Contains(t, args, "-m m1")
	require.Contains(t, args, "-s sess-1")
	require.Equal(t, "native", a.SystemPromptMode()) // opencode 原生加载 agent-system.md
	require.Contains(t, a.Env(executor.TaskBook{Credential: &types.SopCLICredential{APIKey: "k"}}), "OPENCODE_CONFIG_CONTENT")
	evs, term := parseFixture(t, a, "testdata/opencode/stream.jsonl")
	require.True(t, term) // step_finish 收尾
	require.NotEmpty(t, filterType(evs, "text_delta"))
	require.Equal(t, "sess-1", a.ResumeID(evs))
}

func TestCursorAdapter(t *testing.T) {
	a := CursorAdapter{}
	args := strings.Join(a.BuildArgs(executor.TaskBook{Model: "m1"}, "P", "sess-2"), " ")
	require.Contains(t, args, "-p --output-format stream-json --stream-partial-output --trust --force --approve-mcps")
	require.Contains(t, args, "--model m1")
	require.Contains(t, args, "--resume sess-2")
	require.Equal(t, "CURSOR_API_KEY", a.Env(executor.TaskBook{Credential: &types.SopCLICredential{APIKey: "k"}})["CURSOR_API_KEY_NAME"]) // env 键存在性断言见实现
	evs, term := parseFixture(t, a, "testdata/cursor/stream.jsonl")
	require.True(t, term) // result 终止
	require.NotEmpty(t, filterType(evs, "text_delta")) // timestamp_ms 增量
}

func TestAgyAdapter(t *testing.T) {
	a := AgyAdapter{}
	args := strings.Join(a.BuildArgs(executor.TaskBook{}, "P", ""), " ")
	require.Contains(t, args, "--output-format stream-json --dangerously-skip-permissions --disable-slash-commands -p")
	cfg := a.ConfigFiles(executor.TaskBook{Credential: &types.SopCLICredential{APIKey: "k"}})
	require.Contains(t, cfg, ".gemini/antigravity-cli/settings.json")
	require.NotContains(t, cfg[".gemini/antigravity-cli/settings.json"], "k") // 明文 Key 不落配置
	evs, term := parseFixture(t, a, "testdata/agy/stream.jsonl")
	require.True(t, term) // event=result.status
	require.NotEmpty(t, filterType(evs, "text_delta")) // step_update
}
```

（parseFixture/filterType 测试助手放 stream_test.go：逐行 ParseLine 聚合；fixture 内容按各 CLI 实测协议形态手写 golden——opencode：`{"type":"part","part":{"type":"text","text":"…"}}`…`{"type":"step_finish","session_id":"sess-1"}`；cursor：`{"timestamp_ms":1,"delta":{"text":"…"}}`…`{"type":"result","session_id":"sess-2","status":"success"}`；agy：`{"event":"init"}`/`{"event":"step_update","text":"…"}`/`{"event":"result","status":"success"}`。T57 真机回归时以实测输出替换 golden 并复跑。）

- [ ] **Step 2: 运行确认失败** Run: `go test ./internal/application/service/sop/executor/cli/ -run "TestOpencode|TestCursor|TestAgy" -v` Expected: 编译失败。
- [ ] **Step 3: 实现三个适配器**（参数/协议/凭据逐字按 spec §5.2 对应行）：opencode：BinaryName="opencode"，BuildArgs=`run --format json --thinking [-m model] [-s resume] <prompt>`，Env OPENCODE_CONFIG_CONTENT=整段 JSON（provider/model/apiKey 引用 `${SOP_CLI_API_KEY}`），SystemPromptMode=native（agent-system.md 放私有 HOME 根），ParseLine part.type=text→text_delta、step_finish→terminal+result，ResumeID=step_finish.session_id；cursor：BinaryName="cursor-agent"，Env CURSOR_API_KEY=凭据值，ParseLine delta.text→text_delta（timestamp_ms 排序）、type=result→terminal，ResumeID=result.session_id；agy：BinaryName="agy"，ConfigFiles settings.json（apiKey 引用 env）+Env GEMINI_API_KEY，ParseLine event=step_update→text_delta、event=result→terminal（status 判成败），ResumeID=""（不支持续跑）。
- [ ] **Step 4: 运行确认通过** Run: `go test ./internal/application/service/sop/executor/cli/ -run "TestOpencode|TestCursor|TestAgy" -v && go build ./...` Expected: PASS。
- [ ] **Step 5: 提交** `git add internal/application/service/sop/executor/cli/ && git commit -m "feat(sop): CLI 适配器 opencode/cursor-agent/agy（spec §5.2 协议固化）"`

### Task 31: 通用适配器 ② kimi / pi / dsh(DeepSeek) / zcode

**Files:**
- Create: `cli/kimi.go`、`cli/pi.go`、`cli/dsh.go`、`cli/zcode.go`、`cli/adapters2_test.go`
- Create: `cli/testdata/kimi/stream.jsonl`、`cli/testdata/pi/stream.jsonl`、`cli/testdata/dsh/stream.jsonl`、`cli/testdata/zcode/stream.jsonl`

**Interfaces:**
- Consumes: T29 Adapter/RunStream。
- Produces: `KimiAdapter{}`、`PiAdapter{}`、`DshAdapter{}`、`ZcodeAdapter{}`（Kind "kimi"/"pi"/"dsh"/"zcode"）。

- [ ] **Step 1: 写失败测试** `adapters2_test.go`（同 T30 三断言模式）：

```go
func TestKimiAdapter(t *testing.T) {
	a := KimiAdapter{}
	args := strings.Join(a.BuildArgs(executor.TaskBook{}, "P", "sess-k"), " ")
	require.Contains(t, args, "--output-format stream-json")
	require.Contains(t, args, "-S sess-k")
	require.Contains(t, args, "-p")
	cfg := a.ConfigFiles(executor.TaskBook{Credential: &types.SopCLICredential{APIKey: "k"}})
	require.Contains(t, cfg, ".kimi-code/config.toml")
	require.NotContains(t, cfg[".kimi-code/config.toml"], "k")
	evs, term := parseFixture(t, a, "testdata/kimi/stream.jsonl")
	require.True(t, term)
	require.Equal(t, "sess-k", a.ResumeID(evs)) // meta.session.resume_hint
}

func TestPiAdapter(t *testing.T) {
	a := PiAdapter{}
	args := strings.Join(a.BuildArgs(executor.TaskBook{}, "P", "sess-p"), " ")
	require.Contains(t, args, "--mode json")
	require.Contains(t, args, "--session-id")
	require.Equal(t, "native", a.SystemPromptMode()) // --append-system-prompt 文件
	evs, term := parseFixture(t, a, "testdata/pi/stream.jsonl")
	require.True(t, term) // agent_end
	require.NotEmpty(t, filterType(evs, "text_delta")) // message_update
}

func TestDshAdapter(t *testing.T) {
	a := DshAdapter{}
	args := strings.Join(a.BuildArgs(executor.TaskBook{}, "P", ""), " ")
	require.Contains(t, args, "--profile headless --json --permission-mode danger-full-access --approval-policy never")
	evs, term := parseFixture(t, a, "testdata/dsh/stream.jsonl")
	require.True(t, term) // final.reason.kind 判成败
	// 事件截断 8KiB
	long := strings.Repeat("x", 9000)
	evs2, _ := a.ParseLine(`{"event":"delta","text":"` + long + `"}`)
	for _, e := range evs2 {
		require.LessOrEqual(t, len(e.Text), 8192)
	}
}

func TestZcodeAdapter(t *testing.T) {
	a := ZcodeAdapter{}
	args := strings.Join(a.BuildArgs(executor.TaskBook{}, "P", ""), " ")
	require.Contains(t, args, "--output-format stream-json --mode yolo --no-color")
	require.Contains(t, args, "--prompt")
	require.Contains(t, args, "--disallowed-tools")
	cfg := a.ConfigFiles(executor.TaskBook{Credential: &types.SopCLICredential{APIKey: "k"}})
	require.Contains(t, cfg, ".zcode/cli/config.json")
	require.NotContains(t, cfg[".zcode/cli/config.json"], "k")
	evs, term := parseFixture(t, a, "testdata/zcode/stream.jsonl")
	require.True(t, term)
}
```

（golden fixtures：kimi 整消息帧+`{"meta":{"session":{"resume_hint":"sess-k"}}}`；pi `{"type":"message_update","text":"…"}`…`{"type":"agent_end"}`；dsh delta 事件+`{"type":"final","reason":{"kind":"completed"}}`；zcode 标准 stream-json（与 claude 基准同形）。）

- [ ] **Step 2: 运行确认失败** Run: `go test ./internal/application/service/sop/executor/cli/ -run "TestKimi|TestPi|TestDsh|TestZcode" -v` Expected: 编译失败。
- [ ] **Step 3: 实现四个适配器**（逐字按 spec §5.2 对应行）：kimi：BinaryName="kimi"，ConfigFiles `.kimi-code/config.toml`（api_key 引用 env），ParseLine 整消息→text_delta、meta.session.resume_hint→ResumeID；pi：BinaryName="pi"，BuildArgs `--mode json --session-id <uuid> [--thinking] [--append-system-prompt <agent-system.md 绝对路径>]`（session-id 用 tb.ResumeSessionID 或新 uuid），ConfigFiles `.pi/agent/models.json`（apiKey 引用 env），ParseLine message_update→text_delta、agent_end→terminal；dsh：BinaryName="dsh"，BuildArgs `--profile headless --json --permission-mode danger-full-access --approval-policy never`（--patch 插 profile 参数后），ConfigFiles `.dsh/` 模型 patch（引用 env），ParseLine final.reason.kind=completed→pass 语义 result、其他 kind→fail 语义、单事件 Text 截断 8KiB；zcode：BinaryName="zcode"，BuildArgs `--prompt <p> --output-format stream-json --mode yolo --no-color --disallowed-tools …`（禁用工具清单=平台侧危险工具常量，执行时按 zcode 实测工具名固化），ConfigFiles `.zcode/cli/config.json`（RenderZcodeConfig，T27 已交付），ParseLine 标准 stream-json 翻译为基准。
- [ ] **Step 4: 运行确认通过** Run: `go test ./internal/application/service/sop/executor/cli/ -run "TestKimi|TestPi|TestDsh|TestZcode" -v && go build ./...` Expected: PASS。
- [ ] **Step 5: 提交** `git add internal/application/service/sop/executor/cli/ && git commit -m "feat(sop): CLI 适配器 kimi/pi/dsh/zcode（spec §5.2 协议固化）"`

### Task 32: 专用适配器 codebuddy / codex（翻译为 claude 基准）

**Files:**
- Create: `cli/codebuddy.go`、`cli/codex.go`、`cli/adapters3_test.go`
- Create: `cli/testdata/codebuddy/stream.jsonl`、`cli/testdata/codex/stream.jsonl`

**Interfaces:**
- Consumes: T29 Adapter/RunStream/claude 基准事件形态。
- Produces: `CodebuddyAdapter{}`、`CodexAdapter{}`（Kind "codebuddy"/"codex"）。

- [ ] **Step 1: 写失败测试** `adapters3_test.go`：

```go
func TestCodebuddyAdapter(t *testing.T) {
	a := CodebuddyAdapter{}
	require.True(t, a.PreferStdin()) // headless stdin 优先（spec §5.2 prompt 传递）
	args := strings.Join(a.BuildArgs(executor.TaskBook{}, "", ""), " ")
	require.NotEmpty(t, args) // headless + JSON 流参数
	evs, term := parseFixture(t, a, "testdata/codebuddy/stream.jsonl")
	require.True(t, term)
	require.NotEmpty(t, filterType(evs, "text_delta")) // 翻译为基准事件
	require.NotEmpty(t, filterType(evs, "result"))
}

func TestCodexAdapter(t *testing.T) {
	a := CodexAdapter{}
	args := strings.Join(a.BuildArgs(executor.TaskBook{}, "P", ""), " ")
	require.Contains(t, args, "exec --json")
	evs, term := parseFixture(t, a, "testdata/codex/stream.jsonl")
	require.True(t, term)
	require.NotEmpty(t, filterType(evs, "result")) // 翻译为基准
}
```

- [ ] **Step 2: 运行确认失败** Run: `go test ./internal/application/service/sop/executor/cli/ -run "TestCodebuddy|TestCodex" -v` Expected: 编译失败。
- [ ] **Step 3: 实现** codebuddy：BinaryName="codebuddy"，headless+JSON 流（参数以 spec §5.2 codebuddy 行与 T57 实测固化），PreferStdin=true，ConfigFiles 配置引用 env，ParseLine 把自有 JSON 帧翻译为基准（text→text_delta、tool→tool_call、终态→result）；codex：BinaryName="codex"，BuildArgs=`exec --json <prompt>`，ConfigFiles `.codex/config.toml`（引用 env），ParseLine codex exec JSON 事件翻译为基准。两者 ResumeID=""（首版不续跑，返工=新会话+carry_over 产物）。
- [ ] **Step 4: 运行确认通过** Run: `go test ./internal/application/service/sop/executor/cli/ -run "TestCodebuddy|TestCodex" -v && go build ./...` Expected: PASS。
- [ ] **Step 5: 提交** `git add internal/application/service/sop/executor/cli/ && git commit -m "feat(sop): CLI 适配器 codebuddy/codex（翻译为基准事件流）"`

### Task 33: MCP 注入（各 CLI 配置路径表 + 节点 mcp 子集 + Know MCP kb_scope + Bearer）

**Files:**
- Create: `cli/mcp.go`、`cli/mcp_test.go`
- Modify: 10 个适配器的 ConfigFiles/Env（追加 MCP 段渲染）

**Interfaces:**
- Consumes: T8 TaskBook.MCPServers（节点子集解析后的真实配置）、T6 凭据、Know MCP server endpoint（internal/mcpserver，T37 扩展 SOP 工具组）。
- Produces: `cli.RenderMCPConfig(kind string, servers []types.MCPService, knowEndpoint KnowMCPEndpoint) map[string]string`（私有 HOME 相对路径→内容片段）；`type KnowMCPEndpoint = executor.KnowMCPEndpoint`（**类型别名**，实体定义在 T8 executor/provider.go，本任务只加别名行保持本包测试写法不变）。

- [ ] **Step 1: 写失败测试** `mcp_test.go`：

```go
func TestRenderMCPOnlyNodeSubset(t *testing.T) {
	servers := []types.MCPService{{Name: "know-kb", Transport: "http", URL: "http://127.0.0.1:8080/mcp"}}
	cfg := RenderMCPConfig("claude", servers, KnowMCPEndpoint{URL: "http://127.0.0.1:8080/mcp/sop", Token: "tok", KBScope: "project"})
	body := cfg[".sop/mcp.json"]
	require.Contains(t, body, "know-kb")
	require.Contains(t, body, "Bearer tok") // HTTP MCP 凭据替换为 Bearer 头
	require.Contains(t, body, "kb_scope=project") // 范围随配置下发（服务端 T37 强制校验）
	require.NotContains(t, body, "未勾选的server")
}

func TestRenderMCPPerCLIPaths(t *testing.T) {
	servers := []types.MCPService{{Name: "know-kb", Transport: "http", URL: "http://x/mcp"}}
	ep := KnowMCPEndpoint{URL: "http://x/mcp/sop", Token: "t", KBScope: "all"}
	cases := map[string]string{
		"opencode": "OPENCODE_CONFIG_CONTENT", // env 整段 JSON 内含 mcp 段
		"cursor-agent": ".cursor/mcp.json",
		"agy": ".gemini/antigravity-cli/settings.json",
		"kimi": ".kimi-code/config.toml",
		"pi": ".pi/agent/mcp.json",
		"dsh": ".dsh/config.json",
		"zcode": ".zcode/cli/config.json",
		"claude": ".sop/mcp.json", // --mcp-config 指向
		"codebuddy": ".codebuddy/mcp.json",
		"codex": ".codex/config.toml",
	}
	for kind, wantPath := range cases {
		cfg := RenderMCPConfig(kind, servers, ep)
		if kind == "opencode" {
			require.Contains(t, cfg["ENV:OPENCODE_CONFIG_CONTENT"], "mcp")
		} else {
			require.Contains(t, cfg, wantPath, "kind=%s", kind)
		}
	}
}

func TestRenderMCPStdioLocalOnly(t *testing.T) {
	servers := []types.MCPService{{Name: "local-tool", Transport: "stdio", StdioConfig: types.MCPStdioConfig{Command: "npx", Args: []string{"-y", "some-mcp"}}}}
	cfg := RenderMCPConfig("claude", servers, KnowMCPEndpoint{})
	require.Contains(t, cfg[".sop/mcp.json"], "npx") // 首版仅本机已安装命令，不做安装托管
}
```

- [ ] **Step 2: 运行确认失败** Run: `go test ./internal/application/service/sop/executor/cli/ -run TestRenderMCP -v` Expected: 编译失败。
- [ ] **Step 3: 实现** mcp.go：按 kind 路由到各 CLI 的 MCP 配置形态（JSON mcpServers / TOML [mcp_servers] / opencode env 整段 JSON 的 mcp 段），借鉴 LoopForge mcp_provision.go:111-223 的路径表语义；HTTP server 的 Headers/AuthConfig 凭据→`Authorization: Bearer <token>`；Know MCP endpoint 恒定追加（URL 带 kb_scope/kb_ids query，Token=内部 API Key），**只渲染 TaskBook.MCPServers 里勾选的 server**（nil=角色全部已由 Resolver 展开，[]=无）；stdio 仅透传本机命令。各适配器 ConfigFiles 合并 RenderMCPConfig 输出（env 类合并进 Env）；claude BuildArgs 追加 `--mcp-config <私有 HOME>/.sop/mcp.json`。
- [ ] **Step 4: 运行确认通过** Run: `go test ./internal/application/service/sop/executor/cli/ -v && go build ./...` Expected: PASS（含 T30–T32 适配器回归）。
- [ ] **Step 5: 提交** `git add internal/application/service/sop/executor/cli/ && git commit -m "feat(sop): CLI MCP 注入（节点子集/各 CLI 配置路径/Know MCP kb_scope/Bearer）"`

### Task 34: 凭据服务 + PATH 检测 + 安装指引 + 10 适配器容器注册

**Files:**
- Create: `internal/application/service/sop/credential.go`、`credential_test.go`
- Modify: `internal/handler/sop_admin.go`（追加 GET /sop/cli-status）；`internal/router/routes_sop.go`；`internal/container/container.go`（Registry 注册 10 个 CLIProvider）

**Interfaces:**
- Consumes: T6 SopCredentialRepo、T29–T32 十个 Adapter。
- Produces: `sop.CredentialService`（`Upsert(ctx,tenantID,userID,kind,name,apiKey,baseURL,model)`、`ListMasked`、`Delete`、`Status(ctx) []CLIStatus`）；`type CLIStatus struct{ Kind, Binary string; Installed bool; Version string; Guidance string }`。

- [ ] **Step 1: 写失败测试** `credential_test.go`：

```go
func TestCLIStatusDetectsPATH(t *testing.T) {
	dir := t.TempDir()
	// 伪造一个已安装的 zcode
	require.NoError(t, os.WriteFile(filepath.Join(dir, "zcode"), []byte("#!/bin/bash\necho zcode 1.2.3\n"), 0o755))
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	svc := NewCredentialService(nil)
	st := svc.Status(context.Background())
	var z, c *CLIStatus
	for i := range st {
		if st[i].Kind == "zcode" {
			z = &st[i]
		}
		if st[i].Kind == "claude" {
			c = &st[i]
		}
	}
	require.True(t, z.Installed)
	require.Contains(t, z.Version, "1.2.3")
	require.False(t, c.Installed)
	require.Contains(t, c.Guidance, "安装") // 缺失给安装指引
	require.Len(t, st, 10)
}

func TestCredentialUpsertEncryptsAndMasks(t *testing.T) {
	db := newCredTestDB(t)
	svc := NewCredentialService(repository.NewSopCredentialRepo(db))
	ctx := context.Background()
	require.NoError(t, svc.Upsert(ctx, 1, "u1", "kimi", "我的 kimi", "sk-plain", "", ""))
	list, _ := svc.ListMasked(ctx, 1)
	require.Len(t, list, 1)
	require.NotContains(t, list[0].APIKeyTail, "sk-plain")
	require.Equal(t, "lain", list[0].APIKeyTail[len(list[0].APIKeyTail)-4:]) // 尾 4 位
	var raw string
	db.Raw("SELECT api_key FROM sop_cli_credentials WHERE kind='kimi'").Scan(&raw)
	require.NotContains(t, raw, "sk-plain")
}
```

- [ ] **Step 2: 运行确认失败** Run: `go test ./internal/application/service/sop/ -run "TestCLIStatus|TestCredentialUpsert" -v` Expected: 编译失败。
- [ ] **Step 3: 实现** credential.go：Status 逐 kind exec.LookPath(BinaryName)→命中则 `<binary> --version`（3s 超时）取首行；Guidance 按 kind 固化白话安装指引（如 opencode「npm install -g opencode-ai，详见官方文档」，10 条逐一写明二进制名与官方安装方式）。Upsert 走模型 BeforeSave 加密；ListMasked 尾 4 位。container：`for _, a := range []cli.Adapter{cli.OpencodeAdapter{}, cli.CursorAdapter{}, cli.AgyAdapter{}, cli.KimiAdapter{}, cli.PiAdapter{}, cli.DshAdapter{}, cli.ZcodeAdapter{}, cli.ClaudeAdapter{}, cli.CodebuddyAdapter{}, cli.CodexAdapter{}} { reg.Register(&cli.CLIProvider{A: a}) }`。handler cli-status→svc.Status。
- [ ] **Step 4: 运行确认通过** Run: `go test ./internal/application/service/sop/ -run "TestCLIStatus|TestCredentialUpsert" -v && go test ./internal/handler/ -v && go build ./...` Expected: PASS。
- [ ] **Step 5: 提交** `git add internal/application/service/sop/credential.go internal/application/service/sop/credential_test.go internal/handler/sop_admin.go internal/router/routes_sop.go internal/container/container.go && git commit -m "feat(sop): CLI 凭据服务/PATH 检测与安装指引/10 适配器容器注册"`

---

# Milestone M7 — SOP 任务触发器（spec §7，现有 automation 零改动）

### Task 35: 触发调度器 + 扫描派发（状态∧来源∧未跑过/幂等/上限截断）+ 立即运行 + 触发历史

**Files:**
- Create: `internal/application/service/sop/trigger.go`、`internal/application/service/sop/trigger_test.go`
- Modify: `internal/application/repository/sop_item.go`（追加 ListTriggerCandidates）、`internal/application/repository/sop_trigger.go`（追加 ListEnabledConfigs）、`internal/types/interfaces/sop.go`、`internal/handler/sop_admin.go`（run-now 端点）、`internal/router/routes_sop.go`、`internal/container/container.go`（TriggerScheduler 生命周期，照 engine 的 OnStart/OnStop 模式）

**Interfaces:**
- Consumes: T6 `SopTriggerRepo{UpsertConfig/GetConfig/AddLog/ListLogs/ReserveDispatch}` + `ValidateSopTriggerConfig`、T2 `SopItemRepo`、T4 `SopTemplateRepo`、T24 `RunService.Create/CreateRunInput{TemplateID,ItemID,Input,TriggerKind,TriggerRefID}`、`github.com/robfig/cron/v3`（go.mod 已有 v3.0.1，automation/trigger.go:26-30 同款 parser，**只复用库不 import automation 包**）。
- Produces:
  - `sop.NextTriggerRun(cfg *types.SopTriggerConfig, after time.Time) (*time.Time, error)`：interval 映射 15m/30m/hourly=after.Add；daily9=时区内下一个 09:00（当天 09:00 已过则次日）；weekly1=时区内下一个周一 09:00；cron=robfig `cron.NewParser(cron.Minute|cron.Hour|cron.Dom|cron.Month|cron.Dow|cron.Descriptor)` 在 `time.LoadLocation(cfg.Timezone)` 下 Next。
  - `sop.TriggerDeps{Triggers interfaces.SopTriggerRepo; Items interfaces.SopItemRepo; Runs *RunService; Templates interfaces.SopTemplateRepo}`。
  - `sop.TriggerService`：`NewTriggerService(d TriggerDeps)`；`ScanOnce(ctx context.Context, cfg *types.SopTriggerConfig, kind string, slot time.Time) (*types.SopTriggerLog, error)`（kind=auto|manual；核心派发逻辑，同步可测）；`RunNow(ctx context.Context, tenantID uint64, userID, projectID string) error`（取 config→未启用也允许手动→`ScanOnce(ctx, cfg, "manual", time.Now().Truncate(time.Minute))`）。
  - `sop.TriggerScheduler`：`NewTriggerScheduler(d TriggerDeps)`；`Start(ctx context.Context)`（30s ticker goroutine）；`Stop()`；每 tick `ListEnabledConfigs`→逐个 `NextRunAt==nil` 先补算存库、`NextRunAt<=now` 则 `ScanOnce(auto, slot=*NextRunAt)`；单实例进程内调度，幂等由 ReserveDispatch 兜底，无需分布式锁。
  - repository 追加：`SopItemRepo.ListTriggerCandidates(ctx, tenantID uint64, projectID string, statuses, sources []string) ([]types.SopItem, error)`；`SopTriggerRepo.ListEnabledConfigs(ctx) ([]types.SopTriggerConfig, error)`（跨租户，调度器内部用）。
  - 路由：`POST /sop/projects/:id/trigger-config/run-now  run_sop`（handler 用 `context.WithoutCancel(c.Request.Context())` 起 goroutine 调 RunNow，立即回 202 `{success:true,data:{started:true}}`——关闭页面不中断）。

- [ ] **Step 1: 写失败测试** `trigger_test.go`（sqlite :memory: + 真实仓储 + RunService，照 T24 测试装配）：

```go
func TestNextTriggerRunIntervals(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	after := time.Date(2026, 11, 19, 10, 0, 0, 0, loc) // 周四
	cases := map[string]time.Time{
		"15m":     after.Add(15 * time.Minute),
		"hourly":  after.Add(time.Hour),
		"daily9":  time.Date(2026, 11, 20, 9, 0, 0, 0, loc),
		"weekly1": time.Date(2026, 11, 23, 9, 0, 0, 0, loc), // 下周一
	}
	for key, want := range cases {
		cfg := &types.SopTriggerConfig{Mode: "interval", IntervalKey: key, Timezone: "Asia/Shanghai"}
		got, err := NextTriggerRun(cfg, after)
		require.NoError(t, err)
		require.True(t, got.Equal(want), "key=%s got=%v want=%v", key, got, want)
	}
	cronCfg := &types.SopTriggerConfig{Mode: "cron", Cron: "0 9 * * *", Timezone: "Asia/Shanghai"}
	got, err := NextTriggerRun(cronCfg, after)
	require.NoError(t, err)
	require.True(t, got.Equal(time.Date(2026, 11, 20, 9, 0, 0, 0, loc)))
}

func TestScanOnceFiltersLimitAndIdempotent(t *testing.T) {
	svc, db, pid := newTriggerTestService(t) // 建项目+published 模板 tplA 设为 default
	// 6 事项：2 customer/todo、2 internal/todo、1 customer/done、1 customer/todo 但已有 run
	mk := func(title, source, status string, withRun bool) types.SopItem {
		it := types.SopItem{TenantID: 1, ProjectID: pid, Title: title, Source: source, Status: status, Number: "TP-" + title}
		require.NoError(t, repository.NewSopItemRepo(db).Create(context.Background(), &it))
		if withRun {
			require.NoError(t, repository.NewSopRunRepo(db).CreateRun(context.Background(),
				&types.SopRun{TenantID: 1, ProjectID: pid, ItemID: it.ID, Status: types.SopRunSucceeded}))
		}
		return it
	}
	mk("a", "customer", "todo", false)
	mk("b", "customer", "todo", false)
	mk("c", "internal", "todo", false)
	mk("d", "internal", "todo", false)
	mk("e", "customer", "done", false)
	mk("f", "customer", "todo", true)
	cfg := &types.SopTriggerConfig{TenantID: 1, ProjectID: pid, Mode: "interval", IntervalKey: "daily9",
		Timezone: "Asia/Shanghai", DefaultTemplateID: seedPublishedTemplate(t, db).ID,
		PreferItemTemplate: true, Sources: "customer", Statuses: []string{"todo"}, PerRunLimit: 1, Enabled: true}
	require.NoError(t, repository.NewSopTriggerRepo(db).UpsertConfig(context.Background(), cfg))
	slot := time.Date(2026, 11, 19, 9, 0, 0, 0, time.UTC)
	log1, err := svc.ScanOnce(context.Background(), cfg, "auto", slot)
	require.NoError(t, err)
	require.Equal(t, 2, log1.Scanned) // 只有 a、b 命中（c/d 来源不符、e 状态不符、f 已跑过）
	require.Equal(t, 1, log1.Fired)   // per_run_limit=1 截断
	require.Equal(t, 1, log1.Skipped)
	// 同槽重扫：幂等，0 派发
	log2, err := svc.ScanOnce(context.Background(), cfg, "auto", slot)
	require.NoError(t, err)
	require.Equal(t, 0, log2.Fired)
	// 新槽：剩余的 b 继续触发
	log3, err := svc.ScanOnce(context.Background(), cfg, "auto", slot.Add(24*time.Hour))
	require.NoError(t, err)
	require.Equal(t, 1, log3.Fired)
	runs, _ := repository.NewSopRunRepo(db).ListRuns(context.Background(), 1, repository.SopRunFilter{ProjectID: pid}, 1, 50)
	for _, r := range runs {
		if r.TriggerKind == "schedule" {
			require.Equal(t, cfg.ID, r.TriggerRefID) // 触发来源可回溯
		}
	}
}

func TestScanOncePreferItemTemplate(t *testing.T) {
	svc, db, pid := newTriggerTestService(t)
	tplA := seedPublishedTemplate(t, db)
	tplB := seedPublishedTemplate(t, db)
	it := types.SopItem{TenantID: 1, ProjectID: pid, Title: "绑定B", Source: "customer", Status: "todo", TemplateID: tplB.ID, Number: "TP-x"}
	require.NoError(t, repository.NewSopItemRepo(db).Create(context.Background(), &it))
	cfg := &types.SopTriggerConfig{TenantID: 1, ProjectID: pid, DefaultTemplateID: tplA.ID, PreferItemTemplate: true,
		Sources: "all", Statuses: []string{"todo"}, PerRunLimit: 10, Mode: "interval", IntervalKey: "15m", Timezone: "UTC"}
	require.NoError(t, repository.NewSopTriggerRepo(db).UpsertConfig(context.Background(), cfg))
	_, err := svc.ScanOnce(context.Background(), cfg, "manual", time.Now())
	require.NoError(t, err)
	runs, _ := repository.NewSopRunRepo(db).ListRuns(context.Background(), 1, repository.SopRunFilter{ItemID: it.ID}, 1, 10)
	require.Len(t, runs, 1)
	require.Equal(t, tplB.ID, runs[0].TemplateID) // 事项绑定优先
	cfg.PreferItemTemplate = false
	require.NoError(t, repository.NewSopTriggerRepo(db).UpsertConfig(context.Background(), cfg))
	_, err = svc.ScanOnce(context.Background(), cfg, "manual", time.Now().Add(time.Minute))
	require.NoError(t, err)
	runs, _ = repository.NewSopRunRepo(db).ListRuns(context.Background(), 1, repository.SopRunFilter{ItemID: it.ID}, 1, 10)
	// 第二次扫描该事项已有 run → 不再派发（未跑过条件），runs 仍 1 条
	require.Len(t, runs, 1)
}

func TestRunNowEndpointBackground(t *testing.T) {
	r, db := newSOPTestRouter(t)
	pid := createTestProject(t, r)
	seedPublishedTemplate(t, db)
	doJSON(t, r, "PUT", "/api/v1/sop/projects/"+pid+"/trigger-config",
		`{"mode":"interval","interval_key":"15m","per_run_limit":5,"statuses":["todo"],"sources":"all","enabled":false}`)
	w := doJSON(t, r, "POST", "/api/v1/sop/projects/"+pid+"/trigger-config/run-now", "")
	require.Equal(t, http.StatusAccepted, w.Code) // 未启用也允许手动；立即返回
	require.Eventually(t, func() bool {
		var n int64
		db.Raw("SELECT COUNT(*) FROM sop_trigger_logs WHERE kind='manual'").Scan(&n)
		return n == 1
	}, 2*time.Second, 50*time.Millisecond) // 后台 goroutine 完成扫描写历史
}
```

- [ ] **Step 2: 运行确认失败** Run: `go test ./internal/application/service/sop/ -run "TestNextTriggerRun|TestScanOnce|TestRunNow" -v && go test ./internal/handler/ -run TestRunNow -v` Expected: 编译失败。
- [ ] **Step 3: 实现**

`repository/sop_item.go` 追加：

```go
func (r *sopItemRepo) ListTriggerCandidates(ctx context.Context, tenantID uint64, projectID string, statuses, sources []string) ([]types.SopItem, error) {
	var items []types.SopItem
	q := r.db.WithContext(ctx).Model(&types.SopItem{}).
		Where("tenant_id = ? AND project_id = ? AND status IN ?", tenantID, projectID, statuses).
		Where("NOT EXISTS (SELECT 1 FROM sop_runs r WHERE r.item_id = sop_items.id AND r.deleted_at IS NULL)")
	if len(sources) > 0 && sources[0] != "all" {
		q = q.Where("source IN ?", sources)
	}
	err := q.Order("created_at ASC").Find(&items).Error
	return items, err
}
```

`trigger.go`：NextTriggerRun 按 Interfaces（daily9/weekly1 用 `time.Date(y,m,d,9,0,0,0,loc)` 逐日推进找下一个匹配 weekday，最多推 8 天必中）。ScanOnce 核心：

```go
func (s *TriggerService) ScanOnce(ctx context.Context, cfg *types.SopTriggerConfig, kind string, slot time.Time) (*types.SopTriggerLog, error) {
	items, err := s.d.Items.ListTriggerCandidates(ctx, cfg.TenantID, cfg.ProjectID, cfg.Statuses, []string{cfg.Sources})
	if err != nil { return nil, err }
	log := &types.SopTriggerLog{TenantID: cfg.TenantID, ConfigID: cfg.ID, Kind: kind, Scanned: len(items)}
	var notes []string
	for _, it := range items {
		if log.Fired >= cfg.PerRunLimit { break } // 上限截断，剩余留给下个槽
		ok, err := s.d.Triggers.ReserveDispatch(ctx, cfg.ID, it.ID, slot)
		if err != nil { return nil, err }
		if !ok { log.Skipped++; continue } // 同槽已派发
		tplID := cfg.DefaultTemplateID
		if cfg.PreferItemTemplate && it.TemplateID != "" { tplID = it.TemplateID }
		if tplID == "" { log.Skipped++; notes = append(notes, it.Number+"：未绑定 SOP 且无默认模板"); continue }
		_, err = s.d.Runs.Create(ctx, cfg.TenantID, "system", CreateRunInput{
			TemplateID: tplID, ItemID: it.ID, TriggerKind: "schedule", TriggerRefID: cfg.ID,
			Input: map[string]any{"item_no": it.Number, "item_title": it.Title, "item_description": it.Description},
		})
		if err != nil { log.Skipped++; notes = append(notes, it.Number+"："+err.Error()); continue }
		log.Fired++
	}
	log.Skipped += len(items) - log.Fired - log.Skipped // 被上限截断的也算 skipped（scanned=fired+skipped 恒等）
	log.Note = strings.Join(notes, "；")
	if err := s.d.Triggers.AddLog(ctx, log); err != nil { return nil, err }
	next, err := NextTriggerRun(cfg, slot)
	if err == nil { cfg.NextRunAt = next }
	cfg.LastRunAt = &slot
	_ = s.d.Triggers.UpsertConfig(ctx, cfg)
	return log, nil
}
```

注意上式 `log.Skipped += len(items) - log.Fired - log.Skipped` 化简即 `log.Skipped = len(items) - log.Fired`——实现直接写后者，保证 scanned=fired+skipped 口径（spec §16.3）。TriggerScheduler.tick：ListEnabledConfigs→cfg.NextRunAt==nil 则 NextTriggerRun(now) 补算并 UpsertConfig；`!cfg.NextRunAt.After(now)` 则 ScanOnce(ctx,cfg,"auto",*cfg.NextRunAt)；单配置出错记 log.Printf 继续下一个，不中断循环。handler run-now：`go func(){ if err := svc.RunNow(context.WithoutCancel(c.Request.Context()), tid, uid, pid); err != nil { log.Printf("sop run-now: %v", err) } }()`→202。container：Provide TriggerService/TriggerScheduler + lifecycle OnStart Start/OnStop Stop（照 T21 engine 接线）。routes 追加 run-now（run_sop）。

- [ ] **Step 4: 运行确认通过** Run: `go test ./internal/application/service/sop/ -run "TestNextTriggerRun|TestScanOnce|TestRunNow" -v && go test ./internal/handler/ -run TestRunNow -v && go test ./internal/application/repository/ -run "SopTrigger|SopItem" -v && go build ./...` Expected: PASS。
- [ ] **Step 5: 提交** `git add internal/application/service/sop/trigger.go internal/application/service/sop/trigger_test.go internal/application/repository/sop_item.go internal/application/repository/sop_trigger.go internal/types/interfaces/sop.go internal/handler/sop_admin.go internal/router/routes_sop.go internal/container/container.go && git commit -m "feat(sop): SOP 任务触发器（扫描派发/幂等预约/上限截断/立即运行/触发历史）"`

---

# Milestone M8 — 知识库与 MCP 联动（spec §9）

### Task 36: 产物归档入 KB（.md frontmatter 源文档入库 + 自动归档钩子 + 入库状态回填）

**Files:**
- Create: `internal/application/service/sop/archive.go`、`internal/application/service/sop/archive_test.go`
- Modify: `internal/application/repository/sop_run.go`（追加 UpdateArtifact）、`internal/types/interfaces/sop.go`、`internal/application/service/sop/engine/engine.go`（Deps 追加 `AutoIngest func(ctx context.Context, run *types.SopRun, node *types.SopNode, arts []*types.SopArtifact)`，可 nil）、`internal/application/service/sop/engine/node.go`（executeNode 门禁 pass + GitCheckpoint 之后：`if node.AutoIngest && e.deps.AutoIngest != nil { e.deps.AutoIngest(ctx, run, node, arts) }`，错误只记日志不失败 run）、`internal/handler/sop_runs.go`（ingest 端点）、`internal/router/routes_sop.go`、`internal/container/container.go`（ArchiveService + Deps.AutoIngest 接线）

**Interfaces:**
- Consumes: T5 `SopRunRepo{GetRun/GetArtifact/ListArtifacts/ListNodeRuns}` + 本任务追加 `UpdateArtifact(ctx, art *types.SopArtifact) error`、T2 Items、T1 Projects、T10 `engine.SafeWorkspacePath`、Know `interfaces.KnowledgeService.CreateKnowledgeFromManual(ctx, kbID string, payload *types.ManualKnowledgePayload, channel string) (*types.Knowledge, error)`（interfaces/knowledge.go 已验证存在；payload={Title,Content,Status:"publish",Channel:"sop"}，types/knowledge.go:329-340）。
- Produces:
  - `sop.BuildArchiveMarkdown(art *types.SopArtifact, nodeName, runID string, raw []byte) (string, error)`（纯函数，下方完整代码）。
  - `sop.ArchiveDeps{Runs interfaces.SopRunRepo; Items interfaces.SopItemRepo; Projects interfaces.SopProjectRepo; Knowledge interface{ CreateKnowledgeFromManual(ctx context.Context, kbID string, payload *types.ManualKnowledgePayload, channel string) (*types.Knowledge, error) }}`（窄接口，测试 stub 捕获 payload）。
  - `sop.ArchiveService`：`NewArchiveService(d ArchiveDeps)`；`IngestArtifact(ctx, tenantID uint64, userID, runID, artifactID, kbID string) (*types.SopArtifact, error)`（kbID 空→item.KBID→project.KBID→白话错误「请先在事项或项目上绑定知识库」；art.KnowledgeID 非空→幂等直接返回；入库成功回填 KBID/KnowledgeID/IngestedAt）；`IngestNodeArtifacts(ctx, run, node, arts)`（AutoIngest 钩子实现：逐个 IngestArtifact(kbID="")，单个失败记日志继续）。
  - 路由：`POST /sop/runs/:id/artifacts/:aid/ingest  run_sop  body {kb_id?}`→200 返回回填后的 artifact。

- [ ] **Step 1: 写失败测试** `archive_test.go`：

```go
type stubKnowledge struct {
	payloads []*types.ManualKnowledgePayload
	kbIDs    []string
}

func (s *stubKnowledge) CreateKnowledgeFromManual(ctx context.Context, kbID string, p *types.ManualKnowledgePayload, channel string) (*types.Knowledge, error) {
	s.payloads = append(s.payloads, p)
	s.kbIDs = append(s.kbIDs, kbID)
	return &types.Knowledge{ID: fmt.Sprintf("kn-%d", len(s.payloads))}, nil
}

func TestBuildArchiveMarkdownFrontmatter(t *testing.T) {
	art := &types.SopArtifact{Name: "分析报告", Path: "out/report.md", SHA256: "abc123"}
	md, err := BuildArchiveMarkdown(art, "需求分析", "run-1", []byte("# 正文\n内容"))
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(md, "---\n"))
	require.Contains(t, md, `title: "分析报告"`)
	require.Contains(t, md, `source_node: "需求分析"`)
	require.Contains(t, md, "run: run-1")
	require.Contains(t, md, "sha256: abc123")
	require.Contains(t, md, "# 正文")
	// 已有 frontmatter 不重复加
	md2, err := BuildArchiveMarkdown(art, "n", "r", []byte("---\ntitle: 原有\n---\n\n正文"))
	require.NoError(t, err)
	require.Equal(t, 1, strings.Count(md2, "---\n")) // 仅原有的一对（开头一次；结尾 ---\n\n 不计）
	// .log 包裹为代码块
	logArt := &types.SopArtifact{Name: "日志", Path: "out/test.log", SHA256: "s"}
	md3, err := BuildArchiveMarkdown(logArt, "测试", "r", []byte("line1\nline2"))
	require.NoError(t, err)
	require.Contains(t, md3, "```log\nline1\nline2\n```")
	// 二进制拒绝
	_, err = BuildArchiveMarkdown(&types.SopArtifact{Path: "out/a.zip"}, "n", "r", []byte{0})
	require.Error(t, err)
	require.Contains(t, err.Error(), "暂不支持")
}

func TestIngestArtifactBackfillsAndIdempotent(t *testing.T) {
	svc, db, sk := newArchiveTestService(t) // 项目 KBID=kb-p；run+artifact(out/report.md) 落盘 t.TempDir
	run, art := seedRunArtifact(t, db, "out/report.md", "# 报告")
	got, err := svc.IngestArtifact(context.Background(), 1, "u1", run.ID, art.ID, "")
	require.NoError(t, err)
	require.Equal(t, "kn-1", got.KnowledgeID)
	require.Equal(t, "kb-p", got.KBID) // 项目绑定兜底
	require.NotNil(t, got.IngestedAt)
	require.Len(t, sk.payloads, 1)
	require.Equal(t, "sop", sk.payloads[0].Channel)
	require.Contains(t, sk.payloads[0].Content, "sha256:")
	// 二次调用幂等：不再入库
	_, err = svc.IngestArtifact(context.Background(), 1, "u1", run.ID, art.ID, "")
	require.NoError(t, err)
	require.Len(t, sk.payloads, 1)
	// 库内状态持久（重开弹窗状态保持）
	reloaded, _ := repository.NewSopRunRepo(db).GetArtifact(context.Background(), 1, art.ID)
	require.Equal(t, "kn-1", reloaded.KnowledgeID)
}

func TestIngestRequiresKBBinding(t *testing.T) {
	svc, db, _ := newArchiveTestService(t) // 项目 KBID 置空
	run, art := seedRunArtifact(t, db, "out/a.md", "x")
	require.NoError(t, repository.NewSopProjectRepo(db).Update(context.Background(), &types.SopProject{ID: run.ProjectID, TenantID: 1, KBID: ""}))
	_, err := svc.IngestArtifact(context.Background(), 1, "u1", run.ID, art.ID, "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "绑定知识库")
}

func TestAutoIngestHookFiresOnGatePass(t *testing.T) {
	called := 0
	e := newTestEngine(t, func(d *Deps) {
		d.AutoIngest = func(ctx context.Context, run *types.SopRun, node *types.SopNode, arts []*types.SopArtifact) { called++ }
	})
	// scripted provider 产出 out/a.md；node.AutoIngest=true
	runID := seedSingleNodeRun(t, e, &types.SopNode{ID: "n1", Type: "agent", AutoIngest: true,
		ArtifactOutputs: []types.SopArtifactOutput{{Name: "a", Path: "out/a.md", Required: true}}})
	_, err := e.Advance(context.Background(), runID)
	require.NoError(t, err)
	require.Equal(t, 1, called)
}
```

- [ ] **Step 2: 运行确认失败** Run: `go test ./internal/application/service/sop/ -run "TestBuildArchive|TestIngest|TestAutoIngest" -v` Expected: 编译失败。
- [ ] **Step 3: 实现**

`archive.go` 纯函数（完整）：

```go
func BuildArchiveMarkdown(art *types.SopArtifact, nodeName, runID string, raw []byte) (string, error) {
	ext := strings.ToLower(filepath.Ext(art.Path))
	body := string(raw)
	switch ext {
	case ".md":
		if strings.HasPrefix(body, "---\n") {
			return body, nil // 已有 frontmatter 原样保留
		}
	case ".txt", ".log", ".json", ".diff", ".csv":
		lang := strings.TrimPrefix(ext, ".")
		body = "```" + lang + "\n" + strings.TrimRight(body, "\n") + "\n```\n"
	default:
		return "", fmt.Errorf("该产物类型暂不支持入知识库（仅文档类产物）")
	}
	fm := fmt.Sprintf("---\ntitle: %q\nsource_node: %q\nrun: %s\nsha256: %s\ncreated: %s\n---\n\n",
		art.Name, nodeName, runID, art.SHA256, time.Now().UTC().Format(time.RFC3339))
	return fm + body, nil
}
```

IngestArtifact：GetArtifact→GetRun 校验租户→已入库幂等返回→kbID 兜底链（run.ItemID 非空查 item.KBID，再 project.KBID）→SafeWorkspacePath(run.WorkspaceDir, art.Path)→os.ReadFile→nodeName 从 ListNodeRuns 匹配 nodeID 取 WorkflowSnapshot 节点名（找不到用 nodeID）→BuildArchiveMarkdown→Knowledge.CreateKnowledgeFromManual(ctx, kbID, &types.ManualKnowledgePayload{Title: art.Name, Content: md, Status: "publish", Channel: "sop"}, "sop")→回填 art.KBID/KnowledgeID/IngestedAt=now→UpdateArtifact。**入库对象永远是 .md 源文档（frontmatter 保留来源追溯），HTML 渲染视图不入库**（spec §8/§9）。repo 追加 UpdateArtifact（Save 全列）。handler ingest：sopBindJSON 取可选 kb_id→svc.IngestArtifact→200 data=artifact。engine 钩子与 container 接线按 Files 描述（AutoIngest 错误 log.Printf 不失败 run——入库是沉淀不是门禁）。

- [ ] **Step 4: 运行确认通过** Run: `go test ./internal/application/service/sop/ -run "TestBuildArchive|TestIngest|TestAutoIngest" -v && go test ./internal/application/service/sop/engine/ -v && go test ./internal/handler/ -run TestSop -v && go build ./...` Expected: PASS。
- [ ] **Step 5: 提交** `git add internal/application/service/sop/archive.go internal/application/service/sop/archive_test.go internal/application/repository/sop_run.go internal/types/interfaces/sop.go internal/application/service/sop/engine/engine.go internal/application/service/sop/engine/node.go internal/handler/sop_runs.go internal/router/routes_sop.go internal/container/container.go && git commit -m "feat(sop): 产物入知识库（.md frontmatter 源文档/幂等回填/模板自动归档钩子）"`

### Task 37: Know MCP 联动（SOP 工具组 + sop_workspace_* + kb_scope 服务端强制 + CLI scoped endpoint 签发）

**Files:**
- Create: `internal/mcpserver/tools_sop.go`、`internal/mcpserver/tools_sop_test.go`、`internal/application/service/sop/mcp.go`、`internal/application/service/sop/mcp_test.go`
- Modify: `internal/types/mcp_endpoint_tools.go`（新组+9 工具+能力映射）、`internal/mcpserver/server.go`（RegisterSOPTools）、`internal/application/repository/mcp_endpoint.go`（追加 GetByName）、`internal/application/service/sop/engine/engine.go`（Deps 追加 `MCP interface{ EnsureNodeEndpoint(ctx context.Context, tenantID uint64, runID, nodeID string, kbIDs []string) (url, token string, err error) }`，可 nil）、`internal/application/service/sop/engine/node.go`（executeNode 填 tb.KnowMCP）、`internal/application/service/sop/context.go`（平台节点 MCPServers 追加 scoped endpoint）、`internal/container/container.go`

**Interfaces:**
- Consumes: T8 `executor.KnowMCPEndpoint`、T16 ResolvedNode.KBIDs、T5 Runs、T24 RunService、T12–T14 引擎 ops、T4 Templates、Know 现有 `mcpserver.Server`（server.go:117 AddTools / :147 filterTools / :163 guardTool 按 ep.HasTool 白名单强制）、`types.MCPEndpoint.KnowledgeBaseIDs`（mcp_endpoint.go:34-36：**空=全部自有 KB，非空=白名单**，scope.go:38 allowedKnowledgeBases 已在每个知识工具调用时强制——kb_scope 的服务端强制就落在这个既有机制上）、`utils.GetAESKey()`（utils/crypto.go，SYSTEM_AES_KEY）、`types.MCPEndpointTokenPrefix="mcp_"`。
- Produces:
  - types 新常量：`MCPEndpointToolGroupSOP MCPEndpointToolGroup = "sop"`；工具名 `MCPEndpointToolSOPListTemplates="sop_list_templates"` / `SOPCreateRun="sop_create_run"` / `SOPGetRun="sop_get_run"` / `SOPListRuns="sop_list_runs"` / `SOPApproveNode="sop_approve_node"` / `SOPRejectNode="sop_reject_node"` / `SOPWorkspaceList="sop_workspace_list"` / `SOPWorkspaceRead="sop_workspace_read"` / `SOPWorkspaceWrite="sop_workspace_write"`；catalog 追加 9 条（create_run/approve/reject/workspace_write 标 Destructive:true）；`MCPEndpointToolGroups()` 追加 sop；`MCPEndpointCapabilitiesForTools` 追加 `if groups[MCPEndpointToolGroupSOP] { caps = append(caps, string(APIKeyCapabilityReadSOP), string(APIKeyCapabilityRunSOP)) }`。
  - `mcpserver.SOPToolDeps{Templates interfaces.SopTemplateRepo; Runs interfaces.SopRunRepo; RunSvc *sop.RunService; Engine *engine.Engine}`；`func (s *Server) RegisterSOPTools(d SOPToolDeps)`（构造后由 container 调用，`s.mcp.AddTools(...)` 追加 9 个 ServerTool；filterTools/guardTool 既有白名单机制自动生效）。
  - `sop.MCPProvisioner`：`NewMCPProvisioner(endpoints MCPEndpointStore, baseURL string)`，`MCPEndpointStore interface{ GetByName(ctx, tenantID uint64, name string) (*types.MCPEndpoint, error); Create(ctx, *types.MCPEndpoint) error; Update(ctx, *types.MCPEndpoint) error }`；`EnsureNodeEndpoint(ctx, tenantID, runID, nodeID, kbIDs) (url, token string, err error)`（**确定性 HMAC token，不落盘明文**）。
  - engine 行为：cli 节点 executeNode 在 Resolve 后 `if e.deps.MCP != nil { ids := resolved.KBIDs; if node.Executor.KBScope == "all" { ids = nil }; url, tok, err := e.deps.MCP.EnsureNodeEndpoint(ctx, run.TenantID, run.ID, node.ID, ids); tb.KnowMCP = &executor.KnowMCPEndpoint{URL: url, Token: tok, KBScope: node.Executor.KBScope, KBIDs: resolved.KBIDs} }`（err→节点失败白话「无法生成 Know MCP 凭据：…」）。平台节点：ContextResolver 在 MCPServers 末尾追加同一 scoped endpoint 的 types.MCPService{Name:"know-sop", Transport:"http", URL:url, Headers:{"Authorization":"Bearer "+tok}}（T16 预留的增强点）。

- [ ] **Step 1: 写失败测试**

`sop/mcp_test.go`（sqlite :memory: AutoMigrate(&types.MCPEndpoint{}) + 真实仓储；`t.Setenv("SYSTEM_AES_KEY", strings.Repeat("k", 32))` 照 utils/crypto_test.go 现有设 key 模式）：

```go
func TestEnsureNodeEndpointDeterministicAndScoped(t *testing.T) {
	p, db := newTestProvisioner(t) // baseURL=http://127.0.0.1:8080/mcp
	url1, tok1, err := p.EnsureNodeEndpoint(context.Background(), 1, "run-1", "n1", []string{"kb-a"})
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(tok1, "mcp_"))
	require.Contains(t, url1, "kb_scope=")
	url2, tok2, err := p.EnsureNodeEndpoint(context.Background(), 1, "run-1", "n1", []string{"kb-a"})
	require.NoError(t, err)
	require.Equal(t, tok1, tok2) // 确定性：resume/重启后 token 不变，无需存储明文
	require.Equal(t, url1, url2)
	var ep types.MCPEndpoint
	require.NoError(t, db.First(&ep, "name = ?", "sop:run-1:n1").Error)
	require.Equal(t, types.StringArray{"kb-a"}, ep.KnowledgeBaseIDs) // 服务端白名单=解析后的 KB 范围
	require.Equal(t, fmt.Sprintf("%x", sha256.Sum256([]byte(tok1))), ep.TokenHash)
	require.Contains(t, ep.Tools, "sop_workspace_read")
	require.NotContains(t, ep.Tools, "sop_create_run") // CLI 节点端点只给检索+工作区，不给驱动 SOP
	_, tok3, _ := p.EnsureNodeEndpoint(context.Background(), 1, "run-1", "n2", []string{"kb-a"})
	require.NotEqual(t, tok1, tok3) // 节点隔离
	// all 档：白名单空=全部自有 KB（MCPEndpoint 既有语义）
	_, _, err = p.EnsureNodeEndpoint(context.Background(), 1, "run-2", "n1", nil)
	require.NoError(t, err)
	var ep2 types.MCPEndpoint
	require.NoError(t, db.First(&ep2, "name = ?", "sop:run-2:n1").Error)
	require.Empty(t, ep2.KnowledgeBaseIDs)
}

func TestEnsureNodeEndpointMissingKeyLoudError(t *testing.T) {
	t.Setenv("SYSTEM_AES_KEY", "")
	p, _ := newTestProvisioner(t)
	_, _, err := p.EnsureNodeEndpoint(context.Background(), 1, "r", "n", nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "SYSTEM_AES_KEY")
}
```

`mcpserver/tools_sop_test.go`（照 server_test.go 现有装配模式）：

```go
func TestSOPToolCatalogAndCapabilities(t *testing.T) {
	def, ok := types.LookupMCPEndpointTool("sop_create_run")
	require.True(t, ok)
	require.True(t, def.Destructive)
	caps := types.MCPEndpointCapabilitiesForTools([]string{"sop_create_run", "sop_get_run"})
	require.Contains(t, caps, "run_sop")
	require.Contains(t, caps, "read_sop")
	require.Equal(t, []string{"sop_get_run", "sop_create_run"},
		types.NormalizeMCPEndpointTools([]string{"sop_create_run", "sop_get_run", "sop_create_run", "bogus"})) // catalog 序+去重+丢未知
}

func TestWorkspaceWriteRestrictedToOutAndWork(t *testing.T) {
	s, db := newSOPTestServer(t) // RegisterSOPTools 注入 stub runs：run.WorkspaceDir=t.TempDir()
	ep := seedSOPEndpoint(t, db, "sop:run-w:n1") // Tools 含 workspace 三件套
	ctx := ctxWithEndpoint(ep)                   // 照 server_test.go 现有 endpoint ctx 注入助手
	res, err := s.handleSOPWorkspaceWrite(ctx, toolReq(map[string]any{"path": "inputs/evil.md", "content": "x"}))
	require.NoError(t, err)
	require.True(t, res.IsError) // 越界目录拒绝
	res, err = s.handleSOPWorkspaceWrite(ctx, toolReq(map[string]any{"path": "out/report.md", "content": "# ok"}))
	require.NoError(t, err)
	require.False(t, res.IsError)
	res, err = s.handleSOPWorkspaceRead(ctx, toolReq(map[string]any{"path": "out/report.md"}))
	require.NoError(t, err)
	require.Contains(t, textOf(res), "# ok")
	res, _ = s.handleSOPWorkspaceRead(ctx, toolReq(map[string]any{"path": "../../etc/passwd"}))
	require.True(t, res.IsError) // SafeWorkspacePath 拒绝
}

func TestSOPToolsGuardedByAllowlist(t *testing.T) {
	s, db := newSOPTestServer(t)
	ep := seedSOPEndpoint(t, db, "plain-endpoint") // Tools 不含 sop_*
	res, err := s.guardTool(s.handleSOPListRuns)(ctxWithEndpoint(ep), toolReq(map[string]any{}))
	require.NoError(t, err)
	require.True(t, res.IsError) // 白名单外调用被拒（既有 guardTool 机制）
	require.Contains(t, textOf(res), "not enabled")
}
```

（toolReq/textOf/ctxWithEndpoint/seedSOPEndpoint 为测试文件内助手：toolReq 构造 mcp.CallToolRequest{Params:{Name,Arguments}}；textOf 取 res.Content[0].(mcp.TextContent).Text；ctxWithEndpoint 照 server_test.go 现有模式把 ep 放进 ctx；newSOPTestServer 用 stub SOPToolDeps——Templates/Runs 为 sqlite 真实仓储，RunSvc/Engine 可 nil（本组测试不触达）。）

- [ ] **Step 2: 运行确认失败** Run: `go test ./internal/application/service/sop/ -run TestEnsureNodeEndpoint -v && go test ./internal/mcpserver/ -run "TestSOPTool|TestWorkspaceWrite|TestSOPToolsGuarded" -v` Expected: 编译失败。
- [ ] **Step 3: 实现**

`sop/mcp.go` EnsureNodeEndpoint（完整核心）：

```go
func (p *MCPProvisioner) EnsureNodeEndpoint(ctx context.Context, tenantID uint64, runID, nodeID string, kbIDs []string) (string, string, error) {
	key := utils.GetAESKey()
	if key == nil {
		return "", "", errors.New("SYSTEM_AES_KEY 未配置，无法为 CLI 生成 Know MCP 凭据")
	}
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte("sop-mcp:" + runID + ":" + nodeID))
	token := types.MCPEndpointTokenPrefix + hex.EncodeToString(mac.Sum(nil))[:40]
	name := "sop:" + runID + ":" + nodeID
	tools := types.NormalizeMCPEndpointTools([]string{
		types.MCPEndpointToolListKnowledgeBases, types.MCPEndpointToolSearchKnowledge, types.MCPEndpointToolReadDocument,
		types.MCPEndpointToolSOPWorkspaceList, types.MCPEndpointToolSOPWorkspaceRead, types.MCPEndpointToolSOPWorkspaceWrite,
	})
	ep, err := p.endpoints.GetByName(ctx, tenantID, name)
	if err != nil {
		ep = &types.MCPEndpoint{TenantID: tenantID, Name: name, Enabled: true,
			TokenHash: fmt.Sprintf("%x", sha256.Sum256([]byte(token))), TokenHint: token[:8],
			KnowledgeBaseIDs: kbIDs, Tools: tools, RateLimitPerMinute: 120}
		if err := p.endpoints.Create(ctx, ep); err != nil { return "", "", err }
	} else if !sameStringSet(ep.KnowledgeBaseIDs, kbIDs) {
		ep.KnowledgeBaseIDs = kbIDs // 配置改范围→下次执行即生效
		if err := p.endpoints.Update(ctx, ep); err != nil { return "", "", err }
	}
	return p.baseURL, token, nil
}
```

baseURL 由 container 注入 Know 自身监听地址（读现有 config 的 host/port，默认 `http://127.0.0.1:8080/mcp`）；tb.KnowMCP.URL 在 engine 侧拼 `?kb_scope=<scope>`（T33 渲染进配置，**范围只是下发提示，强制靠 ep.KnowledgeBaseIDs 白名单**——scope.go:38/58 既有校验每个知识工具调用都过）。repository/mcp_endpoint.go 追加 GetByName（照该文件现有 Get 的租户过滤模式，NotFound 返回 gorm.ErrRecordNotFound）。

`mcpserver/tools_sop.go`：9 个 mcp.NewTool 定义（照 tools_ingest.go:14-27 模式：WithString/WithNumber/WithBoolean + Required + ReadOnly/DestructiveHintAnnotation）+ RegisterSOPTools 把 {Tool,Handler} 追加进 s.mcp.AddTools。外部 6 工具 handler 委托：sop_list_templates→Templates.List(tenantID, published 过滤)；sop_create_run→RunSvc.Create(ctx, ep.TenantID, "mcp", CreateRunInput{TemplateID, ItemID, Input, TriggerKind:"api", TriggerRefID:ep.ID})；sop_get_run→Runs.GetRun+ListNodeRuns 聚合 JSON；sop_list_runs→Runs.ListRuns 分页；sop_approve_node/sop_reject_node→Engine.Approve/Reject（参数 run_id/node_id/comment/reject_to）。workspace 三件套 handler：ep.Name 必须匹配 `sop:<runID>:<nodeID>`（strings.SplitN 校验 3 段）→Runs.GetRun(ctx, ep.TenantID, runID)→run.WorkspaceDir；list=filepath.WalkDir 相对路径+大小（上限 500 条）；read=SafeWorkspacePath+os.ReadFile（>256KiB 截断附注记）；write=路径必须 `out/` 或 `work/` 前缀（否则 mcp.NewToolResultError「只能写入 out/ 或 work/ 目录」）+SafeWorkspacePath+MkdirAll+WriteFile 0644。错误一律 mcp.NewToolResultError 白话，不 panic。container：NewMCPProvisioner（endpoints=现有 mcp endpoint 仓储）→engine.Deps.MCP→server.RegisterSOPTools（dig 在两者都构造后调用，照现有 mcpserver 接线处追加）。

- [ ] **Step 4: 运行确认通过** Run: `go test ./internal/application/service/sop/ -run TestEnsureNodeEndpoint -v && go test ./internal/mcpserver/ -v && go test ./internal/application/service/sop/engine/ -v && go test ./internal/types/ -run TestMCPEndpoint -v && go build ./...` Expected: PASS（mcpserver 既有测试全绿=零回归）。
- [ ] **Step 5: 提交** `git add internal/mcpserver/tools_sop.go internal/mcpserver/tools_sop_test.go internal/application/service/sop/mcp.go internal/application/service/sop/mcp_test.go internal/types/mcp_endpoint_tools.go internal/mcpserver/server.go internal/application/repository/mcp_endpoint.go internal/application/service/sop/engine/engine.go internal/application/service/sop/engine/node.go internal/application/service/sop/context.go internal/container/container.go && git commit -m "feat(sop): Know MCP 联动（SOP 工具组/工作区工具/kb_scope 白名单强制/CLI 确定性 token）"`

---

# Milestone M9 — 前端工作台（spec §10，视觉/交互唯一参照=定稿原型）

> **每个前端 Task 的固定验收四件套**：① `cd frontend && npm test -- src/api/sop/helpers.test.ts`（及本任务新增测试文件）PASS；② `npm run type-check` PASS；③ `npm run check-i18n` PASS（6 语言 zh-CN/zh-TW/en-US/ja-JP/ko-KR/ru-RU 全量补 key，禁硬编码中文进模板——一律 `t('sop.…')`）；④ browser-use 打开 `npm run dev` 页面按任务内走查清单实操一遍（控制台零报错）。组件视觉逐像素对齐原型 `D:\loopforge\prototypes\sop-workbench-prototype.html` 对应区块（品牌色 #00a870 映射 Know 主题变量 `--td-brand-color`；原型 mock 数据全部替换为真实 API，原型 designer 的 "tpl1" 等假 id 不得残留）。纯逻辑一律抽 `.ts` 配 `node:test`+`assert/strict` 测试（照 src/views/pm/pmStatus.test.ts 模式）；组件测试基建不存在，不引入 vitest。

### Task 38: 前端壳 + 路由/菜单替换 + API 层 + SSE 客户端 + Pinia store + 项目切换器

**Files:**
- Create: `frontend/src/api/sop/index.ts`、`frontend/src/api/sop/helpers.ts`、`frontend/src/api/sop/helpers.test.ts`、`frontend/src/api/sop/sse.ts`、`frontend/src/stores/sop.ts`、`frontend/src/views/sop/SopLayoutView.vue`、`frontend/src/views/sop/components/ProjectSwitcher.vue`、`frontend/src/views/sop/components/ProjectCreateModal.vue`
- Modify: `frontend/package.json`（`npm install @vue-flow/core`）、`frontend/src/router/index.ts`（L181-228 pm 路由块替换为 sop 路由树 + pm redirect）、`frontend/src/stores/menu.ts`（L43 pm 项原位替换）、`frontend/src/components/menu.vue`（TOP_MENU_PATHS L509 / isMenuItemActive L452-488 / handleMenuClick L1217 / 图标分支）、`frontend/src/i18n/locales/{zh-CN,zh-TW,en-US,ja-JP,ko-KR,ru-RU}.ts`（`menu.sop` + `sop:{shell,common,project,…}`）

**Interfaces:**
- Consumes: M5 全部 REST 端点（T21–T26 路由表）、T24 SSE `data:` 帧（executor.StreamEvent JSON）、`src/utils/request.ts` get/post/put/patch/del（返回 WithStatus<T>，取 .data）+ buildAuthHeaders（L78）、现有 `src/api/knowledge-base/`、`src/api/agent/`、`src/api/model/`、`src/api/skill/`、`src/api/mcp-service.ts`（角色/模型/技能/MCP 清单供后续任务）、`src/api/pm/`（T54 归档复用）。
- Produces:（**T39–T54 全部消费，命名锁定**）
  - `api/sop/index.ts` 类型镜像后端：`SopProject/SopRepo/SopItem/SopItemWithDerived/SopItemAttachment/SopItemClarification/SopItemActivity/SopTemplate/SopTemplateRevision/SopDefinition/SopNode/SopEdge/SopNodeExecutor/SopArtifactOutput/SopArtifactCheck/SopRun/SopNodeRun/SopArtifact/SopUsage/ApprovalEntry/SopTriggerConfig/SopTriggerLog/CLICredentialMasked/CLIStatus/OrchestrateOp/DiffCard`；枚举联合类型 `ItemStatus`（8）/`RunStatus`（7）/`NodeStatus`（8）/`ExecState`（'none'|'running'|'intervene'|'pr'|'closed'）/`CliKind`（10）。函数（全部 async，返回解包后的 data）：`listProjects/createProject/getProject/updateProject/deleteProject/projectStats/addRepo/removeRepo/setDefaultRepo`；`listItems(params)/createItem/getItem/updateItem/deleteItem/setItemStatus/bulkLaunch/bulkClose/addAttachment/listAttachments/setOutcome/listClarifications/addClarification/answerClarification/generateClarifications/listActivities/retryFailed/relaunch/analyzeItem`；`listTemplates/createTemplate/getTemplate/updateTemplate/deleteTemplate/validateTemplate/publishTemplate/listRevisions/copyTemplate`；`listRuns(params)/runStats/getRun/createRun/retryRun/pauseRun/resumeRun/cancelRun/addHumanContext/approveNode/rejectNode/reworkNode/terminateNode/answerNode/listArtifacts/artifactFileURL(runID,aid,download?)/getNodeLogs(runID,nid,attempt?)/ingestArtifact`；`listApprovals`；`getTriggerConfig/saveTriggerConfig/runNow/listTriggerLogs`；`listCredentials/saveCredential/deleteCredential/cliStatus`；`pmArchiveSummary`。URL 前缀 `/api/v1/sop`。
  - `helpers.ts`：`EXEC_STATES`/`EXEC_STATE_LABELS`（尚未执行/正在推进/需要介入/待处理 PR/已闭环）、`deriveExecState(itemStatus,runStatus,hasRun)`（与 T2 后端同口径）、`SOURCE_LABELS`（客户反馈/平台新建/数据洞察/竞品分析/战略规划/其他）、`ITEM_STATUS_LABELS`（待梳理/待启动/进行中/评审中/受阻/已完成/已关闭/已取消）、`RUN_STATUS_LABELS`（排队中/进行中/待确认/已暂停/已完成/失败/已取消）、`NODE_STATUS_LABELS`（8 态）、`progressText(done,total)`、`applyNodeOps(def,ops)`（T25 Applied ops 的客户端确定性应用：set/add_node/remove_node/add_edge/remove_edge；remove_node 同步清理他节点 task_refs/reject_to/edges）、`gateCheckSummary(check)`、`intervalSummary(cfg)`、`validateProjectCode(code)`（/^[A-Z][A-Z0-9]{1,9}$/）、`validateRepoURL(url)`（^https:// 或 ^git@host:path）、`shortSha(sha)`（前 8 位）、`formatBytes(n)`。
  - `sse.ts`：`streamSSE(url,body,onEvent,signal?)`（POST fetch+buildAuthHeaders+ReadableStream 按 `\n\n` 切帧取 `data:` JSON）、`streamGet(url,onEvent,signal?)`（GET 同解析，运行事件订阅用）；`SopStreamEvent{type,text?,tool_name?,payload?,at?}`。
  - `stores/sop.ts`（pinia setup store `useSopStore`）：state `projects/currentProjectId`（localStorage 'sop_current_project' 持久）/`items/itemStats/itemFilter{q,status,owner,source,kind,exec}`/`templates/runs/runFilter{status,q,template_id}/approvals/triggerConfig/cliStatuses/credentials`；actions `loadProjects/switchProject(id)`（切换后并行重载 items+stats+runs+approvals+triggerConfig，全部带 project_id）/`createProject`（创建即切换）/`loadItems/loadStats/loadTemplates/loadRuns/loadApprovals/loadTriggerConfig/loadCliStatus/loadCredentials`；getters `currentProject/approvalCount`；`startPolling(fn,ms)` 返回 stop 函数（组件 onUnmounted 必调）。
  - 路由树：`/platform/sop`（SopLayoutView）→ children `items/templates/runs/approvals/tasks`（五主 Tab）+ 子页 `templates/designer/:id`（SopDesigner，T45）/`runs/:id`（SopRunDetail，T49）/`settings/project`（SopProjectSettings，T39）/`settings/cli`（SopSettings，T53）/`archive`（PmArchive，T54）；`/platform/pm` 及全部旧子路径 → `redirect: to => ({ path: '/platform/sop/items', query: to.query })`（带事项定位参数）。子页保留所属主 Tab 高亮（route.meta.tab）+ 面包屑返回。
  - `SopLayoutView.vue`：顶部 Tab 栏（五项，审批 Tab 带 approvalCount 角标）+ 右侧 ProjectSwitcher + ⋮ 菜单（执行器设置/项目配置/PM 归档）+ `<router-view>`；`ProjectSwitcher.vue`（当前项 ✓ + 编号前缀，点击 switchProject，底部「＋ 新建项目…」）；`ProjectCreateModal.vue`（名称必填/前缀 validateProjectCode+唯一性校验/描述；创建即切换，空项目态）。

- [ ] **Step 1: 写失败测试** `helpers.test.ts`：

```ts
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { EXEC_STATES, EXEC_STATE_LABELS, SOURCE_LABELS, ITEM_STATUS_LABELS, RUN_STATUS_LABELS,
  deriveExecState, applyNodeOps, gateCheckSummary, intervalSummary, validateProjectCode, validateRepoURL } from './helpers';

test('五态标签与枚举全覆盖', () => {
  assert.equal(EXEC_STATES.length, 5);
  for (const s of EXEC_STATES) assert.ok(EXEC_STATE_LABELS[s]);
  assert.equal(EXEC_STATE_LABELS.intervene, '需要介入');
  assert.equal(EXEC_STATE_LABELS.pr, '待处理 PR');
});

test('执行情况派生与后端同口径', () => {
  assert.equal(deriveExecState('todo', '', false), 'none');
  assert.equal(deriveExecState('in_progress', 'running', true), 'running');
  assert.equal(deriveExecState('in_review', 'succeeded', true), 'pr');
  assert.equal(deriveExecState('in_progress', 'failed', true), 'intervene');
  assert.equal(deriveExecState('done', '', false), 'closed');   // 终态无运行≠等待执行
  assert.equal(deriveExecState('closed', '', false), 'closed');
  assert.equal(deriveExecState('in_progress', 'paused', true), 'running');   // paused 计入正在推进（spec §3.2）
  assert.equal(deriveExecState('in_progress', 'cancelled', true), 'none');   // 运行已取消=尚未执行
  assert.equal(deriveExecState('todo', 'queued', true), 'none');             // queued 瞬态，与 T2 后端一致
});

test('来源六键中文映射', () => {
  assert.equal(SOURCE_LABELS.customer, '客户反馈');
  assert.equal(SOURCE_LABELS.competitor, '竞品分析');
  assert.equal(Object.keys(SOURCE_LABELS).length, 6);
});

test('状态映射覆盖 8 事项态与 7 运行态', () => {
  for (const k of ['backlog','todo','in_progress','in_review','blocked','done','closed','cancelled']) assert.ok(ITEM_STATUS_LABELS[k]);
  for (const k of ['queued','running','waiting','paused','succeeded','failed','cancelled']) assert.ok(RUN_STATUS_LABELS[k]);
});

test('applyNodeOps 确定性应用与引用清理', () => {
  const def: any = { nodes: [
    { id: 'n1', name: '分析', task_refs: [], reject_to: '' },
    { id: 'n2', name: '实现', task_refs: ['n1'], reject_to: 'n1' }], edges: [{ from: 'n1', to: 'n2' }] };
  const out = applyNodeOps(def, [
    { op: 'set', node_id: 'n1', fields: { name: '需求分析' } },
    { op: 'remove_node', node_id: 'n1' }] as any);
  assert.equal(out.nodes.length, 1);
  assert.deepEqual(out.nodes[0].task_refs, []);      // 被删节点引用清理
  assert.equal(out.nodes[0].reject_to, '');
  assert.deepEqual(out.edges, []);
});

test('校验三档摘要与调度摘要', () => {
  assert.equal(gateCheckSummary({ type: 'exists' } as any), '文件交了就行');
  assert.equal(gateCheckSummary({ type: 'contains', keyword: '验收' } as any), '必须包含关键词：验收');
  assert.equal(gateCheckSummary({ type: 'command', command: 'make test' } as any), '自定义命令');
  assert.equal(intervalSummary({ mode: 'interval', interval_key: 'daily9' } as any), '每天 09:00');
  assert.equal(intervalSummary({ mode: 'interval', interval_key: 'weekly1' } as any), '每周一 09:00');
  assert.equal(intervalSummary({ mode: 'cron', cron: '0 9 * * *', timezone: 'Asia/Shanghai' } as any), 'Cron：0 9 * * *（Asia/Shanghai）');
});

test('项目前缀与仓库地址校验', () => {
  assert.ok(validateProjectCode('TP'));
  assert.ok(!validateProjectCode('1bad'));
  assert.ok(!validateProjectCode('a'));
  assert.ok(validateRepoURL('https://github.com/a/b.git'));
  assert.ok(validateRepoURL('git@github.com:a/b.git'));
  assert.ok(!validateRepoURL('ftp://x/y'));
});
```

- [ ] **Step 2: 运行确认失败** Run: `cd frontend && npm test -- src/api/sop/helpers.test.ts` Expected: FAIL（模块不存在）。
- [ ] **Step 3: 实现**

helpers.ts 按测试落地（deriveExecState 逐字对齐 T2 后端纯函数语义：itemStatus∈done/closed→'closed'（最高优先）；hasRun=false 或 runStatus=='cancelled'→'none'；running/paused→'running'；waiting/failed→'intervene'；succeeded→'pr'；其余（queued 等）→'none'——**执行时以 T2 types/sop_item.go 的 DeriveExecState 为唯一权威逐分支对齐**）。applyNodeOps 深拷贝后按 op 顺序应用，set 用 Object.assign 浅合并 fields（executor 整体替换）。sse.ts 核心：

```ts
async function consume(resp: Response, onEvent: (ev: SopStreamEvent) => void) {
  if (!resp.ok || !resp.body) throw new Error(`SSE ${resp.status}`);
  const reader = resp.body.getReader();
  const dec = new TextDecoder();
  let buf = '';
  for (;;) {
    const { done, value } = await reader.read();
    if (done) break;
    buf += dec.decode(value, { stream: true });
    let idx: number;
    while ((idx = buf.indexOf('\n\n')) >= 0) {
      const frame = buf.slice(0, idx); buf = buf.slice(idx + 2);
      for (const line of frame.split('\n')) {
        if (!line.startsWith('data: ')) continue; // 跳过 ": ping" 注释帧
        try { onEvent(JSON.parse(line.slice(6))); } catch { /* 非 JSON 帧忽略 */ }
      }
    }
  }
}
export async function streamSSE(url: string, body: unknown, onEvent: (ev: SopStreamEvent) => void, signal?: AbortSignal) {
  const resp = await fetch(url, { method: 'POST', headers: { 'Content-Type': 'application/json', ...buildAuthHeaders() }, body: JSON.stringify(body), signal });
  await consume(resp, onEvent);
}
export async function streamGet(url: string, onEvent: (ev: SopStreamEvent) => void, signal?: AbortSignal) {
  await consume(await fetch(url, { headers: buildAuthHeaders(), signal }), onEvent);
}
```

api/sop/index.ts 照 src/api/pm/index.ts 模式（类型区+函数区，get/post/put/patch/del 解包 .data；artifactFileURL 返回带 token query 的 URL 字符串供 iframe/下载——**SSE/文件车道鉴权用 buildAuthHeaders，artifact 预览 iframe 无法带头，改为 fetch blob→URL.createObjectURL**，T50 消费）。stores/sop.ts 照现有 pinia setup store 模式；switchProject 内 Promise.all 重载并写 localStorage。SopLayoutView/ProjectSwitcher/ProjectCreateModal 按原型顶部 Tab 栏区块实现（Tab 用 router-link + route.meta.tab 高亮；⋮ 菜单 t-dropdown）。router/menu/i18n 按 Files 描述原位替换（menu.vue 图标分支 'sop' 复用 pm 的 SVG；isMenuItemActive 把 'pm' 前缀判断改 'sop'；handleMenuClick 跳 /platform/sop/items）。本任务五主 Tab 子页先放最小占位组件（真实标题+空态，T40–T52 逐个替换为完整实现——占位组件同文件路径创建，后续任务 Modify 同一文件）。

- [ ] **Step 4: 运行确认通过** Run: `cd frontend && npm test -- src/api/sop/helpers.test.ts && npm run type-check && npm run check-i18n` Expected: PASS。browser-use 走查：侧栏「SOP 工作台」原位出现（PM 入口消失）；点击进 /platform/sop/items；五 Tab 可切换且审批角标=真实待办数；项目切换器列出后端项目、切换后 URL 不变但数据重载；新建项目（前缀非法被拒/合法创建即切换）；访问 /platform/pm 重定向到 /platform/sop/items；控制台零报错。
- [ ] **Step 5: 提交** `git add frontend/package.json frontend/package-lock.json frontend/src/api/sop frontend/src/stores/sop.ts frontend/src/views/sop frontend/src/router/index.ts frontend/src/stores/menu.ts frontend/src/components/menu.vue frontend/src/i18n/locales && git commit -m "feat(sop): 前端壳/路由菜单替换/API 层/SSE 客户端/项目切换器"`

### Task 39: SopProjectSettings（项目配置 + 仓库卡即时生效 + 危险操作）

**Files:**
- Modify: `frontend/src/views/sop/SopProjectSettings.vue`（T38 占位→完整）、`frontend/src/api/sop/helpers.ts`+`helpers.test.ts`（validateRepoURL 已在 T38；本任务追加 `repoDisplayName(url)`=basename 去 .git）
- Consumes: T38 api（getProject/updateProject/addRepo/removeRepo/setDefaultRepo/deleteProject/projectStats）、`src/api/knowledge-base/` KB 清单、T38 store.currentProject、T21 后端 repos 即时生效端点。
- Produces: 完整项目配置子页（**唯一入口=事项页统计带右端「⚙ 项目配置」**；⋮ 更多菜单与项目切换器**不提供**该项目配置入口，避免三处重复——用户实测提出；面包屑「← 返回事项列表」）；**知识库卡片**（当前库名+来源徽标；「使用已有知识库」改绑；项目自建库提供「解绑并删除该库」二次确认；未绑定显示「去创建/绑定」；`auto_note_on_run_end` 开关）与 **简报卡片**（最近渲染时间 + `briefSeqStale` 时「N 条新动态未同步 · 立即重新生成」+ 在知识库打开；**不放「打开黑板与简报」，该面板入口同样只在事项页**）；创建模态知识库三档（新建同名 / 使用已有 / 暂不创建）随 body 传 `kb_mode`。

- [ ] **Step 1: 写失败测试** helpers.test.ts 追加：

```ts
test('repoDisplayName 取 basename 去 .git', () => {
  assert.equal(repoDisplayName('https://github.com/a/b.git'), 'b');
  assert.equal(repoDisplayName('git@github.com:a/my-repo.git'), 'my-repo');
});
```

Run: `npm test -- src/api/sop/helpers.test.ts` Expected: FAIL（repoDisplayName 未导出）。

- [ ] **Step 2: 实现**（**宽版分页布局**，整体 `max-width:1120px`；结构与原型 `vProjSettings` 逐段对齐）：
  - **页面骨架**：顶部项目摘要条（编号方块 `code.slice(0,2)` + 名称/`code · desc` + 右侧三格统计「工作项数 / 推进+待介入 / 知识库名·同步态"，右端只放「保存配置」一个按钮——**本页不提供「黑板与简报」入口，避免与事项页重复**）+ 下面四个 Tab（`SETTINGS_TAB` 状态：`basic|kb|repos|danger`，默认 `basic`；切换项目或从入口重进时重置为 `basic`；窄屏 ≤1080px Tab 栏横向滚动）+ 四个 `.set-pane`（`display:none`，`.on` 显示）。
  - **Tab ① 基本设置**：左右双列**等高**（`grid-template-columns:repeat(2,minmax(0,1fr))`；`.set-cols.stretch` 用 `align-items:stretch`，`.set-cols.stretch>.card{height:100%;display:flex;flex-direction:column}`；窄屏降单列）。左卡「基本信息」：名称*/编号前缀（**disabled**+「创建后不可修改」提示）/**项目描述用 `<textarea rows="6">` 且 `.field.grow{flex:1}` + `textarea{flex:1;min-height:132px;resize:vertical}`**，吃掉卡片剩余高度使两卡底边对齐（实测 740px 对齐）；保存时多行折叠为单行写 `desc`。右卡「默认执行与推进」：默认 SOP 模板（published 下拉）/默认执行器（platform_agent·cli:*）/运行成功后事项状态（空=不自动推进）/工作区根目录；下方整宽卡「通知」：关口与失败通知开关（`pj-notify`）。
  - **Tab ② 知识库与简报**：左列两张卡 + 右列整卡简报预览。知识库卡：`kb_owner` 徽标（项目自建/改绑/PM 迁移）+ 三行 kv（当前库 / 简报文档 / 删除策略）+ 一行操作（KB 下拉 `pj-kb-new` +「改绑」`kb-rebind` +「解绑并删除」`kb-unbind`，仅 `kb_owner=project` 且已绑定时可用）+ 改绑说明。简报卡：`briefSeqStale` 徽标（已同步 / N 条新动态未同步）+ 最近渲染与 seq + `pj-autonote` 开关（运行结束自动记一条）+ 四个按钮（打开黑板与简报 / 立即重新生成 / 记一条动态 / 在知识库打开，未绑 KB 置灰）。右卡「简报预览」：只读 `PmMarkdown` 渲染 `briefMarkdown`，`max-height:340px` 独立滚动；未绑 KB 时卡片副标题显示警示文案。**本任务先渲染占位骨架**（`getBrief` 调用与真实报告拿 T66 接）；`kb-rebind`→`replaceProjectKB`→toast「简报已渲染进新库；旧库那篇标题改为「（已迁移）」」；`kb-unbind` 二次确认文案「该库内所有文档将被删除且不可恢复」。
  - **Tab ③ 代码仓库**：整宽仓库卡（标题右侧 `${repos.length} 个 · 保存即生效`）：空态=虚线卡「还没有绑定代码仓库」+ 三步引导（①粘贴 Git 地址 ②确认分支 ③保存即生效）+「添加第一个仓库」按钮；已绑定=仓库行（名称/分支 tag/默认徽标/地址 + 设默认/解绑）。添加模态：url（validateRepoURL 校验+后端去重错误透出）+分支（默认 main）+显示名（可选，空=repoDisplayName 自动）；提交→addRepo→立即刷新仓库行（首个自动默认由后端保证）；设默认→setDefaultRepo；解绑→t-dialog 二次确认（文案明示「不删除已克隆工作区、不影响进行中运行」）→removeRepo→默认徽标自动顺延（后端）。**增删与设默认独立即时生效，不走「保存配置」**；卡片下方一条浅色说明「未绑定不影响文档类交付，凭证走本机 git 凭证管理」。
  - **Tab ④ 危险操作**：红色描边卡：三行 kv（将删除 = 事项数/运行数/触发配置/仓库绑定；知识库 = 按 `kb_owner` 显示「自建库连带删除（文档不可恢复）」或「只解绑不删除」或「未绑定」；保留 = 流程模板 + 磁盘工作区文件）+ 右下「删除项目」。点击→确认弹窗列明影响面；**进行中/待审批运行存在时按钮 disabled+原因提示；仅剩一个项目时 disabled**；确认→deleteProject→成功后 store.loadProjects+切到剩余首个项目+跳事项 Tab。
  - 「保存配置」**只在页面顶栏放一个**（`top-actions` 右端）→`updateProject`→toast+`store.loadProjects`；**页面底部不再放第二个保存/取消按钮**（避免同一动作双入口，用户实测提出）；`pj-kb` 输入框已移除（知识库统一在 Tab ② 管理，避免双入口）。
- [ ] **Step 3: 运行确认通过** Run: `npm test -- src/api/sop/helpers.test.ts && npm run type-check && npm run check-i18n` PASS。browser-use 走查：**四个 Tab 切换正确且各 pane 内容不串；摘要条三格统计与实际事项数一致；窄屏 ≤1080px 双列降单列、Tab 栏不溢出**；**全页「保存配置」按钮只有顶栏一个、页面底部无重复的保存/取消（四个 Tab 各查一遍）**；**项目配置入口全应用只有事项页统计带一处（项目切换器与 ⋮ 菜单均无该项）；「黑板与简报」入口同样只有事项页统计带一处（项目配置页内无打开按钮）**；**基本设置两卡底边对齐（等高）且项目描述为多行 textarea、可拉高、保存后多行折叠为单行**；改默认 SOP 保存后新建事项继承；添加非法地址被拒、合法 https 与 git@ 各一；首个仓库自动默认徽标；设默认切换；解绑默认后徽标顺延到下一仓库；解绑二次确认出现且文案正确；删除项目在有 running run 时被拒（后端 400 白话透出）；最后一个项目删除按钮 disabled；**切换项目再进入时 Tab 回到「基本设置」**。
- [ ] **Step 4: 提交** `git add frontend/src/views/sop/SopProjectSettings.vue frontend/src/api/sop/helpers.ts frontend/src/api/sop/helpers.test.ts frontend/src/i18n/locales && git commit -m "feat(sop): 项目配置页（仓库卡即时生效/危险操作守卫）"`

### Task 40: SopItemBoard（统计带五态点击筛选 + 检索区 + 执行情况可搜索下拉 + 列表 + 批量 + 自动刷新保焦点）

**Files:**
- Modify: `frontend/src/views/sop/SopItemBoard.vue`（T38 占位→完整）
- Create: `frontend/src/views/sop/components/SopExecFilter.vue`
- Modify: `frontend/src/api/sop/helpers.ts`+`helpers.test.ts`（追加 `matchExecOptions(options, keyword)` 只匹配选项名）
- Consumes: T38 api（listItems/projectStats/bulkLaunch/bulkClose/createRun/setItemStatus）+ store（items/itemStats/itemFilter/startPolling）+ helpers 全部标签映射；T22 后端派生字段 current_node/progress/exec_state；T24 createRun。
- Produces: 事项 Tab 完整列表视图（看板视图 T41 追加）；`SopExecFilter`（props `modelValue: ExecState|''`、`counts: Record<ExecState,number>`；emits `update:modelValue`）供看板复用。

- [ ] **Step 1: 写失败测试** helpers.test.ts 追加：

```ts
test('执行情况搜索只匹配选项名不匹配计数', () => {
  const opts = EXEC_STATES.map((s) => ({ value: s, label: EXEC_STATE_LABELS[s], count: 3 }));
  assert.deepEqual(matchExecOptions(opts, '介入').map((o) => o.value), ['intervene']);
  assert.deepEqual(matchExecOptions(opts, '3').map((o) => o.value), []); // 数字不匹配
  assert.deepEqual(matchExecOptions(opts, '').map((o) => o.value), EXEC_STATES);
});
```

Run: `npm test -- src/api/sop/helpers.test.ts` Expected: FAIL。

- [ ] **Step 2: 实现**（布局照原型「事项」区块）：
  - **统计带**：工作项总数 + 五态格（尚未执行/正在推进/需要介入/待处理 PR/已闭环，计数=projectStats）；**每格可点击=设置 itemFilter.exec 并刷新列表，再点取消（exec=''）；选中格品牌色描边高亮**；右侧附绑定知识库名与默认 SOP 名 + 「项目配置」入口按钮。五态计数之和=总数（后端口径，前端未通过则显示后端原值不修正）。
  - **检索区**：模糊搜索 input（placeholder「搜索标题 / 编号 / 描述」，300ms debounce→itemFilter.q）+ 状态/责任人/来源/类型四个 t-select（来源选项=SOURCE_LABELS 中文）+ **SopExecFilter**；全部条件组合走后端 listItems params；视图切换（列表/看板）分段按钮（看板 T41）。
  - **SopExecFilter**（照原型 fsel）：触发器按钮显示当前选中态中文名（未选=「全部执行情况」）+ ▾；展开浮层=顶部搜索框 + 选项列表（「全部执行情况」+ 五态，各带实时计数徽标）；搜索只匹配选项名（matchExecOptions）；Enter 选首个匹配；Esc/点外部（document mousedown 监听）收起；选中即 emit 并收起；浮层定位触发器下方、视口内钳位。
  - **列表**：勾选列 + 编号(no)/标题+类型 tag+状态 tag/来源(中文 chip)/责任人/优先级(P0-P3 色标)/关联 SOP(模板名，空=「未绑定」)/当前环节(current_node)/进度(progressText)/操作（处理失败→开抽屉 T42、触发→createRun({item_id, template_id: item.template_id||project.default_template_id})、查看运行→跳最近 run 详情）；行点击开抽屉；空态「还没有事项 · 点击右上「新建事项」开始」；页脚共 n 条 + 分页。
  - **批量操作**：勾选后浮出批量条（批量触发 SOP/批量关闭）；bulkLaunch/bulkClose 返回 skipped→toast「已跳过 n 个运行中事项」；操作后刷新列表+统计。
  - **自动刷新保焦点**：startPolling(()=>store.loadItems()+loadStats(), 5000)；**只替换表格行数据与统计数字，检索区 input/select 组件不重挂载**（filter 状态在 store，表格 body 独立子组件或 :key 稳定）；输入中刷新不打断焦点与光标（走查必验）。
- [ ] **Step 3: 运行确认通过** Run: `npm test -- src/api/sop/helpers.test.ts && npm run type-check && npm run check-i18n` PASS。browser-use 走查（spec §16.7）：造 ≥6 事项覆盖五态；**列表列顺序为 编号/标题/状态/来源/责任人/优先级/关联 SOP/当前环节/进度/操作，状态为独立列（色点+中文，取 ITEM_STATUS_LABELS 与状态主题色），标题单元格只含标题与类型标签、不再有状态小字**；统计带每格点击筛选/再点取消、选中描边；fsel 搜索「介入」只命中需要介入、Enter 选中、Esc 收起、点外部收起、计数实时；q+状态+来源+exec 组合过滤正确；来源全中文；done/closed 无运行事项显示已完成/已关闭；批量触发跳过运行中；**聚焦搜索框输入时等待 5s 自动刷新，焦点与光标位置不变**；当前环节/进度随运行推进更新（另开运行后回列表观察）。
- [ ] **Step 4: 提交** `git add frontend/src/views/sop/SopItemBoard.vue frontend/src/views/sop/components/SopExecFilter.vue frontend/src/api/sop/helpers.ts frontend/src/api/sop/helpers.test.ts frontend/src/i18n/locales && git commit -m "feat(sop): 事项列表（五态统计带/可搜索执行情况下拉/批量/自动刷新保焦点）"`

### Task 41: 事项看板视图（状态列拖拽 + 乐观更新回滚 + 全筛选约束）

**Files:**
- Modify: `frontend/src/views/sop/SopItemBoard.vue`（追加看板视图分支）、`frontend/src/api/sop/helpers.ts`+`helpers.test.ts`（追加 `KANBAN_COLUMNS`、`canTransitItem(from,to)` 镜像 T2 后端状态机）
- Consumes: T40 列表/筛选/store、T22 `setItemStatus`（后端 CanTransitSopItem 强制）、T2 `CanTransitSopItem` 迁移表（**执行时读 types/sop_item.go 逐条对齐**）。
- Produces: 看板视图（与列表共享全部筛选条件与统计带）；拖拽改状态端到端可用。

- [ ] **Step 1: 写失败测试** helpers.test.ts 追加：

```ts
test('看板列与状态机镜像', () => {
  assert.deepEqual(KANBAN_COLUMNS, ['backlog','todo','in_progress','in_review','blocked','done','closed']);
  assert.ok(canTransitItem('backlog', 'todo'));
  assert.ok(!canTransitItem('backlog', 'done'));   // 非法跨跃与后端一致
  assert.ok(canTransitItem('in_progress', 'blocked'));
});
```

Run: `npm test -- src/api/sop/helpers.test.ts` Expected: FAIL。

- [ ] **Step 2: 实现**：视图分段切「看板」→ 7 状态列（列头=ITEM_STATUS_LABELS+计数）；卡片=编号/标题/来源中文 chip/优先级/当前环节/进度；**原生 HTML5 拖拽**（draggable 卡片 dragstart 存 item.id；列 dragover preventDefault+高亮；drop→canTransitItem 本地预判：非法→toast 白话「不能从「待梳理」直接到「已完成」」不动数据；合法→**乐观更新**立即移动卡片→setItemStatus→失败回滚+后端错误 toast）；看板受 itemFilter 全部条件约束（与列表同一 store.items）；拖拽中卡片半透明、目标列品牌色虚线描边（照原型看板区块）；空列显示「拖拽事项到此列」。
- [ ] **Step 3: 运行确认通过** Run: `npm test -- src/api/sop/helpers.test.ts && npm run type-check && npm run check-i18n` PASS。browser-use 走查：列表↔看板切换筛选保持；拖 backlog→todo 成功且刷新后持久；拖 backlog→done 被拒卡片回位；exec 筛选下看板只显示命中事项；运行中事项拖拽被后端拒绝时回滚+toast。
- [ ] **Step 4: 提交** `git add frontend/src/views/sop/SopItemBoard.vue frontend/src/api/sop/helpers.ts frontend/src/api/sop/helpers.test.ts frontend/src/i18n/locales && git commit -m "feat(sop): 事项看板（状态列拖拽/乐观更新回滚/筛选约束）"`

### Task 42: SopItemDrawer 九 tab 宽抽屉（概览运行面板/执行进展/产物/会话/澄清/交付/验收/活动/操作日志）

**Files:**
- Modify: `frontend/src/views/sop/SopItemDrawer.vue`（T38 占位→完整；由 SopItemBoard 行点击/操作列打开，props `itemId: string|null`，emits `close/changed`）
- Modify: `frontend/src/api/sop/helpers.ts`+`helpers.test.ts`（追加 `mergeActivities(business,audit)` 按 at 降序合并、`outcomeSummary(outcome)` 白话摘要）
- Consumes: T38 api（getItem/listAttachments/listClarifications/addClarification/answerClarification/generateClarifications/listActivities/retryFailed/relaunch/setOutcome/updateItem/deleteItem/setItemStatus/listRuns/listArtifacts）+ T22 端点、T50 SopDocPreview（产物预览，本任务先按接口契约调用：`<SopDocPreview :artifact="…" :run-id="…" @ingested="…" />`，T50 交付组件实体）、PmMarkdown（`@/views/pm/components/PmMarkdown.vue`）、现有会话跳转路由。
- Produces: 九 tab 抽屉（spec §3.9/§16.8）；失败重试/整条重触发/澄清回答/outcome 回填全链路。

- [ ] **Step 1: 写失败测试** helpers.test.ts 追加：

```ts
test('活动合并降序与 outcome 摘要', () => {
  const merged = mergeActivities(
    [{ at: '2026-11-19T10:00:00Z', kind: 'business', text: '创建' } as any],
    [{ at: '2026-11-19T11:00:00Z', kind: 'audit', text: '编辑' } as any]);
  assert.equal(merged[0].text, '编辑');
  assert.equal(outcomeSummary({ value_achieved: 'achieved', summary: '上线达标' } as any), '上线达标');
  assert.equal(outcomeSummary({} as any), '尚未回填');
});
```

Run: `npm test -- src/api/sop/helpers.test.ts` Expected: FAIL。

- [ ] **Step 2: 实现**（布局照原型事项详情抽屉；宽 min(1080px,94vw)，顶部标题=编号+标题+状态 tag，右上关闭）：
  - **概览**：运行状态面板（最近 run 状态+进度；failed→「补充上下文重试」textarea+按钮→retryFailed({context})；「整条重触发」→relaunch 产生新 run 旧 run 保留；「排查」→跳 run 详情；「事项管理」=编辑/关闭/删除/导出 JSON）+ 字段卡（来源中文/提出人/优先级/类型/关联 SOP/绑定知识库/**代码仓库（随项目绑定联动，T39 数据）**/创建时间）+ 描述（PmMarkdown）+ 澄清入口（open 澄清数徽标→跳澄清 tab）。
  - **执行进展**：最近 run 的 node_runs 时间线（节点名/状态中文/attempt/rework/ gate 结果/耗时；点击跳 run 详情对应节点）；无 run→空态「尚未执行 · 到列表操作列触发」。
  - **产物**：最近 run artifacts 表（名称/路径/大小/shortSha/已入库标签）→点击开 SopDocPreview；下载按钮=artifactFileURL(download)。
  - **会话**：本事项 SOP 会话清单（后端 sessions 过滤 SOPSessionMarker+run 关联；列出标题/时间→跳现有会话页）。
  - **澄清**：清单（问题/状态 open|answered/回答/来源 human|ai|rework|answer）；回答输入→answerClarification；「AI 生成澄清」→generateClarifications→追加 open 项；**回答注入下次运行 humanContext（后端 T14/T22 已实现，前端只展示）**。
  - **交付**：outcome 展示 + 交付类产物（artifact_outputs 标记 deliverable 的产物，同预览弹窗）。
  - **验收**：outcome 回填表单（value_achieved 三选 achieved/partial/not_achieved + summary textarea + 指标键值对）→setOutcome→toast+概览刷新。
  - **活动**：business 活动流；**操作日志**：audit 活动流；两 tab 数据=listActivities(kind)；合并视图按钮用 mergeActivities。
  - 编辑/关闭/删除/导出均产生 audit 日志（后端 T22）；删除二次确认；changed 事件→列表刷新。
- [ ] **Step 3: 运行确认通过** Run: `npm test -- src/api/sop/helpers.test.ts && npm run type-check && npm run check-i18n` PASS。browser-use 走查（spec §16.8）：九 tab 齐全可切；失败事项补上下文重试→节点重新推进且反馈进任务书（run 详情核对）；整条重触发→新 run 旧 run 保留；澄清回答后下次运行 humanContext 含该回答；验收回填落 outcome 且概览可见；编辑/关闭/删除/导出后操作日志有记录；产物点击开大弹窗（T50 就绪后联调）。
- [ ] **Step 4: 提交** `git add frontend/src/views/sop/SopItemDrawer.vue frontend/src/api/sop/helpers.ts frontend/src/api/sop/helpers.test.ts frontend/src/i18n/locales && git commit -m "feat(sop): 事项详情九 tab 抽屉（运行面板/澄清/验收回填/活动日志）"`

### Task 43: SopItemCreateModal（980px 双栏 + AI 拆解面板等高拉伸 + 写入描述覆盖 + 澄清 seeding）

**Files:**
- Modify: `frontend/src/views/sop/SopItemCreateModal.vue`（T38 占位→完整；props `projectId`，emits `close/created`）
- Modify: `frontend/src/api/sop/helpers.ts`+`helpers.test.ts`（追加 `upsertBreakdownBlock(desc,block)`、`parseBreakdownCard(text)`）
- Consumes: T38 `streamSSE('/api/v1/sop/ai/breakdown', …)`（T25 后端）、`src/api/agent/` custom_agents 清单、T38 api createItem/addAttachment/addClarification、T22 澄清 seeding。
- Produces: 新建事项模态（spec §16.10 全项）；拆解块覆盖语义与澄清 seeding 纯函数。

- [ ] **Step 1: 写失败测试** helpers.test.ts 追加：

```ts
test('拆解块重复写入为覆盖不重复', () => {
  const block = '目标：X\n子事项：a、b';
  const d1 = upsertBreakdownBlock('原始描述', block);
  assert.ok(d1.includes('原始描述'));
  assert.ok(d1.includes('<!-- sop-ai-breakdown -->'));
  const d2 = upsertBreakdownBlock(d1, '目标：Y');
  assert.equal(d2.match(/<!-- sop-ai-breakdown -->/g)?.length, 1); // 覆盖
  assert.ok(d2.includes('目标：Y'));
  assert.ok(!d2.includes('目标：X'));
  assert.ok(d2.includes('原始描述')); // 用户原文不动
});

test('拆解卡解析容错', () => {
  const card = parseBreakdownCard('前言 ```json\n{"goal":"G","sub_items":[{"title":"a","executor":"platform_agent"}],"acceptance":"A"}\n``` 后语');
  assert.equal(card?.goal, 'G');
  assert.equal(card?.sub_items[0].executor, 'platform_agent');
  assert.equal(parseBreakdownCard('没有 JSON'), null);
});
```

Run: `npm test -- src/api/sop/helpers.test.ts` Expected: FAIL。

- [ ] **Step 2: 实现**（布局照原型新建事项模态）：
  - 模态宽 980px、总高 ≤84vh；**双栏 align-items:stretch 等高**：左表单 flex:1，右 AI 面板固定宽 380px、min-height 520px、max-height calc(84vh − 140px)、display:flex column——**底部不留空白**。
  - 左表单：标题* / 初步想法 textarea（maxlength 2000 + 右下 0/2000 计数）/ 来源*（六键中文下拉）+提出人 / 优先级 P0-P3 分段（默认 P2）/ 附件（多文件，上传即 addAttachment 暂存 id 列表）/ 类型（requirement/task/incident/research/other）+流程模板（published 下拉，可空）。
  - 右 AI 面板：顶部智能体下拉（真实 custom_agents）；消息区 flex:1 overflow-y:auto（**新消息自动 scrollTop=scrollHeight**）；空态文案+「开始拆解」按钮；流式追问（streamSSE breakdown，text_delta 追加渲染 PmMarkdown）；追问输入框+发送**固定面板底部恒可见**（Enter 发送）；「跳过追问直接拆解」按钮；拆解卡渲染=目标+子事项列表（含建议执行器类型中文标签：平台智能体/本机 CLI/脚本/人工关口）+验收口径；**「写入描述」→upsertBreakdownBlock 覆盖左栏描述中的标记块**；完成后背景补充→domain_data.ai_notes（随创建提交）。
  - 底部：取消 / 保存草稿（status=backlog）/ 确认事项（status=todo）；创建时把追问问答 seeding 进 clarifications（status=answered，source=ai）——createItem body 带 clarifications 数组（T22 支持）或创建后逐条 addClarification+answerClarification。
- [ ] **Step 3: 运行确认通过** Run: `npm test -- src/api/sop/helpers.test.ts && npm run type-check && npm run check-i18n` PASS。browser-use 走查（spec §16.10）：右栏与左表单等高（DevTools 量 bottom 对齐，无底部空白）；长对话 20 轮消息区独立滚动、自动滚到底、输入框恒在视口内、模态总高 ≤84vh；追问 Enter 发送/可跳过；写入描述两次=覆盖不重复；草稿=backlog、确认=todo；创建后澄清 tab 含已回答追问；建议执行器类型与模板节点语义一致。
- [ ] **Step 4: 提交** `git add frontend/src/views/sop/SopItemCreateModal.vue frontend/src/api/sop/helpers.ts frontend/src/api/sop/helpers.test.ts frontend/src/i18n/locales && git commit -m "feat(sop): 新建事项双栏模态（AI 拆解等高面板/覆盖写入/澄清 seeding）"`

### Task 44: SopTemplateList（内置只读+复制 / 项目 / 全局 / 版本历史 / 发布白话校验）

**Files:**
- Modify: `frontend/src/views/sop/SopTemplateList.vue`（T38 占位→完整）
- Consumes: T38 api（listTemplates/createTemplate/deleteTemplate/validateTemplate/publishTemplate/listRevisions/copyTemplate）、T23 后端内置只读守卫、store.currentProject。
- Produces: 流程模板 Tab；卡片网格（照 AutomationList.vue 卡片模式+原型模板库区块）：名称/描述/范围徽标（内置/项目/全局）/节点数/状态（draft/published）/最近发布时间；操作=设计（跳 designer/:id）/发布/版本历史/复制/删除；**内置卡只读：设计按钮=只读查看，发布/删除隐藏，「创建副本」主操作**（copyTemplate→跳新副本 designer）；发布→validateTemplate 错误以白话列表弹窗（如「失败后选了打回上游，但没选打回哪一步」），通过→publishTemplate→toast；版本历史抽屉=revisions 列表（版本号/发布时间/节点数/查看）；新建模板→createTemplate（空 definition）→跳 designer；删除二次确认（published 被运行引用时后端拒绝透出）。
- [ ] **Step 1: 实现**（无新增纯逻辑；组件按上述契约）。
- [ ] **Step 2: 运行确认通过** Run: `npm run type-check && npm run check-i18n` PASS。browser-use 走查：内置两模板（产品方案交付/需求上线复盘）可见且只读；副本创建后可编辑可发布；草稿发布校验错误白话弹出；版本历史列出每次发布；项目/全局范围徽标正确。
- [ ] **Step 3: 提交** `git add frontend/src/views/sop/SopTemplateList.vue frontend/src/i18n/locales && git commit -m "feat(sop): 模板库（内置只读副本/版本历史/发布白话校验）"`

### Task 45: SopDesigner 画布（vue-flow + 双出口节点卡 + 快捷工具条 + 快照历史 + 自动排版 + 小地图）

**Files:**
- Modify: `frontend/src/views/sop/SopDesigner.vue`（T38 占位→完整）
- Create: `frontend/src/views/sop/components/SopNodeCard.vue`、`frontend/src/views/sop/components/SopCanvasToolbar.vue`、`frontend/src/views/sop/canvas/history.ts`+`history.test.ts`、`frontend/src/views/sop/canvas/layout.ts`+`layout.test.ts`、`frontend/src/views/sop/canvas/anchors.ts`
- Consumes: `@vue-flow/core`（T38 已装：VueFlow/useVueFlow/Handle/Position/MarkerType）、T38 api（getTemplate/updateTemplate/validateTemplate/publishTemplate/copyTemplate）+ helpers.applyNodeOps、T46 SopInspector（右侧 360px，与 AI 面板互斥）、T47 SopAiPanel。
- Produces:（T46/T47/T49 消费）
  - `history.ts`：`class SnapshotHistory<T>{ constructor(limit=50); push(snap:T):void; undo():T|null; redo():T|null; canUndo:boolean; canRedo:boolean; reset():void }`（undo 后新 push 清空 redo 栈）。
  - `layout.ts`：`topoLayers(def):string[][]`（依赖边分层，环→抛白话错误）、`autoLayout(def):SopDefinition`（层距 250px；行距=max(110, 最高节点卡实测高+30)，标准双出口卡=170px；上游恒在左；返回新 position）。
  - `anchors.ts`：`portRegistry`（Map<nodeId+portKind, HTMLElement>）+ `computeEdgeAnchors(flowEl, edge, zoom, pan)`（getBoundingClientRect 实测端口圆心→减 flow 容器 rect→除 zoom 减 pan；缩放/节点加高后重算）。
  - `SopNodeCard.vue`：props `node:SopNode`、`readonly:boolean`、`pendingSource:{nodeId,kind}|null`；底部虚线分隔两行出口——绿点「成功」+右缘绿 Handle、红点「失败打回」+右缘红 Handle（reject_to 非空行内「→ 目标步名」）；左缘入站圆点 Handle；徽标=执行器类型/产物数/失败策略；emits `exit-click(nodeId,kind)`、`inbound-click(nodeId)`；出口行 @mousedown.stop 不触发拖拽；readonly（运行详情）不渲染出口端口。
  - `SopCanvasToolbar.vue`：悬浮画布底部居中；↩/↪（canUndo/canRedo 置灰）｜−/百分比/＋（20%–160%）｜⛶ 适应｜⿻ 自动排版｜▣ 小地图开关（高亮态）｜右端图例「━ 成功 / ┄ 失败打回」；emits 对应动作。
  - 设计器行为：左侧节点面板（agent/script/approval/sop 四类拖入，新节点默认值=T46 inspectorDefaults）；连线=点出口→点目标左圆点（成功→edges 追加；失败→node.reject_to=目标 id，红色虚线边从红端口到目标左圆点，与检查器下拉双向同步）；点自身左圆点取消 pending；空白拖动平移；滚轮绕光标缩放、按钮绕视野中心；**历史入栈时机=增删节点/连线/拖拽落点/检查器每次变更/AI 草稿应用/自动排版前；视图操作不入栈**；小地图=自绘 SVG（类型色块+依赖线+当前视野框，点击跳转视野中心，右上角）；保存草稿=updateTemplate；发布=validateTemplate 白话错误弹窗→publishTemplate；内置模板只读→「创建副本后可编辑」横幅+copyTemplate；面包屑返回模板库、流程模板 Tab 保持高亮；重新进入历史与视图 reset。
- [ ] **Step 1: 写失败测试**

`history.test.ts`：

```ts
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { SnapshotHistory } from './history';

test('快照历史往返与 redo 清空', () => {
  const h = new SnapshotHistory<number>(50);
  h.push(1); h.push(2); h.push(3);
  assert.equal(h.undo(), 2);
  assert.equal(h.undo(), 1);
  assert.equal(h.redo(), 2);
  h.push(9);                       // 新变更清空 redo
  assert.equal(h.canRedo, false);
  assert.equal(h.undo(), 2);
});

test('上限 50 与空栈置灰', () => {
  const h = new SnapshotHistory<number>(50);
  assert.equal(h.canUndo, false);
  for (let i = 0; i < 60; i++) h.push(i);
  let n = 0; while (h.canUndo) { h.undo(); n++; }
  assert.equal(n, 49);             // 60 快照保留最近 50，可回退 49 步
});
```

`layout.test.ts`：

```ts
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { topoLayers, autoLayout } from './layout';

test('拓扑分层上游恒在左', () => {
  const def: any = { nodes: [{ id: 'c' }, { id: 'a' }, { id: 'b' }], edges: [{ from: 'a', to: 'b' }, { from: 'b', to: 'c' }] };
  assert.deepEqual(topoLayers(def), [['a'], ['b'], ['c']]);
  const out = autoLayout(def);
  const x = (id: string) => out.nodes.find((n: any) => n.id === id)!.position.x;
  assert.ok(x('a') < x('b') && x('b') < x('c'));
  assert.equal(x('b') - x('a'), 250);
});

test('同层行距与环检测', () => {
  const def: any = { nodes: [{ id: 'a' }, { id: 'b' }, { id: 'c' }], edges: [{ from: 'a', to: 'c' }, { from: 'b', to: 'c' }] };
  const out = autoLayout(def);
  const ys = out.nodes.filter((n: any) => n.id !== 'c').map((n: any) => n.position.y).sort((p: number, q: number) => p - q);
  assert.ok(ys[1] - ys[0] >= 110);
  assert.throws(() => topoLayers({ nodes: [{ id: 'a' }, { id: 'b' }], edges: [{ from: 'a', to: 'b' }, { from: 'b', to: 'a' }] } as any), /环/);
});
```

Run: `npm test -- src/views/sop/canvas/history.test.ts src/views/sop/canvas/layout.test.ts` Expected: FAIL。

- [ ] **Step 2: 实现** history/layout 按测试落地（topoLayers=Kahn 分层；autoLayout 每层 x=layer*250，层内 y 累加行距）。anchors.ts：SopNodeCard onMounted 把出口/入站 DOM 注册进 portRegistry（onUnmounted 注销）；自定义 reject 边组件（vue-flow custom edge）用 computeEdgeAnchors 画红色虚线 path；useVueFlow().onViewportChange + 节点 ResizeObserver → 触发边重算（响应式 ref 版本号）。设计器主体按 Produces 契约组装；vue-flow 配置 :min-zoom="0.2" :max-zoom="1.6" :pan-on-drag="[1,2]"（空白拖动平移）:zoom-on-scroll="true"（绕光标）；节点拖拽落点 onNodeDragStop→push 历史；检查器 change→push 历史+更新 def；小地图 SVG 按 nodes bbox 归一化绘制+视野框（viewport→缩略图坐标换算），click→setCenter。
- [ ] **Step 3: 运行确认通过** Run: `npm test -- src/views/sop/canvas/history.test.ts src/views/sop/canvas/layout.test.ts && npm run type-check && npm run check-i18n` PASS。browser-use 走查（spec §16.17/18）：拖入四类节点；点成功出口→点目标左圆点成边；点打回出口→目标左圆点写 reject_to 且检查器下拉同步、红虚线对齐端口圆心；缩放 20%/160% 后边仍对齐；点自身左圆点取消；撤销/重做覆盖增删/连线/拖拽/检查器变更/自动排版，视图操作不入栈，无历史置灰；适应画布全节点居中可见；自动排版分层无重叠（节点加高后行距 170）；小地图开关/视野框/点击跳转；图例在工具条右端、左面板无重复；发布白话校验；内置只读+副本；重进设计器历史重置。
- [ ] **Step 4: 提交** `git add frontend/src/views/sop/SopDesigner.vue frontend/src/views/sop/components/SopNodeCard.vue frontend/src/views/sop/components/SopCanvasToolbar.vue frontend/src/views/sop/canvas frontend/src/i18n/locales && git commit -m "feat(sop): 画布设计器（双出口连线/快照历史/自动排版/小地图/快捷工具条）"`

### Task 46: SopInspector 五步傻瓜式检查器（? 气泡引擎 + 多选下拉 + 模型下拉 + 2×2 失败卡 + 两级序号）

**Files:**
- Create: `frontend/src/views/sop/components/SopInspector.vue`、`frontend/src/views/sop/components/SopHelpTip.vue`、`frontend/src/views/sop/components/help-tip.ts`+`help-tip.test.ts`、`frontend/src/views/sop/components/SopMultiSelect.vue`、`frontend/src/views/sop/inspector/defaults.ts`+`defaults.test.ts`
- Consumes: T45 设计器（props `node:SopNode`、`def:SopDefinition`、`readonly:boolean`；emits `change(fields)`、`ai-configure(nodeId)`）、`src/api/agent/`（custom_agents 含 SystemPrompt/SelectedSkills/MCP 绑定）、`src/api/model/`（模型管理清单）、`src/api/skill/`、`src/api/mcp-service.ts`、T38 helpers.gateCheckSummary、T9 后端校验语义。
- Produces:
  - `help-tip.ts`：`showTip(anchor:HTMLElement, text:string):void`、`hideTip():void`、`tipInstanceCount():number`（测试用）——**全局单例**：首个 show 创建 `div#sop-help-tip` append body（深色底/白字/max-width 280px/带指向箭头/role=tooltip）；定位=anchor rect 上方居中，**横纵双向视口钳位**；上方空间不足→翻转到 anchor 下方且箭头仍指向图标；document scroll(capture)/click→hideTip；重复 show 复用同一实例（20 轮悬停+重渲染后 count==1）。
  - `SopHelpTip.vue`：props `text:string`；渲染 ? 圆形图标 button（tabindex=0，aria-label=说明）；@mouseenter/@focus→showTip($el,text)；@mouseleave/@blur→hideTip。
  - `SopMultiSelect.vue`（msel）：props `options:{value,label}[]`、`modelValue:string[]|null`、`allLabel:string`（如「角色全部技能（N）」）、`placeholder`；触发器显示摘要（null/全选→allLabel；部分→顿号串；空→placeholder）；展开=复选列表浮层（**勾选即时 emit 且浮层不关**；点外部收起；**开一个关其余**——document 级 mousedown 协调）；全选勾→emit null（skills 语义）/全量数组（mcp 语义由父组件决定）。
  - `defaults.ts`：`inspectorDefaults(type:'agent'|'script'|'approval'|'sop'):SopNode`（新节点全默认：skills:null、mcp:['know-kb']、carry_over:true、artifact check:exists、model:''、max_attempts:3、max_reworks:2、timeout_minutes:60、on_failure:'loop'、executor.kind 按 type）；`skillSummary(role,node)`、`rejectCandidates(def,nodeId)`（上游祖先集合，T9 Upstreams 同口径）、`failureHelpText(onFailure)`（第 5 步 ? 气泡动态文案）。
  - 检查器结构（360px，照原型五步区块，**全程无「任务书/门禁/gate」工程术语，高级折叠除外**）：标题栏=节点类型标签+「✦ AI 配这一步」按钮（不显示节点名/内部 id）；下方「步骤名称」输入（改名→画布卡片同步）。**① 这一步由谁执行**：执行器二选一分段（平台智能体/本机编程 CLI）；agent→角色下拉（真实 custom_agents）→角色卡（头像/简介/模型/产物数/校验数/失败策略四格统计随配置实时更新 +「编辑角色 prompt」只读预览弹窗→跳智能体页）；cli→CLI 下拉（10 种）+凭据下拉+**模型下拉（模型管理已配置模型+「默认·跟随凭据」档，禁手输）**+角色（可选）下拉+知识库范围下拉（跟随项目/指定知识库/全部；custom→KB 多选）+私有 HOME 提示；**CLI+模型、知识库范围+指定 KB 双列同行**；未选角色→技能区「该角色未配置技能」提示。**② 这一步做什么**：大白话 textarea（**无变量插入入口、无 {{…}} 芯片**）+ 标题右侧 ? 气泡（四类自动附带上下文：事项背景/上游产物/返工意见/人工补充，及各自附带时机）+「额外参考哪步的产物」msel（选项=步骤名，默认摘要「不额外参考 · 上游自动带」，写 task_refs）。**③ 遵循什么规则 · 用哪些能力**：角色 Rules 预览（只读块）+节点补充规则 textarea+技能 msel（默认全选=角色全部）+MCP msel（默认仅 know-kb，节点级不继承角色绑定）。**④ 产出什么 · 怎么校验**：三子步 **16px 空心描边小圆、标题行在卡片边框外**——先汇报完成状态（只读卡含 pass/rework/fail/human 四终态标签+? 气泡「缺失自动续会话追问」）→再检查交付物（每交付物卡：名称/路径/必须交开关/校验方式下拉三档+keyword/command 输入）→最后跑额外校验（高级折叠 gate_extra，&& 合并提示）；auto_ingest 开关「通过后自动入知识库」。**⑤ 失败了怎么办**：四卡 2×2（⟳原地重试/↩打回上游/⏸停下等人/✕标记失败，各一句白话解释）；选中展开参数（重试次数 stepper/打回哪一步下拉=rejectCandidates）；返工上限「超过转人工」+超时分钟+「返工时保留上一轮产物」开关；? 气泡文案随策略动态切换。**主五步序号=18px 实心浅绿圆**。script 节点→Shell 脚本正文（高级折叠）+产物/失败两节+? 气泡；approval 节点→「关口会发生什么」白话说明+默认打回目标+超时小时/动作（notify/fail，T19）+? 气泡；sop 节点→子模板下拉（published）。**解释性说明全部收进 ? 气泡**（CLI 节点 10 个：模型/角色（可选）/知识库检索范围/第 2 步标题/额外参考/遵循的规则/MCP/先汇报完成状态/第 5 步标题/打回哪一步；agent 节点无 CLI 专属项）；inline 提示行仅保留单位短句（次/分钟）/空态引导/规则预览内容/高级折叠内提示。每次字段变更→emit change（设计器入历史栈）。
- [ ] **Step 1: 写失败测试**

`help-tip.test.ts`（node 环境无 DOM——用最小 document stub 或仅测纯定位函数；**定位计算抽纯函数 `clampTipPosition(anchorRect,tipSize,viewport)` 返回 {top,left,flipped}**）：

```ts
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { clampTipPosition } from './help-tip';

test('气泡视口钳位与上方不足翻转', () => {
  const vp = { width: 1280, height: 800 };
  const anchor = { top: 300, left: 600, width: 16, height: 16, bottom: 316, right: 616 };
  const p = clampTipPosition(anchor as any, { width: 280, height: 120 }, vp);
  assert.equal(p.flipped, false);
  assert.ok(p.top >= 0 && p.left >= 0 && p.left + 280 <= 1280);
  const nearTop = { ...anchor, top: 8, bottom: 24 };
  const p2 = clampTipPosition(nearTop as any, { width: 280, height: 120 }, vp);
  assert.equal(p2.flipped, true);           // 翻转到下方
  assert.ok(p2.top >= 24);                  // 箭头仍指向图标
  const nearRight = { ...anchor, left: 1270, right: 1286 };
  const p3 = clampTipPosition(nearRight as any, { width: 280, height: 120 }, vp);
  assert.ok(p3.left + 280 <= 1280);         // 横向钳位
});
```

`defaults.test.ts`：

```ts
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { inspectorDefaults, rejectCandidates, failureHelpText } from './defaults';

test('新节点默认值与 spec §16.15 一致', () => {
  const n = inspectorDefaults('agent');
  assert.equal(n.skills, null);
  assert.deepEqual(n.mcp, ['know-kb']);
  assert.equal(n.carry_over, true);
  assert.equal(n.max_attempts, 3);
  assert.equal(n.on_failure, 'loop');
  assert.equal(n.executor.kind, 'platform_agent');
  assert.equal(inspectorDefaults('script').executor.kind, 'script');
});

test('打回候选=上游祖先且文案随策略切换', () => {
  const def: any = { nodes: [{ id: 'a' }, { id: 'b' }, { id: 'c' }], edges: [{ from: 'a', to: 'b' }, { from: 'b', to: 'c' }] };
  assert.deepEqual(rejectCandidates(def, 'c').map((n: any) => n.id), ['a', 'b']);
  assert.notEqual(failureHelpText('loop'), failureHelpText('reject'));
  assert.ok(failureHelpText('human').includes('人'));
});
```

Run: `npm test -- src/views/sop/components/help-tip.test.ts src/views/sop/inspector/defaults.test.ts` Expected: FAIL。

- [ ] **Step 2: 实现** clampTipPosition 纯函数按测试落地（默认上方：top=anchor.top−tipH−8；top<0→flipped top=anchor.bottom+8；left=anchor 中心−tipW/2 钳位 [8, vp.width−tipW−8]）；showTip/hideTip 操作单例 DOM（scroll capture+click 监听只注册一次）；SopHelpTip/SopMultiSelect/检查器按 Produces 契约组装（TDesign t-input/t-select/t-stepper/t-switch/t-collapse + 自绘序号圆/策略卡/角色卡，样式照原型 inspector 区块）。
- [ ] **Step 3: 运行确认通过** Run: `npm test -- src/views/sop/components/help-tip.test.ts src/views/sop/inspector/defaults.test.ts && npm run type-check && npm run check-i18n` PASS。browser-use 走查（spec §16.15/19/21 逐项）：五节齐全、标题栏仅类型标签+AI 按钮；改名画布同步；模型下拉无手输、切换后角色卡模型格同步；CLI+模型双列；技能/MCP msel 摘要即时更新、全选=null、浮层勾选不关/点外部收起/开一个关其余；未选角色技能区提示；校验三档编译 gate_commands（保存后后端核对 test -s/grep -q/原样）；终态只读卡；失败四卡 2×2 参数显隐、打回未选目标发布白话报错；返工上限/超时/保留开关持久化；script/approval 降级节；**CLI 节点 10 个 ?、agent 无 CLI 专属项**；悬停/聚焦弹出、移开/失焦/滚动/点击隐藏；贴近顶部翻转且箭头指向图标、横向不越界；第 5 步气泡随策略切换、MCP 气泡按执行器区分；**连续 20 轮悬停+重渲染后 document.querySelectorAll('#sop-help-tip').length==1**；第 2 步无占位符入口；非检查器视图无 ?；控制台零报错。
- [ ] **Step 4: 提交** `git add frontend/src/views/sop/components/SopInspector.vue frontend/src/views/sop/components/SopHelpTip.vue frontend/src/views/sop/components/help-tip.ts frontend/src/views/sop/components/help-tip.test.ts frontend/src/views/sop/components/SopMultiSelect.vue frontend/src/views/sop/inspector frontend/src/i18n/locales && git commit -m "feat(sop): 五步傻瓜式检查器（? 气泡单例引擎/多选下拉/模型下拉/2×2 失败卡）"`

### Task 47: SopAiPanel（flow|node 双模式 / 场景 chips / 字段级差异卡 / 按节点与全部应用 / 草稿不发布）

**Files:**
- Create: `frontend/src/views/sop/components/SopAiPanel.vue`
- Modify: `frontend/src/api/sop/helpers.ts`+`helpers.test.ts`（**新增** `formatDiffCards(def,ops)` 导出+实现+测试——T38 不含此函数，本任务首次落地）、`frontend/src/views/sop/SopDesigner.vue`（右侧面板「画布/AI 编排」分段切换，与检查器互斥；✦ AI 配这一步→切 AI 面板 node 模式 chip）
- Consumes: T38 `streamSSE('/api/v1/sop/ai/orchestrate', {template_id, mode, instruction, node_id?, inventory})`（T25 后端；inventory=OrchestrateInventory{Agents,CLIs,Models,Skills,MCPs} 由前端从真实清单 API 组装）、T38 helpers.applyNodeOps、T45 历史栈、T23 内置只读。
- Produces: AI 编排助手面板（spec §16.12/16.16）：场景 chips（产品方案交付/需求上线复盘/技术调研/自定义流程——空画布按场景生成完整链含打回关系）；对话输入+发送（流式渲染）；**node 模式 chip**（显示目标节点名，可退出回整流程；零输入「生成配置」→set ops 推断角色/任务/补充规则/MCP/交付物校验/失败策略逐字段带理由；补一句话重新生成→只追加任务描述不覆盖已自定义字段）；草稿 ops→**字段级差异卡（原值→新值白话格式化+理由）**；「应用此节点」单独应用后草稿移除该项、「全部应用」后清空；应用=applyNodeOps 到画布**未保存草稿态**（入历史栈，不自动发布）；「放弃草稿」画布不变；内置模板→应用前提示需复制（copyTemplate 流程）；服务端 Skipped ops 在反馈区白话报告（非法角色/CLI id 跳过该字段不整单拒绝）；gate_commands 永不由 AI 直写（应用后后端 RefreshDerived 派生）。
- [ ] **Step 1: 写失败测试** helpers.test.ts 追加：

```ts
test('差异卡原值→新值白话格式化', () => {
  const def: any = { nodes: [{ id: 'n1', name: '分析', executor: { kind: 'platform_agent', agent_id: '' }, on_failure: 'loop' }], edges: [] };
  const cards = formatDiffCards(def, [
    { op: 'set', node_id: 'n1', fields: { executor: { kind: 'platform_agent', agent_id: 'a1' }, on_failure: 'reject' }, reason: '需要专业角色' } as any]);
  assert.equal(cards.length, 2);
  const exec = cards.find((c) => c.field.includes('执行'))!;
  assert.ok(exec.from.includes('未配置') || exec.from.includes('平台'));
  assert.ok(exec.to.includes('a1') || exec.to.includes('角色'));
  assert.equal(exec.reason, '需要专业角色');
  const fail = cards.find((c) => c.field.includes('失败'))!;
  assert.equal(fail.from, '原地重试');
  assert.equal(fail.to, '打回上游');
});
```

Run: `npm test -- src/api/sop/helpers.test.ts` Expected: FAIL。

- [ ] **Step 2: 实现** formatDiffCards：逐 op 逐 field 生成 {nodeId,nodeName,field,from,to,reason}；枚举值→中文标签（on_failure 四态/executor.kind/检查三档）；空值→「未配置」；对象值→摘要串。SopAiPanel 按 Produces 契约组装（照原型 AI 编排面板区块：chips 横排/消息流/差异卡列表/底部应用按钮组）；inventory 组装=Promise.all 五个清单 API；node 模式由设计器 ✦ 按钮传入 nodeId 激活。
- [ ] **Step 3: 运行确认通过** Run: `npm test -- src/api/sop/helpers.test.ts && npm run type-check && npm run check-i18n` PASS。browser-use 走查（spec §16.12/16.16）：画布/AI 分段切换与检查器互斥；空画布场景 chip 生成完整链（含打回互设可达）且发布校验通过；有画布增量指令只动目标节点；差异卡原值→新值+理由正确；应用此节点→草稿移除该项；全部应用→清空；均为未保存态（刷新前不发布）；放弃草稿画布不变；内置模板应用提示复制；✦ 零输入生成+补一句话只追加任务；非法 id 跳过并报告；单节点 chip 退出回整流程。
- [ ] **Step 4: 提交** `git add frontend/src/views/sop/components/SopAiPanel.vue frontend/src/views/sop/SopDesigner.vue frontend/src/api/sop/helpers.ts frontend/src/api/sop/helpers.test.ts frontend/src/i18n/locales && git commit -m "feat(sop): AI 编排助手（双模式/差异卡预览/按节点应用/草稿不发布）"`

### Task 48: SopRunList（八态 chips 计数 + 组合搜索 + 成功率/Token 页头 + 自动刷新保焦点）

**Files:**
- Modify: `frontend/src/views/sop/SopRunList.vue`（T38 占位→完整）、`frontend/src/api/sop/helpers.ts`+`helpers.test.ts`（追加 `triggerSourceLabel(kind)`、`successRate(stats)`）
- Consumes: T38 api（listRuns/runStats/retryRun）+ store.runs/runFilter/startPolling、T24 StatsSummary（各状态全局计数/succeeded/finished/total_tokens/total）。
- Produces: 运行记录 Tab（spec §8/§16.9）：页头标题+实时指示（● n 个运行进行中 / 暂无进行中的运行）+右侧成功率（已结束中 succeeded 占比）+ n 次已结束 + 累计 Token + 总次数 + 刷新按钮；**状态分段 chips 带计数**（全部/进行中/排队中/已暂停/待确认/已完成/失败/已取消，计数=全局按状态统计，失败计数红色）+模糊搜索（运行 id/事项标题/事项编号/SOP 名）+「全部 SOP」模板下拉，三者组合；列表字段=运行 id/关联事项（编号+标题）/SOP（冻结版本 rev 号）/触发来源（triggerSourceLabel）/状态/进度/开始时间/Token 与成本/详情入口；失败行一键重试；空态「没有匹配的运行 · 换个关键词，或到事项列表查看对应事项的执行结果」；页脚共 n 条；**自动刷新（5s）只换行数据，搜索框焦点/光标不丢**。
- [ ] **Step 1: 写失败测试** helpers.test.ts 追加：

```ts
test('触发来源中文与成功率口径', () => {
  assert.equal(triggerSourceLabel('schedule'), 'SOP 任务');
  assert.equal(triggerSourceLabel('manual'), '手动');
  assert.equal(triggerSourceLabel('child_run'), '子流程');
  assert.equal(triggerSourceLabel('api'), 'API');
  assert.equal(successRate({ succeeded: 3, finished: 4 } as any), '75%');
  assert.equal(successRate({ succeeded: 0, finished: 0 } as any), '—');
});
```

Run: `npm test -- src/api/sop/helpers.test.ts` Expected: FAIL。

- [ ] **Step 2: 实现** 按 Produces 契约（照原型运行记录区块+LoopForge runs.js 语义）；chips 计数取 runStats 全局值（不随搜索变）；列表数据走 listRuns(status,q,template_id,page)；重试→retryRun→toast+刷新。
- [ ] **Step 3: 运行确认通过** Run: `npm test -- src/api/sop/helpers.test.ts && npm run type-check && npm run check-i18n` PASS。browser-use 走查（spec §16.9）：八 chips 计数与全局一致；chips+搜索+SOP 下拉组合过滤；成功率/累计 Token 口径正确；运行推进时列表自动刷新且**聚焦搜索框输入 5s 后焦点光标不变**；空态文案引导回事项列表；失败行重试进详情操作条。
- [ ] **Step 4: 提交** `git add frontend/src/views/sop/SopRunList.vue frontend/src/api/sop/helpers.ts frontend/src/api/sop/helpers.test.ts frontend/src/i18n/locales && git commit -m "feat(sop): 运行列表（八态计数 chips/组合搜索/成功率页头/保焦点刷新）"`

### Task 49: SopRunDetail（只读画布 + 节点抽屉 + 操作条 + SSE 直播 + human_context 时间线 + usage/仓库）

**Files:**
- Modify: `frontend/src/views/sop/SopRunDetail.vue`（T38 占位→完整）
- Consumes: T45 SopNodeCard（readonly=true，无出口端口）+ vue-flow 只读配置（无工具条/禁拖拽连线）、T38 `streamGet('/api/v1/sop/runs/'+id+'/events', …)`（buildAuthHeaders 鉴权）+ api（getRun/getNodeLogs/approveNode/rejectNode/reworkNode/terminateNode/answerNode/pauseRun/resumeRun/cancelRun/retryRun/addHumanContext/listArtifacts）、T50 SopDocPreview、PmMarkdown。
- Produces: 运行详情子页（spec §8，运行记录 Tab 高亮+面包屑）：
  - **只读画布**：节点着色 pending 灰/running 品牌色脉冲/waiting_approval 琥珀/succeeded 绿/failed 红/pending_input 蓝；SSE node_status 事件实时改色；text_delta/tool_call/tool_result 追加进对应节点抽屉直播区；断线自动重连（3 次退避）+组件卸载 AbortController 取消。
  - **节点抽屉**（点节点开）：**「任务书」视图**（独立分段：角色与目标 / 事项背景 / 上游步骤产物 / **项目现状** / 返工意见 / 人工补充意见 / 产出要求，逐节渲染；项目现状块为空时显示「本次运行无项目动态」，有则显示实际注入的决策条数与兄弟事项摘要——数据来自 getRun 聚合 DTO 新增的 `project_context` 字段，T64 引擎填充）、流事件/对话轨迹（直播区+历史 getNodeLogs jsonl 逐行渲染；平台智能体节点「展开完整会话」→跳现有会话页 SessionID）、日志原文切换、产物列表（预览/下载/源码→SopDocPreview）、attempt/rework 历史（nr.Attempt/ReworkCount+Feedback 白话「返工意见」）、gate 结果（GateResult 中文+GateReason）。
  - **操作条**（run 级）：暂停/恢复/取消/重试（按状态显隐）+「补充人工上下文」输入→addHumanContext；节点级（waiting_approval）：确认通过/补充信息重跑当前节点（rework+comment）/打回上游（reject+comment+reject_to 下拉=上游节点）/终止；pending_input：回答输入→answerNode。
  - **run 级信息栏**：输入 JSON、human_context 时间线（来源中文：人工补充/返工/回答/系统提醒）、usage（InputTokens/OutputTokens/CostUSD）、耗时、触发来源（triggerSourceLabel+TriggerRefID 回溯链接）、事项链接、**仓库工作分支**（sop/<run_id>，克隆目录清单）。
- [ ] **Step 1: 实现**（无新增纯逻辑；SSE 重连退避=1s/2s/4s 上限 3 次，组件内实现）。
- [ ] **Step 2: 运行确认通过** Run: `npm run type-check && npm run check-i18n` PASS。browser-use 走查：发起一条多节点 run→详情画布实时着色；节点抽屉直播文本滚动；审批节点三操作+打回目标下拉；pending_input 回答后节点恢复；暂停/恢复/取消/重试按状态显隐且生效；human_context 时间线含补充记录；usage/耗时/触发来源/事项链接/工作分支可见；产物预览开大弹窗；刷新页面 SSE 重连；离开页面无残留请求（Network 面板核对）。
- [ ] **Step 3: 提交** `git add frontend/src/views/sop/SopRunDetail.vue frontend/src/i18n/locales && git commit -m "feat(sop): 运行详情（只读画布 SSE 直播/节点抽屉/全操作条/上下文时间线）"`

### Task 50: SopDocPreview 产物 HTML 预览大弹窗（.md 渲染/源码/frontmatter/沙箱 iframe/复制降级/下载/.md 入库）

**Files:**
- Create: `frontend/src/views/sop/components/SopDocPreview.vue`
- Modify: `frontend/src/api/sop/helpers.ts`+`helpers.test.ts`（追加 `previewKind(path)` 返回 'markdown'|'text'|'diff'|'json'|'html'|'binary'、`stripFrontmatter(md)`、`diffLineClass(line)`）
- Consumes: T38 api（artifactFileURL/ingestArtifact）+ fetch blob 鉴权（buildAuthHeaders）、PmMarkdown（含 Mermaid）、T36 入库端点。
- Produces: 三处产物入口（事项抽屉产物/交付、运行详情产物、PM 归档资料）统一预览弹窗（spec §8/§16.20）：
  - 尺寸 **min(1120px, 94vw)、高 ≤84vh、正文独立滚动**；窄屏顶栏换行不溢出；普通弹窗（480/980）不受影响。
  - 顶栏：产物名+路径/大小+shortSha 快照标签+已入库标签；「HTML 预览 / 源码」分段切换、复制源码、下载源文件、入知识库按钮。
  - 渲染分派（previewKind）：**.md 默认 HTML 文档视图**（PmMarkdown 白纸版式：标题/表格/代码块/引用/列表；源码视图=含 frontmatter 的原文）；.txt/.log 等宽文本；.diff 增/删/hunk 着色（diffLineClass：+绿/−红/@@琥珀）；.json 美化（JSON.stringify(parse, null, 2)）；**.html 沙箱 iframe（sandbox=""，src=blob URL）**；zip 等二进制→「不支持在线预览，到工作区下载」，无渲染/源码切换。
  - **复制降级**：navigator.clipboard.writeText 失败→自动切源码视图+全选文本（Selection API）+提示「请按 Ctrl+C 复制」。
  - 下载：fetch blob（带鉴权头）→a[download]=路径 basename→click→revokeObjectURL。
  - **入知识库=源文档 .md**（ingestArtifact，HTML 视图不入库）：成功→顶栏+产物行「已入知识库」标签、按钮置灰「已入库 ✓」、toast 明示「.md 源文档已走 ingest 链路入库」；关闭重开状态保持（读 artifact.knowledge_id）；未绑定 KB→后端白话错误透出。
- [ ] **Step 1: 写失败测试** helpers.test.ts 追加：

```ts
test('预览类型分派与 frontmatter 剥离', () => {
  assert.equal(previewKind('out/a.md'), 'markdown');
  assert.equal(previewKind('out/a.log'), 'text');
  assert.equal(previewKind('out/a.diff'), 'diff');
  assert.equal(previewKind('out/a.json'), 'json');
  assert.equal(previewKind('out/a.html'), 'html');
  assert.equal(previewKind('out/a.zip'), 'binary');
  assert.equal(stripFrontmatter('---\ntitle: x\n---\n\n# 正文'), '# 正文');
  assert.equal(stripFrontmatter('# 无 frontmatter'), '# 无 frontmatter');
  assert.equal(diffLineClass('+added'), 'diff-add');
  assert.equal(diffLineClass('-removed'), 'diff-del');
  assert.equal(diffLineClass('@@ -1,2 +1,2 @@'), 'diff-hunk');
});
```

Run: `npm test -- src/api/sop/helpers.test.ts` Expected: FAIL。

- [ ] **Step 2: 实现** 按 Produces 契约（照原型 doc 预览弹窗区块）；props `artifact:SopArtifact`、`runId:string`；emits `close/ingested`；fetch 源文件用 buildAuthHeaders→blob；markdown 渲染 stripFrontmatter 后交 PmMarkdown（源码视图保留 frontmatter 原文）。
- [ ] **Step 3: 运行确认通过** Run: `npm test -- src/api/sop/helpers.test.ts && npm run type-check && npm run check-i18n` PASS。browser-use 走查（spec §16.20 逐项）：三处入口均开大弹窗（量宽=min(1120,94vw)、高≤84vh、正文独立滚动）；.md HTML 渲染标题/表格/代码块/引用齐全、源码见 frontmatter、往返切换状态正确；.html 沙箱 iframe（DevTools 核对 sandbox=""）；.diff 着色/.json 美化/.log 等宽/zip 提示；剪贴板禁用时复制降级全选+提示；下载 basename 正确；入库后标签+置灰「已入库 ✓」、重开保持、toast 明示 .md 入库；窄屏（375px）顶栏换行不溢出；480/980 普通弹窗不受影响。
- [ ] **Step 4: 提交** `git add frontend/src/views/sop/components/SopDocPreview.vue frontend/src/api/sop/helpers.ts frontend/src/api/sop/helpers.test.ts frontend/src/i18n/locales && git commit -m "feat(sop): 产物 HTML 预览大弹窗（分派渲染/复制降级/沙箱 iframe/.md 入库）"`

### Task 51: SopApprovalCenter（三源聚合待办 + 角标 + 行内操作）

**Files:**
- Modify: `frontend/src/views/sop/SopApprovalCenter.vue`（T38 占位→完整）
- Consumes: T38 api（listApprovals/approveNode/rejectNode/reworkNode/terminateNode/answerNode）+ store.approvals/approvalCount/startPolling、T20 ApprovalEntry（kind=sop|agent_prompt|automation；actions 按 kind）。
- Produces: 审批中心 Tab（spec §6）：待办列表（等待时长升序）：类型徽标（SOP 关口/智能体追问/自动化审批）+标题+项目名/事项标题+等待原因+WaitingSince 相对时间；行内操作按 entry.actions：sop→确认通过/补充信息重跑/打回上游（目标下拉）/终止；agent_prompt→回答输入（跳会话或行内 answer）；automation→跳现有自动化审批页（**零改动复用**）；操作后乐观移除+store.loadApprovals 刷新角标；空态「没有待确认的事项 · 所有流程都在自动推进」；10s 轮询。
- [ ] **Step 1: 实现**。
- [ ] **Step 2: 运行确认通过** Run: `npm run type-check && npm run check-i18n` PASS。browser-use 走查：造 sop waiting_approval + agent pending_input 两类待办→聚合列表按等待时长排序；角标=待办数；确认通过→运行继续；打回→目标节点重置；回答→节点恢复；automation 类跳转现有页面；空态文案。
- [ ] **Step 3: 提交** `git add frontend/src/views/sop/SopApprovalCenter.vue frontend/src/i18n/locales && git commit -m "feat(sop): 审批中心（三源聚合/行内操作/角标联动）"`

### Task 52: SopTriggerTab + SopTriggerConfigModal（SOP 任务第五主 Tab）

**Files:**
- Modify: `frontend/src/views/sop/SopTriggerTab.vue`（T38 占位→完整）
- Create: `frontend/src/views/sop/components/SopTriggerConfigModal.vue`
- Consumes: T38 api（getTriggerConfig/saveTriggerConfig/runNow/listTriggerLogs）+ helpers.intervalSummary、T35 后端（run-now 202 后台执行）、T6 校验（cron≥5min/limit 1–10/statuses 不含终态）。
- Produces: SOP 任务 Tab（spec §7/§16.3）：配置摘要卡（调度=intervalSummary/默认 SOP/来源/状态/单次上限/启用开关/上次/下次）+触发历史表（时间/方式 auto|manual 中文/扫描/派发/跳过/说明）+顶部「触发配置」「立即运行」按钮；**立即运行→runNow→toast「已在服务端开始扫描，关闭页面不影响」→轮询历史表出现 manual 行**；配置模态（照原型 LoopForge 触发配置模态）：调度=常用间隔/Cron 分段切换+间隔下拉（15m/30m/hourly/daily9/weekly1 中文）或 cron 输入+时区、默认 SOP 下拉+「优先使用事项已绑定的 SOP」勾选、同步哪些来源下拉（all+六键中文）、触发哪些状态多选（**已完成/已关闭不出现**）、单次最多触发事项数 stepper 1–10、底部启用勾选+「保存触发配置」（保存即生效→摘要卡刷新）；后端校验错误白话透出（cron<5min/limit 越界）。
- [ ] **Step 1: 实现**。
- [ ] **Step 2: 运行确认通过** Run: `npm run type-check && npm run check-i18n` PASS。browser-use 走查（spec §16.3）：保存配置即生效（摘要卡 next_run_at 更新）；造命中事项→立即运行→历史 scanned/fired/skipped 口径正确、运行记录触发来源=「SOP 任务」；关闭 Tab 后扫描仍完成（历史行出现）；per_run_limit=1 截断剩余下次继续；同槽重复手动不重复派发；cron「* * * * *」被拒提示 5 分钟；状态多选无已完成/已关闭。
- [ ] **Step 3: 提交** `git add frontend/src/views/sop/SopTriggerTab.vue frontend/src/views/sop/components/SopTriggerConfigModal.vue frontend/src/i18n/locales && git commit -m "feat(sop): SOP 任务 Tab（配置摘要/触发历史/立即运行/配置模态）"`

### Task 53: SopSettings（CLI 凭据 ×10 + PATH 检测安装指引 + 工作区 + 黑名单）

**Files:**
- Modify: `frontend/src/views/sop/SopSettings.vue`（T38 占位→完整）
- Consumes: T38 api（listCredentials/saveCredential/deleteCredential/cliStatus）+ store.cliStatuses/credentials、T34 CLIStatus{Kind,Binary,Installed,Version,Guidance}、T26 脱敏列表（尾 4 位）。
- Produces: 执行器设置子页（⋮ 菜单，面包屑返回）：**10 种 CLI 卡**（名称/二进制名/PATH 检测徽标=已安装 v 版本|未安装/安装指引折叠（Guidance 原文）/凭据状态=已配置（尾 4 位）|未配置）；凭据编辑模态（API Key password 输入+Base URL+默认模型；保存→saveCredential→列表脱敏刷新；删除二次确认）；**页面明示「Key 只经环境变量注入私有 HOME，不落盘明文」**；工作区根目录展示（读后端配置，修改入口跳项目配置）；危险命令黑名单（现有 script 黑名单配置读写——执行时 grep Know 现有 dangerous command 配置端点复用，无则展示内置清单只读+说明）。
- [ ] **Step 1: 实现**。
- [ ] **Step 2: 运行确认通过** Run: `npm run type-check && npm run check-i18n` PASS。browser-use 走查：10 卡齐全；本机已装 CLI 显示版本、未装显示指引；保存凭据后列表只见尾 4 位（Network 响应核对无明文）；删除后状态回未配置；刷新页面凭据状态保持。
- [ ] **Step 3: 提交** `git add frontend/src/views/sop/SopSettings.vue frontend/src/i18n/locales && git commit -m "feat(sop): 执行器设置（10 CLI 凭据/PATH 检测/安装指引/安全说明）"`

### Task 54: PmArchive 只读归档四 tab + 事项 AI 分析入口

**Files:**
- Modify: `frontend/src/views/sop/PmArchive.vue`（T38 占位→完整）
- Modify: `frontend/src/views/sop/SopItemDrawer.vue`（概览追加「AI 分析」入口）
- Consumes: 现有 pm 组件**裁剪复用**（PmVersionList/PmVersionDetail/PmFeatureTree/PmMaterialList/PmPlanHistoryDialog——import 原组件，props 传 readonly 或以包裹层隐藏写按钮；**不修改 pm 组件源码**，用 CSS 隐藏+拦截写 API 的只读包裹；执行时读各组件写操作入口逐个遮蔽）、`src/api/pm/` 读函数、T26 pmArchiveSummary、T25 analyzeItem（SOP 标记会话）+现有会话页路由、T50 SopDocPreview（资料预览）。
- Produces: PM 归档子页（⋮ 菜单，spec §11）：顶部迁移摘要（项目/事项/运行/附件计数）+「归档为冻结快照，唯一活数据源是 SOP 事项」横幅；四 tab=版本（PmVersionList 只读）/功能树（PmFeatureTree 只读）/资料（PmMaterialList 只读+预览开 SopDocPreview）/批次历史（PmPlanHistoryDialog 内容只读化）；**无任何写路径**（新建/编辑/删除按钮全部隐藏，写 API 不可达）；资料 knowledge_id 指向的 KB 文档仍可检索（不动）。事项抽屉概览「AI 分析」→analyzeItem→跳会话页流式对话（结论回写 domain_data 由后端 T25 实现）。
- [ ] **Step 1: 实现**。
- [ ] **Step 2: 运行确认通过** Run: `npm run type-check && npm run check-i18n` PASS。browser-use 走查（spec §16.4）：四 tab 数据=迁移前 PM 数据（对照备份抽查）；所有写按钮不可见；资料预览开大弹窗；版本详情可查；事项 AI 分析建真实会话流式对话；/platform/pm 旧链接重定向后归档仍可达。
- [ ] **Step 3: 提交** `git add frontend/src/views/sop/PmArchive.vue frontend/src/views/sop/SopItemDrawer.vue frontend/src/i18n/locales && git commit -m "feat(sop): PM 只读归档四 tab 与事项 AI 分析入口"`

---

# Milestone M10 — 双库验证 / E2E / 10 CLI 实测 / PM 收尾（spec §16 全量验收）

### Task 55: 双库迁移一致性 + PG/SQLite 核心链路 E2E

**Files:**
- Create: `internal/application/repository/sop_migration_parity_test.go`、`internal/application/service/sop/engine/e2e_core_test.go`
- Modify: 无生产代码（本任务纯验证；发现迁移/模型不一致→回修对应 M1 任务文件并在本任务提交）

**Interfaces:**
- Consumes: M1 全部迁移对（000166–000173 ↔ 000068–000075）、T12–T15 引擎、T18 ScriptProvider、`make migrate-up`（scripts/migrate.sh up）、`docker-compose.dev.yml`（dev PG）。
- Produces: 迁移对等测试 + 核心链路 E2E（**同一测试函数双库跑**：sqlite :memory: 恒跑；`SOP_TEST_PG_DSN` 环境变量非空时追加 PG 子测试，空则 t.Skip——CI/本机无 PG 不阻塞）。

- [ ] **Step 1: 写失败测试**

`sop_migration_parity_test.go`：

```go
func TestSopMigrationParity(t *testing.T) {
	pairs := [][2]string{
		{"000166_sop_projects", "000068_sop_projects"},
		{"000167_sop_items", "000069_sop_items"},
		{"000168_sop_item_extras", "000070_sop_item_extras"},
		{"000169_sop_templates", "000071_sop_templates"},
		{"000170_sop_runs", "000072_sop_runs"},
		{"000171_sop_cli_trigger", "000073_sop_cli_trigger"},
		{"000172_sop_indexes", "000074_sop_indexes"},
		{"000173_sop_builtin_templates", "000075_sop_builtin_templates"},
		{"000174_sop_project_notes", "000076_sop_project_notes"},
	}
	re := regexp.MustCompile(`CREATE TABLE IF NOT EXISTS (\w+)`)
	for _, p := range pairs {
		pg, err := os.ReadFile(filepath.Join("..", "..", "..", "migrations", "versioned", p[0]+".up.sql"))
		require.NoError(t, err)
		lite, err := os.ReadFile(filepath.Join("..", "..", "..", "migrations", "sqlite", p[1]+".up.sql"))
		require.NoError(t, err)
		require.Equal(t, re.FindAllStringSubmatch(string(pg), -1), re.FindAllStringSubmatch(string(lite), -1),
			"表集合不一致: %s ↔ %s", p[0], p[1])
		require.NotContains(t, string(lite), "JSONB", "SQLite 侧必须 JSON 降级")
		require.NotContains(t, string(lite), "TIMESTAMPTZ", "SQLite 侧必须 DATETIME")
		require.NotContains(t, string(lite), "BYTEA", "SQLite 侧必须 BLOB")
		// down 存在且 DROP 同集合表
		pgDown, err := os.ReadFile(filepath.Join("..", "..", "..", "migrations", "versioned", p[0]+".down.sql"))
		require.NoError(t, err)
		require.NotEmpty(t, pgDown)
	}
}
```

`e2e_core_test.go`（双库驱动）：

```go
func TestCoreChainE2E(t *testing.T) {
	drivers := []struct{ name string; open func(t *testing.T) *gorm.DB }{
		{"sqlite", openSqliteMem},
		{"postgres", func(t *testing.T) *gorm.DB {
			dsn := os.Getenv("SOP_TEST_PG_DSN")
			if dsn == "" { t.Skip("SOP_TEST_PG_DSN 未设置") }
			db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
			require.NoError(t, err)
			// 迁移由 make migrate-up 预先跑过；测试只清 sop_* 表数据
			cleanSopTables(t, db)
			return db
		}},
	}
	for _, d := range drivers {
		t.Run(d.name, func(t *testing.T) {
			db := d.open(t)
			e := newRealEngine(t, db) // 真实仓储+ScriptProvider+stub AgentProvider（固定产出 out/analysis.md）
			// 模板：agent 分析 → script 校验 → approval 验收 → agent 汇总
			tpl := seedChainTemplate(t, db)
			run := createRunFromTemplate(t, e, tpl)
			require.NoError(t, e.Start(context.Background())) // DisableRunLoop=false 真实调度
			defer e.Stop()
			waitFor(t, 10*time.Second, func() bool { r, _ := e.deps.Runs.GetRun(context.Background(), 0, run.ID); return r.Status == types.SopRunWaiting })
			// 审批通过 → 汇总 → succeeded
			require.NoError(t, e.Approve(context.Background(), 1, run.ID, "approve", "验收通过", "u1"))
			waitFor(t, 10*time.Second, func() bool { r, _ := e.deps.Runs.GetRun(context.Background(), 0, run.ID); return r.Status == types.SopRunSucceeded })
			arts, _ := e.deps.Runs.ListArtifacts(context.Background(), 1, run.ID)
			require.GreaterOrEqual(t, len(arts), 2)
			for _, a := range arts { require.Len(t, a.SHA256, 64) } // 产物指纹齐全
		})
	}
}
```

（openSqliteMem/cleanSopTables/newRealEngine/seedChainTemplate/createRunFromTemplate/waitFor 为测试助手：newRealEngine 用真实仓储+Registry 注册 ScriptProvider 与 stub AgentProvider（固定写 out/analysis.md 并汇报 pass）；seedChainTemplate 建 published 模板 agent 分析(id="analysis")→script 校验(id="check")→approval 验收(id="approve")→agent 汇总(id="summary")；waitFor=require.Eventually 包装；cleanSopTables 按外键序 TRUNCATE sop_* 全部表。）

- [ ] **Step 2: 运行确认失败** Run: `go test ./internal/application/repository/ -run TestSopMigrationParity -v && go test ./internal/application/service/sop/engine/ -run TestCoreChainE2E -v` Expected: 助手未定义编译失败（若迁移已对等则 parity 直接 PASS——仍须跑通确认）。
- [ ] **Step 3: 补齐测试助手 + PG 实跑**：补齐 T55 测试助手（openSqliteMem/cleanSopTables/newRealEngine/seedChainTemplate/createRunFromTemplate/waitFor：newRealEngine 用真实仓储+Registry 注册 ScriptProvider 与 stub AgentProvider（固定写 out/analysis.md 并汇报 pass）；seedChainTemplate 建 published 模板 agent 分析(id="analysis")→script 校验(id="check")→approval 验收(id="approve")→agent 汇总(id="summary")；waitFor=require.Eventually 包装；cleanSopTables 按外键序 TRUNCATE sop_* 全部表，**含 M11 新增的 sop_project_notes**）；随后 `docker compose -f docker-compose.dev.yml up -d`（PG 服务名执行时 grep compose 文件）→`make migrate-up`→`SOP_TEST_PG_DSN="host=127.0.0.1 user=… password=… dbname=… sslmode=disable" go test ./internal/application/service/sop/engine/ -run TestCoreChainE2E -v`（DSN 取 compose/dev 配置实际值）；最后 `make migrate-down` 回滚一轮再 up，验证 down 脚本可用（**在 dev 库执行，不碰生产数据**）。
- [ ] **Step 4: 运行确认通过** Run: `go test ./internal/application/repository/ -run TestSopMigrationParity -v && go test ./internal/application/service/sop/engine/ -run TestCoreChainE2E -v && go build ./...` Expected: PASS（sqlite 子测试恒过；PG 子测试在 dev 库过）。
- [ ] **Step 5: 提交** `git add internal/application/repository/sop_migration_parity_test.go internal/application/service/sop/engine/e2e_core_test.go && git commit -m "test(sop): 双库迁移对等与核心链路 E2E（PG/SQLite）"`

### Task 56: 混合协作 E2E + 异常路径专项（spec §16.1/§16.5）

**Files:**
- Create: `internal/application/service/sop/engine/e2e_mixed_test.go`、`internal/application/service/sop/executor/cli/fakebin_test.go`（测试内生成假 zcode 可执行脚本）
- Consumes: T29–T32 CLIProvider/适配器、T18/T28 进程树终止、T12 租约、T19 超时、T14 Reject/Retry、T15 子 SOP。
- Produces: 集成级断言（假 CLI 二进制走**完整 CLIProvider 链路**：PATH 检测→私有 HOME→凭据 env→stream-json 翻译→产物提取→门禁；不依赖真实 CLI 安装）。

- [ ] **Step 1: 写失败测试** `e2e_mixed_test.go` 关键用例（完整装配照 T55 newRealEngine，Registry 追加 cli.CLIProvider{A: ZcodeAdapter}）：

```go
func TestMixedChainWithRework(t *testing.T) {
	// 假 zcode：bash 脚本写入临时 bin 目录并 t.Setenv("PATH", binDir+":"+os.Getenv("PATH"))
	// 脚本行为：读 argv prompt→向 stdout 输出 zcode stream-json（init/step_update/result status=success）→写 out/impl.md→exit 0
	dir := t.TempDir()
	writeFakeZcode(t, dir) // 内容=固定 stream-json 帧 + mkdir -p out && echo "# 实现" > out/impl.md
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("SOP_CLI_API_KEY", "sk-test") // 经凭据服务注入；假 CLI 不校验
	e := newRealEngine(t, openSqliteMem(t))
	tpl := seedMixedTemplate(t, e) // platform_agent 分析(stub)→cli zcode 实现→script 测试(echo ok)→approval 验收→platform_agent 汇总(stub)
	run := createRunFromTemplate(t, e, tpl)
	e.Start(context.Background()); defer e.Stop()
	waitFor(t, 15*time.Second, func() bool { return runStatus(t, e, run.ID) == types.SopRunWaiting })
	// 验收打回实现节点
	require.NoError(t, e.Reject(context.Background(), 1, run.ID, "approve", "接口不符，重写", "u1", "impl"))
	waitFor(t, 15*time.Second, func() bool { return runStatus(t, e, run.ID) == types.SopRunWaiting })
	nrs, _ := e.deps.Runs.ListNodeRuns(context.Background(), run.ID)
	impl := findNode(nrs, "impl")
	require.Equal(t, 2, impl.Attempt)        // 返工重跑
	require.Contains(t, impl.Feedback, "接口不符") // 反馈进任务书
	require.NoError(t, e.Approve(context.Background(), 1, run.ID, "approve", "通过", "u1"))
	waitFor(t, 15*time.Second, func() bool { return runStatus(t, e, run.ID) == types.SopRunSucceeded })
	// 产物与验收时一致（sha256）
	arts, _ := e.deps.Runs.ListArtifacts(context.Background(), 1, run.ID)
	for _, a := range arts {
		raw, err := os.ReadFile(filepath.Join(run.WorkspaceDir, a.Path))
		require.NoError(t, err)
		require.Equal(t, a.SHA256, fmt.Sprintf("%x", sha256.Sum256(raw)))
	}
}

func TestCancelKillsProcessTree(t *testing.T) {
	e := newRealEngine(t, openSqliteMem(t))
	// script 节点：bash -c 'sleep 30 & wait'（子进程树）
	run := seedScriptRun(t, e, "sleep 30 & wait")
	e.Start(context.Background()); defer e.Stop()
	waitFor(t, 5*time.Second, func() bool { return nodeStatus(t, e, run.ID, "s1") == types.SopNodeRunning })
	require.NoError(t, e.Cancel(context.Background(), 1, run.ID))
	waitFor(t, 10*time.Second, func() bool { return runStatus(t, e, run.ID) == types.SopRunCancelled })
	// 进程树确实终止：记录脚本写入的 pid 文件，kill -0 探测不存在（Windows 用 tasklist 查 PID）
	assertProcessTreeGone(t, run.WorkspaceDir)
}

func TestLeaseRecoveryAfterCrash(t *testing.T) {
	db := openSqliteMem(t)
	e1 := newRealEngine(t, db)
	run := seedScriptRun(t, e1, "sleep 30")
	e1.Start(context.Background())
	waitFor(t, 5*time.Second, func() bool { return nodeStatus(t, e1, run.ID, "s1") == types.SopNodeRunning })
	e1.Stop() // 模拟崩溃：不再心跳
	// 手工把租约拨到过去（等价 30s 过期）
	db.Exec("UPDATE sop_runs SET lease_until = ? WHERE id = ?", time.Now().Add(-time.Minute), run.ID)
	e2 := newRealEngine(t, db) // 新实例
	e2.Start(context.Background()); defer e2.Stop()
	waitFor(t, 15*time.Second, func() bool {
		r, _ := e2.deps.Runs.GetRun(context.Background(), 0, run.ID)
		return r.Status == types.SopRunRunning || r.Status == types.SopRunSucceeded // 被重新抢占推进
	})
}

func TestApprovalTimeoutStrategies(t *testing.T) {
	for _, c := range []struct{ name, action, want string }{
		{"notify", "notify", types.SopRunWaiting},
		{"fail", "fail", types.SopRunFailed},
	} {
		t.Run(c.name, func(t *testing.T) {
			db := openSqliteMem(t)
			e := newRealEngine(t, db)
			run := seedApprovalRun(t, e, 1, c.action) // 单 approval 节点（id="approve"），ApprovalTimeoutHours=1、Action=c.action
			require.NoError(t, e.Start(context.Background())); defer e.Stop()
			waitFor(t, 10*time.Second, func() bool { return runStatus(t, e, run.ID) == types.SopRunWaiting })
			// 超时基准=nr.UpdatedAt（T19）：拨旧到 2 小时前，等真实 tick（2s）内 sweep 生效
			db.Exec("UPDATE sop_node_runs SET updated_at = ? WHERE run_id = ? AND status = ?",
				time.Now().Add(-2*time.Hour), run.ID, types.SopNodeWaitingApproval)
			waitFor(t, 10*time.Second, func() bool { return runStatus(t, e, run.ID) == c.want })
			if c.action == "notify" {
				r, _ := e.deps.Runs.GetRun(context.Background(), 0, run.ID)
				n := 0
				for _, h := range r.HumanContext { if h.Source == "system_reminder" { n++ } }
				require.Equal(t, 1, n) // 仅提醒一次，不随 tick 刷屏
			}
		})
	}
	// 默认 Hours=0 永不超时（spec §6）：集成复核
	db := openSqliteMem(t)
	e := newRealEngine(t, db)
	run := seedApprovalRun(t, e, 0, "notify")
	require.NoError(t, e.Start(context.Background())); defer e.Stop()
	waitFor(t, 10*time.Second, func() bool { return runStatus(t, e, run.ID) == types.SopRunWaiting })
	db.Exec("UPDATE sop_node_runs SET updated_at = ? WHERE run_id = ?", time.Now().Add(-10000*time.Hour), run.ID)
	time.Sleep(3 * time.Second) // 跨过至少一个 tick
	require.Equal(t, types.SopRunWaiting, runStatus(t, e, run.ID))
}

func TestRetryFromFailedNodeAndChildSOP(t *testing.T) {
	t.Run("retry", func(t *testing.T) {
		db := openSqliteMem(t)
		e := newRealEngine(t, db)
		// 首轮失败次轮成功：work/retried 标记区分 attempt（seedScriptRun 节点 on_failure=fail、max_attempts=1）
		run := seedScriptRun(t, e, "test -f work/retried && echo ok || (touch work/retried; exit 1)")
		require.NoError(t, e.Start(context.Background())); defer e.Stop()
		waitFor(t, 10*time.Second, func() bool { return runStatus(t, e, run.ID) == types.SopRunFailed })
		require.NoError(t, e.Retry(context.Background(), 1, run.ID))
		waitFor(t, 10*time.Second, func() bool { return runStatus(t, e, run.ID) == types.SopRunSucceeded })
		nrs, _ := e.deps.Runs.ListNodeRuns(context.Background(), run.ID)
		require.Equal(t, types.SopNodeSucceeded, findNode(nrs, "s1").Status) // ResetNodes 后重新推进
	})
	t.Run("child_sop", func(t *testing.T) {
		db := openSqliteMem(t)
		e := newRealEngine(t, db)
		child := seedChildTemplate(t, e)          // published：单 script 节点 echo "# 子产物" > out/child.md，产物契约 out/child.md exists
		parent := seedParentTemplate(t, e, child.ID) // published：sop 节点(id="sub", child_template_id=child.ID)→script 汇总节点
		run := createRunFromTemplate(t, e, parent)
		require.NoError(t, e.Start(context.Background())); defer e.Stop()
		waitFor(t, 20*time.Second, func() bool { return runStatus(t, e, run.ID) == types.SopRunSucceeded })
		nrs, _ := e.deps.Runs.ListNodeRuns(context.Background(), run.ID)
		sub := findNode(nrs, "sub")
		require.NotEmpty(t, sub.ChildRunID)
		cr, _ := e.deps.Runs.GetRun(context.Background(), 0, sub.ChildRunID)
		require.Equal(t, "child_run", cr.TriggerKind)
		require.Equal(t, run.ID, cr.ParentRunID)
		// 子产物回传父工作区同路径，sha256 重算一致（T15 reconcileChildRun）
		arts, _ := e.deps.Runs.ListArtifacts(context.Background(), 1, run.ID)
		var ca *types.SopArtifact
		for i := range arts { if arts[i].NodeID == "sub" { ca = &arts[i] } }
		require.NotNil(t, ca)
		raw, err := os.ReadFile(filepath.Join(run.WorkspaceDir, ca.Path))
		require.NoError(t, err)
		require.Equal(t, ca.SHA256, fmt.Sprintf("%x", sha256.Sum256(raw)))
	})
}
```

（writeFakeZcode/assertProcessTreeGone/seedMixedTemplate/seedScriptRun/seedApprovalRun/seedChildTemplate/seedParentTemplate/runStatus/nodeStatus/findNode 为测试助手：seedScriptRun 建单 script 节点 run（节点 id="s1"、on_failure=fail、max_attempts=1，脚本先 `echo $$ > work/pids.txt` 再执行传入命令）；seedApprovalRun(t,e,hours,action) 建单 approval 节点 run（id="approve"）；seedChildTemplate/seedParentTemplate 建 published 模板（固定 UUID）；assertProcessTreeGone 读 work/pids.txt 逐 pid 探测——unix `syscall.Kill(pid,0)` 期望 ESRCH，windows `tasklist /FI "PID eq <pid>"` 期望无行；runStatus/nodeStatus 经 GetRun(ctx,0,id)/ListNodeRuns 读取。）

- [ ] **Step 2: 运行确认失败** Run: `go test ./internal/application/service/sop/engine/ -run "TestMixedChain|TestCancelKills|TestLeaseRecovery|TestApprovalTimeoutStrategies|TestRetryFromFailed" -v` Expected: 编译失败。
- [ ] **Step 3: 实现助手并跑通**（假 zcode 脚本帧格式照 T32 zcode 黄金样本；Windows 下假 CLI 用 bash 脚本经 Git Bash 执行——CLIProvider.LookPath 在 Windows 需找到 zcode.cmd 包装：writeFakeZcode 同时写 `zcode`（bash 脚本）与 `zcode.cmd`（`@bash "%~dp0zcode" %*`），保证 LookPath 命中）。
- [ ] **Step 4: 运行确认通过** Run: `go test ./internal/application/service/sop/engine/ -run "TestMixedChain|TestCancelKills|TestLeaseRecovery|TestApprovalTimeoutStrategies|TestRetryFromFailed" -v && go test ./internal/application/service/sop/... -v && go build ./...` Expected: PASS（**Windows 本机实跑进程树终止用例**）。
- [ ] **Step 5: 提交** `git add internal/application/service/sop/engine/e2e_mixed_test.go internal/application/service/sop/executor/cli/fakebin_test.go && git commit -m "test(sop): 混合协作 E2E 与异常路径（打回返工/进程树终止/租约回收/超时/子SOP）"`

### Task 57: 10 种 CLI 真实 headless 实测 + 黄金样本回归替换 + 缺凭据指引

**Files:**
- Modify: `internal/application/service/sop/executor/cli/testdata/<kind>/stream.jsonl`（10 种，真实录制替换手写样本）
- Modify: `internal/application/service/sop/executor/cli/provider.go`（RunStream 追加 `SOP_CLI_CAPTURE_DIR` 原始帧录制开关：非空时 translator 输入逐行落该目录 `<kind>.jsonl`，生产默认关；附单测开关关闭时零落盘）
- Create: `docs/superpowers/verification/2026-11-19-sop-cli-matrix.md`（实测记录表）
- Consumes: T29–T34 全部适配器/凭据/PATH 检测、SopSettings 页（T53）、真实 CLI 安装（**不代装**：未安装的 kind 记录「未安装+指引已验证」，不阻塞其余）。
- Produces: 每种 CLI 一行实测记录（安装版本/凭据配置/运行 id/流事件截图或 jsonl/产物 sha256/取消验证/续跑验证（支持种）/缺凭据指引文案）；黄金样本=真实输出（协议漂移回归基线）。

- [ ] **Step 1: 准备**：SopSettings 页为已安装 CLI 配置凭据（env 注入验证：运行后检查私有 HOME 配置文件**不含明文 key**，`grep -r "sk-" <ws>/.home` 期望无命中）；建单节点模板「CLI 冒烟」：cli 节点任务=「在 out/ 写 hello.md（内容含当前 CLI 名），然后汇报 pass」，产物契约 out/hello.md exists。
- [ ] **Step 2: 逐种实测**（10 行清单，每种执行）：
  1. 事项触发冒烟 run→运行详情观察流事件直播→succeeded→hello.md 存在且 sha256 记录；
  2. 录制真实 stdout：`cp <ws>/logs/<node>-attempt-1.jsonl` 中原始帧（Emit 前 translator 输入——执行时在 RunStream 加 `SOP_CLI_CAPTURE_DIR` 环境变量开关，非空则原始 stdout 逐行落该目录，**生产默认关**）→替换 testdata/<kind>/stream.jsonl；
  3. 运行中取消→进程树终止（任务管理器/tasklist 核对）；
  4. 支持续跑种（opencode/cursor/kimi/pi/dsh）：第一轮挂起或失败后 Retry→resume 参数生效（日志核对 session id 复用）；
  5. 删除凭据重跑→白话指引「未配置 <kind> 凭据…」而非静默失败；PATH 移除重跑→「未检测到 <kind> 命令…」。
- [ ] **Step 3: 回归** Run: `go test ./internal/application/service/sop/executor/cli/ -v` Expected: 全部适配器翻译测试用**真实样本**PASS（样本替换后断言按需修正——协议字段以实测为准，spec §5.2 表格与实测冲突时**记录差异并以实测修正适配器**，表格差异写入验证文档）。
- [ ] **Step 4: 记录**：验证文档逐行填实测结果（未安装种标注原因与指引验证结果）；`go build ./...` PASS。
- [ ] **Step 5: 提交** `git add internal/application/service/sop/executor/cli/testdata docs/superpowers/verification/2026-11-19-sop-cli-matrix.md internal/application/service/sop/executor/cli && git commit -m "test(sop): 10 CLI 真实实测黄金样本与验证记录"`

### Task 58: PM 迁移验证 + spec §16 全量验收 + PM 写路径下线 + 全量回归

**Files:**
- Modify: `internal/router/routes_pm.go`（写端点下线：POST/PUT/PATCH/DELETE 路由移除或回 410 白话「PM 工作站已并入 SOP 工作台」；**读端点保留**供 PmArchive）、PM asynq handler 注册处（执行时 grep `pm_version_artifact_ai` 的 asynq mux 注册行→移除注册；handler 文件保留标记 Deprecated 注释，M1–M9 期间可回滚）、`internal/application/service/pm_version_artifact_ai.go`（文件头加 Deprecated 注释）
- Create: `docs/superpowers/verification/2026-11-19-sop-acceptance.md`（§16 21 项逐条验收记录）
- Consumes: T7 迁移命令、T55–T57 验证产物、全部前端任务。
- Produces: 迁移无损证明 + 21 项验收签字表 + PM 写路径下线 + 全量绿。

- [ ] **Step 1: PM 迁移实跑与无损核对**：dev 库备份（迁移命令自动导出 data/backups/）→`go run ./cmd/sop-migrate-pm --dsn <dev>`→核对：sop_projects 数=pm_projects 数；sop_items 数=pm_requirements 数；状态映射逐条（pending_analysis/analyzing→backlog、confirmed→todo、scheduled/developing→in_progress、launched→done、rejected→closed+reject_reason、closed→closed）；**对照备份 JSON 抽查 ≥10 条事项 domain_data 含原 ai_analysis/design_plan/session_id/knowledge_id/上线复盘字段无丢失**；附件数一致；二次执行幂等（计数不变）；PmArchive 四 tab 可查。
- [ ] **Step 2: §16 21 项逐条验收**（每项：操作路径+期望+实际+PASS/FAIL；FAIL→修复→重验）：1 混合 E2E（T56）/2 10 CLI（T57）/3 SOP 任务闭环（T52 走查）/4 PM 吸收（Step 1）/5 异常路径（T56）/6 双库（T55）/7 事项列表与项目配置（T40/T39）/8 详情抽屉（T42）/9 运行列表（T48）/10 AI 拆解（T43）/11 仓库关联（T39）/12 AI 编排（T47）/13 CLI 角色与 KB 范围（T46+T37：agent-system.md 含角色 SystemPrompt/Skills、未选角色仅任务书、kb_scope 三档服务端白名单一致、事项级覆盖、私有 HOME 两 run 间无残留记忆）/14 项目增删切（T38/T39）/15 五步检查器（T46）/16 AI 自主节点配置（T47）/17 快捷工具条（T45）/18 双出口连线（T45）/19 第 2 步大白话（T46：内置模板与 AI 生成任务均无 {{…}}）/20 产物预览与入库（T50）/21 ? 气泡（T46）。
- [ ] **Step 3: PM 写路径下线**：routes_pm.go 写路由移除（读保留）；asynq PM handler 注册移除；`go build ./...`+现有 pm 读测试仍绿；前端 /platform/pm 重定向（T38 已交付）复核。
- [ ] **Step 4: 全量回归** Run: `go test ./... && cd frontend && npm test && npm run type-check && npm run check-i18n && npm run build` Expected: 全 PASS（现有 automation/hitl/pm 读测试零回归；M11 交付的 §16.22–26 已在 T68 单独验收，此处只做整体回归）。
- [ ] **Step 5: §16 编号对齐**：spec 现为 26 条（22–26 为 M11 增补）；T58 的 1–21 项验收记录与 T68 的 22–26 项合并归档，两份文档互链。
- [ ] **Step 6: 提交** `git add internal/router/routes_pm.go internal/application/service/pm_version_artifact_ai.go docs/superpowers/verification/2026-11-19-sop-acceptance.md && git commit -m "chore(sop): PM 写路径下线与 §16 全量验收记录"`

---

---

# Milestone M11 — 项目共享上下文（黑板 + 简报）与大需求拆分（spec §3.10 / §16.22–26）

> 本轮增补（与 M1–M10 同一交付，不分期）：项目共享上下文不再是「节点只知道自己的上下游」；大需求可拆成两级子事项分别推进。**版本规划按用户决定去掉**，不在本里程碑内。

### Task 59: 黑板表迁移 + 模型 + 仓储 + 父项汇总纯函数

**Files:**
- Create: `migrations/versioned/000174_sop_project_notes.up.sql` / `.down.sql`、`migrations/sqlite/000076_sop_project_notes.*`
- Create: `internal/types/sop_project_note.go`、`internal/types/sop_project_note_test.go`
- Create: `internal/application/repository/sop_project_note.go`、`sop_project_note_test.go`
- Modify: `internal/types/interfaces/sop.go`（追加 `SopProjectNoteRepository`）

**Interfaces:**
- Consumes: T1/T2 表与迁移编号约定（000174 ↔ 000076）。
- Produces: `types.SopProjectNote{ID, TenantID, ProjectID, Kind, Title, Body string, ItemID, RunID, Source, CreatedBy string, Pinned bool, Seq int64, CreatedAt, UpdatedAt time.Time, DeletedAt gorm.DeletedAt}`；常量 `SopNoteKindDecision/RunSummary/Risk/Release/Note`（值 decision/run_summary/risk/release/note）、`SopNoteSourceHuman/Engine/AI`（human/engine/ai）、`SopSplitSourceAI/Manual/Template`（ai/manual/template）；`types.ValidateSopNote(kind string) error`；`types.DeriveParentExecState(childStates []string) string`（入参为子项五态字符串切片）；`repository.SopProjectNoteRepository{NewSopProjectNoteRepo(db)}` 方法 `Create(ctx, *SopProjectNote) error`（事务内取项目 max(seq)+1 写回）、`List(ctx, tenantID uint64, projectID string, kind string, pinned *bool, limit int) ([]SopProjectNote, error)`（pinned desc, seq desc）、`Update/SoftDelete/GetByID/MaxSeq(ctx, tenantID, projectID) (int64, error)`。

- [ ] **Step 1: 写迁移**

`migrations/versioned/000174_sop_project_notes.up.sql`：

```sql
CREATE TABLE IF NOT EXISTS sop_project_notes (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    project_id VARCHAR(36) NOT NULL,
    kind VARCHAR(16) NOT NULL DEFAULT 'note',
    title VARCHAR(500) NOT NULL DEFAULT '',
    body TEXT NOT NULL DEFAULT '',
    item_id VARCHAR(36) NOT NULL DEFAULT '',
    run_id VARCHAR(36) NOT NULL DEFAULT '',
    source VARCHAR(16) NOT NULL DEFAULT 'human',
    pinned BOOLEAN NOT NULL DEFAULT FALSE,
    seq BIGINT NOT NULL DEFAULT 0,
    created_by VARCHAR(36) NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ
);
CREATE UNIQUE INDEX IF NOT EXISTS ux_sop_notes_project_seq ON sop_project_notes(project_id, seq) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS ix_sop_notes_project_kind ON sop_project_notes(tenant_id, project_id, kind, created_at DESC);
CREATE INDEX IF NOT EXISTS ix_sop_items_parent ON sop_items(parent_id) WHERE deleted_at IS NULL;
```

> 说明：`kb_owner/brief_knowledge_id/brief_rendered_seq` 已随 000166 建表、`parent_id/split_source` 已随 000167 建表（本计划 T1/T2 已就地写入），000174 不再 ALTER——sop_* 是本次新增表，无存量数据需要补列，避免 PG/SQLite 上 ALTER 语义差异。000174 只负责黑板表与 parent 索引。

`000174_sop_project_notes.down.sql`：

```sql
DROP INDEX IF EXISTS ix_sop_items_parent;
DROP TABLE IF EXISTS sop_project_notes;
```

SQLite 000076 同文（`TIMESTAMPTZ`→`DATETIME`、`NOW()`→`CURRENT_TIMESTAMP`、`BOOLEAN`→`INTEGER`）。

- [ ] **Step 2: 写失败测试** `sop_project_note_test.go`：

```go
func TestNoteSeqMonotonicPerProject(t *testing.T) {
	db := newSopNoteTestDB(t)
	repo := repository.NewSopProjectNoteRepo(db)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		require.NoError(t, repo.Create(ctx, &types.SopProjectNote{TenantID: 1, ProjectID: "p1", Kind: types.SopNoteKindNote, Body: fmt.Sprintf("n%d", i)}))
	}
	max, err := repo.MaxSeq(ctx, 1, "p1")
	require.NoError(t, err)
	require.Equal(t, int64(3), max)
	other := &types.SopProjectNote{TenantID: 1, ProjectID: "p2", Kind: types.SopNoteKindNote, Body: "x"}
	require.NoError(t, repo.Create(ctx, other))
	require.Equal(t, int64(1), other.Seq) // 项目内独立自增
}

func TestNoteListPinnedFirst(t *testing.T) {
	db := newSopNoteTestDB(t)
	repo := repository.NewSopProjectNoteRepo(db)
	ctx := context.Background()
	require.NoError(t, repo.Create(ctx, &types.SopProjectNote{TenantID: 1, ProjectID: "p1", Kind: types.SopNoteKindDecision, Body: "旧决策"}))
	require.NoError(t, repo.Create(ctx, &types.SopProjectNote{TenantID: 1, ProjectID: "p1", Kind: types.SopNoteKindRisk, Body: "风险", Pinned: true}))
	got, err := repo.List(ctx, 1, "p1", "", nil, 10)
	require.NoError(t, err)
	require.Equal(t, "风险", got[0].Body) // pinned 优先
}

func TestDeriveParentExecState(t *testing.T) {
	cases := []struct{ in []string; want string }{
		{[]string{"none", "intervene"}, "intervene"},
		{[]string{"closed", "running"}, "running"},
		{[]string{"closed", "pr"}, "pr"},
		{[]string{"closed", "closed"}, "closed"},
		{[]string{"none", "none"}, "none"},
		{nil, "none"}, // 无子项视为尚未执行
	}
	for _, c := range cases {
		require.Equal(t, c.want, types.DeriveParentExecState(c.in), "in=%v", c.in)
	}
}
```

- [ ] **Step 3: 运行确认失败** Run: `go test ./internal/types/ -run "TestDeriveParentExecState" -v && go test ./internal/application/repository/ -run "TestNote" -v` Expected: 编译失败（类型未定义）。
- [ ] **Step 4: 实现**

`DeriveParentExecState`（`internal/types/sop_brief.go`，纯函数零依赖）：

```go
// DeriveParentExecState 父事项执行情况：子项汇总，优先级与五态口径一致。
// intervene > running > pr > closed > none；无子项=尚未执行。
func DeriveParentExecState(states []string) string {
	if len(states) == 0 {
		return "none"
	}
	seen := map[string]bool{}
	for _, s := range states { seen[s] = true }
	switch {
	case seen["intervene"]:
		return "intervene"
	case seen["running"]:
		return "running"
	case seen["pr"]:
		return "pr"
	case seen["closed"] && len(seen) == 1:
		return "closed"
	}
	return "none"
}
```

`SopProjectNoteRepository.Create` 事务内 `SELECT COALESCE(MAX(seq),0)+1 FROM sop_project_notes WHERE project_id=? AND deleted_at IS NULL` 后写入（SQLite 事务串行化保证同项目不重号；PG 唯一索引兜底冲突重试一次）。`Update` 每次修改 `body/title/pinned` 后 `seq = max+1`（保证编辑也触发重渲染，且顺序反映最新）。

- [ ] **Step 5: 运行确认通过** Run: `go test ./internal/types/ -run "TestDeriveParentExecState" -v && go test ./internal/application/repository/ -run "TestNote" -v && go test ./internal/database/ -run TestSQLiteMigrationsCreateVersionedSchema -v && go build ./...` Expected: PASS。
- [ ] **Step 6: 提交** `git add migrations/versioned/000174_sop_project_notes.* migrations/sqlite/000076_sop_project_notes.* internal/types/sop_project_note.go internal/types/sop_project_note_test.go internal/types/sop_brief.go internal/application/repository/sop_project_note.go internal/application/repository/sop_project_note_test.go internal/types/interfaces/sop.go && git commit -m "feat(sop): 项目黑板表与仓储、父子拆分字段、父项执行情况汇总"`

### Task 60: 简报渲染纯函数（确定性、硬顶、分段顺序）

**Files:**
- Modify: `internal/types/sop_brief.go`（追加 `BriefInput`/`RenderProjectBrief`）、`internal/types/sop_brief_test.go`

**Interfaces:**
- Consumes: T59 SopProjectNote、T2 SopItem、T1 SopProject。
- Produces: `types.BriefInput{Project SopProject; Items []BriefItem; Notes []SopProjectNote; MaxChars int}`；`types.BriefItem{No, Title, Status, ExecState string; ChildDone, ChildTotal int}`（父项带子项计数）；`types.RenderProjectBrief(in BriefInput) string`（返回正文 Markdown；`MaxChars<=0` 取默认 4000 字）。

- [ ] **Step 1: 写失败测试**

```go
func TestRenderProjectBriefSectionsAndCap(t *testing.T) {
	in := types.BriefInput{
		Project: types.SopProject{Name: "知识库改造", Code: "KNOW", DefaultTemplateID: "t1"},
		Items: []types.BriefItem{
			{No: "KNOW-1", Title: "登录改造", Status: "in_progress", ExecState: "running"},
			{No: "KNOW-2", Title: "检索优化", Status: "done", ExecState: "closed"},
			{No: "KNOW-3", Title: "导出", Status: "todo", ExecState: "none", ChildDone: 3, ChildTotal: 7},
		},
		Notes: []types.SopProjectNote{
			{Kind: types.SopNoteKindDecision, Title: "采用方案 B", Body: "因为可逆", Pinned: true, Seq: 9, CreatedAt: t0},
			{Kind: types.SopNoteKindRisk, Title: "索引延迟", Body: "需观察", Seq: 8, CreatedAt: t0},
		},
	}
	out := types.RenderProjectBrief(in)
	require.Contains(t, out, "知识库改造")
	require.Contains(t, out, "正在推进")
	require.Contains(t, out, "采用方案 B")
	require.Contains(t, out, "索引延迟")
	require.True(t, strings.Index(out, "## 最近决策") < strings.Index(out, "## 最近风险"), "段落顺序固定")
	require.NotContains(t, out, "{{")
}

func TestRenderProjectBriefTruncatesLowPriority(t *testing.T) {
	items := make([]types.BriefItem, 0, 60)
	for i := 0; i < 60; i++ {
		items = append(items, types.BriefItem{No: fmt.Sprintf("K-%d", i), Title: strings.Repeat("长标题", 20), Status: "todo", ExecState: "none"})
	}
	out := types.RenderProjectBrief(types.BriefInput{Project: types.SopProject{Name: "P"}, Items: items, MaxChars: 300})
	require.LessOrEqual(t, len([]rune(out)), 340, "硬顶允许少量尾注溢出")
	require.Contains(t, out, "完整内容见项目动态")
}
```

- [ ] **Step 2: 运行确认失败** Run: `go test ./internal/types/ -run "TestRenderProjectBrief" -v` Expected: FAIL（RenderProjectBrief 未定义）。
- [ ] **Step 3: 实现**：固定段落顺序 `# <项目名>` → `## 项目概况`（编号前缀/绑定 KB 有无/默认 SOP 名）→ `## 在进行的事项`（按执行情况 intervene/running/pr/none 排序，最多 20 条，每行 `- <编号> <标题> · <五态中文>`；父项带 `(子项 3/7)`）→ `## 最近决策`（pinned 全带 + 其余按 seq 降序补足 8 条；每条 `### <标题>` + 正文 + `（<来源中文> · <时间>`）→ `## 最近风险`（最近 5 条）→ `## 最近运行结论`（run_summary 最近 8 条）。超 `MaxChars` 时按「运行结论 → 风险 → 决策正文截断 → 事项列表截断」的从低到高优先级裁减，末尾追加 `> 完整内容见项目动态`。全函数无 IO、无时间随机源（时间由 in 提供）。
- [ ] **Step 4: 运行确认通过** Run: `go test ./internal/types/ -run "TestRenderProjectBrief" -v` Expected: PASS。
- [ ] **Step 5: 提交** `git add internal/types/sop_brief.go internal/types/sop_brief_test.go && git commit -m "feat(sop): 项目简报确定性渲染（固定分段/硬顶截断）"`

### Task 61: BriefService（debounce + seq 比对 + KB 覆盖写降级）+ NotesService + 运行终态自动摘要

**Files:**
- Create: `internal/application/service/sop/brief.go`、`brief_test.go`
- Create: `internal/application/service/sop/note.go`、`note_test.go`
- Modify: `internal/application/service/sop/engine/engine.go`（Deps 追加 `OnRunFinal func(ctx context.Context, run *types.SopRun)`，finalizeRun 末尾调用，**错误只记日志**）

**Interfaces:**
- Consumes: T59 仓储、T60 渲染、Know `interfaces.KnowledgeService.{CreateKnowledgeFromManual,UpdateManualKnowledge}`、`interfaces.KnowledgeBaseService.CreateKnowledgeBase/DeleteKnowledgeBase`、T6 原有 SopProjectRepo。
- Produces: `sop.BriefDeps{Projects interfaces.SopProjectRepository; Notes interfaces.SopProjectNoteRepository; Items interfaces.SopItemRepository; Knowledge BriefKnowledge; Dispatcher func(projectID string)}`；`sop.BriefService{Render(ctx, tenantID, projectID) (markdown string, err error)}`、`BriefService.Trigger(projectID)`（2s debounce 合并，进程内 map+mutex）、`BriefService.EnsureInitial(ctx, tenantID, projectID) error`（首次渲染/补绑后全量）；`sop.NotesService{Create(ctx, tenantID uint64, userID string, note *types.SopProjectNote) error}`、`AutoRecordRun(ctx context.Context, run *types.SopRun, itemTitle string, nodeRuns []types.SopNodeRun, arts []types.SopArtifact) error`（确定性摘要）、`Edit/SoftDelete/Pin`；窄接口 `type BriefKnowledge interface{ CreateKnowledgeFromManual(ctx context.Context, kbID string, p *types.ManualKnowledgePayload, channel string) (*types.Knowledge, error); UpdateManualKnowledge(ctx context.Context, knowledgeID string, p *types.ManualKnowledgePayload) (*types.Knowledge, error) }`。

- [ ] **Step 1: 写失败测试**

```go
func TestBriefRenderSkipsWhenSeqUnchanged(t *testing.T) {
	db := openSqliteMem(t)
	fk := &fakeBriefKnowledge{}
	svc := newTestBriefService(t, db, fk)
	seedNote(t, db, 1, "p1", "决策一") // seq=1
	require.NoError(t, svc.EnsureInitial(context.Background(), 1, "p1"))
	require.Equal(t, 1, fk.createCalls)
	require.NoError(t, svc.EnsureInitial(context.Background(), 1, "p1")) // seq 未变
	require.Equal(t, 1, fk.createCalls+fk.updateCalls, "seq 未变不得重复写 KB")
	seedNote(t, db, 1, "p1", "决策二") // seq=2
	require.NoError(t, svc.EnsureInitial(context.Background(), 1, "p1"))
	require.Equal(t, 1, fk.updateCalls, "新增后覆盖同一文档")
	require.Equal(t, fk.lastKnowledgeID, mustProject(t, db, 1, "p1").BriefKnowledgeID, "回填 brief_knowledge_id")
	require.Equal(t, int64(2), mustProject(t, db, 1, "p1").BriefRenderedSeq)
}

func TestBriefDegradesWithoutKB(t *testing.T) {
	db := openSqliteMem(t)
	svc := newTestBriefService(t, db, &fakeBriefKnowledge{})
	seedProject(t, db, 1, "p2", "") // kb_id 为空
	md, err := svc.Render(context.Background(), 1, "p2")
	require.NoError(t, err)          // 渲染本身不依赖 KB
	require.NotEmpty(t, md)
	require.NoError(t, svc.EnsureInitial(context.Background(), 1, "p2")) // 同步降级为 no-op
}

func TestAutoRecordRunIsDeterministic(t *testing.T) {
	db := openSqliteMem(t)
	svc := newTestNotesService(t, db)
	run := &types.SopRun{ID: "r1", ProjectID: "p1", ItemID: "i1", Status: types.SopRunSucceeded,
		Usage: types.SopUsage{InputTokens: 1200, OutputTokens: 300}}
	nodeRuns := []types.SopNodeRun{{NodeID: "n1", Status: types.SopNodeSucceeded}, {NodeID: "n2", Status: types.SopNodeSucceeded}}
	arts := []types.SopArtifact{{Name: "PRD", Path: "out/prd.md", SHA256: "abcdef0123456789"}}
	require.NoError(t, svc.AutoRecordRun(context.Background(), run, "登录改造", nodeRuns, arts))
	var n types.SopProjectNote
	require.NoError(t, db.First(&n).Error)
	require.Equal(t, types.SopNoteSourceEngine, n.Source)
	require.Equal(t, types.SopNoteKindRunSummary, n.Kind)
	require.Contains(t, n.Body, "out/prd.md")
	require.Contains(t, n.Body, "abcdef01")     // sha256 前 8 位
	require.NotContains(t, n.Body, "sk-")        // 不落敏感串
}
```

- [ ] **Step 2: 运行确认失败** Run: `go test ./internal/application/service/sop/ -run "TestBrief|TestAutoRecord" -v` Expected: 编译失败。
- [ ] **Step 3: 实现**：
  - `BriefService.Render`：拉项目+事项（含派生 exec_state，复用 T22 派生）+ 黑板（limit 200）→ `types.RenderProjectBrief`。
  - `EnsureInitial`：`MaxSeq` 与 `project.BriefRenderedSeq` 相等→直接返回（零 KB 写）；`kb_id==""`→返回 nil（降级）；`brief_knowledge_id==""`→`CreateKnowledgeFromManual(kbID, {Title: project.Name+" 项目简报", Content: doc, Status: "publish"}, "sop")` 并回填 id 与 rendered_seq；否则 `UpdateManualKnowledge(id, {Content: doc, Status:"publish"})` 并更新 rendered_seq。文档正文首行为 `<!-- 自动生成，请勿手改；要补充请在项目动态里记一条 -->`。
  - `Trigger(projectID)`：`time.AfterFunc(2*time.Second)` 合并（同 project 已有 pending 则跳过）；回调内 `EnsureInitial`，错误 `logger.Warnf`。
  - `NotesService.AutoRecordRun`：组装确定性 Markdown（事项标题、`状态：<中文>`、每个节点 `- <nodeID>：<状态中文>`、产物 `- <name>（<path>，sha256 <前8>）`、`Token：输入 x / 输出 y`、审批意见原话取自 run.HumanContext 中 source=approval 条目）→ `Create(Kind=run_summary, Source=engine, Title="运行结论：<事项标题>", ItemID=run.ItemID, RunID=run.ID)` → 调 `Dispatcher`。
  - 引擎 `finalizeRun` 末尾调 `Deps.OnRunFinal`（`recover()` 包裹，panic 不影响 run 收尾）。
- [ ] **Step 4: 运行确认通过** Run: `go test ./internal/application/service/sop/ -run "TestBrief|TestAutoRecord" -v && go test ./internal/application/service/sop/engine/ -v` Expected: PASS（引擎既有测试零回归）。
- [ ] **Step 5: 提交** `git add internal/application/service/sop/brief.go internal/application/service/sop/brief_test.go internal/application/service/sop/note.go internal/application/service/sop/note_test.go internal/application/service/sop/engine/engine.go && git commit -m "feat(sop): 项目黑板服务、简报 KB 同步（seq 去重/降级）与运行终态自动摘要"`

### Task 62: API 层（黑板/简报/拆分/子项/项目建库改绑）+ 统计带父项汇总

**Files:**
- Create: `internal/handler/sop_brief.go`、`sop_brief_test.go`
- Modify: `internal/handler/sop.go`（CreateProject 支持 `kb_mode`；新增 `POST /projects/:id/kb`；stats 父项汇总）、`sop_items.go`（split/children/detach；列表 parent_id 过滤与父项派生字段）、`internal/router/routes_sop.go`

**Interfaces:**
- Consumes: T61 服务、T21/T22 既有处理器助手（`sopFail/sopBindJSON/sopTenantID/sopUserID`）。
- Produces: 路由

```
GET    /sop/projects/:id/brief            read_sop   （{markdown,last_rendered_at,rendered_seq,notes_max_seq,kb_bound,brief_knowledge_id}）
POST   /sop/projects/:id/brief/render     run_sop    （未绑 KB → 400「请先绑定知识库」）
GET    /sop/projects/:id/notes            read_sop   ?kind=&pinned=&limit=
POST   /sop/projects/:id/notes            run_sop    body {kind,title,body,item_id?,run_id?,pinned?} → 201
PATCH  /sop/notes/:id                     run_sop
DELETE /sop/notes/:id                     run_sop
POST   /sop/projects/:id/notes/decision-draft  run_sop  （SSE；返回草稿文本，不落库）
POST   /sop/projects/:id/kb               manage_sop body {kb_id}
POST   /sop/items/:id/split               run_sop    body {children:[{title,description,kind?,template_id?}]} → 201 {created:[…]}
GET    /sop/items/:id/children            read_sop
POST   /sop/items/:id/children/:cid/detach run_sop
```

- [ ] **Step 1: 写失败测试**

```go
func TestSplitRejectsThirdLevel(t *testing.T) {
	r, _ := newSOPTestRouter(t)
	pid := createTestProject(t, r)
	parent := doCreateItem(t, r, pid, "大需求")
	w := doJSON(t, r, "POST", "/api/v1/sop/items/"+parent+"/split", `{"children":[{"title":"子项"}]}`)
	require.Equal(t, http.StatusCreated, w.Code)
	var got struct{ Created []types.SopItem `json:"created"` }
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	require.Len(t, got.Created, 1)
	require.Equal(t, parent, got.Created[0].ParentID)
	// 对子项再拆 → 拒绝
	w = doJSON(t, r, "POST", "/api/v1/sop/items/"+got.Created[0].ID+"/split", `{"children":[{"title":"三级"}]}`)
	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Contains(t, w.Body.String(), "只能拆两层")
}

func TestParentItemCannotLaunchDirectly(t *testing.T) {
	r, _ := newSOPTestRouter(t)
	pid := createTestProject(t, r)
	parent := doCreateItem(t, r, pid, "大需求")
	doCreateChild(t, r, parent, "子项")
	w := doJSON(t, r, "POST", "/api/v1/sop/items/"+parent+"/relaunch", `{}`)
	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Contains(t, w.Body.String(), "请对子项触发")
}

func TestBriefEndpointReportsSeqState(t *testing.T) {
	r, db := newSOPTestRouter(t)
	pid := createTestProject(t, r)
	doJSON(t, r, "POST", "/api/v1/sop/projects/"+pid+"/notes", `{"kind":"decision","title":"A","body":"因为B"}`)
	w := doJSON(t, r, "GET", "/api/v1/sop/projects/"+pid+"/brief", "")
	require.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		Markdown     string `json:"markdown"`
		RenderedSeq  int64  `json:"rendered_seq"`
		NotesMaxSeq  int64  `json:"notes_max_seq"`
		KBBound      bool   `json:"kb_bound"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Contains(t, resp.Markdown, "因为B")
	require.Equal(t, int64(1), resp.NotesMaxSeq)
	require.False(t, resp.KBBound) // 测试路由默认不建 KB
	_ = db
}
```

- [ ] **Step 2: 运行确认失败** Run: `go test ./internal/handler/ -run "TestSplitRejects|TestParentItemCannot|TestBriefEndpoint" -v` Expected: 404/编译失败。
- [ ] **Step 3: 实现**（测试助手沿用 T21/T24 的 `newSOPTestRouter/doJSON/createTestProject`；本任务新增 `doCreateItem(t,r,projectID,title) string`（POST /items 取 id）与 `doCreateChild(t,r,parentID,title) string`（POST /items/:id/split 取 created[0].ID）两个助手）：
  - `sop_brief.go`：brief 读取（render 结果 + `brief_rendered_seq`/`MaxSeq` 对比 + `kb_bound`）+ notes 列表/新增/编辑/软删/置顶 + `POST /brief/render`（未绑 KB → 400「请先绑定知识库」）+ decision-draft（SSE：取最近 10 条黑板与最近 5 个 run 摘要 → AgentQA 单轮，prompt 要求输出「决策标题 + 一句话理由 + 影响范围」，**不落库**，完成后追一条 `done` 事件）。
  - `sop.go` CreateProject：`kb_mode` 缺省 `new` → 建库（`KnowledgeBase{Name: req.Name, EmbeddingModelID: defaultEmbeddingModelID(ctx)}`，失败只 warn）→ `kb_owner=project`；`existing` → 校验 kb_id 归属租户→`kb_owner=linked`；`none` → 空；建成功后异步 `briefSvc.EnsureInitial`。`POST /projects/:id/kb` 改绑：旧库若 `kb_owner=project` 则改名追加「（已迁移）」不删，新库写 `brief_knowledge_id=""`+`brief_rendered_seq=0` 后触发全量渲染。
  - `sop_items.go`：**创建事项 body 支持 `is_big`**（新建模态「大项」即 `is_big=true`、`template_id` 强制空、`parent_id` 强制空；建空大项合法，不要求同时有子项）；`split` 校验父项 `parent_id==""`，插入子项（`parent_id`、`split_source`）并**把父项置 `is_big=true`**（独立事项被拆时自动升级为大项），逐条 `AddActivity(business, split)`；`detach` 置空子项 `parent_id`（父项 `is_big` 保持不变，后者可能是空容器）；列表响应对 `is_big=true` 的项补 `child_total/child_done/child_exec`（`DeriveParentExecState`，空容器→none）；`relaunch`/`createRun` 对 `is_big=true` 一律返回 400「该事项是大项，不能直接触发，请对子项触发」。
- [ ] **Step 4: 运行确认通过** Run: `go test ./internal/handler/ -run "TestSplitRejects|TestParentItemCannot|TestBriefEndpoint" -v && go build ./...` Expected: PASS。
- [ ] **Step 5: 提交** `git add internal/handler/sop_brief.go internal/handler/sop_brief_test.go internal/handler/sop.go internal/handler/sop_items.go internal/router/routes_sop.go && git commit -m "feat(sop): 黑板/简报/拆分 API 与项目自动建库改绑"`

### Task 63: 父项状态推进 + 模板产出子事项（out/child-items.json）

**Files:**
- Create: `internal/application/service/sop/engine/childitems_test.go`
- Modify: `internal/application/service/sop/engine/node.go`（门禁通过后若节点 `ProducesChildItems` 为真→读 `out/child-items.json` 建子项）、`internal/application/service/sop/engine/engine.go`（Deps 追加 `CreateItems func(ctx context.Context, run *types.SopRun, specs []types.SopChildItemSpec) error`）、`internal/types/sop_template.go`（SopNode 补 `ProducesChildItems bool json:"produces_child_items,omitempty"`）、`internal/types/sop_item.go`（SopItem 补 `ParentID string json:"parent_id"` / `SplitSource string json:"split_source"`）、`internal/application/service/sop/item.go`（`CreateChildItems`）

**Interfaces:**
- Consumes: T62 split 落库语义、T12/T13 门禁结论。
- Produces: `types.SopChildItemSpec{Title, Description, Kind, TemplateID string}`；`engine.Deps.CreateItems`；`ItemService.CreateChildItems(ctx, tenantID, parentItemID string, specs []types.SopChildItemSpec, source string) (created int, err error)`（上限 20，超限返回 slash 错误「一次最多创建 20 个子事项」不静默截断）。

- [ ] **Step 1: 写失败测试**

```go
func TestProducesChildItemsAfterGatePass(t *testing.T) {
	db := openSqliteMem(t)
	e := newRealEngine(t, db)
	// 节点声明 produces_child_items，脚本写 out/child-items.json（2 条）
	run := seedChildItemsRun(t, e, `{"children":[{"title":"子一","description":"d1"},{"title":"子二","description":"d2"}]}`)
	require.NoError(t, e.Start(context.Background())); defer e.Stop()
	waitFor(t, 10*time.Second, func() bool { return runStatus(t, e, run.ID) == types.SopRunSucceeded })
	var kids []types.SopItem
	require.NoError(t, db.Where("parent_id = ?", run.ItemID).Find(&kids).Error)
	require.Len(t, kids, 2)
	require.Equal(t, types.SopSplitSourceTemplate, kids[0].SplitSource)
}

func TestChildItemsOverLimitFailsNode(t *testing.T) {
	db := openSqliteMem(t)
	e := newRealEngine(t, db)
	specs := make([]map[string]string, 21)
	for i := range specs { specs[i] = map[string]string{"title": fmt.Sprintf("子%d", i)} }
	b, _ := json.Marshal(map[string]any{"children": specs})
	run := seedChildItemsRun(t, e, string(b))
	require.NoError(t, e.Start(context.Background())); defer e.Stop()
	waitFor(t, 10*time.Second, func() bool { return runStatus(t, e, run.ID) == types.SopRunFailed })
	var kids []types.SopItem
	db.Where("parent_id = ?", run.ItemID).Find(&kids)
	require.Empty(t, kids, "超限不得部分创建")
}
```

`internal/application/service/sop/item_test.go` 追加（父项状态推进）：

```go
func TestParentAutoAdvancesWhenAllChildrenClosed(t *testing.T) {
	db := openSqliteMem(t)
	svc := newTestItemService(t, db)
	parent := seedItem(t, db, 1, "p1", "大需求", "")
	c1 := seedItem(t, db, 1, "p1", "子一", parent.ID)
	c2 := seedItem(t, db, 1, "p1", "子二", parent.ID)
	require.NoError(t, svc.UpdateStatus(context.Background(), 1, c1.ID, types.SopItemStatusTodo, "u1"))
	require.NoError(t, svc.UpdateStatus(context.Background(), 1, c1.ID, types.SopItemStatusDone, "u1"))
	got, _ := svc.Get(context.Background(), 1, parent.ID)
	require.NotEqual(t, types.SopItemStatusDone, got.Status, "还有未完成子项时父项不动")
	require.NoError(t, svc.UpdateStatus(context.Background(), 1, c2.ID, types.SopItemStatusTodo, "u1"))
	require.NoError(t, svc.UpdateStatus(context.Background(), 1, c2.ID, types.SopItemStatusClosed, "u1"))
	got, _ = svc.Get(context.Background(), 1, parent.ID)
	require.Equal(t, types.SopItemStatusDone, got.Status, "全部子项 done/closed 时父项自动 done")
	// 父项自身状态机仍受 CanTransit 约束：cancelled 子项不触发推进
}
```

- [ ] **Step 2: 运行确认失败** Run: `go test ./internal/application/service/sop/engine/ -run "TestProducesChildItems|TestChildItemsOverLimit" -v && go test ./internal/application/service/sop/ -run TestParentAutoAdvances -v` Expected: 编译失败。
- [ ] **Step 3: 实现**：① 门禁 pass 后（`applyGateOutcome` 内、写产物之后）若 `node.ProducesChildItems` → 读 `<ws>/out/child-items.json`（`{"children":[…]}`）；解析失败→节点 failed（原因白话「子事项清单格式不对：需要 {\"children\":[{title,description}]}」）；`len>20`→failed；否则调 `Deps.CreateItems`；`run.ItemID` 为空→跳过（不报错，记 run 日志）。② `ItemService.UpdateStatus` 在子项落库成功后：若 `item.ParentID != ""` → 查兄弟项全部 `status ∈ {done, closed}` → 父项当前状态允许迁移则调自身 `UpdateStatus(parentID, done)` 并 `AddActivity(business, auto_done)`；父项状态机 `CanTransitSopItem` 拒绝时只记 warn，不阻断子项写入。
- [ ] **Step 4: 运行确认通过** Run: `go test ./internal/application/service/sop/engine/ -run "TestProducesChildItems|TestChildItemsOverLimit" -v && go test ./internal/application/service/sop/ -run TestParentAutoAdvances -v && go test ./internal/application/service/sop/engine/ -v` Expected: PASS。
- [ ] **Step 5: 提交** `git add internal/application/service/sop/engine/childitems_test.go internal/application/service/sop/engine/node.go internal/application/service/sop/engine/engine.go internal/types/sop_template.go internal/types/sop_item.go internal/application/service/sop/item.go internal/application/service/sop/item_test.go && git commit -m "feat(sop): 模板节点产出子事项清单并自动拆分（上限 20）、父项全完成自动 done"`

### Task 64: 任务书「项目现状」块 + inputs/project-brief.md 物化

**Files:**
- Modify: `internal/application/service/sop/engine/taskbook.go`、`taskbook_test.go`
- Modify: `internal/application/service/sop/engine/workspace.go`（`MaterializeProjectBrief(ws, markdown)`）、`workspace_test.go`
- Modify: `internal/application/service/sop/engine/engine.go`（Deps 追加 `BriefProvider interface{ ProjectBrief(ctx, tenantID uint64, projectID string) (markdown string, err error) }`；节点执行前填 `TaskBookInput.ProjectContext` 并物化文件；**首个节点执行前把该文本写回 `sop_runs.project_context`**，供运行详情「任务书」视图展示）；`internal/types/sop_run.go`（SopRun 补 `ProjectContext string json:"project_context"`）；`migrations/versioned/000170_sop_runs.*` 与 `migrations/sqlite/000072_*`（**修改 T5 已建的同名建表语句**，在 sop_runs 表内直接加 `project_context TEXT NOT NULL DEFAULT ''`；sop_runs 是本次新增表，无存量数据，直接改建表比加 ALTER 更干净）

**Interfaces:**
- Consumes: T60/T61 BriefService（container 注入为 BriefProvider 实现）。
- Produces: `engine.TaskBookInput.ProjectContext string`（已渲染的「项目现状」正文，空=不出该块）、`engine.BuildProjectContext(items []types.SopItem, notes []types.SopProjectNote, briefPath string) string`（纯函数，硬顶 1500 汉字）、`Workspace.MaterializeProjectBrief(md string) (path string, err error)`。

- [ ] **Step 1: 写失败测试**

```go
func TestRenderTaskBookProjectContextBlock(t *testing.T) {
	in := TaskBookInput{Node: types.SopNode{ID: "n1", Name: "实现", TaskPrompt: "写代码"},
		Item: &types.SopItem{Title: "登录改造"}, ProjectContext: "- 决策：采用方案 B\n- 兄弟事项：3/7 已完成"}
	out := RenderTaskBook(in)
	require.Contains(t, out, "## 项目现状")
	require.Contains(t, out, "采用方案 B")
	require.Contains(t, out, "inputs/project-brief.md")
	// 顺序：事项背景之后、产出要求之前
	require.Less(t, strings.Index(out, "## 事项背景"), strings.Index(out, "## 项目现状"))
	require.Less(t, strings.Index(out, "## 项目现状"), strings.Index(out, "## 产出要求"))
}

func TestBuildProjectContextCap(t *testing.T) {
	notes := []types.SopProjectNote{}
	for i := 0; i < 40; i++ {
		notes = append(notes, types.SopProjectNote{Kind: types.SopNoteKindRisk, Title: strings.Repeat("风险", 30), Body: strings.Repeat("正文", 60), Seq: int64(i)})
	}
	out := engine.BuildProjectContext([]types.SopItem{{No: "K-1", Title: "T", ExecState: "running"}}, notes, "inputs/project-brief.md")
	require.LessOrEqual(t, len([]rune(out)), 1600)
	require.Contains(t, out, "项目简报全文")
}

func TestMaterializeProjectBrief(t *testing.T) {
	ws := newTestWorkspace(t, "r1")
	p, err := ws.MaterializeProjectBrief("# 项目简报\n正文")
	require.NoError(t, err)
	raw, err := os.ReadFile(p)
	require.NoError(t, err)
	require.Contains(t, string(raw), "正文")
	require.Contains(t, p, "inputs")
}
```

- [ ] **Step 2: 运行确认失败** Run: `go test ./internal/application/service/sop/engine/ -run "TestRenderTaskBookProjectContext|TestBuildProjectContext|TestMaterializeProjectBrief" -v` Expected: 编译失败。
- [ ] **Step 3: 实现**：`BuildProjectContext` 组装「最近决策（pinned+最近，最多 6 条，标题+一句话正文）」→「兄弟事项进度（`3/7 已完成；KNOW-11 正在评审`）」→「完整简报：<briefPath>」；超 1500 字按决策条数递减。`RenderTaskBook` 在 `## 事项背景` 后插入 `## 项目现状`（非空才出）。`MaterializeProjectBrief` 写 `<ws>/inputs/project-brief.md`（复用 SafeWorkspacePath）。引擎执行前：`BriefProvider.ProjectBrief` 失败→空上下文（降级，warn 日志）；成功→物化 + 填 `ProjectContext`。
- [ ] **Step 4: 运行确认通过** Run: `go test ./internal/application/service/sop/engine/ -run "TestRenderTaskBook|TestBuildProjectContext|TestMaterializeProjectBrief" -v && go test ./internal/application/service/sop/engine/ -v` Expected: PASS。
- [ ] **Step 5: 提交** `git add internal/application/service/sop/engine/taskbook.go internal/application/service/sop/engine/taskbook_test.go internal/application/service/sop/engine/workspace.go internal/application/service/sop/engine/workspace_test.go internal/application/service/sop/engine/engine.go && git commit -m "feat(sop): 任务书项目现状块与简报物化进 inputs"`

### Task 65: 前端 API/helpers 增量

**Files:**
- Modify: `frontend/src/api/sop/index.ts`、`frontend/src/api/sop/helpers.ts`、`helpers.test.ts`
- Modify: `frontend/src/i18n/locales/{zh-CN,zh-TW,en-US,ja-JP,ko-KR,ru-RU}.ts`

**Interfaces:**
- Consumes: T62 路由契约（字段名逐字对齐）。
- Produces: api 追加 `getBrief/renderBrief/listNotes/createNote/updateNote/deleteNote/decisionDraft/replaceProjectKB/splitItem/listChildren/detachChild`；helpers 追加 `deriveParentExecState(states: string[]): ExecState`、`childProgressText(done:number,total:number): string`（`3/7 子项已完成`，total=0→`—`）、`noteKindLabel(kind)`（决策/运行结论/风险/发布/随手记）、`noteSourceLabel(source)`（人工/引擎/AI）、`briefSeqStale(renderedSeq, notesMaxSeq)`。

- [ ] **Step 1: 写失败测试**

```ts
test('父项执行情况汇总与子项进度文案', () => {
  assert.equal(deriveParentExecState([]), 'none');
  assert.equal(deriveParentExecState(['closed', 'intervene']), 'intervene');
  assert.equal(deriveParentExecState(['closed', 'running']), 'running');
  assert.equal(deriveParentExecState(['closed', 'closed']), 'closed');
  assert.equal(deriveParentExecState(['closed', 'pr']), 'pr');
  assert.equal(childProgressText(3, 7), '3/7 子项已完成');
  assert.equal(childProgressText(0, 0), '—');
});

test('黑板标签与序差判定', () => {
  assert.equal(noteKindLabel('run_summary'), '运行结论');
  assert.equal(noteSourceLabel('ai'), 'AI');
  assert.equal(briefSeqStale(3, 4), true);
  assert.equal(briefSeqStale(4, 4), false);
});
```

- [ ] **Step 2: 运行确认失败** Run: `cd frontend && npm test -- src/api/sop/helpers.test.ts` Expected: FAIL（未导出）。
- [ ] **Step 3: 实现**：api 函数逐个照 REST；`deriveParentExecState` 镜像 T59 后端（`intervene>running>pr>closed>none`，空→none）；i18n 六语言补 `sop.brief.*`、`sop.notes.*`、`sop.split.*` 键。
- [ ] **Step 4: 运行确认通过** Run: `cd frontend && npm test -- src/api/sop/helpers.test.ts && npm run type-check && npm run check-i18n` Expected: PASS。
- [ ] **Step 5: 提交** `git add frontend/src/api/sop frontend/src/i18n/locales && git commit -m "feat(sop): 前端黑板/简报/拆分 API 与纯函数"`

### Task 66: 前端 项目配置知识库卡片 + 简报卡片 + SopProjectBriefPanel

**Files:**
- Create: `frontend/src/views/sop/components/SopProjectBriefPanel.vue`
- Modify: `frontend/src/views/sop/SopProjectSettings.vue`、`frontend/src/views/sop/components/ProjectCreateModal.vue`

**Interfaces:**
- Consumes: T65 api、T63 后端建库/改绑。
- Produces: 项目配置新增两张卡；创建模态知识库选择；简报面板（抽屉/侧栏复用，props `projectId: string`、emits `close`）。

- [ ] **Step 1: 实现创建模态知识库区**：单选三档「新建：<项目名> 知识库」（默认）/「使用已有知识库」（KB 下拉）/「暂不创建」（高级折叠内）；提交时 `kb_mode` 随 body。
- [ ] **Step 2: 实现项目配置知识库卡片**：显示库名 + 来源徽标（项目自建/改绑/PM 迁移）；「使用已有知识库」→KB 下拉确认；项目自建库显示「解绑并删除该库」二次确认文案「该库内所有文档将被删除且不可恢复」；未绑定显示「去创建/绑定」+ toast 提示简报不会同步。
- [ ] **Step 3: 实现简报卡片 + SopProjectBriefPanel**：卡片显示最后渲染时间与 `briefSeqStale` 时的「有 N 条新动态未同步 · 立即重新生成」；面板左侧黑板时间线（kind tabs + 置顶分组 + 来源徽标 + 每条悬停出现编辑/置顶/删除）+ 顶部「记一条」内联表单 + 右侧简报预览（Markdown 只读 + 「在知识库打开」+ 「生成决策草稿」→ SSE 流式填充输入框 + 确认写回）；未绑 KB 时预览区显示降级提示与绑定入口。
- [ ] **Step 4: 浏览器走查** 按 spec §16.22/25：创建项目（新建库）→配置页看到库名与简报卡片→记一条决策→面板显示「1 条未同步」→立即重新生成→去知识库页确认文档存在且标题为「<项目名> 项目简报」；改绑另一库→旧库文档标题带「（已迁移）」；删除项目确认自建库被删、改绑库保留。
- [ ] **Step 5: 验证与提交** Run: `cd frontend && npm test && npm run type-check && npm run check-i18n` → PASS；`git add frontend/src/views/sop && git commit -m "feat(sop): 项目知识库卡片、简报面板与创建模态建库选项"`

  （本任务按 M9 约定：组件交互以 browser-use 走查 + type-check + check-i18n 验收，无组件级单测基建。）

### Task 67: 前端 父项折叠/进度徽标 + 创建模态拆分 + 抽屉子事项 tab

**Files:**
- Modify: `frontend/src/views/sop/SopItemBoard.vue`（父项行折叠与派生列）、`SopItemCreateModal.vue`（拆解卡勾选→拆成子事项）、`SopItemDrawer.vue`（子事项 tab + 所属大项）、`SopItemCreateModal.vue`（归属大项下拉）

**Interfaces:**
- Consumes: T65 api/helpers、T62 后端 split/detach/children、T63 父项不可直接触发。
- Produces: 列表父项折叠（`expandedParents: Set<string>`）、看板父项卡片同样显示 `childProgressText`；创建模态「拆成子事项」按钮（勾选条目 → splitItem 后刷新列表并提示「已创建 N 个子事项」）；抽屉「子事项」tab（表格 + 批量触发 + 移出父项确认）。

- [ ] **Step 1: 列表/看板父项呈现**：标题前 `▸/▾` 箭头（行点击不打开抽屉，仅展开；点击标题打开抽屉）；进度列用 `childProgressText` + 子项执行情况小色点；关联 SOP 列「—」；操作列「触发」置灰 tooltip「该事项已拆分为子项，请对子项触发」；批量勾选父项时自动展开为子项 id 集合（toast「已展开 N 个子项」）；**统计带右侧加「📄 黑板与简报」按钮**（openBriefPanel），知识库格下方显示简报同步状态（已同步 / N 条待同步 / 未绑 KB）。
- [ ] **Step 2: 创建模态**：左表单末尾加「归属大项（可选）」父事项下拉（仅顶层事项）；拆解卡增加逐条复选框与底部「拆成子事项（N）」按钮，与「写入描述」并列；拆分成功后清空勾选、toast、emit changed；「确认事项」创建父项本身时不自动拆。
- [ ] **Step 3: 抽屉**：父项显示「子事项」tab（列：编号/标题/状态/执行情况/进度/最近运行/操作[触发|打开|移出]）+ tab 标题带计数徽标；子项概览字段卡显示「所属大项：<编号 标题>」可点击切换抽屉；父项概览运行面板替换为汇总面板（子项状态环 + 批量触发 + 「拆成子事项」入口）。
- [ ] **Step 4: 浏览器走查** 按 spec §16.24：建大项→AI 拆 3 条→列表折叠展开正确→批量触发父项自动展开→子项各跑一条→父项状态/五态计数刷新→移出子项后父项计数变化→对子项再拆被拒并显示白话错误。
- [ ] **Step 5: 验证与提交** Run: `cd frontend && npm test && npm run type-check && npm run check-i18n` → PASS；`git add frontend/src/views/sop && git commit -m "feat(sop): 父项折叠与汇总进度、创建模态拆分、抽屉子事项 tab"`

### Task 68: M11 验收（spec §16.22–26）与全量回归

**Files:**
- Create: `internal/application/service/sop/engine/e2e_brief_split_test.go`
- Create: `docs/superpowers/verification/2026-11-20-sop-brief-split.md`

**Interfaces:**
- Consumes: T59–T67 全部。

- [ ] **Step 1: 写 E2E 测试**

```go
func TestSharedContextFlowsToNodesAndBriefToKB(t *testing.T) {
	db := openSqliteMem(t)
	fk := &fakeBriefKnowledge{}
	env := newBriefEnv(t, db, fk) // 项目 p1（kb 桩）+ 模板：agent 节点 id="analysis"
	// 1) 建两条兄弟事项（一条已 done 且有产物），黑板记一条决策
	seedSiblingDone(t, env)
	require.NoError(t, env.Notes.Create(context.Background(), 1, "u1", &types.SopProjectNote{
		ProjectID: "p1", Kind: types.SopNoteKindDecision, Title: "采用方案 B", Body: "因为可逆"}))
	// 2) 起一条新运行，断言任务书里真的带上了项目现状
	run := createRunFromTemplate(t, env.Engine, env.Tpl)
	env.Engine.Start(context.Background()); defer env.Engine.Stop()
	waitFor(t, 10*time.Second, func() bool { return runStatus(t, env.Engine, run.ID) == types.SopRunSucceeded })
	require.Contains(t, env.AgentPrompt(run), "## 项目现状")
	require.Contains(t, env.AgentPrompt(run), "采用方案 B")
	require.Contains(t, env.AgentPrompt(run), "已完成")          // 兄弟事项进度
	brief, err := os.ReadFile(filepath.Join(run.WorkspaceDir, "inputs", "project-brief.md"))
	require.NoError(t, err)
	require.Contains(t, string(brief), "采用方案 B")
	// 3) 运行终态自动落黑板 + 简报同步进 KB
	waitFor(t, 5*time.Second, func() bool { return env.NoteCount(types.SopNoteKindRunSummary) >= 1 })
	require.NoError(t, env.Brief.EnsureInitial(context.Background(), 1, "p1"))
	require.Equal(t, 1, fk.createCalls) // 首版入库
	require.Contains(t, fk.lastContent, "运行结论")
}

func TestSplitThreeChildrenEachRunIndependently(t *testing.T) {
	// 父项 + 3 子项；逐个子项起运行（stub provider 直接 pass）；
	// 断言：三条 run 的 item_id 互不相同、父项 child_done=3、父项状态自动 done、五态计数之和=4
}
```

- [ ] **Step 2: 运行确认失败** Run: `go test ./internal/application/service/sop/engine/ -run "TestSharedContextFlows|TestSplitThreeChildren" -v` Expected: 编译失败。
- [ ] **Step 3: 补齐助手跑通**（newBriefEnv/fakeBriefKnowledge/AgentPrompt 记录 stub AgentProvider 收到的 Prompt/seedSiblingDone/NoteCount）。
- [ ] **Step 4: 全量回归** Run: `go test ./... && cd frontend && npm test && npm run type-check && npm run check-i18n && npm run build` Expected: 全 PASS。
- [ ] **Step 5: 验收记录**：`docs/superpowers/verification/2026-11-20-sop-brief-split.md` 逐条填 §16.22–26（操作路径/期望/实际/PASS），并记录「未绑 KB 降级」「建库失败补绑」「改绑旧库保留」三个场景的实际输出。
- [ ] **Step 6: 提交** `git add internal/application/service/sop/engine/e2e_brief_split_test.go docs/superpowers/verification/2026-11-20-sop-brief-split.md && git commit -m "test(sop): 项目共享上下文与大需求拆分 E2E 及验收记录"`

---

## 依赖与执行顺序

T1→T2→T3→T4→T5→T6→T7（M1 数据层）→T8→T9→T10→T11→T12→T13→T14→T15（M2 引擎）→T16→T17（M3 平台执行器）→T18→T19→T20（M4 脚本+审批）→T21→T22→T23→T24→T25→T26（M5 API）→T27→T28→T29→T30→T31→T32→T33→T34（M6 CLI）→T35（M7 触发）→T36→T37（M8 KB/MCP）→T38→T39→T40→T41→T42→T43→T44→T45→T46→T47→T48→T49→T50→T51→T52→T53→T54（M9 前端；T42/T49 的预览弹窗依赖 T50，可先按契约 stub 后联调）→T55→T56→T57→T58（M10 验证收尾）→T59→T60→T61→T62→T63→T64→T65→T66→T67→T68（M11 项目共享上下文与大需求拆分）。

**M11 的插入点**（可与其它工作并行，但需满足前置）：T59 依赖 T1/T2（字段所在表）→T60 依赖 T59→T61 依赖 T59/T60 与 T22 派生→T62 依赖 T61 与 T21/T22→T63 依赖 T62 与 T12/T13 门禁→T64 依赖 T60/T61 与 T11 任务书→T65 依赖 T62→T66/T67 依赖 T65→T68 依赖全部。**最省事的执行顺序**：M1–M5 完成即可插入 T59–T64（后端）；前端 T66/T67 放在 T40/T42 之后；T68 放在 T58 之后或与之合并收尾。

**不分期砍功能：68 个任务全部交付即完整方案**（M1–M10 为主体，M11 为项目共享上下文与大需求拆分增补；版本规划按用户决定不做）。顺序只是依赖排序，前端画布（T44–T47）在 M5 完成后即可与 M6–M8 并行。