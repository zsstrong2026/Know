-- Migration: 000172_sop_indexes
-- Description: SOP 派生字段 JOIN 复合索引（列表/看板/统计的热路径）
--
-- 部分索引（WHERE deleted_at IS NULL）只覆盖活行，索引更小且命中率更高；
-- PG 与 SQLite 均支持。编号 000172 在 T6 的 000171 之后应用（迁移按文件名顺序执行）。

CREATE INDEX IF NOT EXISTS idx_sop_runs_item_created ON sop_runs(item_id, created_at DESC) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_sop_runs_project_status ON sop_runs(project_id, status) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_sop_node_runs_run_node ON sop_node_runs(run_id, node_id);
CREATE INDEX IF NOT EXISTS idx_sop_artifacts_run_node ON sop_artifacts(run_id, node_id);
CREATE INDEX IF NOT EXISTS idx_sop_items_project_status ON sop_items(project_id, status) WHERE deleted_at IS NULL;
