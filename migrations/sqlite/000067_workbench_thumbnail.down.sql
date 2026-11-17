-- Migration: 000067_workbench_thumbnail (down)
ALTER TABLE workbench_items DROP COLUMN thumbnail;
ALTER TABLE workbench_items DROP COLUMN thumbnail_content_type;
ALTER TABLE workbench_items DROP COLUMN thumbnail_version;
