-- Mirrors versioned migration 000093_doc_assistant: 回滚文档助手三张表

DROP TABLE IF EXISTS doc_documents;
DROP TABLE IF EXISTS doc_generation_tasks;
DROP TABLE IF EXISTS doc_templates;
