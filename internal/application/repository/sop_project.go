package repository

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// 编译期断言：项目仓储实现必须满足接口契约（CountActiveRuns 依赖 sop_runs）。
var _ interfaces.SopProjectRepository = (*SopProjectRepo)(nil)

// SopProjectRepo 是 SOP 项目仓储的 GORM 实现。
// 所有方法都显式接收 tenantID 并写进 WHERE，避免跨租户越权。
type SopProjectRepo struct{ db *gorm.DB }

// NewSopProjectRepo 构造 SOP 项目仓储。
func NewSopProjectRepo(db *gorm.DB) *SopProjectRepo { return &SopProjectRepo{db: db} }

// Create 插入项目：仓库绑定先做去重与默认值规范化。
func (r *SopProjectRepo) Create(ctx context.Context, p *types.SopProject) error {
	p.Repos = types.NormalizeSopRepos(p.Repos)
	return r.db.WithContext(ctx).Create(p).Error
}

// GetByID 按 (租户, id) 取项目，查不到返回 gorm.ErrRecordNotFound。
func (r *SopProjectRepo) GetByID(ctx context.Context, tenantID uint64, id string) (*types.SopProject, error) {
	var p types.SopProject
	err := r.db.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, id).First(&p).Error
	return &p, err
}

// ListByTenant 按创建时间升序列出租户下全部项目（软删自动排除）。
func (r *SopProjectRepo) ListByTenant(ctx context.Context, tenantID uint64) ([]types.SopProject, error) {
	var ps []types.SopProject
	err := r.db.WithContext(ctx).Where("tenant_id = ?", tenantID).Order("created_at ASC").Find(&ps).Error
	return ps, err
}

// Update 全列保存项目；code 创建后不可改由 service 层守卫。
// 条件强制拼接 tenant_id（与 pmUpdateByID 同构），避免拿别的租户的
// 结构体把行改写过去；未命中（软删或不存在）返回 gorm.ErrRecordNotFound，
// 不再退化成 Save 的 upsert 而抛出 UNIQUE 约束错误。
// JSON 列不允许 NULL，空值在落库前补成空集合。
func (r *SopProjectRepo) Update(ctx context.Context, p *types.SopProject) error {
	if p.Repos == nil {
		p.Repos = []types.SopProjectRepo{}
	}
	if p.DomainData == nil {
		p.DomainData = map[string]any{}
	}
	return pmUpdateByID(ctx, r.db, p, p.ID, p.TenantID, gorm.ErrRecordNotFound)
}

// Delete 软删项目（只按租户 + id 命中）。
func (r *SopProjectRepo) Delete(ctx context.Context, tenantID uint64, id string) error {
	return r.db.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, id).Delete(&types.SopProject{}).Error
}

// AllocItemNo 事务内自增编号游标并返回本次分配编号（首次=1）。
// PostgreSQL 用 UPDATE … RETURNING 一条语句完成；SQLite 退化为同事务
// UPDATE 后 SELECT，事务内写锁保证并发调用不会拿到重复编号。
func (r *SopProjectRepo) AllocItemNo(ctx context.Context, tenantID uint64, projectID string) (int, error) {
	var no int
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if tx.Dialector.Name() == "postgres" {
			res := tx.Raw(`UPDATE sop_projects SET next_item_no = next_item_no + 1, updated_at = NOW()
				WHERE id = ? AND tenant_id = ? AND deleted_at IS NULL RETURNING next_item_no`,
				projectID, tenantID).Scan(&no)
			if res.Error != nil {
				return res.Error
			}
			if res.RowsAffected == 0 {
				return gorm.ErrRecordNotFound
			}
			return nil
		}
		res := tx.Exec(`UPDATE sop_projects SET next_item_no = next_item_no + 1
			WHERE id = ? AND tenant_id = ? AND deleted_at IS NULL`, projectID, tenantID)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return tx.Raw(`SELECT next_item_no FROM sop_projects WHERE id = ?`, projectID).Scan(&no).Error
	})
	return no, err
}

// mutateRepos 在事务内读出项目、变形仓库列表后整列写回。
// 写回前统一过 NormalizeSopRepos，让「默认仓库顺延」这类规则只在一处生效。
func (r *SopProjectRepo) mutateRepos(
	ctx context.Context, tenantID uint64, id string,
	fn func([]types.SopProjectRepo) ([]types.SopProjectRepo, error),
) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var p types.SopProject
		if err := tx.Where("tenant_id = ? AND id = ?", tenantID, id).First(&p).Error; err != nil {
			return err
		}
		repos, err := fn(p.Repos)
		if err != nil {
			return err
		}
		// JSON 列按字符串写回：两种 dialector 都无需理解 Go 结构体。
		raw, err := json.Marshal(types.NormalizeSopRepos(repos))
		if err != nil {
			return err
		}
		return tx.Model(&p).Update("repos", string(raw)).Error
	})
}

// AddRepo 绑定一个仓库并返回入库后的绑定（含 id/name/branch/default）。
func (r *SopProjectRepo) AddRepo(
	ctx context.Context, tenantID uint64, id string, repo types.SopProjectRepo,
) (*types.SopProjectRepo, error) {
	if err := types.ValidateSopRepoURL(repo.URL); err != nil {
		return nil, err
	}
	var added *types.SopProjectRepo
	err := r.mutateRepos(ctx, tenantID, id, func(rs []types.SopProjectRepo) ([]types.SopProjectRepo, error) {
		for _, existing := range rs {
			if existing.URL == repo.URL {
				return nil, fmt.Errorf("该仓库已绑定")
			}
		}
		if repo.ID == "" {
			repo.ID = uuid.NewString()
		}
		next := types.NormalizeSopRepos(append(append([]types.SopProjectRepo{}, rs...), repo))
		added = &next[len(next)-1]
		return next, nil
	})
	return added, err
}

// RemoveRepo 解绑仓库：剩余第一个自动成为默认。
func (r *SopProjectRepo) RemoveRepo(ctx context.Context, tenantID uint64, id, repoID string) error {
	return r.mutateRepos(ctx, tenantID, id, func(rs []types.SopProjectRepo) ([]types.SopProjectRepo, error) {
		out := make([]types.SopProjectRepo, 0, len(rs))
		for _, x := range rs {
			if x.ID != repoID {
				out = append(out, x)
			}
		}
		return out, nil // NormalizeSopRepos 会把默认顺延到剩余第一个
	})
}

// SetDefaultRepo 把指定仓库设为默认，并把它排到列表首位。
func (r *SopProjectRepo) SetDefaultRepo(ctx context.Context, tenantID uint64, id, repoID string) error {
	return r.mutateRepos(ctx, tenantID, id, func(rs []types.SopProjectRepo) ([]types.SopProjectRepo, error) {
		out := make([]types.SopProjectRepo, 0, len(rs))
		found := false
		for _, x := range rs {
			if x.ID == repoID {
				found = true
				x.Default = true
				out = append([]types.SopProjectRepo{x}, out...) // 默认仓库排第一
			} else {
				x.Default = false
				out = append(out, x)
			}
		}
		if !found {
			return nil, gorm.ErrRecordNotFound
		}
		return out, nil
	})
}

// CountActiveRuns 统计项目下 status ∈ queued/running/waiting/paused 的运行数
// （「进行中运行」额度口径，见 types.IsSopRunActive）。租户过滤强制拼接。
func (r *SopProjectRepo) CountActiveRuns(ctx context.Context, tenantID uint64, projectID string) (int64, error) {
	var n int64
	err := r.db.WithContext(ctx).Model(&types.SopRun{}).
		Where("tenant_id = ? AND project_id = ?", tenantID, projectID).
		Where("status IN ?", types.SopRunActiveStatuses).
		Count(&n).Error
	return n, err
}
