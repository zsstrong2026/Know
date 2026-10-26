package repository

import (
	"context"
	"errors"

	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

var ErrPMRequirementAttachmentNotFound = errors.New("pm requirement attachment not found")

type pmRequirementAttachmentRepository struct{ db *gorm.DB }

// NewPMRequirementAttachmentRepository 需求附件仓储
func NewPMRequirementAttachmentRepository(db *gorm.DB) interfaces.PMRequirementAttachmentRepository {
	return &pmRequirementAttachmentRepository{db: db}
}

func (r *pmRequirementAttachmentRepository) Create(ctx context.Context, a *types.PMRequirementAttachment) error {
	return r.db.WithContext(ctx).Create(a).Error
}

func (r *pmRequirementAttachmentRepository) GetByID(
	ctx context.Context, tenantID uint64, id string,
) (*types.PMRequirementAttachment, error) {
	var a types.PMRequirementAttachment
	err := r.db.WithContext(ctx).Where("id = ? AND tenant_id = ?", id, tenantID).First(&a).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrPMRequirementAttachmentNotFound
	}
	if err != nil {
		return nil, err
	}
	return &a, nil
}

// ListByRequirement 列表不取文件正文，避免把 BYTEA 拉进内存
func (r *pmRequirementAttachmentRepository) ListByRequirement(
	ctx context.Context, tenantID uint64, requirementID string,
) ([]*types.PMRequirementAttachment, error) {
	var list []*types.PMRequirementAttachment
	err := r.db.WithContext(ctx).
		Select("id", "tenant_id", "requirement_id", "file_name", "content_type", "size",
			"text_content", "created_by", "created_at", "updated_at").
		Where("tenant_id = ? AND requirement_id = ?", tenantID, requirementID).
		Order("created_at ASC").
		Find(&list).Error
	return list, err
}

func (r *pmRequirementAttachmentRepository) Delete(ctx context.Context, tenantID uint64, id string) error {
	res := r.db.WithContext(ctx).Where("id = ? AND tenant_id = ?", id, tenantID).Delete(&types.PMRequirementAttachment{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrPMRequirementAttachmentNotFound
	}
	return nil
}
