package repository

import (
	"context"
	"errors"

	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

var (
	ErrPMProjectNotFound  = errors.New("pm project not found")
	ErrPMProjectNameTaken = errors.New("pm project name already exists")
)

type pmProjectRepository struct{ db *gorm.DB }

// NewPMProjectRepository 项目仓储
func NewPMProjectRepository(db *gorm.DB) interfaces.PMProjectRepository {
	return &pmProjectRepository{db: db}
}

func (r *pmProjectRepository) Create(ctx context.Context, p *types.PMProject) error {
	if err := r.db.WithContext(ctx).Create(p).Error; err != nil {
		// 唯一索引兜底并发重名，翻译成业务错误供上层提示「项目已存在」
		if isUniqueViolation(err) {
			return ErrPMProjectNameTaken
		}
		return err
	}
	return nil
}

func (r *pmProjectRepository) GetByID(ctx context.Context, tenantID uint64, id string) (*types.PMProject, error) {
	var p types.PMProject
	err := r.db.WithContext(ctx).Where("id = ? AND tenant_id = ?", id, tenantID).First(&p).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrPMProjectNotFound
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func (r *pmProjectRepository) GetByName(ctx context.Context, tenantID uint64, name string) (*types.PMProject, error) {
	var p types.PMProject
	err := r.db.WithContext(ctx).Where("tenant_id = ? AND name = ?", tenantID, name).First(&p).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrPMProjectNotFound
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func (r *pmProjectRepository) List(ctx context.Context, tenantID uint64, status string) ([]*types.PMProject, error) {
	var list []*types.PMProject
	q := r.db.WithContext(ctx).Where("tenant_id = ?", tenantID)
	if status != "" {
		q = q.Where("status = ?", status)
	}
	// PM 数据量小，不做分页；统一按最近更新时间倒序
	err := q.Order("updated_at DESC").Find(&list).Error
	return list, err
}

func (r *pmProjectRepository) Update(ctx context.Context, p *types.PMProject) error {
	return pmUpdateByID(ctx, r.db, p, p.ID, p.TenantID, ErrPMProjectNotFound)
}

// Delete 删除项目：事务内先清理无软删标记的关联/任务表，再软删业务表，最后软删项目。
// 先删子表再删项目，避免半途失败留下孤立数据；项目已不存在时返回 ErrPMProjectNotFound。
func (r *pmProjectRepository) Delete(ctx context.Context, tenantID uint64, id string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 关联表无 deleted_at，按项目范围硬删
		hardDeletes := []struct {
			stmt string
			args []any
		}{
			{
				`DELETE FROM pm_version_requirements
				 WHERE tenant_id = ? AND version_id IN (
				   SELECT id FROM pm_versions WHERE tenant_id = ? AND project_id = ?)`,
				[]any{tenantID, tenantID, id},
			},
			{
				`DELETE FROM pm_feature_iterations
				 WHERE tenant_id = ? AND feature_id IN (
				   SELECT id FROM pm_features WHERE tenant_id = ? AND project_id = ?)`,
				[]any{tenantID, tenantID, id},
			},
			{
				`DELETE FROM pm_tasks WHERE tenant_id = ? AND project_id = ?`,
				[]any{tenantID, id},
			},
		}
		for _, d := range hardDeletes {
			if err := tx.Exec(d.stmt, d.args...).Error; err != nil {
				return err
			}
		}

		// 业务表保留软删，便于审计与问题追溯
		for _, model := range []any{
			&types.PMRequirement{}, &types.PMVersion{}, &types.PMFeature{},
			&types.PMMaterial{}, &types.PMPlanBatch{},
		} {
			if err := tx.Where("tenant_id = ? AND project_id = ?", tenantID, id).Delete(model).Error; err != nil {
				return err
			}
		}

		res := tx.Where("id = ? AND tenant_id = ?", id, tenantID).Delete(&types.PMProject{})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return ErrPMProjectNotFound
		}
		return nil
	})
}
