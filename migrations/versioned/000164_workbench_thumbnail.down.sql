-- Migration: 000164_workbench_thumbnail (down)
ALTER TABLE workbench_items DROP COLUMN IF EXISTS thumbnail;
ALTER TABLE workbench_items DROP COLUMN IF EXISTS thumbnail_content_type;
ALTER TABLE workbench_items DROP COLUMN IF EXISTS thumbnail_version;
