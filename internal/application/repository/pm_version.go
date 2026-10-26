package repository

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

var (
	ErrPMVersionNotFound = errors.New("pm version not found")
	ErrPMVersionNoTaken  = errors.New("pm version number already exists")
)

type pmVersionRepository struct{ db *gorm.DB }

// NewPMVersionRepository 版本仓储（含版本-需求关联表）
func NewPMVersionRepository(db *gorm.DB) interfaces.PMVersionRepository {
	return &pmVersionRepository{db: db}
}

func (r *pmVersionRepository) Create(ctx context.Context, v *types.PMVersion) error {
	if err := r.db.WithContext(ctx).Create(v).Error; err != nil {
		// 版本号唯一索引：同项目定稿版本号重复说明并发生成，交由上层重试
		if isUniqueViolation(err) {
			return ErrPMVersionNoTaken
		}
		return err
	}
	return nil
}

func (r *pmVersionRepository) GetByID(ctx context.Context, tenantID uint64, id string) (*types.PMVersion, error) {
	var v types.PMVersion
	err := r.db.WithContext(ctx).Where("id = ? AND tenant_id = ?", id, tenantID).First(&v).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrPMVersionNotFound
	}
	if err != nil {
		return nil, err
	}
	return &v, nil
}

func (r *pmVersionRepository) List(ctx context.Context, tenantID uint64, projectID string) ([]*types.PMVersion, error) {
	var list []*types.PMVersion
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND project_id = ?", tenantID, projectID).
		Order("updated_at DESC").
		Find(&list).Error
	return list, err
}

func (r *pmVersionRepository) Update(ctx context.Context, v *types.PMVersion) error {
	return pmUpdateByID(ctx, r.db, v, v.ID, v.TenantID, ErrPMVersionNotFound)
}

// GetLatestVersionNo 取项目内最大的已定稿版本号；无历史版本返回 ""
func (r *pmVersionRepository) GetLatestVersionNo(
	ctx context.Context, tenantID uint64, projectID string,
) (string, error) {
	var nos []string
	// 只取非空版本号（定稿后才生成），避免空串参与比较
	err := r.db.WithContext(ctx).Model(&types.PMVersion{}).
		Where("tenant_id = ? AND project_id = ? AND version_no <> ''", tenantID, projectID).
		Pluck("version_no", &nos).Error
	if err != nil {
		return "", err
	}
	latest := ""
	for _, no := range nos {
		// 在 Go 侧比较，避免依赖 PostgreSQL 的 split_part，便于 SQLite 测试
		if latest == "" || pmCompareVersionNo(no, latest) > 0 {
			latest = no
		}
	}
	return latest, nil
}

// ReplaceRequirements 事务内全量替换版本关联需求（先删后插）
func (r *pmVersionRepository) ReplaceRequirements(
	ctx context.Context, versionID string, requirementIDs []string,
) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 关联表插入需要 tenant_id，先从版本行回填
		var v types.PMVersion
		err := tx.Select("id", "tenant_id").Where("id = ?", versionID).First(&v).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrPMVersionNotFound
		}
		if err != nil {
			return err
		}

		if err := tx.Where("version_id = ?", versionID).
			Delete(&types.PMVersionRequirement{}).Error; err != nil {
			return err
		}

		rows := make([]*types.PMVersionRequirement, 0, len(requirementIDs))
		seen := make(map[string]bool, len(requirementIDs))
		for _, rid := range requirementIDs {
			// 去重 + 跳过空值，防止撞唯一索引 (version_id, requirement_id)
			if rid == "" || seen[rid] {
				continue
			}
			seen[rid] = true
			rows = append(rows, &types.PMVersionRequirement{
				TenantID:      v.TenantID,
				VersionID:     v.ID,
				RequirementID: rid,
			})
		}
		if len(rows) == 0 {
			return nil
		}
		return tx.CreateInBatches(rows, 100).Error
	})
}

func (r *pmVersionRepository) ListRequirementIDs(ctx context.Context, versionID string) ([]string, error) {
	var ids []string
	err := r.db.WithContext(ctx).Model(&types.PMVersionRequirement{}).
		Where("version_id = ?", versionID).
		Order("created_at ASC, id ASC").
		Pluck("requirement_id", &ids).Error
	return ids, err
}

// CountVersionsByRequirementID 需求仍关联的版本数，用于判断是否需要回退需求状态
func (r *pmVersionRepository) CountVersionsByRequirementID(
	ctx context.Context, requirementID string,
) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&types.PMVersionRequirement{}).
		Where("requirement_id = ?", requirementID).
		Count(&count).Error
	return count, err
}

// RemoveRequirementFromAllVersions 删除需求与全部版本的关联（需求搁置）
func (r *pmVersionRepository) RemoveRequirementFromAllVersions(
	ctx context.Context, requirementID string,
) error {
	return r.db.WithContext(ctx).
		Where("requirement_id = ?", requirementID).
		Delete(&types.PMVersionRequirement{}).Error
}

// pmParseVersionNo 解析三段版本号；非法输入返回零值，保证排序不 panic
func pmParseVersionNo(no string) [3]int {
	var nums [3]int
	cur := strings.TrimPrefix(strings.TrimSpace(no), "v")
	for i, part := range strings.SplitN(cur, ".", 3) {
		n, err := strconv.Atoi(part)
		if err != nil {
			return [3]int{}
		}
		nums[i] = n
	}
	return nums
}

// pmCompareVersionNo 按 major.minor.patch 数值比较，a>b 返回 1
func pmCompareVersionNo(a, b string) int {
	x, y := pmParseVersionNo(a), pmParseVersionNo(b)
	for i := 0; i < 3; i++ {
		if x[i] != y[i] {
			if x[i] > y[i] {
				return 1
			}
			return -1
		}
	}
	return 0
}
