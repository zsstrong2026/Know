-- Migration: 000164_workbench_thumbnail
-- Description: 条目列表卡片封面：保存前端截取的编辑器缩略图。
-- 图片字节只在缩略图接口读取，列表只带 thumbnail_version，避免翻页传输图片。

ALTER TABLE workbench_items ADD COLUMN IF NOT EXISTS thumbnail bytea;
ALTER TABLE workbench_items ADD COLUMN IF NOT EXISTS thumbnail_content_type varchar(32) NOT NULL DEFAULT '';
ALTER TABLE workbench_items ADD COLUMN IF NOT EXISTS thumbnail_version integer NOT NULL DEFAULT 0;
