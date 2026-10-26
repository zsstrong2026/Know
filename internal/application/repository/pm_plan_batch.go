package repository

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

var ErrPMPlanBatchNotFound = errors.New("pm plan batch not found")

type pmPlanBatchRepository struct{ db *gorm.DB }

// NewPMPlanBatchRepository 规划批次仓储
func NewPMPlanBatchRepository(db *gorm.DB) interfaces.PMPlanBatchRepository {
	return &pmPlanBatchRepository{db: db}
}

func (r *pmPlanBatchRepository) Create(ctx context.Context, b *types.PMPlanBatch) error {
	return r.db.WithContext(ctx).Create(b).Error
}

func (r *pmPlanBatchRepository) GetByID(ctx context.Context, tenantID uint64, id string) (*types.PMPlanBatch, error) {
	var b types.PMPlanBatch
	err := r.db.WithContext(ctx).Where("id = ? AND tenant_id = ?", id, tenantID).First(&b).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrPMPlanBatchNotFound
	}
	if err != nil {
		return nil, err
	}
	return &b, nil
}

func (r *pmPlanBatchRepository) ListByProject(
	ctx context.Context, tenantID uint64, projectID string,
) ([]*types.PMPlanBatch, error) {
	var list []*types.PMPlanBatch
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND project_id = ?", tenantID, projectID).
		Order("created_at DESC").
		Find(&list).Error
	return list, err
}

func (r *pmPlanBatchRepository) Update(ctx context.Context, b *types.PMPlanBatch) error {
	return pmUpdateByID(ctx, r.db, b, b.ID, b.TenantID, ErrPMPlanBatchNotFound)
}

func (r *pmPlanBatchRepository) GetActiveByProject(
	ctx context.Context, tenantID uint64, projectID string,
) (*types.PMPlanBatch, error) {
	var b types.PMPlanBatch
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND project_id = ? AND status = ?",
			tenantID, projectID, types.PlanBatchStatusActive).
		First(&b).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrPMPlanBatchNotFound
	}
	if err != nil {
		return nil, err
	}
	return &b, nil
}

// ActivateBatch 事务内先归档同项目旧 active 批次，再把目标批次置 active。
// 「同时最多一个 active」由部分唯一索引兜底，两步必须同事务避免中间态。
func (r *pmPlanBatchRepository) ActivateBatch(
	ctx context.Context, tenantID uint64, projectID string, batchID string,
) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&types.PMPlanBatch{}).
			Where("tenant_id = ? AND project_id = ? AND status = ? AND id <> ?",
				tenantID, projectID, types.PlanBatchStatusActive, batchID).
			Update("status", types.PlanBatchStatusArchived).Error; err != nil {
			return err
		}

		res := tx.Model(&types.PMPlanBatch{}).
			Where("id = ? AND tenant_id = ? AND project_id = ?", batchID, tenantID, projectID).
			Updates(map[string]any{
				"status":       types.PlanBatchStatusActive,
				"activated_at": time.Now(),
			})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return ErrPMPlanBatchNotFound
		}
		return nil
	})
}

// Delete 软删规划批次：查不到返回 ErrPMPlanBatchNotFound
func (r *pmPlanBatchRepository) Delete(ctx context.Context, tenantID uint64, id string) error {
	if _, err := r.GetByID(ctx, tenantID, id); err != nil {
		return err
	}
	return r.db.WithContext(ctx).
		Delete(&types.PMPlanBatch{}, "id = ? AND tenant_id = ?", id, tenantID).Error
}
