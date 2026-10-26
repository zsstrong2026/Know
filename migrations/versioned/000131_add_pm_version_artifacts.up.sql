-- 版本产物与评审闭环补充字段
ALTER TABLE pm_versions
    ADD COLUMN IF NOT EXISTS prd_knowledge_id VARCHAR(36) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS prototype_knowledge_id VARCHAR(36) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS artifact_round INT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS artifact_feature_count INT NOT NULL DEFAULT 0,
    -- 生成时关联的需求 ID 快照，用于提示「需求集合已变动」
    ADD COLUMN IF NOT EXISTS artifact_requirement_ids JSONB NOT NULL DEFAULT '[]',
    ADD COLUMN IF NOT EXISTS update_outline TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS generated_at TIMESTAMP WITH TIME ZONE;

-- 需求搁置原因（评审不通过时写入）
ALTER TABLE pm_requirements
    ADD COLUMN IF NOT EXISTS reject_reason TEXT NOT NULL DEFAULT '';
