-- Migration: 000173_sop_builtin_templates
-- Description: SOP 工作台：内置模板 seed（产品方案交付 / 需求上线复盘）
--
-- 内置模板以 tenant_id=0、is_builtin=TRUE 下发，对所有租户只读可见
-- （读路径谓词见 repository/sop_template.go 的 sopTemplateReadPredicate）。
-- 固定 id 是前后端契约：前端列表/复制按 id 定位，重复下发不得换 id。
-- 澄清 17：agent 节点 executor.agent_id 留空（各安装的角色 id 不同），
-- 内置模板只读参照、不可直接发起运行；复制后绑定真实角色再发布运行。
-- seed 幂等：已存在同 id 行时不覆盖（ON CONFLICT DO NOTHING / INSERT OR IGNORE）。

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

INSERT INTO sop_templates (id, tenant_id, project_id, name, description, status, version, is_builtin, metric_category, definition, created_at, updated_at)
VALUES ('b0000000-0000-4000-8000-000000000002', 0, '', '需求上线复盘',
 'PM 场景沉淀：效果核对→价值判定→复盘报告→回填事项结论。复制后绑定智能体角色即可运行。',
 'published', 1, TRUE, '交付',
 '{"nodes":[
  {"id":"n1","name":"效果核对","type":"agent","executor":{"kind":"platform_agent","agent_id":"","kb_scope":"project"},"task_prompt":"对照上线前的目标与埋点数据，核对本次需求的实际效果：指标变化、用户反馈、异常与遗留问题。","skills":[],"mcp":["know-kb"],"artifact_outputs":[{"name":"效果核对","path":"out/effect.md","media_type":"text/markdown","required":true,"check":{"type":"exists"}}],"on_failure":"loop","max_attempts":3,"max_reworks":2,"loop_max_iterations":3,"carry_over":true,"timeout_minutes":60,"position":{"x":0,"y":0}},
  {"id":"n2","name":"价值判定","type":"approval","approver_hint":"判定价值是否达成（达成/部分达成/未达成）","on_failure":"reject","reject_to":"n1","position":{"x":250,"y":0}},
  {"id":"n3","name":"复盘报告","type":"agent","executor":{"kind":"platform_agent","agent_id":"","kb_scope":"project"},"task_prompt":"结合效果核对与价值判定结论撰写复盘报告：做对了什么、哪里不足、后续改进项与责任人。","skills":[],"mcp":["know-kb"],"artifact_outputs":[{"name":"复盘报告","path":"out/retro.md","media_type":"text/markdown","required":true,"check":{"type":"contains","keyword":"复盘"}}],"on_failure":"loop","max_attempts":3,"max_reworks":2,"loop_max_iterations":3,"carry_over":true,"timeout_minutes":60,"position":{"x":500,"y":0}},
  {"id":"n4","name":"回填事项","type":"script","script":"set -e\ntest -s out/retro.md\necho outcome-filled","artifact_outputs":[],"on_failure":"fail","position":{"x":750,"y":0}}],
  "edges":[{"id":"e1","source":"n1","target":"n2","when":"success"},{"id":"e2","source":"n2","target":"n3","when":"success"},{"id":"e3","source":"n3","target":"n4","when":"success"}],
  "input_schema":["title","description","attachments"],"on_success_item_status":"done"}',
 NOW(), NOW())
ON CONFLICT (id) DO NOTHING;
