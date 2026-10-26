-- PM 工作站：9 张表。所有查询均以 tenant_id 隔离，除关联/任务表外统一软删（deleted_at）。
-- 类型基线对齐 000115_meeting_room：主键 VARCHAR(36)、tenant_id BIGINT、时间列 TIMESTAMP WITH TIME ZONE。

-- ===== 项目 =====
CREATE TABLE pm_projects (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    name VARCHAR(255) NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    kb_id VARCHAR(36) NOT NULL DEFAULT '',               -- 项目知识库
    session_id VARCHAR(36) NOT NULL DEFAULT '',          -- 常驻 PM 会话
    status VARCHAR(16) NOT NULL DEFAULT 'active',        -- active / archived
    owner_id VARCHAR(36) NOT NULL DEFAULT '',
    created_by VARCHAR(36) NOT NULL DEFAULT '',
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMP WITH TIME ZONE
);
CREATE INDEX idx_pm_projects_tenant_status ON pm_projects (tenant_id, status, updated_at DESC);
-- 同租户下项目名唯一（软删不计入），支撑「按名称判断项目已存在」
CREATE UNIQUE INDEX uq_pm_projects_tenant_name ON pm_projects (tenant_id, name) WHERE deleted_at IS NULL;

-- ===== 需求 =====
CREATE TABLE pm_requirements (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    project_id VARCHAR(36) NOT NULL,
    title VARCHAR(255) NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    priority VARCHAR(8) NOT NULL DEFAULT 'P1',           -- P0 / P1 / P2 / P3
    -- pending_analysis / analyzing / confirmed / scheduled / developing / launched / rejected / closed
    status VARCHAR(32) NOT NULL DEFAULT 'pending_analysis',
    source VARCHAR(32) NOT NULL DEFAULT 'other',         -- customer / internal / data / competitor / strategy / other
    proposer VARCHAR(128) NOT NULL DEFAULT '',
    ai_analysis JSONB NOT NULL DEFAULT '{}',             -- AI 分析结果（结论 / 建议 / 优先级）
    design_plan TEXT NOT NULL DEFAULT '',                -- AI 设计方案
    session_id VARCHAR(36) NOT NULL DEFAULT '',          -- 需求级会话
    knowledge_id VARCHAR(36) NOT NULL DEFAULT '',        -- 需求文档
    actual_launch_date DATE,
    effect_notes TEXT NOT NULL DEFAULT '',
    value_achieved VARCHAR(16) NOT NULL DEFAULT '',      -- achieved / partial / not_achieved（空串 = 未回填）
    retro_notes TEXT NOT NULL DEFAULT '',
    value_achieved_at TIMESTAMP WITH TIME ZONE,
    owner_id VARCHAR(36) NOT NULL DEFAULT '',
    created_by VARCHAR(36) NOT NULL DEFAULT '',
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMP WITH TIME ZONE
);
CREATE INDEX idx_pm_requirements_project_status ON pm_requirements (tenant_id, project_id, status);
CREATE INDEX idx_pm_requirements_project_updated ON pm_requirements (tenant_id, project_id, updated_at DESC);

-- ===== 版本 =====
CREATE TABLE pm_versions (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    project_id VARCHAR(36) NOT NULL,
    version_no VARCHAR(32) NOT NULL DEFAULT '',          -- 定稿后才生成
    name VARCHAR(255) NOT NULL DEFAULT '',
    -- planning / reviewing / finalized / developing / launched / shelved
    status VARCHAR(16) NOT NULL DEFAULT 'planning',
    review_round INT NOT NULL DEFAULT 0,
    review_opinions JSONB NOT NULL DEFAULT '[]',         -- 多轮评审意见
    review_conclusion VARCHAR(16) NOT NULL DEFAULT '',   -- pass / conditional / reject（空串 = 未评审）
    reviewers VARCHAR(512) NOT NULL DEFAULT '',
    release_note TEXT NOT NULL DEFAULT '',
    plan_date DATE,
    plan_launch_date DATE,
    finalize_date TIMESTAMP WITH TIME ZONE,
    launch_date TIMESTAMP WITH TIME ZONE,
    owner_id VARCHAR(36) NOT NULL DEFAULT '',
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMP WITH TIME ZONE
);
CREATE INDEX idx_pm_versions_project_status ON pm_versions (tenant_id, project_id, status);
-- 版本号定稿后才生成，空串不参与唯一约束
CREATE UNIQUE INDEX uq_pm_versions_no ON pm_versions (tenant_id, project_id, version_no)
    WHERE version_no <> '' AND deleted_at IS NULL;

-- ===== 版本 ↔ 需求 =====
CREATE TABLE pm_version_requirements (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    version_id VARCHAR(36) NOT NULL,
    requirement_id VARCHAR(36) NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX uq_pm_version_requirements ON pm_version_requirements (version_id, requirement_id);
CREATE INDEX idx_pm_version_requirements_req ON pm_version_requirements (tenant_id, requirement_id);

-- ===== 功能清单（三级：模块 / 功能 / 操作）=====
CREATE TABLE pm_features (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    project_id VARCHAR(36) NOT NULL,
    requirement_id VARCHAR(36) NOT NULL DEFAULT '',      -- 来自需求时回填
    parent_id VARCHAR(36) NOT NULL DEFAULT '',           -- 自关联，空串为模块级
    module_name VARCHAR(255) NOT NULL DEFAULT '',
    feature_name VARCHAR(255) NOT NULL DEFAULT '',
    operation_name VARCHAR(255) NOT NULL DEFAULT '',
    source VARCHAR(16) NOT NULL DEFAULT 'manual',        -- roadmap / prd / manual
    status VARCHAR(16) NOT NULL DEFAULT 'planned',       -- planned / launched
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMP WITH TIME ZONE
);
CREATE INDEX idx_pm_features_project ON pm_features (tenant_id, project_id, parent_id);
CREATE INDEX idx_pm_features_requirement ON pm_features (tenant_id, requirement_id);

-- ===== 功能点迭代历史 =====
CREATE TABLE pm_feature_iterations (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    feature_id VARCHAR(36) NOT NULL,
    version_id VARCHAR(36) NOT NULL DEFAULT '',
    change_type VARCHAR(16) NOT NULL,                    -- add / change / remove
    change_note TEXT NOT NULL DEFAULT '',
    changed_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_pm_feature_iterations_feature ON pm_feature_iterations (tenant_id, feature_id);
CREATE INDEX idx_pm_feature_iterations_version ON pm_feature_iterations (tenant_id, version_id);

-- ===== 项目资料 =====
CREATE TABLE pm_materials (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    project_id VARCHAR(36) NOT NULL,
    material_type VARCHAR(16) NOT NULL,                  -- manual / brochure / ppt
    version_id VARCHAR(36) NOT NULL DEFAULT '',
    knowledge_id VARCHAR(36) NOT NULL DEFAULT '',
    title VARCHAR(255) NOT NULL DEFAULT '',
    status VARCHAR(16) NOT NULL DEFAULT 'draft',         -- draft / published
    archived BOOLEAN NOT NULL DEFAULT FALSE,
    created_by VARCHAR(36) NOT NULL DEFAULT '',
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMP WITH TIME ZONE
);
CREATE INDEX idx_pm_materials_project ON pm_materials (tenant_id, project_id, material_type);

-- ===== 规划批次 =====
CREATE TABLE pm_plan_batches (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    project_id VARCHAR(36) NOT NULL,
    batch_name VARCHAR(128) NOT NULL,
    plan_type VARCHAR(16) NOT NULL DEFAULT 'initial',    -- initial / iteration
    version_id VARCHAR(36) NOT NULL DEFAULT '',
    change_summary TEXT NOT NULL DEFAULT '',
    status VARCHAR(16) NOT NULL DEFAULT 'draft',         -- draft / active / archived
    -- 5 步引导草稿与进度（对方案 §3.7 的补充）
    draft JSONB NOT NULL DEFAULT '{}',
    current_step SMALLINT NOT NULL DEFAULT 1,
    roadmap_knowledge_id VARCHAR(36) NOT NULL DEFAULT '',
    snapshot_knowledge_id VARCHAR(36) NOT NULL DEFAULT '',
    generated_at TIMESTAMP WITH TIME ZONE,
    snapshot_at TIMESTAMP WITH TIME ZONE,
    operator_id VARCHAR(36) NOT NULL DEFAULT '',
    activated_at TIMESTAMP WITH TIME ZONE,
    created_by VARCHAR(36) NOT NULL DEFAULT '',
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMP WITH TIME ZONE
);
CREATE INDEX idx_pm_plan_batches_project ON pm_plan_batches (tenant_id, project_id, created_at DESC);
-- 同一项目同时最多一个生效批次
CREATE UNIQUE INDEX uq_pm_plan_batches_active ON pm_plan_batches (tenant_id, project_id)
    WHERE status = 'active' AND deleted_at IS NULL;

-- ===== 异步任务 =====
CREATE TABLE pm_tasks (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    project_id VARCHAR(36) NOT NULL DEFAULT '',
    task_type VARCHAR(64) NOT NULL,
    status VARCHAR(16) NOT NULL DEFAULT 'pending',       -- pending / running / succeeded / failed
    progress SMALLINT NOT NULL DEFAULT 0,
    payload JSONB NOT NULL DEFAULT '{}',
    result JSONB NOT NULL DEFAULT '{}',
    error_message TEXT NOT NULL DEFAULT '',
    created_by VARCHAR(36) NOT NULL DEFAULT '',
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    finished_at TIMESTAMP WITH TIME ZONE
);
CREATE INDEX idx_pm_tasks_tenant_status ON pm_tasks (tenant_id, status, created_at DESC);
