-- Migration: 000160_insight (down)
DROP INDEX IF EXISTS uq_insight_self_owner;
DROP TABLE IF EXISTS insight_self;
DROP INDEX IF EXISTS idx_insight_reports_owner_run;
DROP INDEX IF EXISTS idx_insight_reports_owner_theme;
DROP TABLE IF EXISTS insight_reports;
DROP INDEX IF EXISTS idx_insight_evidence_owner_run;
DROP INDEX IF EXISTS idx_insight_evidence_owner_theme;
DROP TABLE IF EXISTS insight_evidence;
DROP INDEX IF EXISTS idx_insight_runs_owner_theme;
DROP INDEX IF EXISTS idx_insight_runs_owner_status;
DROP TABLE IF EXISTS insight_runs;
DROP INDEX IF EXISTS idx_insight_themes_owner;
DROP TABLE IF EXISTS insight_themes;
