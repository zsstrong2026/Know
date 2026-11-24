-- Migration: 000175_sop_project_notes (down)
DROP INDEX IF EXISTS ix_sop_items_parent;
DROP TABLE IF EXISTS sop_project_notes;
