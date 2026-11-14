-- 回滚前需确认已无超长作用域（meeting:{roomID}）数据，否则缩容会失败。
ALTER TABLE temporary_documents ALTER COLUMN session_id TYPE VARCHAR(36);
