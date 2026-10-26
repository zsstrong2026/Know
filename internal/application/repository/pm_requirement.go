package repository

import (
	"context"
	"errors"

	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

var ErrPMRequirementNotFound = errors.New("pm requirement not found")

type pmRequirementRepository struct{ db *gorm.DB }

// NewPMRequirementRepository 需求仓储
func NewPMRequirementRepository(db *gorm.DB) interfaces.PMRequirementRepository {
	return &pmRequirementRepository{db: db}
}

func (r *pmRequirementRepository) Create(ctx context.Context, req *types.PMRequirement) error {
	return r.db.WithContext(ctx).Create(req).Error
}

func (r *pmRequirementRepository) GetByID(ctx context.Context, tenantID uint64, id string) (*types.PMRequirement, error) {
	var req types.PMRequirement
	err := r.db.WithContext(ctx).Where("id = ? AND tenant_id = ?", id, tenantID).First(&req).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrPMRequirementNotFound
	}
	if err != nil {
		return nil, err
	}
	return &req, nil
}

func (r *pmRequirementRepository) List(
	ctx context.Context, tenantID uint64, projectID string, status string,
) ([]*types.PMRequirement, error) {
	var list []*types.PMRequirement
	q := r.db.WithContext(ctx).Where("tenant_id = ? AND project_id = ?", tenantID, projectID)
	if status != "" {
		q = q.Where("status = ?", status)
	}
	err := q.Order("updated_at DESC").Find(&list).Error
	return list, err
}

func (r *pmRequirementRepository) ListByIDs(
	ctx context.Context, tenantID uint64, ids []string,
) ([]*types.PMRequirement, error) {
	if len(ids) == 0 {
		return []*types.PMRequirement{}, nil
	}
	var list []*types.PMRequirement
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND id IN ?", tenantID, ids).
		Order("updated_at DESC").
		Find(&list).Error
	return list, err
}

func (r *pmRequirementRepository) Update(ctx context.Context, req *types.PMRequirement) error {
	return pmUpdateByID(ctx, r.db, req, req.ID, req.TenantID, ErrPMRequirementNotFound)
}

// Delete 删除需求：事务内先硬删版本关联（关联表无 deleted_at），
// 再软删附件与需求本体；需求已不存在时返回 ErrPMRequirementNotFound。
func (r *pmRequirementRepository) Delete(ctx context.Context, tenantID uint64, id string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(
			`DELETE FROM pm_version_requirements WHERE tenant_id = ? AND requirement_id = ?`,
			tenantID, id,
		).Error; err != nil {
			return err
		}
		if err := tx.Where("tenant_id = ? AND requirement_id = ?", tenantID, id).
			Delete(&types.PMRequirementAttachment{}).Error; err != nil {
			return err
		}
		res := tx.Where("id = ? AND tenant_id = ?", id, tenantID).Delete(&types.PMRequirement{})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return ErrPMRequirementNotFound
		}
		return nil
	})
}
