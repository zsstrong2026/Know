-- 会议文件复用临时文档表：作用域键为 meeting:{roomID}（8 + 36 = 44 字符），
-- 原 session_id 仅为会话 UUID 设计（VARCHAR(36)），这里扩容以容纳会议作用域。
ALTER TABLE temporary_documents ALTER COLUMN session_id TYPE VARCHAR(64);
