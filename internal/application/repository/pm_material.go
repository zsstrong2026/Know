package repository

import (
	"context"
	"errors"

	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

var ErrPMMaterialNotFound = errors.New("pm material not found")

type pmMaterialRepository struct{ db *gorm.DB }

// NewPMMaterialRepository 项目资料仓储
func NewPMMaterialRepository(db *gorm.DB) interfaces.PMMaterialRepository {
	return &pmMaterialRepository{db: db}
}

func (r *pmMaterialRepository) Create(ctx context.Context, m *types.PMMaterial) error {
	return r.db.WithContext(ctx).Create(m).Error
}

func (r *pmMaterialRepository) GetByID(ctx context.Context, tenantID uint64, id string) (*types.PMMaterial, error) {
	var m types.PMMaterial
	err := r.db.WithContext(ctx).Where("id = ? AND tenant_id = ?", id, tenantID).First(&m).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrPMMaterialNotFound
	}
	if err != nil {
		return nil, err
	}
	return &m, nil
}

func (r *pmMaterialRepository) List(
	ctx context.Context, tenantID uint64, projectID string, materialType string,
) ([]*types.PMMaterial, error) {
	var list []*types.PMMaterial
	q := r.db.WithContext(ctx).Where("tenant_id = ? AND project_id = ?", tenantID, projectID)
	if materialType != "" {
		q = q.Where("material_type = ?", materialType)
	}
	err := q.Order("updated_at DESC").Find(&list).Error
	return list, err
}

func (r *pmMaterialRepository) Update(ctx context.Context, m *types.PMMaterial) error {
	return pmUpdateByID(ctx, r.db, m, m.ID, m.TenantID, ErrPMMaterialNotFound)
}

// Delete 软删（DeletedAt 由 GORM 维护）；先按租户确认存在，避免跨租户误删
func (r *pmMaterialRepository) Delete(ctx context.Context, tenantID uint64, id string) error {
	if _, err := r.GetByID(ctx, tenantID, id); err != nil {
		return err
	}
	return r.db.WithContext(ctx).Delete(&types.PMMaterial{}, "id = ? AND tenant_id = ?", id, tenantID).Error
}
