-- Migration: 000067_workbench_thumbnail（PostgreSQL 000164 的 SQLite 对应实现）
-- 图片字节只在缩略图接口读取，列表只带 thumbnail_version。

ALTER TABLE workbench_items ADD COLUMN thumbnail BLOB;
ALTER TABLE workbench_items ADD COLUMN thumbnail_content_type TEXT NOT NULL DEFAULT '';
ALTER TABLE workbench_items ADD COLUMN thumbnail_version INTEGER NOT NULL DEFAULT 0;
