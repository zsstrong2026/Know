-- Migration: 000074_sop_indexes（PostgreSQL 000172 的 SQLite 对应实现）
-- 部分索引 SQLite 同样支持（WHERE deleted_at IS NULL）。
-- 编号 000074 在 T6 的 000073 之后应用（迁移按文件名顺序执行）。

CREATE INDEX IF NOT EXISTS idx_sop_runs_item_created ON sop_runs(item_id, created_at DESC) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_sop_runs_project_status ON sop_runs(project_id, status) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_sop_node_runs_run_node ON sop_node_runs(run_id, node_id);
CREATE INDEX IF NOT EXISTS idx_sop_artifacts_run_node ON sop_artifacts(run_id, node_id);
CREATE INDEX IF NOT EXISTS idx_sop_items_project_status ON sop_items(project_id, status) WHERE deleted_at IS NULL;
