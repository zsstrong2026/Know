package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/hibiken/asynq"
)

// DocTemplateRepository 文档模板仓储（个人私有：tenant_id + owner_id 双隔离）
type DocTemplateRepository interface {
	Create(ctx context.Context, tpl *types.DocTemplate) error
	Update(ctx context.Context, tpl *types.DocTemplate) error
	// Delete 软删，返回是否命中
	Delete(ctx context.Context, tenantID uint64, ownerID, id string) error
	FindByID(ctx context.Context, tenantID uint64, ownerID, id string) (*types.DocTemplate, error)
	ListByOwner(ctx context.Context, tenantID uint64, ownerID string) ([]*types.DocTemplate, error)
}

// DocTaskRepository 文档生成任务仓储
type DocTaskRepository interface {
	Create(ctx context.Context, task *types.DocGenerationTask) error
	Update(ctx context.Context, task *types.DocGenerationTask) error
	FindByID(ctx context.Context, tenantID uint64, ownerID, id string) (*types.DocGenerationTask, error)
	ListByOwner(ctx context.Context, tenantID uint64, ownerID string) ([]*types.DocGenerationTask, error)
	// Delete 物理删除（任务删除时级联删除文档）
	Delete(ctx context.Context, tenantID uint64, ownerID, id string) error
}

// DocDocumentRepository 生成文档仓储（版本化）
type DocDocumentRepository interface {
	Create(ctx context.Context, doc *types.DocDocument) error
	Update(ctx context.Context, doc *types.DocDocument) error
	FindByID(ctx context.Context, tenantID uint64, ownerID, id string) (*types.DocDocument, error)
	ListByTask(ctx context.Context, tenantID uint64, ownerID, taskID string) ([]*types.DocDocument, error)
	// ListDocumentsByOwner 跨任务的全局文档列表（P2-5），返回摘要与是否还有下一页。
	// latest_only 由 filter 决定；标题取自任务的 theme。
	ListDocumentsByOwner(
		ctx context.Context, tenantID uint64, ownerID string, filter types.DocDocumentFilter,
	) ([]*types.DocDocumentSummary, bool, error)
	MaxVersion(ctx context.Context, tenantID uint64, taskID string) (int, error)
	// Delete 物理删除（用于任务/文档删除）
	Delete(ctx context.Context, tenantID uint64, ownerID, id string) error
}

// DocArchiveStatusFiller 给章节文档列表批量补归档状态（P2-5）。
//
// 由工作台服务实现（source_type='doc' 的映射在 workbench_archives），
// 放在这里而不是让 doc_assistant 反向依赖 workbench 包：依赖方向仍是
// workbench -> doc_assistant 的注入顺序，只在 handler 组装时用窄接口桥接。
type DocArchiveStatusFiller interface {
	FillDocArchiveStatus(ctx context.Context, docs []*types.DocDocumentSummary) error
}

// DocAssistantService 文档助手服务接口
type DocAssistantService interface {
	// ---- 模板 CRUD（个人私有） ----
	CreateTemplate(ctx context.Context, tpl *types.DocTemplate) (*types.DocTemplate, error)
	UpdateTemplate(ctx context.Context, tpl *types.DocTemplate) (*types.DocTemplate, error)
	DeleteTemplate(ctx context.Context, id string) error
	GetTemplate(ctx context.Context, id string) (*types.DocTemplate, error)
	ListTemplates(ctx context.Context) ([]*types.DocTemplate, error)

	// ---- 生成任务 ----
	// CreateGenerationTask 创建生成任务并入队异步执行
	CreateGenerationTask(ctx context.Context, task *types.DocGenerationTask) (*types.DocGenerationTask, error)
	// ProcessGeneration 异步任务 handler（逐章生成，checkpoint 续跑）
	ProcessGeneration(ctx context.Context, t *asynq.Task) error
	ListTasks(ctx context.Context) ([]*types.DocGenerationTask, error)
	GetTask(ctx context.Context, taskID string) (*types.DocGenerationTask, error)
	DeleteTask(ctx context.Context, taskID string) error

	// ---- 文档（版本化） ----
	GetDocument(ctx context.Context, docID string) (*types.DocDocument, error)
	// ListDocuments 列出任务下全部版本（倒序）
	ListDocuments(ctx context.Context, taskID string) ([]*types.DocDocument, error)
	// ListAllDocuments 跨任务的全局文档列表（P2-5）；分页与搜索由 filter 决定
	ListAllDocuments(
		ctx context.Context, filter types.DocDocumentFilter,
	) (*types.DocDocumentPage, error)
	// RegenerateChapter 重新生成某章（v+1 仅替换该章）
	RegenerateChapter(ctx context.Context, docID string, chapterNo int) (*types.DocDocument, error)
	// UpdateChapterContent 编辑章节文字内容（v+1）
	UpdateChapterContent(ctx context.Context, docID string, chapterNo int, content string) (*types.DocDocument, error)
	DeleteDocument(ctx context.Context, docID string) error

	// ---- 导出 ----
	// RenderMarkdown 渲染文档 md 内容（供下载/预览）
	RenderMarkdown(ctx context.Context, doc *types.DocDocument) (string, error)
	// RenderWord 渲染文档 docx 二进制
	RenderWord(ctx context.Context, doc *types.DocDocument) ([]byte, error)
}
