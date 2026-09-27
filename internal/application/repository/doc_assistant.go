package repository

import (
	"context"
	"errors"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

var (
	// ErrDocTemplateNotFound 模板不存在
	ErrDocTemplateNotFound = errors.New("doc template not found")
	// ErrDocTaskNotFound 任务不存在
	ErrDocTaskNotFound = errors.New("doc generation task not found")
	// ErrDocDocumentNotFound 文档不存在
	ErrDocDocumentNotFound = errors.New("doc document not found")
)

// DocTemplateRepository 基于 GORM 的模板仓储实现
type DocTemplateRepository struct {
	db *gorm.DB
}

// NewDocTemplateRepository 创建模板仓储
func NewDocTemplateRepository(db *gorm.DB) interfaces.DocTemplateRepository {
	return &DocTemplateRepository{db: db}
}

func (r *DocTemplateRepository) Create(ctx context.Context, tpl *types.DocTemplate) error {
	if tpl == nil {
		return errors.New("doc template is nil")
	}
	return r.db.WithContext(ctx).Create(tpl).Error
}

func (r *DocTemplateRepository) Update(ctx context.Context, tpl *types.DocTemplate) error {
	if tpl == nil || tpl.ID == "" {
		return errors.New("doc template id is empty")
	}
	return r.db.WithContext(ctx).Model(tpl).Updates(map[string]interface{}{
		"name":          tpl.Name,
		"description":   tpl.Description,
		"global_config": tpl.GlobalConfig,
		"chapters":      tpl.Chapters,
		"updated_at":    time.Now().UTC(),
	}).Error
}

func (r *DocTemplateRepository) Delete(ctx context.Context, tenantID uint64, ownerID, id string) error {
	if id == "" {
		return errors.New("doc template id is empty")
	}
	res := r.db.WithContext(ctx).
		Where("id = ?", id).
		Where("tenant_id = ?", tenantID).
		Where("owner_id = ?", ownerID).
		Delete(&types.DocTemplate{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrDocTemplateNotFound
	}
	return nil
}

func (r *DocTemplateRepository) FindByID(ctx context.Context, tenantID uint64, ownerID, id string) (*types.DocTemplate, error) {
	if id == "" {
		return nil, errors.New("doc template id is empty")
	}
	var tpl types.DocTemplate
	if err := r.db.WithContext(ctx).
		Where("id = ?", id).
		Where("tenant_id = ?", tenantID).
		Where("owner_id = ?", ownerID).
		First(&tpl).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrDocTemplateNotFound
		}
		return nil, err
	}
	return &tpl, nil
}

func (r *DocTemplateRepository) ListByOwner(ctx context.Context, tenantID uint64, ownerID string) ([]*types.DocTemplate, error) {
	var templates []*types.DocTemplate
	if err := r.db.WithContext(ctx).
		Where("tenant_id = ?", tenantID).
		Where("owner_id = ?", ownerID).
		Order("created_at DESC").
		Find(&templates).Error; err != nil {
		return nil, err
	}
	return templates, nil
}

// DocTaskRepository 基于 GORM 的生成任务仓储实现
type DocTaskRepository struct {
	db *gorm.DB
}

// NewDocTaskRepository 创建生成任务仓储
func NewDocTaskRepository(db *gorm.DB) interfaces.DocTaskRepository {
	return &DocTaskRepository{db: db}
}

func (r *DocTaskRepository) Create(ctx context.Context, task *types.DocGenerationTask) error {
	if task == nil {
		return errors.New("doc generation task is nil")
	}
	return r.db.WithContext(ctx).Create(task).Error
}

func (r *DocTaskRepository) Update(ctx context.Context, task *types.DocGenerationTask) error {
	if task == nil || task.ID == "" {
		return errors.New("doc generation task id is empty")
	}
	return r.db.WithContext(ctx).Model(task).Updates(map[string]interface{}{
		"status":      task.Status,
		"progress":    task.Progress,
		"error":       task.Error,
		"finished_at": task.FinishedAt,
	}).Error
}

func (r *DocTaskRepository) FindByID(ctx context.Context, tenantID uint64, ownerID, id string) (*types.DocGenerationTask, error) {
	if id == "" {
		return nil, errors.New("doc generation task id is empty")
	}
	var task types.DocGenerationTask
	if err := r.db.WithContext(ctx).
		Where("id = ?", id).
		Where("tenant_id = ?", tenantID).
		Where("owner_id = ?", ownerID).
		First(&task).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrDocTaskNotFound
		}
		return nil, err
	}
	return &task, nil
}

func (r *DocTaskRepository) ListByOwner(ctx context.Context, tenantID uint64, ownerID string) ([]*types.DocGenerationTask, error) {
	var tasks []*types.DocGenerationTask
	if err := r.db.WithContext(ctx).
		Where("tenant_id = ?", tenantID).
		Where("owner_id = ?", ownerID).
		Order("created_at DESC").
		Find(&tasks).Error; err != nil {
		return nil, err
	}
	return tasks, nil
}

func (r *DocTaskRepository) Delete(ctx context.Context, tenantID uint64, ownerID, id string) error {
	if id == "" {
		return errors.New("doc generation task id is empty")
	}
	res := r.db.WithContext(ctx).
		Where("id = ?", id).
		Where("tenant_id = ?", tenantID).
		Where("owner_id = ?", ownerID).
		Delete(&types.DocGenerationTask{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrDocTaskNotFound
	}
	return nil
}

// DocDocumentRepository 基于 GORM 的生成文档仓储实现
type DocDocumentRepository struct {
	db *gorm.DB
}

// NewDocDocumentRepository 创建生成文档仓储
func NewDocDocumentRepository(db *gorm.DB) interfaces.DocDocumentRepository {
	return &DocDocumentRepository{db: db}
}

func (r *DocDocumentRepository) Create(ctx context.Context, doc *types.DocDocument) error {
	if doc == nil {
		return errors.New("doc document is nil")
	}
	return r.db.WithContext(ctx).Create(doc).Error
}

func (r *DocDocumentRepository) Update(ctx context.Context, doc *types.DocDocument) error {
	if doc == nil || doc.ID == "" {
		return errors.New("doc document id is empty")
	}
	return r.db.WithContext(ctx).Model(doc).Updates(map[string]interface{}{
		"status":   doc.Status,
		"chapters": doc.Chapters,
		"md_path":  doc.MdPath,
	}).Error
}

func (r *DocDocumentRepository) FindByID(ctx context.Context, tenantID uint64, ownerID, id string) (*types.DocDocument, error) {
	if id == "" {
		return nil, errors.New("doc document id is empty")
	}
	var doc types.DocDocument
	if err := r.db.WithContext(ctx).
		Where("id = ?", id).
		Where("tenant_id = ?", tenantID).
		Where("owner_id = ?", ownerID).
		First(&doc).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrDocDocumentNotFound
		}
		return nil, err
	}
	return &doc, nil
}

func (r *DocDocumentRepository) ListByTask(ctx context.Context, tenantID uint64, ownerID, taskID string) ([]*types.DocDocument, error) {
	var docs []*types.DocDocument
	if err := r.db.WithContext(ctx).
		Where("tenant_id = ?", tenantID).
		Where("owner_id = ?", ownerID).
		Where("task_id = ?", taskID).
		Order("version DESC").
		Find(&docs).Error; err != nil {
		return nil, err
	}
	return docs, nil
}

func (r *DocDocumentRepository) MaxVersion(ctx context.Context, tenantID uint64, taskID string) (int, error) {
	var maxVer int
	if err := r.db.WithContext(ctx).
		Model(&types.DocDocument{}).
		Where("tenant_id = ?", tenantID).
		Where("task_id = ?", taskID).
		Select("COALESCE(MAX(version), 0)").
		Scan(&maxVer).Error; err != nil {
		return 0, err
	}
	return maxVer, nil
}

func (r *DocDocumentRepository) Delete(ctx context.Context, tenantID uint64, ownerID, id string) error {
	if id == "" {
		return errors.New("doc document id is empty")
	}
	res := r.db.WithContext(ctx).
		Where("id = ?", id).
		Where("tenant_id = ?", tenantID).
		Where("owner_id = ?", ownerID).
		Delete(&types.DocDocument{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrDocDocumentNotFound
	}
	return nil
}
