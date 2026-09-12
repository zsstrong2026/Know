# WeKnora 知识库 45 期专题 · 技术拆解说明

> 基于对 [WeKnora](file:///D:/wiki/WeKnora-main)（腾讯开源的企业级 LLM 知识管理框架）源码与文档的理解，将第 1–45 期专题逐条映射到真实工程实现。专题分 9 大阶段组织，便于对照代码。

## 0. 前置：整体架构一张图

WeKnora 采用模块化三层架构，全链路可替换：

- **接入层**：Web UI / REST API / CLI `weknora` / Chrome 插件 / 微信小程序 / IM 频道 / MCP Server / 数据源连接器（飞书、Notion、语雀、RSS）。
- **知识加工层**：`docreader`（Python 独立服务，gRPC 与后端通信）负责解析与分块；Go 后端负责向量化、入库、建索引；`internal/container` 负责依赖装配与组件实例化。
- **智能应用层**：`internal/agent`（ReAct 推理）、RAG 检索、Agent 工具、Wiki 生成、知识图谱、多轮会话。
- **基础设施**：PostgreSQL(pgvector) / SQLite(sqlite-vec) 为主体，可选 Milvus / Weaviate / Qdrant / Elasticsearch / OpenSearch / VectorDB；对象存储（本地 / COS / S3 / OSS / TOS / MinIO / KS3 / OBS）；MQ 异步任务 + 分阶段 Worker 池；Langfuse 全链路可观测；gRPC + 静态加密 + SSRF 防护。

核心代码目录：`docreader/`（解析分块）、`internal/application/`（检索仓储）、`internal/agent/`（智能体）、`internal/searchutil/`（检索归一化）、`internal/middleware/`（拦截器链）、`internal/tracing/`（可观测）。

---

## 阶段一 · 知识导入与解析（1–3）

### 第 1 期 智能知识库总览
**技术拆解**：面向企业的知识框架，核心是把「非结构化文档」变成「可查询、可推理、可持续演进的知识资产」。三层解耦让每一层都能换：解析引擎可换、向量库可换、模型厂商可换。整体由「数据入库 → 知识加工 → 智能问答/推理/自动 Wiki」三条主线构成，统一走 REST + 前端，模型经 `internal/modelcontext` 统一接入 20+ 家厂商（OpenAI/DeepSeek/Qwen/智谱/混元/Gemini/MiniMax/NVIDIA/Ollama 等）。

### 第 2 期 文档解析与分块
**技术拆解**：解析在 `docreader/parser/`，通过 `registry.py` 注册多种引擎（pdf/docx/xlsx/html/md/epub/mhtml/image/markitdown/opendataloader 等），`chain_parser.py` 支持链式组合与并发。分块在 `docreader/splitter/splitter.py`：
- 标题层级识别：`header_hook.py` 用规则 + 机器学习识别标题，得到层级结构。
- 三层分块组合拳：按文档结构层级、段落语义、字数限制做自适应分层分块（对应 README「自适应三层分块」）。
- 父子分块（parent-child chunking）：小块供检索、父块供上下文，兼顾精度与完整性。
- 解析结果经 gRPC 回传后端，`internal/searchutil/chunkmerge.go` 负责块合并 / 归一化。

### 第 3 期 文档解析案例赏析
**技术拆解**：系统级演示覆盖五类真实文档：
- 表格类：`excel_parser.py` / `xlsx_merge.py` / `xlsx_repair.py` / `excel_convert.py`，表格转文本/块。
- 公文类：`docx_parser.py` + `docx_merge.py`，保留标题与结构。
- 扫描件（图片/OCR）：`image_parser.py`，配合 PaddleOCR-VL / VLM 识别。
- 大文件：`concurrency.py` 并发解析，`docx2_parser.py` 分页拆分，避免内存爆炸。
- 前端展示解析进度时间线（Langfuse 风格 Span 树，逐阶段进度 + 可中止）。

---

## 阶段二 · 向量化与存储选型（4）

### 第 4 期 向量数据库选型与实战
**技术拆解**：`internal/container/engine_factory.go` 按配置实例化不同向量引擎，三条路线：
1. **PostgreSQL + pgvector**（默认）：复用关系库，事务/权限/元数据一体，HNSW 索引（支持 1024 维）。
2. **SQLite + sqlite-vec / sqlite-vec-go-bindings**（轻量）：单机零运维。
3. **专用向量库**：Milvus / Weaviate / Qdrant / Elasticsearch / OpenSearch / VectorDB / Doris。

选 Milvus 的理由：支持混合检索（稀疏 BM25 + 稠密 Dense 双路）、元数据下推到向量库（filter 在库内完成，`LIKE/IN/IS_NULL`）、内置 HNSW 图索引加速近似检索，适合大规模知识库。

---

## 阶段三 · 检索全链路（5–6、24）

### 第 5 期 知识检索全链路
**技术拆解**：普通 RAG 只有单路稠密召回；WeKnora 走「多路并行召回 + 粗排精排」：
- 召回：BM25 稀疏召回 + Dense 稠密召回 + 图谱 GraphRAG 第三路召回，`internal/application/repository/retriever/` 多仓储支持，跨向量库扇出。
- 粗排精排：先按基础分粗筛，再经 Rerank 模型精排（支持火山引擎 Rerank / LKEAP 等，自动分批请求）。
- 上下文组装：命中「父分块」补充完整段落，或走「全文」。
- 追溯机制：每块记录来源（来源文件、页码、图表），确保答案有据可查。

### 6 期 知识检索系统演示
**技术拆解**：以《水资源公报》为例演示检索。后端返回按文件聚合的候选集；前端对匹配片段高亮，锚定出处；提供四种展示方式（引用浮层 / 引用抽屉 / RAG 分阶段进度 / 术语匹配高亮），归因可信。

### 24 期 知识检索评测框架
**技术拆解**：检索好不好用数据说话。`tests/` 与 dataset 构建检索+生成全链路评估，自动化计算召回率、命中率（Hit Rate），以及答案质量指标 BLEU / ROUGE，形成回归基线，防止检索改坏。

---

## 阶段四 · Wiki 自治生成（7）

### 第 7 期 LLM-WIKI
**技术拆解**：实质是「让 Agent 自己消化知识」。借鉴卡帕西（Karpathy）的编译器范式——把文档当作「源程序」，用 Agent 编译成结构化的「中间表示」（实体 + 关系），再生成相互链接的 Markdown Wiki 页面与知识图谱。三层架构：原始文档 → 中间表示（实体/关系/术语）→ 渲染层（Wiki 页面）。
- `internal/agent/prompts_wiki.go`：Wiki 生成的专用 Prompt（把文档转成条目、链接、引用）。
- 出处标注：每个 Wiki 条目携带原始文档引用。
- 冲突治理：多文档说法矛盾时标记冲突。
- 反哺对话：Wiki 又能作为检索上下文回喂问答，形成闭环。
- `internal/container/recover_pending_wiki_tasks.go`：Wiki 任务队列与死信恢复，支撑万级文档库。

---

## 阶段五 · 知识图谱与本体（8–9、13）

### 第 8 期 知识图谱
**技术拆解**：向量只认识「相似」，图谱认识「关系」。上传即建图：解析时抽取实体与关系（伴随后处理抽取）。跨文档织网：同名实体跨文档合并成网。第三路召回：GraphRAG 沿关系路径召回。可视化溯源：前端图谱 + 页面浏览器 + 路径跳回原文。存储支持 Neo4j（`internal/application/repository/retriever/neo4j/`）为知识图谱后端。

### 第 9 期 本体论在知识库中的应用
**技术拆解**：借鉴 Palantir Ontology——「先画格子再放东西」。先定义本体（Schema/模型：实体类型、属性、关系），再约束数据填充；用「婚假」等业务例讲清本体如何把开放问题变成选择题，让 AI 在既定框架内做「判断/选择」，而不是自由生成，极大提升结果可控性与可治理性。

### 13 期 智能问答 - 基于 GRAPH 的检索问答
**技术拆解**：把图谱接进问答。查询先做实体召回（NER/实体链接），再沿关系路径（路径遍历/多跳）组织答案，每一步都有实体与关系作为证据，支持「有条件可查」（回答附出处与推理路径）。

---

## 阶段六 · 知识生命周期与治理（10–12、28、30）

### 10 期 知识全生命周期管理
**技术拆解**：管知识的「生与死」：上传后经审批上岗；到期自动下架（失效窗口）；编辑分块后索引自动重建同步；删除知识时级联清理向量、摘要、条目、缓存等多处冗余，避免脏数据。

### 11 期 知识冲突检测
**技术拆解**：多篇文档说法矛盾时自动识别六类冲突（数值矛盾、时间矛盾、命名分歧、定义冲突、归属冲突、事实矛盾），对每条冲突做置信度评估，并逐条对照出处原文定位冲突双方。

### 12 期 知识裁决
**技术拆解**：检测出冲突后谁来拍板。AI 先给出裁决建议（倾向性结论 + 依据），再由人工做三选一（采纳 / 融合 / 判不冲突）；裁决后一键修复知识库（更新或下架冲突条目），裁决过程写入审计日志。

### 28 期 知识审批流
**技术拆解**：审批不通过的文档不进索引。上传文档先进入「待审」状态，通过审核才触发向量化与入索引；拒绝则不进候选集，从源头保证检索只暴露被认可的内容。

### 30 期 知识生效时间窗口
**技术拆解**：知识条目携带生效时间（`valid_from`/`valid_to`）与生效标识。检索时过滤过期知识，到期自动摘除，保证回答只基于当前有效内容。

---

## 阶段七 · 问答与智能体（14–19、21–23、25–27、29、36–37）

### 14 期 智能问答系统演示
**技术拆解**：端到端问答实录：提问 → 检索召回 → 重排 → 生成（LLM）→ 答案带引用，引用标签一键跳回原文。核心是 RAG 流水线，`internal/searchutil` 负责结果归一化，保证展示与归因可靠。

### 15 期 知识问答 · 智能体
**技术拆解**：问答从「一次性 RAG」进化为「智能体」。`internal/agent/` 用 ReAct 范式让 Agent 拆解问题（Task Decomposition）、调用工具（`agent/tools/`）、自查知识库与 Wiki，根据中间结果决定下一步动作。

### 16 期 智能体问答演示
**技术拆解**：多轮追问、工具调用、思考过程全程可见。`internal/agent/think.go`（思考）、`act.go`（行动）、`observe.go`（观察）构成 ReAct 循环；`stream.go` + SSE 把思考/调用/结果流式推给前端。

### 17 期 智能体架构选型
**技术拆解**：单智能体 vs 多智能体、ReAct vs Plan-Execute 的权衡。WeKnora 选择 ReAct 渐进式（边想边做，适合动态多步任务）+ 可选多智能体编排；最终选型 AgentScope-Java 作为 Agent 运行时/基础设施（README 提及 Agent 模式 ReACT、工具调用与运行时）。`internal/agent/engine.go` 为核心执行引擎。

### 18 期 上下文工程
**技术拆解**：上下文是最贵资源。`internal/agent/token/` 做 token 预算管理，对上下文做裁剪、压缩、结构化拼装：只保留命中块 + 必要背景，LLM 上下文别名压缩（README v0.7.0），把每分 token 用在刀刃上。

### 19 期 内置工具
**技术拆解**：智能体的「武器库」。`internal/agent/tools/` 内置检索（知识库）、Wiki、图谱、最终回答等工具，统一封装为可被 ReAct 调用的工具接口，支持并行工具调用与 `@Skill/@MCP` 提及范围化。`internal/agent/tool_policy.go` 处理工具授权策略。

### 20 期 引用重复
**技术拆解**：同一来源被反复引用时的去重与合并。`internal/searchutil/chunkmerge.go` 通过 `already_seen` 记录已引用块，合并重复来源，减少冗余 token 与重复展示。

### 21 期 多轮对话压力系统
**技术拆解**：聊得越久越容易翻车（上下文膨胀、幻觉、早期信息被冲淡）。多轮对话下做压力测试，配合 token 三板斧（截断/合并/紧急压缩）与 KbRedaction（跨轮替换知识库结果）作为兜底，控制上下文在可控区间。

### 22 期 Harness 工程 · 历史问答及记忆系统
**技术拆解**：让智能体「长记性」。`internal/agent/memory/` 收纳历史问答，建立记忆系统的检索（把相关历史作为上下文）、存储（结构化保存）与治理；同时支持会话级临时附件（图片/文档异步解析）与会话记忆。

### 23 期 多智能体系统
**技术拆解**：从单打独斗到团队协作。主 Agent（调度/编排）+ 子 Agent（专精检索/分析/绘图）+ 结果汇聚。`internal/agent/` 支持自定义 Agent（`internal/handler/custom_agent.go`）与数据分析 Agent，通过工具调用与消息传递协作。

### 25 期 可观测性
**技术拆解**：每次检索可复盘。三件套：日志（`internal/logger/`）、链路追踪（Langfuse，OTLP/OTel 迁移，W3C traceparent 跨服务传播，追踪 ReAct 循环、Token 消耗、工具调用、任务流水线）、指标看板（Langfuse UI + 运行时任务队列面板）。`internal/tracing/` 注入 span。

### 26 期 提示词优化
**技术拆解**：提示词也是工程资产。`internal/agent/prompts*.go` 集中管理 prompt，支持在线 Prompt 编辑、模板化、版本化管理；配合离线/端到端评测做效果回归，避免改坏。

### 27 期 权限下推检索
**技术拆解**：越权内容不进候选。检索时把类目级权限 + 生效标识下推到向量库/查询层（filter 下推到库内），在召回阶段就剔除无权/未生效内容，而非生成后过滤，兼顾性能与安全。

### 29 期 FAQ 知识库
**技术拆解**：FAQ 类型知识库标准问法直接命中答案。支持导入（批量）、加成（相似问法扩展命中）、独立生效时间（时长控制）。`internal/` 区分 FAQ 与文档两类库，FAQ 走精确/近似匹配快速出答案。

### 36 期 给智能体一把 grep
**技术拆解**：向量「意会」+ 正则「精确」。在向量召回之外，为智能体提供正则/关键词精确检索工具（`internal/agent/tools/`，配合 web_fetch 等），处理需要精确匹配（编号、法条、代码、专有名词）的场景。

### 37 期 already_seen 去重
**技术拆解**：省 token 省到骨头里。`internal/searchutil/chunkmerge.go` 用 `already_seen` 标记已引用文本块，跨召回路去重，避免同一片段重复进入上下文，节省 token 与模型调用成本。

---

## 阶段八 · 工程化与优化（31–35、38–39、41、44）

### 31 期 小文件 Direct Load
**技术拆解**：小文档跳过向量库，直接全量载入。对于体积/块数小于阈值的文档，直接把全文作为上下文交给生成阶段，省去检索与向量化往返，降低延迟、提升小文档准确率。

### 32 期 分块编辑与重新向量化
**技术拆解**：改一个字，索引跟上。支持界面上直接编辑检索分块，保留逐版本快照，提供 diff 与一键回滚；编辑后自动重建索引（重新向量化该块），保证检索与内容一致。文档自定义元数据也可编辑。

### 33 期 批量重新解析
**技术拆解**：解析算法升级后存量文档一键重跑。通过批次解析配置（`process_config`）+ reparse API 为多篇文档重新排队解析，可覆盖解析引擎/分块/多模态（VLM/ASR）/图谱抽取/问题生成等配置，无需手动逐篇处理。

### 34 期 更换 embedding 模型不重建全库
**技术拆解**：向量空间平滑迁移。支持多 embedding 模型、按知识库级别选择、Embedding 维度覆盖；切换模型时通过可迁移/双写/维度对齐机制平滑迁移存量数据，不用推倒全库重跑，降低迁移成本。

### 35 期 元数据自动提取与自定义过滤
**技术拆解**：自动提取文档元数据（类型、作者、标签、日期等），支持自定义元数据。检索时用 `LIKE / IN / IS_NULL` 等过滤条件对元数据下推过滤，实现精准范围内检索，可与权限/生效窗口叠加。

### 38 期 跨轮 KB 结果替换（KbRedaction）
**技术拆解**：防止上下文无限膨胀。在长多轮对话中，对已注入的知识库结果做「跨轮替换/折叠」：新结果替换旧结果，只保留最新 KB 证据，避免 KB 上下文随轮次线性累积。

### 39 期 Token 安全三板斧
**技术拆解**：`internal/agent/token/` 三个兜底动作——截断（超预算时裁掉次要内容）、合并（多片段拼装去重）、紧急压缩（上下文濒临超限时强制摘要压缩），配合 `already_seen` 与 KbRedaction 守住 token 上限。

### 41 期 过程可视化
**技术拆解**：`internal/stream/` + SSE 实时把「正在检索 → 正在重排 → 正在生成」分阶段进度推给前端，配合引用浮层/引用抽屉、流式 Markdown，让用户看到 RAG 流水线每一步，增强信任与可控性。

### 44 期 双服务异步任务架构
**技术拆解**：解析 500 页大文档不卡死问答。解析在独立的 `docreader` 服务（Python）进行，与后端通过 gRPC（TLS + Token）通信，双服务解耦；后端用 MQ 异步任务 + 分阶段独立 Worker 池（core/后处理/enrichment/maintenance + Wiki 独立池 + 弹性共享池），按模型并发治理，保证解析任务不阻塞在线问答。

---

## 阶段九 · 平台与安全（40、42–43、45）

### 40 期 引用标签端到端
**技术拆解**：回答里的 `[1][2]` 可点击跳原文。`internal/searchutil/citations.go` 生成并统一管理引用；前端引用抽屉区分网络/知识库来源，点击标签跳回原文位置（来源文件/页码），形成完整归因闭环。

### 42 期 Skills 体系
**技术拆解**：给智能体装技能包。`internal/agent/skills/` 支持「技能与工具绑定」（技能声明需要哪些工具，运行时绑定）与「知识迁移」（技能跨 Agent/会话复用）；`@Skill` 提及可范围化 Agent 运行时；技能有沙盒隔离（`internal/sandbox/`）防止越权。

### 43 期 信创适配
**技术拆解**：一套代码支持国产数据库。后端基于 GORM（`gorm.io/gorm`）抽象数据层，通过驱动/方言切换支持 PostgreSQL / SQLite / MySQL（`go-sql-driver/mysql`）。面向信创可扩展到达梦（DM）/ 人大金仓（KingbaseES）/ 神舟通用（Oscar）等国产数据库，做到「一套业务代码、多种数据库底座」。

### 45 期 License 与审计拦截器链
**技术拆解**：`internal/middleware/` 拦截器链。License 校验拦截器（许可证/授权校验，未授权拒绝访问）+ 审计拦截器（`internal/handler/audit_log.go` 记录请求与操作）+ 鉴权/权限拦截器（RBAC/API Key/Principal），按顺序串成链，形成「认证 → 授权 → 审计」的统一安全入口，审计落库支持按空间/知识库追踪。

---

## 总结：45 期 → 技术分层对照

| 主题域 | 期号 | 核心代码/组件 |
|---|---|---|
| 解析与分块 | 2–3 | `docreader/parser|splitter`、`searchutil/chunkmerge` |
| 向量与存储 | 4 | `container/engine_factory`、pgvector/Milvus/HNSW |
| 检索链路 | 5–6,24 | `application/repository/retriever`、rerank、评测 |
| Wiki | 7 | `agent/prompts_wiki`、Wiki 任务队列 |
| 图谱/本体 | 8–9,13 | Neo4j repo、GraphRAG、本体约束 |
| 生命周期/治理 | 10–12,28,30 | 审批、失效窗口、冲突检测裁决 |
| 问答/智能体 | 14–19,21–23,25–27,29 | `internal/agent`、`tools`、`memory`、Langfuse |
| 工程优化 | 31–35,38–39,41,44 | `token`、`stream`、`searchutil`、MQ Worker 池 |
| 平台安全 | 40,42–43,45 | `middleware`、`skills`、`citations`、GORM 信创 |
