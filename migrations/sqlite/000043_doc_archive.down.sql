-- Migration: 000043_doc_archive (down)
-- 先删索引，再逐列移除归档字段。

DROP INDEX IF EXISTS idx_doc_generation_tasks_output_kb;

ALTER TABLE doc_generation_tasks DROP COLUMN archive_error;
ALTER TABLE doc_generation_tasks DROP COLUMN archive_status;
ALTER TABLE doc_generation_tasks DROP COLUMN auto_archive;
ALTER TABLE doc_generation_tasks DROP COLUMN output_kb_id;
