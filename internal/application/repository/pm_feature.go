package repository

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

var ErrPMFeatureNotFound = errors.New("pm feature not found")

type pmFeatureRepository struct{ db *gorm.DB }

// NewPMFeatureRepository 功能清单仓储（含迭代记录）
func NewPMFeatureRepository(db *gorm.DB) interfaces.PMFeatureRepository {
	return &pmFeatureRepository{db: db}
}

func (r *pmFeatureRepository) Create(ctx context.Context, f *types.PMFeature) error {
	return r.db.WithContext(ctx).Create(f).Error
}

func (r *pmFeatureRepository) CreateBatch(ctx context.Context, fs []*types.PMFeature) error {
	if len(fs) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).CreateInBatches(fs, 100).Error
}

func (r *pmFeatureRepository) GetByID(ctx context.Context, tenantID uint64, id string) (*types.PMFeature, error) {
	var f types.PMFeature
	err := r.db.WithContext(ctx).Where("id = ? AND tenant_id = ?", id, tenantID).First(&f).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrPMFeatureNotFound
	}
	if err != nil {
		return nil, err
	}
	return &f, nil
}

func (r *pmFeatureRepository) ListByProject(
	ctx context.Context, tenantID uint64, projectID string,
) ([]*types.PMFeature, error) {
	var list []*types.PMFeature
	// 前端按 parent_id 组装三级树，created_at 作为同级稳定顺序
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND project_id = ?", tenantID, projectID).
		Order("updated_at DESC, created_at ASC").
		Find(&list).Error
	return list, err
}

func (r *pmFeatureRepository) Update(ctx context.Context, f *types.PMFeature) error {
	return pmUpdateByID(ctx, r.db, f, f.ID, f.TenantID, ErrPMFeatureNotFound)
}

func (r *pmFeatureRepository) CreateIteration(ctx context.Context, it *types.PMFeatureIteration) error {
	if it.ChangedAt.IsZero() {
		it.ChangedAt = time.Now()
	}
	return r.db.WithContext(ctx).Create(it).Error
}

// ListIterations 按版本上线计划日升序返回，无计划日的记录排最后
func (r *pmFeatureRepository) ListIterations(
	ctx context.Context, featureID string,
) ([]*types.PMFeatureIteration, error) {
	var list []*types.PMFeatureIteration
	err := r.db.WithContext(ctx).
		Table("pm_feature_iterations AS fi").
		Select("fi.*").
		Joins("LEFT JOIN pm_versions AS v ON v.id = fi.version_id AND v.deleted_at IS NULL").
		Where("fi.feature_id = ?", featureID).
		Order("CASE WHEN v.plan_launch_date IS NULL THEN 1 ELSE 0 END, " +
			"v.plan_launch_date ASC, fi.changed_at ASC").
		Scan(&list).Error
	return list, err
}
