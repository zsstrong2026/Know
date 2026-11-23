package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

// 编译期断言：凭据仓储实现必须满足接口契约（T19 凭据服务、T18 管理 API 的消费面）。
var _ interfaces.SopCredentialRepository = (*SopCredentialRepo)(nil)

// SopCredentialRepo 是 CLI 凭据的 GORM 实现。
//
// 不变式：
//   - 每条查询都带 tenant_id，跨租户同一 owner/kind 也互不可见；
//   - 唯一性口径是 (owner_user_id, cli_kind)（迁移 idx ux_sop_cli_cred_owner_kind），
//     同一用户同一 CLI 只有一份密钥，Upsert 更新原行而不是堆叠；
//   - api_key 的加解密由模型 BeforeSave/AfterFind 承担，仓储只做持久化，
//     绝不在此打印或返回明文以外的处理（json:"-" 保证不出网）。
type SopCredentialRepo struct{ db *gorm.DB }

// NewSopCredentialRepo 构造 CLI 凭据仓储。
func NewSopCredentialRepo(db *gorm.DB) *SopCredentialRepo { return &SopCredentialRepo{db: db} }

// sopCredentialUpdateColumns 是 Upsert 命中已有行时允许改写的列白名单。
//
// 归属键（id/tenant_id/owner_user_id/cli_kind）不许改：换 CLI 种类应删旧建新，
// 否则唯一索引会以难以理解的方式撞车。
var sopCredentialUpdateColumns = []string{
	"api_key", "base_url", "default_model", "extra", "updated_at",
}

// Upsert 按 (租户, owner, cli_kind) 落凭据：存在则更新密钥与地址，否则插入。
//
// 返回的 cfg 带真实 ID（调用方据此做响应 DTO）；APIKey 明文只留在入参与返回里，
// 落库由 BeforeSave 加密。
func (r *SopCredentialRepo) Upsert(ctx context.Context, c *types.SopCLICredential) error {
	if c == nil {
		return errors.New("sop credential: nil credential")
	}
	if !types.IsSopCLIKind(c.CLIKind) {
		return fmt.Errorf("%w: unknown cli_kind %q", types.ErrInvalidSopCredential, c.CLIKind)
	}
	if c.Extra == nil {
		c.Extra = map[string]any{}
	}
	var existing types.SopCLICredential
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND owner_user_id = ? AND cli_kind = ?", c.TenantID, c.OwnerUserID, c.CLIKind).
		First(&existing).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return r.db.WithContext(ctx).Create(c).Error
	}
	if err != nil {
		return err
	}

	c.ID = existing.ID
	c.CreatedAt = existing.CreatedAt
	// 空 APIKey 表示「只改地址/模型，密钥沿用旧值」：直接跳过写 api_key 列，
	// 避免把已存密钥抹成空串。
	columns := sopCredentialUpdateColumns
	if c.APIKey == "" {
		columns = []string{"base_url", "default_model", "extra", "updated_at"}
	}
	// Model(c) 而不是空模型：GORM 的 BeforeSave 钩子只对 Model/ReflectValue 指向的
	// 结构体调用，Model 传零值时 SetColumn 改的是那个空结构体，Dest 里的明文会原样
	// 落库（更新路径绕过加密）。传 c 后钩子改写同一块内存，AfterSave 再换回明文。
	return r.db.WithContext(ctx).Model(c).
		Where("id = ? AND tenant_id = ?", existing.ID, c.TenantID).
		Select(columns).
		Updates(c).Error
}

// Get 按 (租户, owner, cli_kind) 取凭据；查不到返回 gorm.ErrRecordNotFound。
// 返回结构的 APIKey 已由 AfterFind 解密为明文（供 executor 注入 env）。
func (r *SopCredentialRepo) Get(
	ctx context.Context, tenantID uint64, ownerUserID, cliKind string,
) (*types.SopCLICredential, error) {
	var c types.SopCLICredential
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND owner_user_id = ? AND cli_kind = ?", tenantID, ownerUserID, cliKind).
		First(&c).Error
	return &c, err
}

// Delete 软删某条凭据（(租户, id) 命中）；查不到返回 gorm.ErrRecordNotFound。
//
// 凭据是用户私有数据：删了不复活，软删只保留审计痕迹。
func (r *SopCredentialRepo) Delete(ctx context.Context, tenantID uint64, id string) error {
	res := r.db.WithContext(ctx).
		Where("tenant_id = ? AND id = ?", tenantID, id).Delete(&types.SopCLICredential{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// ListByOwner 列某用户在本租户的全部凭据（cli_kind 升序，前端下拉稳定顺序）。
func (r *SopCredentialRepo) ListByOwner(
	ctx context.Context, tenantID uint64, ownerUserID string,
) ([]types.SopCLICredential, error) {
	var list []types.SopCLICredential
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND owner_user_id = ?", tenantID, ownerUserID).
		Order("cli_kind ASC").
		Find(&list).Error
	return list, err
}
