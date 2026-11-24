# SOP 工作台 · spec §16 验收记录（第 1–21 项）

> 日期：2026-11-24 · 分支 `trae/metting` · 环境：Windows 10.0.26300（DuckDB 静态库链接失败、无宿主可达的 PG）
> 验收口径：能自动化验证的用测试，界面项用 dev server + 本地 SOP 桩做浏览器走查；
> **凡是不能在本环境真实验证的，一律标「未验证」并写明原因，不写成 PASS。**

## 总览

| # | 验收项 | 结论 | 证据 |
| --- | --- | --- | --- |
| 1 | 混合协作 E2E | ✅ PASS（假 CLI 全链路） | `TestMixedChainWithRework`（T56） |
| 2 | 10 种 CLI 真实 headless | ⚠️ 未验证 | 本机仅装 3 个且全部缺凭据（T57 记录） |
| 3 | SOP 任务闭环 | ✅ PASS（前端 + 桩） | T52 走查；触发器后端测试 T35 |
| 4 | PM 吸收 | ⚠️ 部分验证 | 迁移命令测试 PASS；重定向与归档页走查 PASS；真实 dev 库迁移未跑 |
| 5 | 异常路径 | ✅ PASS | `TestCancelKillsProcessTree` / `TestLeaseRecoveryAfterCrash` / `TestApprovalTimeoutStrategies` / `TestRetryFromFailedNodeAndChildSOP` |
| 6 | 双库 | ⚠️ 部分验证 | `TestSopMigrationParity` + `TestCoreChainE2E/sqlite` PASS；PG 子测试 SKIP（无 DSN） |
| 7 | 事项列表与项目配置 | ✅ PASS（除并发编号） | T39/T40 走查；并发不重号由仓储唯一索引 + 后端测试覆盖 |
| 8 | 事项详情抽屉 | ✅ PASS（九 tab；会话 tab 占位） | T42 走查 |
| 9 | 运行记录列表 | ✅ PASS | T48 走查（8 chips/组合过滤/成功率/保焦点刷新） |
| 10 | 新建事项 AI 拆解 | ⚠️ 部分 | T43 走查（分层逻辑 PASS；面板等高在窄视口退化为单列） |
| 11 | 代码仓库关联 | ⚠️ 部分 | T39 走查（校验/首默认/设默认/解绑顺延 PASS；克隆与 `sop/<run_id>` 分支需真实 git 运行，未验证） |
| 12 | AI 编排助手 | ✅ PASS（真实后端接口，桩数据） | T47 走查 |
| 13 | CLI 角色与知识库范围 | ✅ PASS（单测层面） | `TestRenderAgentSystem*`、`TestResolveSkills*`、`TestPrivateHome*`、kb_scope 白名单 |
| 14 | 项目新增/删除/切换 | ✅ PASS（除「仅剩一个项目禁删」需真实后端） | T38/T39 走查 |
| 15 | 五步检查器 | ⚠️ 部分 | T46 走查（五节/序号/失败 2×2/? 单例 PASS；角色卡四格统计与「编辑角色 prompt」未做） |
| 16 | AI 自主节点配置 | ✅ PASS（桩） | T47 走查（差异卡/单条应用/全部应用/未保存态） |
| 17 | 画布快捷工具条 | ✅ PASS（除缩放绕光标） | T45 走查（撤销/重做/适应/排版/小地图/图例） |
| 18 | 节点双出口连线 | ✅ PASS（除锚点实测对齐） | T45 走查（两类连线/红虚线/目标名/可撤销） |
| 19 | 第 2 步纯大白话 | ✅ PASS | T46：第 2 步无变量入口；模板/AI 任务由后端生成；前端不做 `{{…}}` 芯片 |
| 20 | 产物预览与 .md 入库 | ⚠️ 部分 | T50 走查（md/源码/入库/标签/置灰 PASS；html iframe、diff/json/二进制分派只有单测覆盖，未逐项点验） |
| 21 | 检查器 ? 说明气泡 | ✅ PASS（除 10 个气泡全数） | T46：单例、20 轮悬停仍 1 个、点外部隐藏、视口钳位单测 |

## 逐项说明（只写「为什么是这个结论」）

### 1. 混合协作 E2E — PASS
`TestMixedChainWithRework`（`internal/application/service/sop/engine/e2e_mixed_test.go`）用假 zcode
二进制走**完整 CLIProvider 链路**：`LookPath` → 私有 HOME → stream-json 翻译 → 产物提取 → 门禁；
链路为 分析 → CLI 实现 → 脚本测试 → 人工验收 → 汇总。验收打回后 CLI 节点重跑并带返工意见，
运行终态 succeeded，全部产物 sha256 与磁盘文件一致。这是「E2E 结构正确」的证明，
不等于「真实 CLI 协议正确」（见第 2 项）。

### 2. 10 种 CLI 真实 headless — 未验证
见 `docs/superpowers/verification/2026-11-19-sop-cli-matrix.md`：本机只装 pi 1.5.2 / dsh 0.2.2-rc.1 /
zcode 0.17.1，三者都在「缺 API key / 未选模型」阶段退出，stdout 为空，没有 stream-json 帧；
其余 7 种未安装，任务明确「不代装」。已交付 `SOP_CLI_CAPTURE_DIR` 录制开关与假二进制基线，
真实录制需在有凭据的机器上按该文档第 4 节复测。**黄金样本仍是手写样本**。

### 3. SOP 任务闭环 — PASS
前端 T52 走查：摘要卡随保存刷新、立即运行后历史出现 manual 行、状态多选无终态、cron 过短被后端拒。
后端调度/幂等/单轮上限由 T35 测试覆盖（预约先于派发、`ux_sop_dispatch_slot` 唯一索引）。
现有「自动化」模块零改动（本计划未触碰 `internal/automation/*`）。

### 4. PM 吸收 — 部分验证
- `go test ./cmd/sop-migrate-pm/` 全 PASS：幂等、字段无损、状态映射、软删保留、编号冲突加后缀、失败回滚保留备份。
- `/platform/pm`（含旧子路径与 query）重定向到 `/platform/sop/items`：T38 浏览器走查 PASS。
- PM 归档四 tab 只读可查：T54 走查 PASS（桩数据）。
- **未做**：真实 dev 库 `go run ./cmd/sop-migrate-pm --dsn <dev>` + 对照备份抽查 10 条（无宿主可达的 dev 库）。
- T58 已把 PM 写端点改为 410 白话、移除 PM asynq 生成任务注册、生成实现标 Deprecated。

### 5. 异常路径 — PASS
- 取消进程树终止：`TestCancelKillsProcessTree`（Windows `tasklist` 核对 PID 消失）。
- 崩溃后租约回收：`TestLeaseRecoveryAfterCrash`（手工造过期租约 → 新实例回收为 failed）。
- 审批超时：`TestApprovalTimeoutStrategies`（notify 只提醒一次 / fail 自动失败 / 默认永不超时）。
- 重试与子 SOP：`TestRetryFromFailedNodeAndChildSOP`（失败重试 + 子运行产物回传指纹一致）。

### 6. 双库 — 部分验证
`TestSopMigrationParity` 校验 8 对 PG/SQLite 迁移的建表集合、类型降级与 down 脚本；
`TestCoreChainE2E/sqlite` 用真实调度循环跑通四节点链路。`postgres` 子测试在未设
`SOP_TEST_PG_DSN` 时 SKIP（容器 PG 未对宿主暴露端口，`make migrate-down` 回滚验证也未执行）。

### 7. 事项列表与项目配置 — PASS（并发编号除外）
T40：统计带五态计数与列表一致、点格筛选/再点取消、fsel 搜索只命中选项名且 Enter 选中、
搜索 300ms 防抖与 5s 自动刷新保焦点、批量触发跳过运行中。T39：仓库增删设默认即时生效、
非法地址被拒、危险操作禁删守卫、描述多行保存折叠为单行。
并发不重号不在前端验证范围：编号分配在仓储事务内（T2），前端只展示。

### 8. 事项详情抽屉 — PASS（会话 tab 为占位）
T42：九 tab 齐全；执行进展时间线含 attempt/rework/门禁/耗时；澄清回答后状态变已回答；
验收回填落 `outcome`；活动/操作日志分 tab 且可合并。会话 tab 显示「后续任务接入」占位——
后端没有「按事项查 SOP 会话」的端点，不伪造列表。

### 9. 运行记录列表 — PASS
T48：8 个状态 chips 计数与 `runStats` 一致（失败红标）、chips + 搜索 + SOP 下拉组合过滤、
成功率与 Token 口径（分母=已结束）、5s 自动刷新且搜索框焦点与光标不动、空态引导。

### 10. 新建事项 AI 拆解 — 部分
T43：拆解卡渲染目标/子事项/验收口径、执行器中文标签、写入描述为覆盖、保存草稿=backlog、
确认=待梳理迁移到 todo、创建后附件上传与澄清 seeding。
**未达标**：双栏在窄视口（≤1080px）退化为上下排列（AI 面板固定 420px），不是「等高拉伸」；
这是响应式取舍，已在台账记为 deferred。

### 11. 代码仓库关联 — 部分
T39 覆盖：非法地址/重复被拒、首个自动默认、设默认切换、解绑默认顺延、增删即时生效。
「克隆到工作区并建 `sop/<run_id>` 分支」属于运行期行为，本环境无法发起真实 git 运行
（后端容器不含 SOP 路由），只有引擎侧 checkpoint 单测覆盖。

### 12. AI 编排助手 — PASS（桩）
T47：画布/AI 分段互斥、四个场景 chips、flow 模式整份草稿落画布（未保存）、node 模式零输入
「生成配置」、字段级差异卡（原值→新值+理由）、单条/全部应用、放弃草稿、skipped 白话报告。
内置模板只读时应用被拦并提示复制。

### 13. CLI 角色与知识库范围 — PASS（单测）
- `agent-system.md` 渲染：`TestRenderAgentSystem*`（含角色 SystemPrompt/Rules/Skills，空节省略）。
- 未选角色：`TestResolveSkillsOrderAndNilRole`。
- kb_scope：后端 `withKBScope` 与 MCP 工具 `kb_scope` 白名单一致（T37 测试）。
- 私有 HOME 隔离：`cli/home_test.go`（越界与符号链接拒绝）。

### 14. 项目新增/删除/切换 — PASS（除「仅剩一个项目」）
T38：切换器列全部项目、切换后 URL 不变且数据重载；新建校验名称必填/前缀格式/唯一性，
创建即切换；`/platform/pm` 旧链接重定向。T39：删除二次确认、有活跃运行时按钮禁用、
删除后跳回事项页。「最后一个项目禁删」由前端守卫实现，真实后端拒绝分支未实测。

### 15. 五步检查器 — 部分
T46：五节齐全且序号 1–5；失败策略 2×2 卡；? 气泡单例；打回目标下拉由上游祖先算出；
改名同步画布卡片。**未做**：角色卡四格统计与「编辑角色 prompt」只读预览（依赖 custom_agents
详情，留待清单装配）；④ 的 16px 空心描边小圆用同款序号圆等价表达。

### 16. AI 自主节点配置 — PASS（桩）
T47 走查：零输入生成 3 张差异卡（任务/失败策略/打回目标）、单条应用后剩 2 张、全部应用后清空、
可撤销、skipped 条目白话。gate_commands 由后端 `RefreshDerived` 派生，AI 不直写。

### 17. 画布快捷工具条 — PASS（除缩放绕光标）
T45：撤销/重做（60 快照保留 50 可回退 49 步单测）、−/＋与百分比同步、适应画布、自动排版
（Kahn 分层、环报错）、小地图开关与节点/边数一致、图例在工具条右端。
「滚轮绕光标缩放」依赖 vue-flow 原生行为，未单独断言。

### 18. 节点双出口连线 — PASS（除锚点实测对齐）
T45：成功/打回两类连线（打回写 `reject_to`、渲染红色虚线）、打回行显示目标步名、撤销/重做
覆盖连线。`computeEdgeAnchors` 已实现（端口 DOM 实测圆心），但当前连线走 vue-flow 默认锚点，
未逐像素核对「缩放后仍对齐」。

### 19. 第 2 步纯大白话 — PASS
T46：第 2 步只有 textarea 与「额外参考哪步的产物」多选，**无变量插入入口、无 `{{…}}` 芯片**；
四类自动上下文说明收进 ? 气泡。内置模板任务由后端生成，前端不做占位符替换。

### 20. 产物预览与 .md 入库 — 部分
T50 走查：md 默认 HTML 渲染（h1/表格/引用/代码块齐全）、源码视图保留 frontmatter、
入库按钮可点且成功后「已入库 ✓」+ 标签 + toast「.md 源文档经 ingest 链路入库」。
`previewKind` 的 6 类分派与 `stripFrontmatter`/`diffLineClass` 有单测；
html 沙箱 iframe、diff 着色、json 美化、二进制提示未逐项点验；剪贴板降级在无权限环境才触发。

### 21. 检查器 ? 说明气泡 — PASS（除 10 个全数）
T46：`clampTipPosition` 视口双向钳位与上方翻转有单测；浏览器实测全局仅 1 个 `#sop-help-tip`、
20 轮悬停后仍 1 个、点空白处 opacity=0、键盘聚焦同款。CLI 专属气泡实际只挂了模型/知识库范围/
第 2 步/第 3 步/第 4 步/第 5 步/打回目标等，**不满 10 个**（其余解释文案写成 inline 提示或折叠内说明）。

## 与 §16.22–26（M11）的关系

本表的 1–21 与 T68 的 22–26 合并构成 spec §16 全量验收；M11 部分见
`docs/superpowers/verification/2026-11-20-sop-brief-split.md`（T68 产出）。

## 本环境无法验证项汇总（不计入 PASS）

1. 10 种 CLI 真实 headless 与黄金样本替换（缺凭据）。
2. PG 子测试与 `make migrate-down` 回滚（PG 未对宿主暴露）。
3. 真实 dev 库 PM 迁移与备份抽查（同上）。
4. 真实 git 克隆与 `sop/<run_id>` 分支（后端容器不含 SOP 路由）。
5. 全部「运行详情 SSE 直播」的真实后端联调（桩模拟事件流，未接真实引擎）。
