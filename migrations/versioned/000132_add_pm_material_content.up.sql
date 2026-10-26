-- 资料草稿正文：生成任务完成后回写，发布时据此落库知识库并导出 docx/pptx
ALTER TABLE pm_materials
    ADD COLUMN IF NOT EXISTS content TEXT NOT NULL DEFAULT '';
