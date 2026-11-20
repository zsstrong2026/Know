# Know SOP 工作台完整设计（吸收 PM 工作站）

日期：2026-11-19
状态：待评审
上游调研：LoopForge（D:\loopforge\LoopForge）SOP 引擎/执行器深度调研 + Know PM 工作站/automation/agent 底座调研

---

## 1. 背景与定位

Know 现有 PM 工作站只服务产品经理场景（需求→版本→功能树→资料）。目标是借鉴 LoopForge，把 Know 升级为**通用 SOP 作业工作台**：

- 任意事项（需求/缺陷/任务/调研/自定义）都能挂一条 SOP 流程交付；
- SOP 节点既可选**平台维护的智能体**（custom_agents + ReAct 引擎），也可选**外部编程 Agent CLI**（10 种，本机子进程执行）；
- 关键节点**人工确认**：审批放行、打回指定环节返工；
- 完整作业闭环：**事项列表 → 流程模板 → 运行记录 → 定时触发 → 审批中心 → 产物归档**；
- PM 工作站被新工作台**吸收**：需求数据迁移为通用事项，PM 场景沉淀为内置 SOP 模板，PM 专属历史数据只读归档。

### 1.1 已确认决策

| 决策点 | 结论 |
| --- | --- |
| 与 PM 工作站关系 | 新工作台吸收 PM 工作站（PM 站下线，数据迁移 + 只读归档） |
| CLI 执行环境 | 本机子进程 + 私有 HOME（无 Docker 依赖；个人可信环境，无安全隔离边界） |
| CLI 接入范围 | 通用适配器全家桶：7 种通用（opencode/cursor/agy/kimi/pi/dsh/zcode）+ 3 种专用（claude/codebuddy/codex），共 10 种 |
| 交付形式 | 本设计文档 + 可交互 HTML 原型，确认后进实现 |
| 分期 | 不分期，一次性完整交付（内部实现按依赖排序，见 §15） |
| 实现路径 | 原生重建 + 选择性借鉴 LoopForge 设计语义；不整体移植其引擎（多实例 lease/MySQL 方言/团队账号对本地单用户是负资产） |

### 1.2 产品边界（沿用 Know 定位）

个人使用、本地存储、不订阅、桌面 Web、不做移动端、不做多人实时协作。单实例部署为默认形态；引擎不做多实例抢占设计（保留 DB 租约字段以便未来扩展，但调度器为进程内单循环）。

### 1.3 命名

Know 已有 `workbench` 模块（AFFINE 式文档/画布编辑器，`workbench_items` 等表），**与本模块无关**。新模块定名 **`sop`**：后端包 `internal/application/service/sop`、表前缀 `sop_`、路由 `/api/v1/sop/*`、前端路由 `/platform/sop`、界面名「SOP 工作台」。

---

## 2. 总体架构

```
┌─────────────────────────── 前端 Vue3 + TDesign ───────────────────────────┐
│ 事项看板/列表 │ 模板库+画布设计器(vue-flow) │ 运行详情 │ 审批中心 │ SOP任务 │ PM归档 │
└──────────────────────────────────┬────────────────────────────────────────┘
                                   │ /api/v1/sop/* (REST + SSE)
┌──────────────────────────────────┴────────────────────────────────────────┐
│ internal/handler/sop_*.go + router/routes_sop.go                          │
├───────────────────────────────────────────────────────────────────────────┤
│ internal/application/service/sop/                                         │
│  ├─ item.go        事项 CRUD/状态机/附件/关联                              │
│  ├─ template.go    模板 CRUD/校验/版本发布(不可变快照)                     │
│  ├─ engine/        确定性推进引擎：调度循环/节点状态机/打回/重试/暂停      │
│  ├─ executor/      执行器抽象 Provider                                    │
│  │   ├─ agent.go   平台智能体执行器(复用 agent 引擎 + automation 后台模式) │
│  │   ├─ cli/       CLI 执行器：通用适配器×7 + 专用×3 + 私有HOME + 流翻译   │
│  │   └─ script.go  脚本执行器(本机子进程)                                  │
│  ├─ approval.go    人工关口 + HITL prompt 聚合                             │
│  └─ trigger.go     SOP 任务触发配置调度器(复用 automation 触发器/租约工具) │
├───────────────────────────────────────────────────────────────────────────┤
│ 复用底座：agent 引擎(ReAct/hitl/suspension) │ automation(触发器/租约/心跳) │
│          notifications │ sessions/messages │ sandbox/workspace(git检查点)│
│          mcp/client │ knowledge 检索 │ tenant 体系 │ asynq(仅 PM 遗留)   │
└───────────────────────────────────────────────────────────────────────────┘
```

核心原则：

1. **流程在引擎里，智能在执行器里。** 节点推进、依赖判定、打回、重试全部是确定性代码，不交给模型自觉。这是与 Know 现有 ReAct 软编排（todo_write/collaboration）的本质区别，二者共存：SOP 节点内部，平台智能体仍可用自己的 ReAct 循环与委派能力。
2. **单一真相。** 事项只存 `sop_items`；运行只存 `sop_runs`；PM 迁移后 `pm_requirements` 冻结只读，不再是活数据源。
3. **一切产物落盘 + 落库。** 每次运行有独立工作目录；节点产物按契约校验后快照入库，可预览/下载/追溯。

---

## 3. 领域模型与表结构

所有表遵循 Know 约定：`tenant_id BIGINT` 贯穿、VARCHAR(36) 主键、软删、无物理外键（字段关联）、JSONB（SQLite 侧按现有双库迁移约定降级 JSON）。新增迁移从 000166 起。

### 3.1 sop_projects（项目/工作区）

轻量分组容器，吸收 `pm_projects`。项目配置页参照 LoopForge 项目设置（原型 ⚙ 项目配置）。

| 字段 | 说明 |
| --- | --- |
| name / description | 租户内 name 唯一（部分索引，沿用 pm_projects 约定） |
| code | 事项编号前缀（如 `KNOW`），租户内唯一；**创建后不可修改**，避免历史编号断链 |
| next_item_no | 编号自增游标（事务内 `UPDATE … RETURNING`，保证并发创建不重号） |
| kb_id | 绑定知识库：**新建项目默认自动创建同名知识库**（写入租户默认 Embedding 模型，创建失败不阻塞建项目而是留空待补）；创建模态可改为「使用已有知识库」，高级保留「暂不创建」；PM 迁移项目保留原 KB。知识库是平台智能体节点检索范围、产物归档目标，也是《<项目名> 项目简报》的同步目标 |
| kb_owner | `project`=项目自建（删除项目时连带删库）/ `linked`=用户改绑的已有库（删除项目只解绑不删库）/ `pm`=PM 迁移保留（按 linked 处理，不删库） |
| brief_knowledge_id | 《<项目名> 项目简报》在 kb_id 中的知识文档 id（引擎渲染成功后回填；空=尚未渲染或未绑定 KB） |
| brief_rendered_seq | 最近一次渲染对应的黑板序号（与 `sop_project_notes` 最大 seq 比对，相等则跳过，避免空转触发重新分块与嵌入） |
| default_template_id | 默认 SOP 模板（事项未指定 template_id 时使用） |
| default_executor | 新建 Agent 节点的默认执行器：`platform_agent` 或 `cli:<kind>`；节点级可覆盖 |
| on_run_success_item_status | 运行成功后事项自动推进的目标状态（空=不自动推进）；模板级 `on_success_item_status` 优先 |
| notify_on_gate_or_fail | 到达人工关口 / 运行失败时写 notifications（默认开） |
| workspace_root | 本项目运行工作区根目录（默认 `data/sop/workspaces/<project_id>/`） |
| repos JSONB | 代码仓库关联列表 `[{name,url,branch,default}]`（参照 LoopForge 项目设置·代码仓库）：url 仅允许 HTTPS/SSH Git 地址且同项目去重；name 留空自动取仓库名；branch 默认 `main`；**第一个仓库自动设为默认**，可切换；解绑默认仓库时自动顺延到剩余第一个。**独立即时生效**——增删/设默认走独立接口立即落库，不随「保存配置」提交；私有仓库凭证走本机 git 凭证管理（credential helper / ssh-agent），不落项目配置。发起运行时快照进 run（§4.4 克隆） |
| status | active / archived |
| domain_data JSONB | 域扩展（PM 迁移项目的遗留配置） |

**项目新增 / 删除入口与语义**：项目切换器列出全部项目（当前项 ✓ + 编号前缀），底部「＋ 新建项目…」打开创建模态（名称* / 编号前缀*（2–10 位字母数字、字母开头、租户内唯一、创建后锁定）/ **知识库（默认「新建：<项目名> 知识库」，可切换为「使用已有知识库」，高级内「暂不创建」）** / 描述 / 默认 SOP / 工作区根目录（随前缀自动建议 `data/sop/workspaces/<code>/`））；创建成功即切换到新项目，事项/运行/触发配置/仓库绑定按项目隔离，流程模板为全局资产共享。**自动建库规则**：建库成功→`kb_id` 写入、`kb_owner=project`、异步渲染首版《<项目名> 项目简报》进库；建库失败（如未配置 Embedding 模型）→项目照建、`kb_id` 留空，配置页显示「未绑定知识库 · 去创建/绑定」，简报与产物归档降级但不影响运行（黑板条目与 `inputs/project-brief.md` 照常产出）；后续补绑 KB 时自动渲染完整简报。删除入口在项目配置页「危险操作」卡片：二次确认列明影响面（N 条事项、M 条运行记录随项目级联删除不可恢复；触发配置与仓库绑定随删；流程模板不受影响；`kb_owner=project` 的知识库连带删除，`linked`/`pm` 库仅解绑不删；磁盘工作区文件不自动删除需手动清理）；**存在进行中/待审批运行时禁止删除**（提示先取消）；**至少保留一个项目**。

### 3.2 sop_items（事项）——通用工作项，吸收 pm_requirements

| 字段 | 说明 |
| --- | --- |
| project_id / title / description | 基础 |
| no | 项目内展示编号 `<code>-<n>`（如 KNOW-12），创建时由 next_item_no 分配，项目内唯一索引；迁移事项按创建时间顺序补号 |
| kind | requirement / bug / task / research / custom（域标签，替代 PM 专属语义） |
| priority | P0-P3（沿用） |
| status | 8 态通用状态机：`backlog / todo / in_progress / in_review / done / closed / blocked / cancelled`（借鉴 LoopForge issue 状态；映射见 §11） |
| source / proposer / tags JSONB | 来源（customer/internal/data/competitor/strategy/other 沿用）、提出人、标签 |
| template_id | 可选：事项级覆盖默认模板 |
| kb_id / knowledge_id | 可选知识绑定 / 关联知识文档 |
| attachments | 复用 pm_requirement_attachments 模式：`sop_item_attachments`（data BYTEA + text_content 抽取文本供 AI 上下文） |
| outcome JSONB | 通用结果回填：actual_date / effect_notes / value_achieved(achieved/partial/not_achieved) / retro_notes（吸收 PM 上线复盘字段，去 PM 化） |
| domain_data JSONB | 域扩展数据（PM 迁移事项的 ai_analysis/design_plan/reject_reason 等原样保留） |
| last_run_id | 最近一次运行（冗余，便于列表展示） |
| parent_id | 可选父事项（**只允许两级**：父项 parent_id 必须为空，由创建/更新校验硬拦）。空=顶层事项；非空=子项，独立编号/状态/运行，父项只做容器与进度汇总 |
| split_source | 子项来源：`ai`（AI 拆解散列）/ `manual`（手工加行）/ `template`（模板节点产出 `out/child-items.json` 自动建） |

状态机唯一写入口 `UpdateStatus → CanTransit`（沿用 pm_requirement.go:143-166 模式）。运行终态可配置自动推进事项状态：模板级 `on_success_item_status` 优先，其次项目级 `on_run_success_item_status`，都为空则不动，避免隐式改状态。

**父子拆分语义**：父项是普通事项，但**不能直接发起运行**（详情抽屉「触发」置灰，替换为「批量触发子项」）；列表行的「当前环节/进度」列改为子项徽标 `已完成/总数`，「关联 SOP」显示「—」。子项编号仍为平铺自增（`KNOW-13`），不使用 `KNOW-12.3` 之类层级编号，父子关系只靠 `parent_id` 表达。父项的执行情况与状态由子项汇总（`DeriveParentExecState` 纯函数，口径与五态一致：任一子项 intervene→intervene；否则任一 running→running；全部 closed→closed；否则任一 pr→pr；否则 none）；全部子项进入 done/closed 时经唯一写入口 `UpdateStatus` 把父项推进为 done。**三个拆分入口**：① 新建事项模态的 AI 拆解产出「建议子事项清单」，勾选后批量创建（`split_source=ai`，状态 backlog）；② 已有事项详情抽屉「拆成子事项」手工加行或 AI 再拆（`manual`/`ai`）；③ 模板节点声明 `produces_child_items=true` 时产出 `out/child-items.json`（`[{title,description,kind,template_id}]`，上限 20 条），门禁通过后引擎自动建子项（`split_source=template`）。拆分层级限制在创建与更新时校验，拒绝三级嵌套。

列表页的「当前环节 / 进度 / 执行情况」为**派生字段，不落库**：由 last_run_id 关联的 sop_runs + sop_node_runs 实时计算（当前环节=运行中/待审批/失败节点名，事项 done=已完成、closed=已关闭；进度=已成功+已跳过节点数/总节点数；**执行情况=五态口径**：尚未执行=无运行或运行已取消 / 正在推进=running+paused / 需要介入=waiting+failed / 待处理 PR=运行 succeeded 但事项未闭环（产物待人工验收或合并）/ 已闭环=事项 done 或 closed）。列表接口一次 JOIN 带出，避免 N+1。来源键（customer/internal/data/competitor/strategy/other）在全部 UI 面（列表芯片/筛选下拉/详情字段卡/操作日志/新建与触发配置）统一显示中文映射（客户反馈/平台新建/数据洞察/竞品分析/战略规划/其他），键值落库不变。

### 3.3 sop_templates + sop_template_revisions（流程模板）

借鉴 LoopForge `WorkflowDefinition`（domain/workflow.go:146-187）与不可变快照（repo/catalog.go:474 PublishWorkflow / DefinitionRevision）。

sop_templates：name / description / project_id（可空=全局模板）/ status(draft/published/archived) / version / published_revision_id / definition JSONB（当前草稿）/ is_builtin（内置只读，复制后修改，沿用 LoopForge 内置 SOP 约定）/ metric_category（交付/调研/运维，供统计）。

sop_template_revisions：template_id / revision / definition JSONB（发布即冻结，运行引用 revision 而非活模板）。

**definition JSONB 结构**（节点/边/契约）：

```jsonc
{
  "nodes": [{
    "id": "n1", "name": "需求分析",
    "type": "agent",                    // agent | script | approval | sop
    "executor": {
      "kind": "platform_agent",         // platform_agent | cli
      "agent_id": "custom_agent uuid",  // platform_agent 时
      "cli_kind": "zcode",              // cli 时：10 种之一
      "model": "默认",                    // 可选覆盖；检查器为下拉（选项=Know 模型管理已配置模型 + 「默认·跟随凭据配置」档），不允许手输
      "credential_id": "…",             // CLI 凭据引用，见 3.7
      "role_id": null,                  // cli 可选：复用 custom_agents 角色（SystemPrompt/Rules/Skills）作系统提示，执行引擎仍是 CLI；null=仅任务书
      "kb_scope": "project",            // cli 知识库检索范围：project（跟随项目/事项绑定，默认）| custom | all
      "kb_ids": []                      // kb_scope=custom 时的知识库清单；platform_agent 节点沿用 agent 自身 KB 绑定，不受此字段影响
    },
    "task_prompt": "按方案实现，完成后运行自测；返工时按意见重做。",   // 节点任务书=大白话正文（检查器「这一步做什么」），不写占位符；上下文由引擎运行时自动附带（§4.3）
    "task_refs": [],                    // 可选：额外参考产物的节点 id 清单（检查器为步骤名多选下拉）；直接上游产物默认自动带，无需勾选
    "extra_rules": "",                  // 节点级补充规则（大白话，渲染进任务书「遵循规则」段，叠加在角色 Rules 之上）
    "skills": null,                     // 节点级技能子集：null=用角色全部 Skills；数组=只勾选的技能名（检查器复选框）
    "mcp": ["know-kb"],                 // 节点级 MCP 子集（Know 知识库 / 本地文件 / 浏览器等，检查器复选框；默认仅 know-kb）
    "artifact_outputs": [               // ArtifactContract（借鉴 workflow.go:96-101）+ 傻瓜式校验声明
      {"name":"分析报告","path":"out/analysis.md","media_type":"text/markdown","required":true,
       "check":{"type":"exists"}}       // exists=文件存在且非空（默认）| contains=必须包含关键词 {"type":"contains","keyword":"…"} | command=自定义命令 {"type":"command","command":"…"}
    ],
    "gate_commands": ["test -s out/analysis.md"], // 命令门禁（派生字段）：由各产物 check 编译（exists→test -s / contains→grep -q / command→原样）+ gate_extra 合并，set -e 执行；检查器不直接暴露此数组
    "gate_extra": "",                   // 高级：额外门禁命令原文（&& 分隔），折叠在「高级」里，编译进 gate_commands
    "on_failure": "loop",               // loop | reject | fail | human
    "reject_to": "n0",                  // on_failure=reject 或人工打回默认目标
    "max_attempts": 3, "max_reworks": 2, "loop_max_iterations": 3,
    "carry_over": true,                 // 返工时保留上轮产物目录
    "serial_group": "",                 // 同组节点强制串行（可选）
    "timeout_minutes": 60,
    "position": {"x":0,"y":0}           // 画布坐标
  }],
  "edges": [{"id":"e1","source":"n0","target":"n1","when":"success"}],
  "input_schema": ["title","description","attachments"],  // 运行输入声明
  "on_success_item_status": "in_review"                   // 可选
}
```

script 节点：`script`（shell 正文）+ `credential_env`（可选环境变量注入）；approval 节点：`approver_hint`/`notification` 文案；sop 节点：`child_template_id` + 输入映射。

**门禁派生规则**：`gate_commands` 不是用户直接填写的字段，而是发布/保存时由 `artifact_outputs[].check` 与 `gate_extra` 确定性编译（exists→`test -s <path>`、contains→`grep -q "<keyword>" <path>`、command→原样命令，再拼接 gate_extra 按 `&&` 拆分的条目）。检查器只暴露「校验方式」下拉（文件交了就行 / 必须包含关键词 / 自定义命令）与折叠的高级门禁输入，非编程用户全程不见 shell；引擎侧仍按 LoopForge 语义把 gate_commands 合并为单脚本 set -e 执行。

**发布校验**（配置检查，借鉴 LoopForge 设计器）：DAG 无环、reject_to 必须是上游可达节点、agent 节点执行器配置完整、产物路径不越出工作区、子 SOP 不递归引用自身。

**AI 编排助手**（设计器右侧面板，参照 LoopForge「AI 编排」截图）：工具栏「画布 | ✦AI 编排」分段切换；展开时以 360px 面板替代节点检查器——问候语 + 说明（空画布描述要搭的流程；已有流程直接说改哪一步，在原流程上调整、不用先清空；也可说「帮我配全所有步骤」）+ 场景推荐 chips（可换一批、点击填入输入框）+ 对话区 + 底部输入框与模型下拉（模型沿用 Know 模型管理，不接外部订阅）。**仅生成草稿**：AI 返回编排草稿（ops 清单 + 摘要），「应用到画布」写入草稿 def 并置未保存标记，**不自动保存、不自动发布**；「放弃草稿」画布保持原样。空画布按场景生成完整链（需求交付/缺陷修复/发布前检查/PR 评审/巡检/调研，含失败打回关系与五步全配置）；有画布做增量修改（补步骤/加关口/删节点并桥接上下游/切换执行器），意图规则优先级：删除 > 配全 > 补构建 > 加关口 > 换执行器 > 兜底追加 Agent 节点。内置模板可应用草稿但需先复制为自定义才能保存。后端 `POST /sop/templates/ai-orchestrate`（SSE，入参 message + 画布快照 + mode，出参 draft ops），复用平台智能体引擎；草稿 ops 由前端确定性应用，保证画布与草稿一致。

**AI 自主节点配置（双入口，把五步配置从手动降为确认）**：

- **ops 扩为全配置**：除 add/del/edge/mod 外新增 `set` op = `{op:"set", id, name, patch, reasons}`，patch 覆盖 task_prompt（大白话）、extra_rules、skills 子集、mcp 子集、artifact_outputs（含 check）、on_failure/reject_to/max_attempts/max_reworks/timeout/carry_over、executor（role/model/kb_scope 合并而非整体替换）；reasons 为逐字段一句理由。门禁命令仍由 check 派生（§3.3 门禁派生规则），**AI 不直接写 gate_commands/shell**。
- **入口一·整流程**：意图「配全/自动配/帮我配/全配置」对全部 agent 节点逐个生成 set ops；空画布场景链的每个节点直接带五步全配置（角色从真实 custom_agents 选、报告类交付物默认 contains 关键词、实现类 loop / 评审测试类 reject 到实现节点并互设打回目标）。
- **入口二·单节点**：检查器标题栏「✦ AI 配这一步」按钮 → AI 面板进入单节点模式（顶部目标 chip + 退出链接），**零输入可用**：按步骤名关键词（实现/评审/调研/分析）+ 上下游节点 + 已选角色推断 patch；输入框补一句话则追加进任务描述后重新生成。推断只补空/默认字段，已自定义字段不覆盖。
- **字段级差异预览**：set op 渲染为差异卡——逐字段「原值（删除线）→ 新值（品牌色）」+ 理由小字；值用白话格式化（失败策略名/技能与 MCP 名称串/交付物含校验方式/角色名）。每卡「应用此节点」可单独应用（应用后从草稿移除），底部「全部应用到画布」/「放弃草稿」；应用后任何字段仍可在检查器手改。
- **清单约束与服务端校验**：AI 只能从真实清单取值（角色=custom_agents、模型=模型管理、MCP=已注册 server、技能=角色 Skills）；服务端对 draft ops 做 id 校验，非法引用**跳过该字段并在应用反馈中报告**（原型以 toast 列出跳过项），不整单拒绝。业务验收口径（contains 关键词等）属 AI 推断，人在预览确认——符合关键节点人工确认边界；CLI 安装/凭据等环境事实不由 AI 代办。
- **后端 mode**：`mode=flow`（整流程，入参 message+画布快照）| `mode=node`（入参 node_id+节点快照+上下游摘要+清单 inventory+可选 hint，出参单个 set op+reasons）；同一 SSE endpoint，结构化输出 schema 约束 ops 格式，低温度。

### 3.4 sop_runs（运行记录）

借鉴 domain/run.go:10-56，裁剪多实例/仓库团队字段。

| 字段 | 说明 |
| --- | --- |
| template_id / revision_id / workflow_snapshot JSONB | 冻结快照（运行不受模板后续修改影响） |
| project_id / item_id | 事项关联（可空=独立运行） |
| trigger_kind | manual / schedule / api / child_run |
| trigger_ref_id | sop_trigger_configs.id（SOP 任务）或父 run id |
| status | queued / running / waiting / succeeded / failed / cancelled / paused |
| input JSONB | 运行输入（事项标题/描述/附件清单/自定义字段） |
| human_context JSONB | 累计人工补充上下文（审批附带意见 + 发起运行时从事项已回答澄清问答 seeding，进下游 {{humanContext}}） |
| workspace_dir | 本次运行工作目录 |
| lease_until / heartbeat_at | 单实例租约（照抄 automation_runs 模式，崩溃回收用） |
| cancel_requested / pause_requested | 控制位 |
| usage JSONB / execution_duration_ms | token 成本与耗时（平台智能体节点精确统计；CLI 节点按流事件尽力提取） |
| error / started_at / finished_at | 终态信息 |

### 3.5 sop_node_runs（节点运行）

借鉴 domain/run.go:146-168：run_id / node_id / status(pending/running/waiting_approval/pending_input/succeeded/failed/cancelled/skipped) / attempt / rework_count / output TEXT（进下游 {{upstream}}）/ error / feedback TEXT（打回原因+人工上下文，进 {{feedback}}）/ gate_result(pass/rework/fail/human) / session_id（平台智能体节点会话）/ resume_session_id（CLI 续跑会话）/ child_run_id / artifacts快照引用 / usage / 时间戳。

### 3.6 sop_artifacts（产物）

run_id / node_id / attempt / name / path（工作区相对路径）/ media_type / size / sha256 / storage（工作区文件 + 可选归档到绑定 KB 成为知识文档——打通"交付物沉淀为知识"）。

### 3.7 sop_cli_credentials（CLI 凭据）

owner_user_id / cli_kind / api_key（加密存储，沿用 Know 现有凭据加密机制）/ base_url / default_model / extra JSONB。每种 CLI 每人一份；运行时注入私有 HOME（§7.2）。平台智能体节点的模型凭据沿用 Know 现有模型管理，不在此表。

### 3.8 审批与通知

不新建审批表：approval 节点状态落 `sop_node_runs.waiting_approval`；平台智能体节点内部 HITL 沿用 `agent_prompts` + `agent_run_suspensions`（000141）。审批中心是二者的**聚合视图**。通知复用 `notifications` 表（000150），新增 kind：`sop_approval` / `sop_run_finished` / `sop_run_failed`。

### 3.9 sop_item_clarifications / sop_item_activities（详情抽屉支撑）
| 表 | 字段 | 用途 |
| --- | --- | --- |
| sop_item_clarifications | item_id / question / answer / status(open/answered) / source(ai/human) | 「澄清」tab：AI 结合知识库与附件生成问题，人工回答；**新建事项模态的 AI 拆解追问问答在创建时直接 seeding 为 answered**；已回答问答在下次运行组装任务书时注入 `{{humanContext}}`（与审批补充上下文同通道） |
| sop_item_activities | item_id / kind(business/audit) / actor / action / detail / created_at | 「活动」tab 取 business（创建/触发/审批/重试/状态变更，与运行事件合并展示）；「操作日志」tab 取 audit（编辑/关闭/删除/导出日志等管理动作）。只增不改不删 |

事项详情抽屉参照 LoopForge 需求详情（宽抽屉 + 顶部 tab）：**概览 / 执行进展 / 产物 / 会话 / 澄清 / 交付 / 验收 / 活动 / 操作日志** 九个 tab。概览首屏为运行状态面板（失败=红：停在哪+补充上下文重试失败节点+整条重触发+排查记录+事项管理；待审批=橙；推进中=绿；成功=绿+产物/验收入口；未触发=灰+模板选择+发起），下接字段卡网格（责任人/优先级/来源/关联知识库/触发方式/关联 SOP/代码仓库/工作区）、描述卡、「描述不够清楚？帮我问清楚」澄清入口。**父事项抽屉**在九 tab 之前多一个「子事项」tab（子项列表：编号/标题/状态/执行情况/进度/最近运行，支持批量触发、批量转 todo、把子项移出父项）。**子事项**抽屉不出现「子事项」tab，其余九 tab 一致，并在概览字段卡显示「所属大项」（可点击跳父项）。

### 3.10 sop_project_notes（项目黑板）+ 《<项目名> 项目简报》（知识库投影）

**问题**：SOP 节点的上下文只沿依赖边向下流。节点不知道项目里其它事项的进展、历史决策与风险，平台智能体的 KB 检索是机会式的（向量召回靠运气、有索引延迟），CLI 节点还被私有 HOME 隔离、跨 run 无记忆。

**解法分两层，职责不重叠**：

| 层 | 载体 | 谁写 | 特性 |
| --- | --- | --- | --- |
| 黑板（唯一写入源） | `sop_project_notes` 表 | 人工记一条 / 审批通过与打回自动落 / 运行终态自动落 / AI 总结**由人确认后**落 | 可编辑、可置顶、可软删；每条带来源可追溯 |
| 简报（可重建投影） | 项目 KB 中固定一篇《<项目名> 项目简报》Markdown（id 存 `sop_projects.brief_knowledge_id`） | 只由引擎渲染覆盖，文档头写明「自动生成，请勿手改；要补充请在项目动态里记一条」 | 丢失可重建；手改会被下次渲染覆盖，行为可预期 |

`sop_project_notes` 字段：`project_id / kind(decision|run_summary|risk|release|note) / title / body / item_id / run_id / source(human|engine|ai) / pinned / seq(项目内自增) / created_by / created_at / deleted_at`。**只增不改不删（软删）**，`seq` 用于渲染增量判定与排序。

**渲染规则**：简报按「项目概况（名称/编号前缀/绑定 KB/默认 SOP）→ 当前在工作的事项（按执行情况分组，最多 20 条）→ 最近决策（pinned 全带 + 其余最近 8 条）→ 最近风险 → 最近运行结论（8 条）」组装 Markdown，硬顶约 4000 字；超限时截断低优先级段落并在文末注明「完整内容见项目动态」。渲染触发：黑板新增（进程内 2 秒 debounce 合并）或项目 KB 补绑；`brief_rendered_seq == max(seq)` 时跳过，避免空转触发重新分块与嵌入。渲染失败只记日志与黑板 `risk` 告警项，**不反向影响运行**。

**各智能体如何知晓**（三路并行，不依赖单一机制）：

| 通道 | 内容 | 何时 |
| --- | --- | --- |
| 任务书「项目现状」块（§4.3 第 6 块） | 最近 6 条黑板（决策优先）+ 兄弟事项进度摘要（`3/7 已完成，KNOW-11 正在评审`），硬顶 1500 汉字 | 每次必带 |
| `inputs/project-brief.md` | 简报全文物化进运行工作区，任务书告知路径 | 每次运行 |
| 知识库检索 | 简报以 `publish` 进项目 KB，平台智能体绑定 KB 天然可命中；CLI 节点 `kb_scope=project` 覆盖 | 补充通道（有索引延迟） |

**运行终态自动落**：默认写**确定性摘要**（事项标题、状态变化、各节点终态、产物清单与 sha256 前 8 位、审批意见原话），**不调大模型**（零成本零幻觉）；项目配置 `auto_note_on_run_end` 开关默认开。语义级「结论/取舍」总结走事项抽屉「AI 总结成一条决策」：生成草稿填入输入框，**人确认才写入黑板**（`source=ai`）。

**并发与一致性**：黑板追加走事务；多 run 同时收尾时简报渲染 debounce 合并；渲染用 `UpdateManualKnowledge` 覆盖同一 `brief_knowledge_id`，首次未绑 KB 时不渲染、补绑后全量渲染一次。

---

## 4. SOP 引擎（确定性推进）

新包 `service/sop/engine`，预估 3-5k 行。单实例进程内调度循环 + DB 状态推进，不依赖 Redis/asynq（桌面 Lite 无 Redis 也可跑）。

### 4.1 调度循环

照抄 automation_scheduler 的成熟模式并简化：`claimDue`（CAS 抢 queued run）→ `startQueued` → 节点推进 → `expireLeases`（回收崩溃租约，run 置 failed 可重试）→ `reconcileAwaiting`（审批超时策略：默认无限等待，可配超时自动 failed+通知）。心跳续租照抄 automation_runner.go:85（goroutine 定期续 lease_until 并响应 cancel/pause 控制位）。

### 4.2 节点推进规则

- 节点声明依赖（edges），**全部上游 succeeded 后自动就绪**；引擎按拓扑推进，无依赖关系的就绪节点默认并行执行；节点可声明 `serial_group`，同组内强制串行（如共用同一 CLI 凭据限流）。
- 节点执行前：`attempt++`，物化上游产物到工作区 `inputs/` 目录（借鉴 materializeInputs），组装任务书（§4.3），交执行器。
- 节点执行后：① 读结构化终态 gate_result（pass/rework/fail/human，缺失时 CLI 恢复同 session 追问一次，§4.3）→ ② 收产物并按 ArtifactContract 逐文件校验（存在/常规文件/大小上限，借鉴 artifacts.go:240-265）+ 各产物 check 编译的门禁 → ③ 执行 gate_commands 余下条目（含 gate_extra，合并单脚本 `set -e`）→ 综合定终态。
- **失败策略**（借鉴 node_outcome.go:109-117）：
  - `loop`：同节点带 feedback 重试，受 loop_max_iterations 限制；
  - `reject`：自动打回 reject_to，目标及全部后代重置 pending，target.rework_count++；rework_count ≥ max_reworks 转 waiting_approval 交人裁定；
  - `fail`：run 置 failed；
  - `human`：转 waiting_approval。
  - 执行器异常（非门禁失败）且 attempt < max_attempts：自动重试，error 写入 feedback。
- **人工打回**（借鉴 run_ops.go:271-351）：`Reject(runID, nodeID, targetID, reason, context)`——targetID 默认节点 reject_to，可指定任意上游；reason+context 写入目标节点 feedback，返工时经 {{feedback}} 进任务书；carry_over=true 保留上轮产物目录供对比。
- **人工放行**：`Approve(runID, nodeID, humanContext)`——节点置 succeeded，decision 落盘 `decision.md` 并产物化，humanContext 累积进 run。
- **暂停/恢复/取消**：pause_requested 在节点边界生效（不强杀执行中节点）；resume 继续；cancel 终止执行中节点进程（进程组级，§7.4）并置 cancelled。
- **从任意节点重跑**：借鉴 Retry(run_ops.go:359)——目标节点及后代重置 pending，attempt 保留历史快照，上游产物不动。
- **子 SOP**：sop 节点创建 child run（trigger_kind=child_run），父节点等待子 run 终态；子 run 产物按输入映射回传父工作区。

### 4.3 任务书组装（借鉴 prompt.go:24-93 / 197-223）

任务书正文为**大白话 prose**（检查器「这一步做什么」只写要做的事，不插变量、不写表达式）；运行时由引擎自动附带上下文块，用户不感知占位符：

| 上下文块（白话名） | 内容 | 附带时机 |
| --- | --- | --- |
| 事项背景 | 事项标题/描述/附件文本/运行输入 | 每次必带 |
| 上游步骤产物 | 各直接上游节点 output 摘要（按节点名分节）+ `task_refs` 勾选节点的完整输出 | 每次必带 |
| 返工意见 | 上轮失败原因 / 打回意见 | 返工时 |
| 人工补充意见 | 人工审批累计补充 + 事项澄清已回答问答 | 审批/澄清后 |
| 产出目录约定 | 工作区产物/输入目录绝对路径 + 「写到第 4 步约定路径」指引 | 每次必带 |
| 项目现状 | 最近 6 条黑板（决策优先）+ 兄弟事项进度摘要 + 当前项目简报路径 | 每次必带（硬顶 1500 汉字，见 §3.10） |

引擎内部仍经占位符通道渲染（{{input}}/{{upstream}}/{{node.X.output}}/{{feedback}}/{{humanContext}}/{{artifacts_dir}}/{{inputs_dir}}，兼容含占位符的旧发布模板），但**检查器不提供任何变量插入入口**：第 2 步 = 大白话 textarea + 「这一步做什么」小标题右侧 ? 气泡（悬停/键盘聚焦显示运行时自动附带的四类上下文及附带时机：事项背景/上游步骤产物每次必带、返工意见返工时带、人工补充意见审批/澄清后带；不再占用 inline 只读卡）+ 可选「额外参考哪步的产物」多选下拉（步骤名清单，写 `task_refs`，默认空=上游自动带）；删除节点时同步清理其他节点 task_refs 中对它的引用。

任务书结构：角色与目标 → 交付物清单（ArtifactContract 附录）→ 门禁说明 → 重试/打回策略告知 → 结果提交方式。平台智能体节点额外注入系统提示（角色 agent 的 SystemPrompt + Rules + Skills，经 skills.Manager 渐进披露）；CLI 节点系统提示写 `agent-system.md`（借鉴 materialize.go:113-137），按 CLI 能力原生加载或 `<system_instructions>` 前置包裹。**CLI 角色来源（可选）**：节点 `executor.role_id` 选中 custom_agents 角色时，`agent-system.md` = 该角色 SystemPrompt + Rules + Skills 渲染 + 节点任务书「角色与目标」段；未选角色时仅来自节点任务书。**角色在 Know 智能体管理页维护，SOP 仅引用不复制**：运行冻结的是模板结构与 role_id 引用，提示词正文在节点执行时按最新智能体配置渲染（修改角色影响后续执行，已完成运行不受影响）。角色只是提示词资产复用，不改变执行引擎（仍是本机 CLI 子进程），也不继承该角色的模型/MCP/KB 绑定（模型走节点覆盖或凭据默认，MCP 走节点 `mcp` 子集、KB 走 §5.2/§9 注入规则）——智能体管理里的知识库关联不会带入 CLI 节点，CLI 检索范围只由节点 `kb_scope` 决定。

**结果提交与校验顺序（检查器第 4 步内置展示，只读不可关）**：① 先提交结构化终态——平台智能体节点注册专用工具 `submit_node_result(pass|rework|fail|human, summary)`（节点运行期临时注入工具集）；CLI 节点约定写 `submit_gate_result.json` 文件（借鉴 LoopForge），缺失时恢复同 session 追问一次（借鉴 node_execute.go:845-876），仍缺失则交门禁裁定。终态四值对应用户语言：pass=完成 / rework=需返工 / fail=失败 / human=需人裁定。② 再校验产物——按 ArtifactContract 逐文件校验 + 各产物 check 编译出的门禁命令。③ 最后触发额外门禁——gate_extra 高级命令。检查器把 ① 做成只读终态标签卡（「先汇报完成状态」+ 四个终态标签，「没汇报会自动恢复同一会话再问一次」提示收进小标题右侧 ? 气泡），② 做成每个交付物的「校验方式」下拉，③ 折叠进高级，用户无需理解 gate_result / gate_commands 等工程概念。

**节点级规则与能力渲染**：任务书「遵循规则」段 = 角色 Rules（预览）+ 节点 `extra_rules`（大白话补充）；Skills 按节点 `skills` 子集过滤（null=角色全部）后经 skills.Manager 渐进披露（平台智能体）或渲染进 agent-system.md（CLI）；MCP 按节点 `mcp` 子集注入（§5.2 / §9），默认仅 know-kb。

### 4.4 工作区

每次运行独立目录 `<workspace_root>/runs/<run_id>/`：`inputs/`（上游产物物化）、`out/`（节点产物）、`work/`（CLI 自由工作区）、`.sop/private-home/<cli_kind>/`（私有 HOME）、`logs/`（节点日志/流事件）、`decision.md` 等。可选 git 检查点：复用 sandbox/workspace 的 workspace.git 机制，每节点终态 commit 一次，支持产物 diff 回溯（默认开启，纯本地无远程）。

**代码仓库克隆**：发起运行时把项目 repos 快照进 run（后续解绑不影响进行中运行）。首个 agent/script 节点执行前，把快照仓库 `git clone --branch <branch> --single-branch` 到 `work/repos/<name>/`（浅克隆 `--depth 1` 可选），并创建工作分支 `sop/<run_id>`；CLI 节点在该分支上提交，产物提取与 git 检查点照常。克隆失败=节点失败（可补充上下文重试），凭证走本机 git 凭证管理，不注入明文。未绑定仓库不影响文档/方案类交付（工作区照常创建）。运行详情「运行信息」展示克隆仓库与工作分支。

---

## 5. 执行器抽象

```go
type Provider interface {
    Kind() string                                   // platform_agent | cli:<kind> | script
    Prepare(ctx, NodeSpec, RunWorkspace) error      // 凭据/HOME/配置物化
    Execute(ctx, TaskBook, ExecOptions) (ExecResult, error)
    // ExecResult: Output / GateResult / Artifacts / Usage / SessionID / StreamEvents
    Cancel(ctx) error
}
```

流事件统一翻译为内部事件模型（text_delta / tool_call / tool_result / result / error），SSE 推给运行详情页；CLI 侧以 Claude stream-json 为归一化目标（借鉴 agent_cli_stream.go:162-194 的翻译层设计）。

### 5.1 平台智能体执行器（agent.go）

- 取 custom_agents 配置（提示词/模型/工具白名单/Skills/MCP/KB 绑定），以 **RunModeDeferred + Suspendable** 调 agentQA（照抄 automation_runner.go:464 的脱连接后台模式），per-node 新建会话（session 落 sop_node_runs.session_id，可在运行详情里展开完整对话轨迹）。**节点级覆盖**：`skills` 子集过滤角色 Skills（null=全部）、`mcp` 子集限定本节点可用 MCP（默认仅 know-kb，角色其余 MCP 绑定不注入）、`extra_rules` 追加进系统提示；模型/KB 绑定仍继承角色配置。
- 工作区接入：节点运行期把 run 工作目录注入 agent 的文件/shell 工具根（桌面本地执行上下文，复用 localsandbox 可用后端；Windows 上文件工具直接走受控本地路径，shell 工具受危险命令黑名单约束）。
- HITL：agent 内部审批 prompt 经现有 broker 落 agent_prompts，审批中心聚合展示；挂起/恢复走 agent_run_suspensions 快照（已验证机制）。
- 知识能力：节点 agent 绑定的 KB 检索照常可用——这是平台智能体相对 CLI 的差异化优势。
- 迭代预算：节点级 max_iterations 覆盖 agent 默认值，防止单节点无限循环。

### 5.2 CLI 执行器（cli/，全家桶 10 种）

**适配器清单与协议**（借鉴 LoopForge 实测结论，逐种实测后固化）：

| CLI | 模式 | 启动参数要点 | 输出协议 | 凭据注入 |
| --- | --- | --- | --- | --- |
| opencode | 通用 | `run --format json --thinking [-m <model>] [-s resume] <prompt>` | part.type/step_finish 收尾 | OPENCODE_CONFIG_CONTENT 整段 JSON |
| cursor-agent | 通用 | `-p --output-format stream-json --stream-partial-output --trust --force --approve-mcps [--model\|--resume]` | timestamp_ms 增量、result 终止 | CURSOR_API_KEY 环境变量 |
| antigravity(agy) | 通用 | `--output-format stream-json --dangerously-skip-permissions --disable-slash-commands -p` | event=init/step_update/result.status | .gemini/antigravity-cli/settings.json + GEMINI_API_KEY |
| kimi | 通用 | `--output-format stream-json [-S resume] -p` | 整消息、meta.session.resume_hint | .kimi-code/config.toml |
| pi | 通用 | `--mode json --session-id <uuid> [--thinking] [--append-system-prompt 文件]` | message_update/agent_end | .pi/agent/models.json |
| dsh(DeepSeek) | 通用 | `--profile headless --json --permission-mode danger-full-access --approval-policy never` | final.reason.kind 判成败、事件截断 8KiB | .dsh/ 模型 patch（--patch 插 profile 参数后） |
| zcode | 通用 | `--prompt <p> --output-format stream-json --mode yolo --no-color --disallowed-tools …` | stream-json | .zcode/cli/config.json |
| claude | 专用 | headless print + stream-json，prompt 可走 stdin | Claude stream-json（归一化基准） | 环境变量/配置引用 |
| codebuddy | 专用 | headless + JSON 流 | 翻译为基准 | 配置引用 |
| codex | 专用 | `exec --json` 系 | 翻译为基准 | 配置引用 |

**通用机制**（全部借鉴 LoopForge 已验证设计）：

- **私有 HOME**：`<run_workspace>/.sop/private-home/<cli_kind>/`，os.Root 逐段防符号链接（agent_cli.go:332-367）；API Key 只进环境变量（如 `SOP_CLI_API_KEY`），配置文件仅引用变量名，不落明文 Key（agent_cli.go:374-516 模式）。
- **prompt 传递**：默认 argv；超过阈值改写文件并传路径（LoopForge 96KiB；**Windows 命令行上限约 32KiB，阈值降为 24KiB**，claude/codebuddy 优先 stdin）。
- **系统提示**：opencode/pi 原生加载文件，其余 `<system_instructions>` 前置包裹。agent-system.md 内容 = 角色 SystemPrompt + Rules + **按节点 `skills` 子集过滤后的 Skills**（null=全部）+ 节点 `extra_rules` + 任务书「角色与目标」段。
- **MCP 注入**：按各 CLI 配置路径表渲染（mcp_provision.go:111-223 模式）；**只注入节点 `mcp` 子集勾选的 server**（检查器复选框，默认仅 know-kb；不继承角色在智能体管理里的 MCP 绑定）+ **Know 自身 MCP endpoint**（让 CLI 能检索知识库，§9）；**Know MCP 的检索范围按节点 `executor.kb_scope` 渲染**：`project`=项目绑定 KB（事项级覆盖优先）、`custom`=`kb_ids` 指定清单、`all`=全部知识库，范围随注入配置下发并在 MCP 服务端强制校验；HTTP MCP 凭据替换为 Bearer 头。stdio MCP 首版仅允许本机已安装命令，不做安装脚本托管。
- **成败判定**：无 result 帧且 exit 0 → 不判失败，交门禁裁定；max-turns 类错误转 human（借鉴 runner.go:101-144）。
- **取消/排水**：收到终态后 2.5s 排水即 cancel；进程组级终止（§7.4 Windows 适配）。
- **续跑**：支持 resume 的 CLI（opencode/cursor/kimi/pi/dsh）记录 resume_session_id，返工/重试优先续会话。
- **记忆边界（明确决策）**：私有 HOME 按 run 隔离，CLI 原生记忆（历史/CLAUDE.md 等）不跨 run 持久；同一 run 内返工靠 resume_session_id 续会话；跨 run 的"记忆"只走事项层——澄清问答与审批意见经 `{{humanContext}}`、上游产物经 `inputs/` 进入新运行任务书。**不开放持久 HOME / 跨 run CLI 记忆开关**；知识库绑定是检索范围，不是记忆。
- **安装**：首版不托管 CLI 安装（LoopForge managedcli 不搬）；设置页检测 PATH 中二进制，缺失给出安装指引；凭据页按 CLI 配 Key/BaseURL/模型。

### 5.3 脚本执行器（script.go）

本机子进程跑 shell（Windows 走 Git Bash，Know 部署已依赖），Dir=run 工作区，受控 env 叠加 + credential_env 注入，输出落 logs/ 并 SSE 直播，exit code 定成败。确定性步骤不用 AI。

---

## 6. 人工关口与审批中心

- **approval 节点**：run 停 waiting，节点置 waiting_approval，发 notifications（kind=sop_approval，link 直达运行详情）；操作三选一（借鉴 LoopForge 运行详情）：**确认通过**（可附补充上下文）/ **补充信息重跑当前节点** / **打回上游**（选目标节点+理由）。
- **审批中心页**：聚合三类待办——SOP approval 节点、平台智能体 HITL prompts（agent_prompts）、automations awaiting_approval；统一列表 + 直达处理。现有「待我确认」类入口统一指向这里。
- **审批超时**：默认无限等待（个人产品，人不在就停着）；模板可配超时策略（通知提醒 / 自动 failed）。
- **留痕**：每次审批 decision（人/时间/结论/意见）落盘 decision.md 并产物化，运行详情时间线可见。

---

## 7. SOP 任务（SOP 工作台顶部 Tab，现有自动化不改动）

**不改动现有 automations 表、runner 与前端页面**：现有对话类自动化（触发器 cron/once + 时区 + misfire/overlap、租约心跳、审批恢复）原样保留。SOP 的定时派发收进 **SOP 工作台顶部 Tab「SOP 任务」**（第五个主 Tab），不新建独立页面、不占用自动化模块入口。

**每项目一份触发配置**（参照 LoopForge 触发配置模态），新表 `sop_trigger_configs`（project_id 唯一）：

| 字段 | 说明 |
| --- | --- |
| mode / interval_key / cron | 调度二选一：常用间隔（15m/30m/hourly/daily9/weekly1）或 Cron 表达式（时区沿用 automation 触发器解析，最小间隔 5min） |
| default_template_id | 默认 SOP 模板 |
| prefer_item_template | 优先使用事项已绑定的 SOP（没有则用默认），默认开 |
| sources | `all`=同步所有已配置来源，或单一事项来源键（customer/internal/data/competitor/strategy/other） |
| statuses JSONB | 触发哪些状态（待梳理/待启动/进行中/评审中/受阻 多选）；已完成/已关闭永不触发 |
| per_run_limit | 单次最多触发事项数 1–10，默认 10；其余事项后续执行继续触发 |
| enabled / last_run_at / next_run_at | 启用开关与调度游标 |

**扫描派发语义**：到点扫描本项目事项，命中条件 = 状态∈statuses ∧ 来源匹配 ∧ **尚未跑过 SOP**（无关联 run）；运行中/已跑过自动跳过。按 per_run_limit 截断，逐事项创建 run（trigger_kind=schedule，trigger_ref_id=trigger_config.id），模板取 `prefer_item_template ? item.template_id : default_template_id`。**逐事项幂等**（借鉴 ReserveIssueDispatch：同一事项同一调度槽已派发则拒绝）。修改配置保存即生效；「立即运行」= 服务端手动扫描一次，关闭页面不中断（后台 goroutine + 租约）。

**调度实现**：sop 模块内新增轻量 trigger scheduler，复用 automation 的触发器解析/claim/租约/心跳工具函数（不新建调度框架、不动 automation_runner）。每次扫描写 `sop_trigger_logs`（config_id / kind=auto|manual / scanned / fired / skipped / note / created_at），即页面「触发历史」。派发的 run 审批待办统一进 SOP 工作台「审批中心」；运行记录触发来源显示「SOP 任务」可回溯。

---

## 8. 运行记录与可观测

运行详情页（核心页面）：

- 节点图实时状态（画布只读渲染，节点着色：pending/running/waiting_approval/succeeded/failed）；
- 节点抽屉：任务书、流事件/对话轨迹（平台智能体节点可展开完整会话）、日志、产物列表（预览/下载/源码）、attempt/rework 历史、gate 结果；
- 操作条：暂停/恢复/取消/从节点重跑/审批/打回；
- run 级：输入、human_context 时间线、usage 成本、耗时、触发来源、事项链接。

**产物预览弹窗（HTML 渲染，大弹窗）**：事项抽屉「产物/交付」、运行详情「产物」、PM 归档「资料」三处的预览统一打开宽弹窗（min(1120px, 94vw)，高 ≤84vh，正文独立滚动）：

- 顶栏：产物名 + 路径/大小 + sha256 快照标签 + 已入库标签；「HTML 预览 / 源码」分段切换、复制源码、下载源文件、入知识库按钮。
- .md 产物默认渲染为 HTML 文档视图（白纸版式：标题/表格/代码块/引用/列表），源码视图展示含 frontmatter（title/source_node/run/sha256/created）的 .md 原文；.txt/.log 渲染为等宽文本，.diff 按增/删/hunk 着色，.json 美化展示，.html 产物用沙箱 iframe（sandbox=""）渲染交互原型；zip 等二进制附件提示「不支持在线预览，到工作区下载」，不提供渲染/源码切换。
- **入知识库的是源文档本身（.md）**，不是渲染后的 HTML：走现有 ingest 链路，frontmatter 保留来源追溯；入库后产物行与弹窗顶栏出现「已入知识库」标签、按钮置灰为「已入库 ✓」，重复打开状态保持。
- 复制源码失败时自动切到源码视图并全选文本（提示 Ctrl+C），保证任何环境都有可用复制路径；下载以 Blob 落源文件（文件名=产物路径 basename）。

列表页（参照 LoopForge 运行记录页）：

- 页头：标题 + 实时指示（● n 个运行进行中 / 暂无进行中的运行）；右侧成功率（已结束运行中 succeeded 占比）+ n 次已结束、累计 Token + 总次数、刷新按钮。
- 检索区：**状态分段 chips 带计数**（全部/进行中/排队中/已暂停/待确认/已完成/失败/已取消，计数为全局按状态统计，失败计数红色）+ 模糊搜索（运行 id / 事项标题 / 事项编号 / SOP 名）+ 「全部 SOP」模板下拉；三者可组合。
- 列表字段：运行 id / 关联事项（编号+标题）/ SOP（冻结版本）/ 触发来源 / 状态 / 进度 / 开始时间 / Token 与成本 / 详情入口；失败行一键重试进详情操作条。
- 空态：「没有匹配的运行 · 换个关键词，或到事项列表查看对应事项的执行结果」；页脚共 n 条。
- 运行推进时列表自动刷新，但保留检索输入框焦点与光标（不打断输入）。

统计沿用 Know 现有口径，新增 SOP 维度（交付量/成功率/平均耗时/token 成本，按 metric_category 分组）。

---

## 9. 知识库与 MCP 联动

- 项目/事项绑定 KB：平台智能体节点天然可检索（沿用 agent 自身 KB 绑定）；CLI 节点通过私有 HOME 注入 **Know MCP endpoint**（Know 已有 MCP server 与 API Key 能力体系），CLI 内可 search_knowledge/read_document——外部 CLI 也能"懂你的知识"。**CLI 检索范围节点级可配**（`executor.kb_scope`）：跟随项目（默认，事项级覆盖优先）/ 指定知识库清单 / 全部知识库，设计器检查器在 CLI 执行器下提供该下拉；范围只是检索授权，不构成跨 run 记忆（§5.2 记忆边界）。
- **项目知识库生命周期**：新建项目默认自动建库（同名、写入租户默认 Embedding 模型）；`kb_owner=project` 的库随项目删除，`linked`/`pm` 的库只解绑不删；未绑定 KB 时功能降级但不阻塞（黑板与 `inputs/project-brief.md` 照常，产物归档与简报同步暂缺）。
- **项目简报同步**：黑板（`sop_project_notes`）为唯一写入源，《<项目名> 项目简报》是它在项目 KB 中的覆盖式投影（§3.10），文档头标注自动生成，手改会被下次渲染覆盖。
- 产物归档：节点产物可一键（或模板声明自动）归档为绑定 KB 的知识文档，走现有 ingest 链路——交付物沉淀为知识，形成"知识→作业→新知识"闭环。**入库对象是源文档（.md 含 frontmatter 来源信息），HTML 仅为预览渲染视图不入库**；预览弹窗见 §8。
- MCP 对外：SOP 工作台能力（列模板/触发运行/查运行/审批）经 Know 现有 MCP endpoint 暴露为新工具组，API Key 新增能力 `manage_sop` / `run_sop`（沿用 tenant_api_key 能力枚举模式），外部 Agent 可程序化驱动 SOP。

---

## 10. 前端设计

技术栈沿用 Vue3 + Pinia + vue-router + TDesign；**新增依赖 `@vue-flow/core`**（画布，Vue3 原生）；Markdown/产物预览复用 PmMarkdown（含 Mermaid 渲染）。视觉与布局沿用 Know 现有主题与壳结构（参照现网 PM 工作站形态）。

**入口与壳结构**（参照现网 PM 工作站截图确认）：

- 全局侧边栏保持 Know 现有导航（新对话/知识库/墨匣/方案助手/对话产物/智能体/会议室/自动化/工具箱/共享空间 + 会话历史），**「PM 工作站」入口原位替换为「SOP 工作台」**，不新增侧栏分组；
- SOP 工作台页面内用**顶部 Tab 切换五个主视图：事项 / 流程模板 / 运行记录 / 审批中心 / SOP 任务**（审批 Tab 带待办角标），Tab 栏右侧为**项目切换器**（列出全部项目：当前项 ✓ + 编号前缀，点击即切换；底部「＋ 新建项目…」创建模态，见 §3.1）与 ⋮ 菜单；
- **执行器设置、PM 归档**收进 ⋮ 菜单（非主 Tab），进入后为带返回链接的子页；
- 画布设计器、运行详情为子页：保留所属主 Tab 高亮（流程模板/运行记录）+ 面包屑返回；
- **SOP 定时派发收进 SOP 工作台**：顶部 Tab「SOP 任务」（见 §7）；**Know 现有「自动化」模块完全不改动**（表/runner/前端均不动）。

路由 `/platform/sop`（SopLayoutView 壳 + Tab 子路由）：

| 页面 | 职责 | 借鉴/复用 |
| --- | --- | --- |
| SopItemBoard（Tab·事项） | 参照 LoopForge 需求列表：**项目统计带**（工作项总数 + 执行情况五态计数：尚未执行/正在推进/需要介入/待处理 PR/已闭环，口径见 §3.2；**每格可点击=按该执行情况筛选列表**，再点取消，选中格品牌色描边高亮；附绑定知识库与默认 SOP，入口按钮进项目配置）；**检索区**（模糊搜索标题/编号/描述 + 状态/责任人/来源/类型四个下拉 + **执行情况可搜索单选下拉**：触发器显示当前选中态、展开含搜索框与「全部执行情况+五态」选项各带实时计数，搜索只匹配选项名、Enter 选首个匹配、Esc/点外部收起，选中即过滤列表与看板并同步统计带高亮）；来源全 UI 中文映射（§3.2）；**列表/看板双视图**（列表默认，看板按状态列拖拽，均受全部筛选条件约束）；列表字段：编号(no)/标题+类型+状态/来源(中文)/责任人/优先级/关联 SOP/当前环节/进度/操作（处理失败·触发·查看运行）；**父事项行**在标题前加「▸」折叠箭头（展开行内缩进显示子项），「关联 SOP」列显示「—」，「当前环节/进度」列换成子项徽标 `已完成/总数` 与子项执行情况小色点（口径见 §3.2 父子拆分语义）；批量操作勾选父项=对其全部子项批量触发/关闭；**批量操作**（勾选后批量触发 SOP/批量关闭，已在运行中的事项跳过）；**事项详情宽抽屉**参照 LoopForge 需求详情：九 tab（概览/执行进展/产物/会话/澄清/交付/验收/活动/操作日志），概览含运行状态面板（失败重试/整条重触发/排查/事项管理）+ 字段卡 + 澄清入口，详见 §3.9 | 吸收 PmRequirementBoard + PmRequirementDetail/Form；检索/统计/字段/详情布局参照 LoopForge 需求列表与详情 |
| SopItemCreateModal（宽模态 980px） | 参照 PM 工作站「编辑需求」双栏：左表单（标题* / 初步想法 textarea 带 0/2000 计数 / 来源*+提出人 / 优先级 P0-P3 分段默认 P2 / 附件 / 类型+流程模板）；右栏 **AI 需求拆解**面板（**与左表单等高拉伸、不留底部空白**：宽 380px、min-height 520 / max-height 84vh−140，消息区独立滚动并自动滚到底，输入框+发送固定面板底部恒可见，长对话不撑破模态；智能体下拉=真实 custom_agents；空态+开始拆解；追问 1-2 轮关键细节，Enter 发送回答，可跳过追问直接拆解；产出拆解卡=目标+**建议子事项清单（每条含标题/一句话范围/建议执行器类型，带勾选框）**+验收口径；**「写入描述」把拆解结果以结构化块追加进左栏描述**，重复写入为覆盖不重复；**「拆成子事项」把勾选的条目直接创建为子事项**（`split_source=ai`，状态 backlog，两个按钮互不排斥）；完成后补充背景记入 domain_data.ai_notes）；底部 取消/保存草稿(backlog)/确认事项(todo)；追问问答创建时 seeding 进 sop_item_clarifications | 借鉴 PM 工作站 AI 需求分析交互；拆解走平台智能体 SSE |
| SopTemplateList（Tab·流程模板） | 模板库：内置（只读+复制）/项目/全局；版本历史 | LoopForge 流程模板页 |
| SopTemplateDesigner（子页） | vue-flow 画布：拖节点（agent/script/approval/sop）、连线、**五步傻瓜式节点检查器**（参照 LoopForge 检查器但全面去工程化，360px）：**① 这一步由谁执行**——执行器二选一（平台智能体=Know 内置全套能力 / 本机编程 CLI），角色下拉=真实 custom_agents 列表，选中出角色卡（头像/简介/模型/产物数/校验数/失败策略四格统计 + 「编辑角色 prompt」只读预览弹窗→跳智能体页）；CLI 另含 CLI 下拉+凭据+**模型下拉（选项=Know「模型管理」已配置模型 + 默认档，禁止手输）**+**角色（可选）下拉**（仅复用规则与技能作系统提示，引擎仍是 CLI）+**知识库范围下拉**（跟随项目/指定/全部）+私有 HOME 提示；**② 这一步做什么**——大白话 textarea（只写要做的事，无变量插入入口）+ 小标题右侧 ? 气泡（悬停/聚焦显示四类自动附带上下文及附带时机）+ 可选「额外参考哪步的产物」多选下拉（步骤名清单，写 task_refs，默认空=上游自动带）；**③ 遵循什么规则 · 用哪些能力**——角色 Rules 预览 + 节点补充规则输入 + 技能复选框（默认全选=角色全部）+ MCP 复选框（默认仅 Know 知识库）；**④ 产出什么 · 怎么校验**——子步「先汇报完成状态」（pass/rework/fail/human 四终态标签只读卡，缺失自动续会话追问提示收进标题右侧 ? 气泡）+ 每交付物卡片（名称/路径/必须交/校验方式下拉：文件交了就行·必须包含关键词·自定义命令）+ 高级额外门禁折叠；**⑤ 失败了怎么办**——四张大白话策略卡（⟳原地重试/↩打回上游/⏸停下等人/✕标记失败，各带一句解释），选中展开对应参数（重试次数/打回哪一步下拉），返工上限「超过转人工」+ 超时分钟 + 「返工时保留上一轮产物」开关。script 节点保留 shell 正文（高级）+ 产物/失败两节；approval 节点「关口会发生什么」白话说明 + 默认打回目标。**检查器标题栏**只保留节点类型标签 + 「✦ AI 配这一步」按钮，不重复显示节点名与内部节点 id（名称仅在标题栏下方的「步骤名称」输入框维护，避免长名换行挤压标题栏）。**排版紧凑化**：CLI+模型、知识库范围+指定知识库（custom 时）双列同行；技能与 MCP 用**多选下拉选择框**（触发器显示已选摘要：技能全选时「角色全部技能（N）」、否则顿号串；MCP 显示名称串；展开为复选列表浮层，点外部收起，勾选即时写回且浮层不关）替代竖排复选框；失败策略四卡 2×2 网格；**序号两级样式**：主五步用 18px 实心浅绿圆、第 4 步内部三个子步（先汇报完成状态/再检查交付物/最后跑额外校验）用 16px 空心描边小圆且标题行统一置于卡片边框外（内置状态卡只含终态标签）；**解释性说明统一收进 ? 气泡**：对应小标题/字段标签右侧加 ? 图标（模型/角色/知识库检索范围/第 2 步标题/额外参考/遵循的规则/MCP/先汇报完成状态/第 5 步标题/打回哪一步等），鼠标悬停或键盘聚焦弹出深色说明气泡（最大宽 280px、带指向箭头），移开/失焦/滚动/点击即隐藏；气泡在视口内横纵双向钳位，上方空间不足时翻转到图标下方且箭头始终指向图标；原 inline 解释提示行全部移除，仅保留单位短句（次/分钟）、空态引导、规则预览内容与高级折叠内提示；长解释另收进角色预览弹窗与高级折叠；节间距与字段间距收紧，360px 栏内五步一屏半可达。画布节点徽标显示执行器/产物数/失败策略；**节点双出口**（参照 LoopForge 节点卡）：卡片底部虚线分隔两行出口——绿点「成功」+ 右缘绿色端口、红点「失败打回」+ 右缘红色端口（已设打回目标时行内显示「→ 目标步名」）；先点出口再点目标节点左侧圆点完成连线（成功=依赖边；失败打回=写 reject_to，与检查器「打回哪一步」下拉双向同步），点自身左圆点取消连线；打回边为红色虚线、从红端口画到目标左圆点；边锚点按端口 DOM 实测位置计算（随缩放/节点高度自适应），节点加高后自动排版行距 170px；运行详情画布只读、不渲染出口；**画布快捷工具条**（参照 LoopForge，悬浮画布底部居中）：↩ 撤销 / ↪ 重做（快照式历史栈，上限 50 步；增删节点/连线/拖拽落点/检查器每次变更/AI 草稿应用/自动排版前入栈，视图操作不入栈；无历史时按钮置灰）｜ − / 百分比 / ＋ 缩放（20%–160%，按钮绕视野中心、滚轮绕光标）｜ ⛶ 适应画布（按节点包围盒居中缩放）｜ ⿻ 自动排版（按依赖边拓扑分层，层距 250px / 行距 110px，可撤销）｜ ▣ 小地图开关（右上角缩略图：类型色块+依赖线+当前视野框，点击跳转视野中心）｜ 图例「━ 成功 / ┄ 失败打回」内置工具条右端（左侧节点面板不再重复图例）；空白处拖动平移画布；运行详情画布只读、不带工具条；发布校验与版本发布（报错用白话，如「失败后选了打回上游，但没选打回哪一步」）；**AI 编排助手**右侧面板（画布/AI 编排分段切换、场景 chips、对话式草稿生成、**字段级差异卡预览（原值→新值+理由）与按节点/全部应用**、单节点模式 chip、应用到画布/放弃草稿，仅草稿不自动发布，详见 §3.3）；检查器标题栏「**✦ AI 配这一步**」按钮直达单节点零输入配置 | LoopForge flows.js 语义 + 五步检查器 + AI 编排面板，Vue 重写 |
| SopRunList（Tab·运行记录） / SopRunDetail（子页） | 列表页参照 LoopForge 运行记录：页头实时指示+成功率/累计 Token+刷新；状态 chips 带计数（全部/进行中/排队中/已暂停/待确认/已完成/失败/已取消）+ 模糊搜索（事项标题/编号/SOP/运行 id）+ 全部 SOP 下拉；字段含进度列；空态引导回事项列表；详见 §8 | LoopForge runs.js 语义与列表样式 |
| SopApprovalCenter（Tab·审批中心） | §6 聚合待办 | 新建 |
| SopTriggerTab（Tab·SOP 任务） | 触发配置摘要卡（调度/默认 SOP/来源/状态/单次上限/启用开关/上次下次）+ 触发历史表（时间/方式/扫描/派发/跳过/说明）+ 顶部「触发配置」「立即运行」；**触发配置模态**参照 LoopForge：调度=常用间隔/Cron 分段切换 + 间隔下拉或 cron 输入、默认 SOP 下拉 + 「优先使用事项已绑定的 SOP」勾选、同步哪些来源下拉、触发哪些状态多选（已完成/已关闭不触发）、单次最多触发事项数 1–10、底部启用勾选 + 保存触发配置（保存即生效） | 新增主 Tab；LoopForge 触发配置模态 |
| Automation（现有模块·不改动） | Know 现有自动化页面/表/runner 原样保留，本方案零改动；原型中仅以现有形态演示 | 不改动 |
| SopProjectSettings（⋮ 菜单 / 统计带 / 项目切换器子页） | 项目配置（参照 LoopForge 项目设置）：名称/描述、编号前缀（创建后锁定）、绑定知识库、默认 SOP 模板、默认执行器、运行成功后事项状态、工作区根目录、关口/失败通知开关；**代码仓库卡片**（独立于「保存配置」，保存即生效）：空态=虚线卡「还没有绑定代码仓库」+ ①粘贴 Git 地址 ②确认分支 ③保存即生效 + 「添加第一个仓库」；已绑定=仓库行（名称/分支 tag/默认徽标/地址 + 设默认/解绑）；添加模态=url（HTTPS/SSH 校验）+分支(默认 main)+显示名(可选自动取名)；第一个自动设默认，解绑默认自动顺延；解绑二次确认（不删已克隆工作区、不影响进行中运行）；事项概览/交付 tab 的「代码仓库」字段卡随绑定联动；**危险操作卡片**：删除项目（二次确认列明影响面；进行中/待审批运行时禁止；最后一个项目不可删；级联语义见 §3.1） | 新建 |
| SopSettings（⋮ 菜单子页） | CLI 凭据管理（10 种，PATH 检测+安装指引）、工作区根目录、危险命令黑名单 | 新建 |
| PmArchive（⋮ 菜单子页） | PM 遗留只读归档（§11） | pm 页面裁剪 |

事项分析对话（原 PmRequirementAnalysisChat 的 SSE 直连模式）保留为事项详情内的「AI 分析」入口：建真实会话流式对话，结论回写事项 domain_data——轻量探索不走 SOP，重交付走 SOP，与 LoopForge「对话 vs SOP」双模式同构。

---

## 11. PM 工作站吸收与迁移

### 11.1 数据映射（迁移脚本 000166+，手动执行约定沿用）

| PM 源 | 去向 |
| --- | --- |
| pm_projects | → sop_projects（kb_id/session 绑定保留；常驻会话转项目级 AI 分析入口） |
| pm_requirements | → sop_items：kind=requirement；状态映射 pending_analysis/analyzing→backlog、confirmed→todo、scheduled/developing→in_progress、launched→done、rejected→closed(domain_data 留 reject_reason)、closed→closed；ai_analysis/design_plan/session_id/knowledge_id/上线复盘字段 → domain_data + outcome（原样保留，不丢字段） |
| pm_requirement_attachments | → sop_item_attachments（同构直迁） |
| pm_versions / pm_version_requirements / pm_features / pm_feature_iterations / pm_materials / pm_plan_batches / pm_tasks | **冻结只读归档**：表与数据保留，新增 PmArchive 只读页（版本/功能树/资料/批次历史四个 tab，复用现有组件裁掉写操作）；不提供新功能。资料/PRD 类 knowledge_id 指向的 KB 文档不动，仍可检索 |

归档是冻结快照而非第二活数据源：PmArchive 无任何写路径，PM 需求自迁移日起唯一活源是 sop_items。迁移脚本幂等（按源 id 判重），迁移前自动导出 JSON 备份到 data/backups/。

### 11.2 PM 场景沉淀为内置 SOP 模板

- **产品方案交付**（内置）：需求分析(platform_agent，pm-requirement-analysis 技能) → 方案确认(approval) → PRD 生成(platform_agent，pm-prd-generator) → 原型生成(platform_agent，pm-prototype) → 评审(approval，可打回 PRD) → 归档(script：产物入 KB)。
- **需求上线复盘**（内置）：效果核对(script/agent) → 价值判定(approval) → 复盘报告(agent) → 回填事项 outcome(script)。
- 原 PM 的 asynq 后台产物生成链路（pm_version_artifact_ai）由 SOP 后台 run 替代；迁移完成后 PM 专属 asynq handler 下线。

### 11.3 路由与代码处置

`/platform/pm` 路由 301 到 `/platform/sop`（带事项定位参数）；routes_pm.go 写端点下线、读端点保留给 PmArchive；cli/skills/pm-* 5 个技能保留（SOP 平台智能体节点继续消费）。

---

## 12. API 设计（/api/v1/sop/*）

沿用 Know handler/router 分层与 tenant ctx 注入；SSE 复用 agent-chat 流式基建。

```
projects:   GET/POST /projects · GET/PATCH/DELETE /projects/:id（PATCH=项目配置；code 创建后拒绝修改；DELETE=级联删除事项/运行/触发配置/仓库绑定，存在进行中运行或仅剩一个项目时拒绝，磁盘工作区不自动删除）
            GET /projects/:id/stats（统计带：total/none/running/intervene/pr/closed，五态口径见 §3.2）
            POST /projects/:id/repos · DELETE /projects/:id/repos/:repo_id · POST /projects/:id/repos/:repo_id/default
            （代码仓库独立接口，即时生效不走 PATCH；url 校验 HTTPS/SSH + 去重；首个自动默认，删默认自动顺延）
items:      GET/POST /items · GET/PATCH/DELETE /items/:id · POST /items/:id/status
            GET /items?q=&status=&owner=&exec=&source=&kind=&view=list|kanban
            （q 模糊匹配 title/no/description；exec 按最近运行终态过滤；响应带派生字段 current_node/progress/exec_state）
            POST /items/bulk-launch · POST /items/bulk-close（body: ids[]；运行中事项跳过并返回 skipped）
            POST /items/:id/attachments · POST /items/:id/analyze(SSE 会话)
            POST /breakdown (SSE · 创建前预览：title/desc/kind/agent_id → 追问轮 + 拆解结果；不落库，确认创建时随 item 保存)
            POST /items/:id/outcome
            GET/POST /items/:id/clarifications · POST /items/:id/clarifications/:cid/answer
            POST /items/:id/clarify-generate（AI 生成澄清问题）
            GET /items/:id/activities?kind=business|audit
            POST /items/:id/retry-failed（body: context；失败节点置 pending 并注入反馈）
            POST /items/:id/relaunch（整条从头新运行）
templates:  GET/POST /templates · GET/PATCH/DELETE /templates/:id
            POST /templates/ai-orchestrate (SSE · mode=flow: message+画布快照 | mode=node: node_id+节点快照+上下游+inventory+hint → 编排草稿 ops[add/del/edge/mod/set+reasons]；不落库，应用后走保存/发布；服务端校验 ops 内 id 引用，非法字段跳过并报告)
            POST /templates/:id/validate · POST /templates/:id/publish
            GET /templates/:id/revisions · POST /templates/:id/copy
runs:       POST /runs (item_id|template_id+input) · GET /runs?status=&q=&template_id= · GET /runs/:id
            GET /runs/stats（成功率/已结束次数/累计 Token/总次数/各状态计数）
            GET /runs/:id/events(SSE) · GET /runs/:id/nodes/:nid
            POST /runs/:id/pause|resume|cancel|retry
            POST /runs/:id/nodes/:nid/approve|reject|rerun
            GET /runs/:id/artifacts/:aid (预览/下载)
approvals:  GET /approvals (聚合) · POST /approvals/:kind/:id/resolve
credentials: GET/PUT /cli-credentials/:kind · GET /cli-status (PATH 检测)
schedules:  现有 /api/v1/automations 完全不动
trigger:    GET/PUT /projects/:id/trigger-config（每项目一份，保存即生效）
            POST /projects/:id/trigger-config/run-now（服务端手动扫描，关闭页面不中断）
            GET /projects/:id/trigger-logs（触发历史：scanned/fired/skipped/note）
archive:    GET /pm-archive/* (只读)
```

API Key 能力：`manage_sop`（模板/事项/凭据写）、`run_sop`（触发/审批）、`read_sop`（查询）；MCP 工具组按能力映射（沿用 mcp_endpoint_tools 模式）。

---

## 13. 安全与边界

- 本机子进程**无安全隔离**（已确认决策）：个人可信环境假设；CLI 以 yolo/full-access 模式运行，文档明示风险。
- 缓解：每次运行独立工作目录 + 私有 HOME（os.Root 防符号链接逃逸）；Key 只进环境变量不落盘；script/shell 工具沿用危险命令黑名单；GIT_CEILING_DIRECTORIES 防 git 越界；工作区路径校验（产物路径不得越出 run 目录）。
- 凭据加密存储沿用 Know 现有机制；审批留痕不可篡改（decision 产物化 + sha256）。
- 租户隔离：所有新表 tenant_id 贯穿，查询强制租户过滤（沿用 ctx 注入模式）。

---

## 14. 风险与对策

| 风险 | 等级 | 对策 |
| --- | --- | --- |
| Windows 进程树终止：LoopForge 用 Setpgid+SIGKILL（Unix），Windows 无此机制 | 高 | Job Object（KILL_ON_JOB_CLOSE）封装进程组；降级 taskkill /T /F；取消/超时路径专项测试 |
| 10 种 CLI 协议漂移（版本更新改 JSON 事件） | 高 | 流翻译层每 CLI 独立文件 + 黄金样本回归测试（录制真实输出 fixture）；无 result 帧不判失败的兜底语义 |
| Windows 命令行长度上限（~32KiB） | 中 | prompt 文件阈值 24KiB；claude/codebuddy stdin 优先 |
| 双库（PostgreSQL/SQLite）JSONB 与迁移兼容 | 中 | 沿用 Know 现有双库迁移约定；SQLite 侧 JSON 降级；两库各跑迁移测试 |
| PM 迁移丢字段/状态语义错位 | 中 | domain_data 原样保留全部 PM 字段；映射表（§11.1）评审确认；迁移前 JSON 备份 + 幂等脚本 |
| 平台智能体节点长任务失控 | 中 | 节点级 max_iterations + timeout_minutes + 心跳租约回收 |
| vue-flow 画布与 TDesign 风格融合 | 低 | 原型先行确认视觉（本次交付 HTML 原型） |
| 桌面 Lite 无 Redis | 低 | 引擎不依赖 asynq/Redis，进程内调度 + DB 租约 |

---

## 15. 工作量估算（不分期，一次性交付；内部按依赖排序）

| # | 模块 | 人周 |
| --- | --- | --- |
| 1 | 数据模型 + 双库迁移 + PM 数据迁移脚本/备份 | 1.0 |
| 2 | SOP 引擎（调度/状态机/打回/重试/暂停/子SOP/任务书/契约门禁） | 2.0 |
| 3 | 平台智能体执行器（后台模式/工作区接入/submit_node_result/HITL 聚合） | 1.0 |
| 4 | CLI 执行器全家桶（Provider/私有HOME/流翻译×10/凭据管理/Windows 进程组/续跑） | 2.5 |
| 5 | 脚本执行器 + 审批中心 + 通知 | 0.5 |
| 6 | SOP 任务触发配置（调度器/扫描派发/幂等/触发历史） | 0.5 |
| 7 | 知识库/MCP 联动（Know MCP 注入 CLI/产物归档/MCP 工具组） | 0.5 |
| 8 | 前端：事项列表+看板（检索/统计带/批量/编号/进度）/项目配置/模板库/画布设计器（五步傻瓜式检查器+AI 自主配置差异预览）+AI编排助手/运行列表+详情/审批中心/SOP任务/设置/PM归档 | 6.0 |
| 9 | API 层 + API Key 能力 + SSE | 0.5 |
| 10 | 联调 + 10 种 CLI 实测 + 双库测试 + E2E（含 PM 迁移验证） | 1.5 |
| | **合计** | **约 16 人周（4 个月左右，一人全职）** |

内部实现顺序：1→2→3→5→9（先跑通平台智能体闭环）→4→6→7→8→10。前端画布可与引擎并行。

---

## 16. 验收标准（关键路径）

1. **混合协作 E2E**：一条模板含 平台智能体分析 → CLI 实现（zcode）→ 脚本测试 → 人工验收 → 平台智能体汇总；从事项看板发起，运行详情实时可见各节点轨迹，验收打回实现节点后返工再放行，产物齐全且与验收时一致（sha256）。
2. **10 种 CLI**：每种至少一次真实 headless 运行成功（凭据/私有 HOME/流事件/产物提取/取消各验证一遍）；未安装/无凭据时给出明确指引而非静默失败。
3. **SOP 任务闭环**：入口为 SOP 工作台顶部 Tab，现有自动化模块零改动；触发配置按 状态∧来源∧未跑过 扫描派发，单次上限截断且剩余后续继续；逐事项幂等不重复派发；运行记录触发来源=「SOP 任务」可回溯；立即运行服务端执行、关闭页面不中断；触发历史 scanned/fired/skipped 口径正确；保存配置即生效。
4. **PM 吸收**：迁移后 sop_items 字段无损（对照备份抽查），/platform/pm 重定向，PmArchive 只读可查，内置产品方案 SOP 跑通原 PM 核心链路。
5. **异常路径**：执行中取消（进程树确实终止）、崩溃后租约回收、审批超时策略、从任意节点重跑、子 SOP 嵌套。
6. **双库**：PostgreSQL 与 SQLite（桌面 Lite）迁移与核心链路各通过一遍。
7. **事项列表与项目配置**：编号并发创建不重号、前缀锁定；检索区各条件与模糊搜索（标题/编号/描述）组合过滤正确；**执行情况五态（尚未执行/正在推进/需要介入/待处理 PR/已闭环）为统计带与筛选下拉共用口径**，五态计数之和=工作项总数；执行情况可搜索单选下拉切换即过滤（列表与看板一致）、搜索只匹配选项名、Enter/Esc/点外部行为正确、选中态触发器与统计带高亮同步；统计带每格点击筛选/再点取消；来源在列表芯片/筛选/详情字段卡/日志均为中文映射（客户反馈/平台新建/数据洞察/竞品分析/战略规划/其他）；当前环节/进度随运行推进实时更新，done/closed 事项无运行时显示已完成/已关闭而非等待执行；批量触发跳过运行中事项；项目配置修改默认 SOP/知识库/自动推进状态后对新建事项与新运行生效。
8. **事项详情抽屉**：九 tab 齐全；失败面板补充上下文重试后失败节点重新推进且反馈进入任务书；整条重触发产生新运行且旧运行保留；澄清回答注入下次运行 {{humanContext}}；验收回填落 outcome；编辑/关闭/删除/导出均留操作日志，活动与运行事件合并可查。
9. **运行记录列表**：八种状态 chips 计数与全局一致且可组合搜索/SOP 下拉过滤；成功率与累计 Token 口径正确；运行推进时列表自动刷新且检索输入焦点不丢失；空态文案引导回事项列表。
10. **新建事项 AI 拆解**：双栏模态追问→拆解→写入描述链路完整；**右栏 AI 面板与左表单等高拉伸（底部无空白）**，长对话时消息区独立滚动、自动滚到底、输入框固定面板底部且恒在视口内，模态总高不超 84vh；拆解块重复写入为覆盖；追问问答创建后进入事项澄清 tab 且为已回答；草稿=backlog、确认=todo；拆解建议的执行器类型与模板节点执行器语义一致（平台智能体/CLI/脚本/人工关口）。
11. **代码仓库关联**：非法地址/重复绑定被拒；首个仓库自动设默认、可切换、解绑默认自动顺延；增删即时生效（不依赖「保存配置」）；绑定后新运行克隆仓库到工作区并建 `sop/<run_id>` 工作分支，运行信息可见；解绑后进行中运行不受影响、下次运行不再克隆；事项概览/交付字段卡随绑定联动。
12. **AI 编排助手**：画布/AI 编排切换与检查器互斥正确；空画布按场景生成完整链（含打回关系）且配置检查通过；有画布增量修改（补步骤/加关口/删节点桥接/换执行器）只动目标节点；草稿应用=未保存草稿态、不自动发布，放弃草稿画布不变；内置模板应用后需复制才能保存；模型下拉取自模型管理。
13. **CLI 角色与知识库范围**：CLI 节点选角色后 agent-system.md 含该角色 SystemPrompt/Rules/Skills + 任务书，未选角色仅任务书；执行引擎不因选角色改变（仍本机 CLI）；kb_scope 三档（跟随项目/指定清单/全部）在 Know MCP 注入与服务端校验中一致生效，事项级 KB 覆盖在 project 档优先；平台智能体节点不受 kb_scope 影响（沿用 agent 绑定）；私有 HOME 按 run 隔离，两次运行间 CLI 原生记忆不残留，跨 run 上下文仅经 {{humanContext}} 与上游产物传递。
14. **项目新增 / 删除 / 切换**：切换器列出全部项目并可切换，切换后事项/运行/审批/触发配置/仓库均为目标项目数据且编号前缀独立自增；新建模态校验名称必填、前缀格式与唯一性，创建即切换且为空项目；流程模板跨项目共享；删除需二次确认，进行中/待审批运行时被拒、最后一个项目被拒；删除后其事项/运行/触发配置/仓库绑定级联消失，模板保留，磁盘工作区文件不动。
15. **五步傻瓜式检查器**：agent 节点检查器按 ①谁执行 ②做什么 ③规则能力 ④产物校验 ⑤失败怎么办 五节呈现，标题栏仅含节点类型标签 + 「✦ AI 配这一步」按钮（不显示节点名与内部 id，名称只在「步骤名称」输入框维护且改名后画布卡片同步），全程无「任务书/门禁/gate」等工程术语（高级折叠除外）；**模型为下拉**（选项=模型管理已配置模型+默认档，无手输入口），切换后角色卡模型格同步；CLI+模型双列同行、技能/MCP 多选下拉（摘要触发器+复选浮层）、失败策略 2×2 网格排版生效；角色卡统计（模型/产物/校验/失败策略）随配置实时更新，「编辑角色 prompt」为只读预览并可跳智能体页；技能/MCP 多选下拉：触发器摘要随勾选即时更新（全选=「角色全部技能（N）」且 skills:null、部分勾选=子集数组），浮层勾选不关闭、点外部收起、开一个关其余；MCP 默认仅 Know 知识库且节点级生效（不继承角色 MCP 绑定）；未选角色时技能区显示「该角色未配置技能」提示而非下拉；交付物校验方式三档（文件交了就行/必须包含关键词/自定义命令）正确编译为 gate_commands（test -s / grep -q / 原样），高级额外门禁按 && 合并；结构化终态内置卡只读展示 pass/rework/fail/human 与缺失自动续会话追问；失败策略四卡切换后对应参数（重试次数/打回目标下拉）正确显隐，打回未选目标时配置检查以白话报错；返工上限/超时/保留上轮产物开关持久化进 definition；script/approval 节点检查器降级为对应精简节；AI 草稿新增节点带全部默认值（skills:null/mcp:[know-kb]/carry_over:true/check:exists/model:默认）且门禁即时派生。
16. **AI 自主节点配置**：检查器「✦ AI 配这一步」零输入生成该节点 set 草稿（推断角色/任务/补充规则/MCP/交付物校验/失败策略，逐字段带理由），补一句话重新生成且只追加任务描述不覆盖已自定义字段；差异卡原值→新值白话格式化正确，「应用此节点」单独应用后草稿移除该项、「全部应用」后草稿清空，均为未保存草稿态不自动发布；整流程「帮我配全」对全部 agent 节点产 set ops，空画布四场景链带五步全配置且配置检查通过（打回目标互设可达）；set 应用后 gate_commands 由 check 重新派生；非法角色/智能体/CLI id 被跳过该字段并在反馈中报告，不整单拒绝；单节点模式 chip 可退出回整流程；AI 不直接写 gate_commands。
17. **画布快捷工具条**：撤销/重做对增删节点、连线、拖拽落点、检查器字段变更、AI 草稿应用、自动排版均可往返还原（重做栈在新变更后清空，无历史时按钮置灰）；缩放 −/＋ 与百分比标签同步（20%–160%），缩放后拖拽位移按倍率换算正确，滚轮绕光标缩放；适应画布后全部节点包围盒可见且居中；自动排版按依赖拓扑分层（上游恒在左）且可撤销；小地图开关切换缩略图显隐与按钮高亮，缩略图含视野框、点击跳转视野中心；空白处拖动平移；图例在工具条右端、左侧面板不再重复；视图操作（缩放/平移/小地图）不产生历史记录；运行详情画布无工具条；重新进入设计器历史与视图重置。
18. **节点双出口连线**：每张节点卡底部有「成功」（绿）与「失败打回」（红）两行出口及右缘同色端口，打回行显示当前目标步名；点成功出口→点目标左圆点新增依赖边，点打回出口→点目标左圆点写入 reject_to 且检查器下拉同步、红色虚线边从红端口连到目标左圆点；两类连线均可撤销/重做；点自身左圆点取消连线且不产生历史；出口行点击不触发节点拖拽；边端点与端口圆心对齐（实测锚点，缩放后仍对齐）；节点加高后模板/AI 生成链/自动排版均无重叠；运行详情画布不渲染出口端口。
19. **第 2 步纯大白话**：检查器第 2 步无「插入上下文变量」入口、无 {{…}} 芯片，textarea 与全部内置模板/AI 生成任务均为不含占位符的大白话；第 2 步小标题右侧 ? 气泡列明四类自动附带上下文及附带时机（悬停/聚焦显示）；「额外参考哪步的产物」多选下拉以步骤名为选项（默认摘要「不额外参考 · 上游自动带」），勾选写 task_refs 且摘要即时更新、可撤销；删除节点后其他节点 task_refs 中对它的引用被清理；运行详情/澄清页的反馈与人工上下文标签均为白话（不出现占位符）；引擎占位符通道仅作为旧模板兼容保留于服务端。
20. **产物 HTML 预览与 .md 入库**：三处产物入口（事项抽屉产物/交付、运行详情产物、PM 归档资料）预览均打开大弹窗（min(1120px,94vw)、≤84vh、正文独立滚动、窄屏顶栏换行不溢出）；.md 默认 HTML 渲染（标题/表格/代码块/引用齐全），可切源码视图见 frontmatter 与原文，往返切换状态正确；.html 产物沙箱 iframe 渲染、.diff 增删着色、.json 美化、.txt/.log 等宽文本、zip 二进制提示不渲染；复制源码在剪贴板不可用时自动切源码视图并全选；下载落源文件（basename 正确）；入知识库后产物行/弹窗顶栏出现「已入知识库」标签且按钮置灰「已入库 ✓」，关闭重开状态保持，入库 toast 明示 .md 源文档走 ingest 链路；普通弹窗（480/980）不受 doc 模式影响；刷新后 mock 入库状态重置。
21. **检查器 ? 说明气泡**：检查器内解释性说明全部收进 ? 气泡，inline 提示行不再保留（单位短句/空态引导/规则预览内容/高级折叠内提示除外）；CLI 节点共 10 个 ? 图标（模型/角色（可选）/知识库检索范围/第 2 步标题/额外参考哪步的产物/遵循的规则/连接的 MCP 服务/先汇报完成状态/第 5 步标题/打回哪一步），平台智能体节点无 CLI 专属项；悬停任一 ? 弹出对应说明文案，键盘聚焦同样弹出，移开/失焦/滚动/点击即隐藏；气泡横纵均不越出视口，图标贴近顶部边缘时翻转到下方显示且箭头仍指向图标；第 5 步气泡内容随所选失败策略（原地重试/打回上游/停下等人/标记失败）动态切换，MCP 气泡按 CLI/平台执行器区分文案；approval 节点（关口会发生什么）与 script 节点（Shell 脚本·高级）同样带 ? 气泡；连续 20 轮悬停+重渲染后全局仍仅一个气泡实例、无残留；事项列表等非检查器视图无 ? 图标；控制台无报错。