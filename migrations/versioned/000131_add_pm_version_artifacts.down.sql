ALTER TABLE pm_versions
    DROP COLUMN IF EXISTS generated_at,
    DROP COLUMN IF EXISTS update_outline,
    DROP COLUMN IF EXISTS artifact_requirement_ids,
    DROP COLUMN IF EXISTS artifact_feature_count,
    DROP COLUMN IF EXISTS artifact_round,
    DROP COLUMN IF EXISTS prototype_knowledge_id,
    DROP COLUMN IF EXISTS prd_knowledge_id;

ALTER TABLE pm_requirements
    DROP COLUMN IF EXISTS reject_reason;
