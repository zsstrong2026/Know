-- Migration: 000074_sop_indexes (down)
DROP INDEX IF EXISTS idx_sop_items_project_status;
DROP INDEX IF EXISTS idx_sop_artifacts_run_node;
DROP INDEX IF EXISTS idx_sop_node_runs_run_node;
DROP INDEX IF EXISTS idx_sop_runs_project_status;
DROP INDEX IF EXISTS idx_sop_runs_item_created;
