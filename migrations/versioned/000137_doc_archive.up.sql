-- Migration: 000137_doc_archive
-- Description: 文档助手生成任务增加归档目标知识库与归档状态字段
-- 归档由后台异步执行：output_kb_id 为空表示不归档；archive_status 记录待归档/成功/失败。

ALTER TABLE doc_generation_tasks ADD COLUMN IF NOT EXISTS output_kb_id varchar(36);
ALTER TABLE doc_generation_tasks ADD COLUMN IF NOT EXISTS auto_archive boolean NOT NULL DEFAULT false;
ALTER TABLE doc_generation_tasks ADD COLUMN IF NOT EXISTS archive_status varchar(16) NOT NULL DEFAULT '';
ALTER TABLE doc_generation_tasks ADD COLUMN IF NOT EXISTS archive_error text;

CREATE INDEX IF NOT EXISTS idx_doc_generation_tasks_output_kb
  ON doc_generation_tasks (output_kb_id);
