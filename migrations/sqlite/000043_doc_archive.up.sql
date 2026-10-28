-- Migration: 000043_doc_archive
-- Description: 对齐 versioned 000137：文档生成任务增加归档目标与归档状态
-- doc_generation_tasks 已在 000016_doc_assistant 建好，这里直接 ALTER。

ALTER TABLE doc_generation_tasks ADD COLUMN output_kb_id VARCHAR(36);
ALTER TABLE doc_generation_tasks ADD COLUMN auto_archive INTEGER NOT NULL DEFAULT 0;
ALTER TABLE doc_generation_tasks ADD COLUMN archive_status VARCHAR(16) NOT NULL DEFAULT '';
ALTER TABLE doc_generation_tasks ADD COLUMN archive_error TEXT;

CREATE INDEX IF NOT EXISTS idx_doc_generation_tasks_output_kb
  ON doc_generation_tasks (output_kb_id);
