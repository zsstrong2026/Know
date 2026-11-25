package repository

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

// 编译期断言：模板仓储实现必须满足接口契约。
var _ interfaces.SopTemplateRepository = (*SopTemplateRepo)(nil)

// ErrSopTemplateBuiltinReadonly 内置模板只读：Create / Update / Delete 一律拒绝，
// 修改路径是 Copy 出副本后再改（spec §3.3）。
//
// Create 拒绝 IsBuiltin=true 是归属边界而非风格约束：读路径带 `OR is_builtin = true`，
// 若放行任意调用方插入内置行，该行就对所有租户可见（跨租户读泄漏）。内置模板只能由
// seed 以 tenant_id=0 的 SQL migration 下发，仓储方法不作为种子入口。
var ErrSopTemplateBuiltinReadonly = errors.New("sop template: builtin is readonly")

// SopTemplateRepo 是 SOP 流程模板与发布 revision 的 GORM 实现。
//
// 不变式：
//   - 每条查询都带 tenant_id（内置模板行按 is_builtin 跨租户只读共享）；
//   - definition 只是当前草稿；运行引用 revision 的冻结快照，发布后改草稿不影响已发布的
//     revision（发布即冻结，spec §3.3）；
//   - status / version / published_revision_id 由 Publish 独占写入，Update 不碰，
//     避免 DTO 零值把发布态冲回草稿。
type SopTemplateRepo struct{ db *gorm.DB }

// NewSopTemplateRepo 构造 SOP 模板仓储。
func NewSopTemplateRepo(db *gorm.DB) *SopTemplateRepo { return &SopTemplateRepo{db: db} }

// Create 插入模板：草稿默认值（status=draft/version=1）由模型 BeforeCreate 补齐。
//
// IsBuiltin=true 一律拒绝（ErrSopTemplateBuiltinReadonly）：内置行跨租户可读，
// 只能由 seed SQL 以 tenant_id=0 下发，否则任意租户都能建出全员可见的模板。
func (r *SopTemplateRepo) Create(ctx context.Context, tpl *types.SopTemplate) error {
	if tpl != nil && tpl.IsBuiltin {
		return ErrSopTemplateBuiltinReadonly
	}
	return r.db.WithContext(ctx).Create(tpl).Error
}

// sopTemplateReadPredicate 是读路径的归属谓词：本租户的行 + 内置模板。
// 内置模板由 T7 以 tenant_id=0 的 seed 下发并全租户只读共享；跨租户可见不构成
// 越权修改面——Update/Delete 对内置行返回只读错误，Publish 按 TenantID 严格拒绝。
const sopTemplateReadPredicate = "id = ? AND (tenant_id = ? OR is_builtin = ?)"

// GetByID 按 (租户, id) 取模板；内置模板跨租户只读可见。
// 查不到返回 gorm.ErrRecordNotFound。
func (r *SopTemplateRepo) GetByID(ctx context.Context, tenantID uint64, id string) (*types.SopTemplate, error) {
	var tpl types.SopTemplate
	err := r.db.WithContext(ctx).
		Where(sopTemplateReadPredicate, id, tenantID, true).
		First(&tpl).Error
	return &tpl, err
}

// ListByTenant 列本租户可用模板：本租户全局（project_id 为空）+ 指定项目 + 内置模板。
// 别的项目、别的租户的自建模板一律不返回。
func (r *SopTemplateRepo) ListByTenant(
	ctx context.Context, tenantID uint64, projectID string,
) ([]types.SopTemplate, error) {
	q := r.db.WithContext(ctx).Where(
		"(tenant_id = ? AND (project_id = ? OR project_id = ?)) OR is_builtin = ?",
		tenantID, "", projectID, true,
	)
	var list []types.SopTemplate
	err := q.Order("created_at ASC").Find(&list).Error
	return list, err
}

// sopTemplateUpdateColumns 是 Update 允许写入的列白名单（用户可编辑的业务字段）。
//
// 其余列一律不由 Update 改动：
//   - status/version/published_revision_id：发布态归属列，唯一写入口是 Publish，
//     否则 DTO 零值会把已发布模板冲回 draft、或伪造 version；
//   - tenant_id/id/project_id/is_builtin/created_by/created_at/deleted_at：归属与不可变列。
var sopTemplateUpdateColumns = []string{
	"name", "description", "definition", "metric_category", "updated_at",
}

// Update 只写可编辑列白名单；内置模板返回 ErrSopTemplateBuiltinReadonly。
// definition/name 的零值允许落库（清空描述是合法编辑）；发布态归属列不变。
// 条件强制拼接 tenant_id（与 pmUpdateByID 同构），未命中返回 gorm.ErrRecordNotFound。
func (r *SopTemplateRepo) Update(ctx context.Context, tpl *types.SopTemplate) error {
	if tpl == nil {
		return gorm.ErrRecordNotFound
	}
	types.EnsureSopDefinitionSlices(&tpl.Definition)
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		cur, err := requireSopTemplate(tx, tpl.TenantID, tpl.ID)
		if err != nil {
			return err
		}
		if cur.IsBuiltin {
			return ErrSopTemplateBuiltinReadonly
		}
		tpl.UpdatedAt = time.Now()
		res := tx.Model(&types.SopTemplate{}).
			Where("id = ? AND tenant_id = ?", tpl.ID, tpl.TenantID).
			Select(sopTemplateUpdateColumns).
			Updates(tpl)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return nil
	})
}

// Delete 软删模板；内置模板返回 ErrSopTemplateBuiltinReadonly。
// 已发布的 revision 是独立快照，不随模板软删而消失。
func (r *SopTemplateRepo) Delete(ctx context.Context, tenantID uint64, id string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		cur, err := requireSopTemplate(tx, tenantID, id)
		if err != nil {
			return err
		}
		if cur.IsBuiltin {
			return ErrSopTemplateBuiltinReadonly
		}
		res := tx.Where("tenant_id = ? AND id = ?", tenantID, id).Delete(&types.SopTemplate{})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return nil
	})
}

// Publish 把当前草稿冻结成一条不可变 revision，并回写模板的
// published_revision_id / status=published / version=新 revision 号。
//
// 事务内：读当前草稿 → revision = max(revision)+1 → 插入 revisions → 回写模板。
// 插入的是草稿的深拷贝，之后改草稿不会影响该 revision；
// (template_id, revision) 唯一索引兜住并发重复发布。
// 只允许发布本租户自己的行：内置模板可跨租户只读（Copy 出副本再改），
// 发布这条写路径的归属校验按 TenantID 严格判定，绝不把发布态回写到别人的行上。
// 本租户内置行（如 T7 seed 后再发布）允许发布——T23 的内置豁免发布校验依赖此路径。
func (r *SopTemplateRepo) Publish(
	ctx context.Context, tenantID uint64, id, publishedBy string,
) (*types.SopTemplateRevision, error) {
	var rev types.SopTemplateRevision
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		tpl, err := requireSopTemplate(tx, tenantID, id)
		if err != nil {
			return err
		}
		if tpl.TenantID != tenantID {
			return gorm.ErrRecordNotFound
		}
		def, err := cloneSopDefinition(tpl.Definition)
		if err != nil {
			return err
		}
		// 内容指纹：规范化 JSON（encoding/json 对 map 键排序）的 sha256。与当前
		// 已发布版一致就直接复用——重复点「发布」不该在版本历史里堆同样的快照。
		if tpl.PublishedRevisionID != "" {
			var cur types.SopTemplateRevision
			if err := tx.Where("tenant_id = ? AND id = ?", tpl.TenantID, tpl.PublishedRevisionID).
				First(&cur).Error; err == nil {
				// 两侧都过一遍 clone（补空切片），否则「nil vs []」会让内容相同的
				// 定义算出不同指纹。
				curDef, err := cloneSopDefinition(cur.Definition)
				if err != nil {
					return err
				}
				same, err := sameSopDefinition(curDef, def)
				if err != nil {
					return err
				}
				if same {
					rev = cur
					return nil
				}
			} else if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
		}
		var maxRev int
		if err := tx.Model(&types.SopTemplateRevision{}).
			Where("tenant_id = ? AND template_id = ?", tpl.TenantID, tpl.ID).
			Select("COALESCE(MAX(revision), 0)").
			Row().Scan(&maxRev); err != nil {
			return err
		}
		rev = types.SopTemplateRevision{
			TenantID:    tpl.TenantID,
			TemplateID:  tpl.ID,
			Revision:    maxRev + 1,
			Definition:  def,
			PublishedBy: publishedBy,
		}
		if err := tx.Create(&rev).Error; err != nil {
			return err
		}
		return tx.Model(&types.SopTemplate{}).
			Where("tenant_id = ? AND id = ?", tpl.TenantID, tpl.ID).
			UpdateColumns(map[string]any{
				"published_revision_id": rev.ID,
				"status":                types.SopTemplateStatusPublished,
				"version":               rev.Revision,
				"updated_at":            rev.CreatedAt,
			}).Error
	})
	if err != nil {
		return nil, err
	}
	return &rev, nil
}

// GetRevision 按 (租户, revision id) 取冻结快照；查不到返回 gorm.ErrRecordNotFound。
func (r *SopTemplateRepo) GetRevision(
	ctx context.Context, tenantID uint64, id string,
) (*types.SopTemplateRevision, error) {
	var rev types.SopTemplateRevision
	err := r.db.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, id).First(&rev).Error
	return &rev, err
}

// ListRevisions 列某模板的全部 revision（revision 降序，最新在前）。
func (r *SopTemplateRepo) ListRevisions(
	ctx context.Context, tenantID uint64, templateID string,
) ([]types.SopTemplateRevision, error) {
	var list []types.SopTemplateRevision
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND template_id = ?", tenantID, templateID).
		Order("revision DESC").
		Find(&list).Error
	return list, err
}

// Copy 深拷贝模板定义生成独立的自建草稿（内置模板的修改路径，spec §3.3）。
//
// 新行：新 uuid、is_builtin=false、status=draft、version=1、published_revision_id 空，
// 归属复制方租户并沿用源模板的 project_id / metric_category；
// name 为空时回退「<源模板名> 副本」。
func (r *SopTemplateRepo) Copy(
	ctx context.Context, tenantID uint64, srcID, name string,
) (*types.SopTemplate, error) {
	src, err := r.GetByID(ctx, tenantID, srcID)
	if err != nil {
		return nil, err
	}
	if name == "" {
		name = src.Name + " 副本"
	}
	def, err := cloneSopDefinition(src.Definition)
	if err != nil {
		return nil, err
	}
	cp := &types.SopTemplate{
		TenantID:       tenantID,
		ProjectID:      src.ProjectID,
		Name:           name,
		Description:    src.Description,
		Status:         types.SopTemplateStatusDraft,
		Version:        1,
		Definition:     def,
		IsBuiltin:      false,
		MetricCategory: src.MetricCategory,
	}
	if err := r.Create(ctx, cp); err != nil {
		return nil, err
	}
	return cp, nil
}

// requireSopTemplate 在事务内按 (租户, id) 取模板（软删视为不存在），谓词与 GetByID 一致：
// 内置模板跨租户可读，但写路径的归属由各方法自己再判定（Update/Delete 按 IsBuiltin 拒绝，
// Publish 按 TenantID 拒绝）。
func requireSopTemplate(tx *gorm.DB, tenantID uint64, id string) (*types.SopTemplate, error) {
	var tpl types.SopTemplate
	if err := tx.Where(sopTemplateReadPredicate, id, tenantID, true).First(&tpl).Error; err != nil {
		return nil, err
	}
	return &tpl, nil
}

// cloneSopDefinition 用 JSON 往返做 definition 的深拷贝（含嵌套切片/指针），
// 保证 revision 快照与副本都不与源结构共享底层数组。
func cloneSopDefinition(in types.SopDefinition) (types.SopDefinition, error) {
	raw, err := json.Marshal(in)
	if err != nil {
		return types.SopDefinition{}, err
	}
	var out types.SopDefinition
	if err := json.Unmarshal(raw, &out); err != nil {
		return types.SopDefinition{}, err
	}
	types.EnsureSopDefinitionSlices(&out)
	return out, nil
}

// sameSopDefinition 判断两份定义是否等价：按规范化 JSON（encoding/json 对 map
// 键排序、结构体字段顺序固定）比较 sha256，而不是比原始字节——键序不同但语义
// 相同的定义必须认成同一份内容。
func sameSopDefinition(a, b types.SopDefinition) (bool, error) {
	ha, err := sopDefinitionFingerprint(a)
	if err != nil {
		return false, err
	}
	hb, err := sopDefinitionFingerprint(b)
	if err != nil {
		return false, err
	}
	return ha == hb, nil
}

func sopDefinitionFingerprint(d types.SopDefinition) ([sha256.Size]byte, error) {
	raw, err := json.Marshal(d)
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	return sha256.Sum256(raw), nil
}
