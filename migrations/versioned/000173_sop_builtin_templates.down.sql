-- Migration: 000173_sop_builtin_templates（回滚）
-- 只删固定 id 的 seed 行；用户复制出的自建副本 id 不同，不受影响。
DELETE FROM sop_templates WHERE id IN ('b0000000-0000-4000-8000-000000000001','b0000000-0000-4000-8000-000000000002');
