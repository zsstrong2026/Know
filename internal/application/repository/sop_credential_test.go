package repository

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/Tencent/WeKnora/internal/types"
)

// newSopCredentialTestDB 建独立 SQLite 内存库并 AutoMigrate 凭据表，
// 按迁移原文补 (owner_user_id, cli_kind) 部分唯一索引（GORM tag 表达不了 WHERE 条件），
// 并注入 32 字节 AES 密钥让加解密走真实路径。
func newSopCredentialTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	t.Setenv("SYSTEM_AES_KEY", "0123456789abcdef0123456789abcdef")
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, db.AutoMigrate(&types.SopCLICredential{}))
	require.NoError(t, db.Exec(
		"CREATE UNIQUE INDEX IF NOT EXISTS ux_sop_cli_cred_owner_kind "+
			"ON sop_cli_credentials(owner_user_id, cli_kind) WHERE deleted_at IS NULL",
	).Error)
	return db
}

// TestCredentialUpsertReusesRow 同一 (租户, owner, cli_kind) 二次 Upsert 更新原行：
// 不新增、密钥换新、空密钥时沿用旧值（只改地址/模型不抹掉已存密钥）。
func TestCredentialUpsertReusesRow(t *testing.T) {
	db := newSopCredentialTestDB(t)
	repo := NewSopCredentialRepo(db)
	ctx := context.Background()

	first := &types.SopCLICredential{
		TenantID: 1, OwnerUserID: "u1", CLIKind: types.SopCLIKindZCode,
		APIKey: "sk-first", BaseURL: "https://a.example", DefaultModel: "m1",
	}
	require.NoError(t, repo.Upsert(ctx, first))
	require.NotEmpty(t, first.ID)

	second := &types.SopCLICredential{
		TenantID: 1, OwnerUserID: "u1", CLIKind: types.SopCLIKindZCode,
		APIKey: "sk-second", BaseURL: "https://b.example", DefaultModel: "m2",
	}
	require.NoError(t, repo.Upsert(ctx, second))
	require.Equal(t, first.ID, second.ID, "同 (owner, kind) 必须复用原行 id")

	var count int64
	require.NoError(t, db.Model(&types.SopCLICredential{}).Count(&count).Error)
	require.EqualValues(t, 1, count)

	got, err := repo.Get(ctx, 1, "u1", types.SopCLIKindZCode)
	require.NoError(t, err)
	require.Equal(t, "sk-second", got.APIKey)
	require.Equal(t, "https://b.example", got.BaseURL)

	// 更新路径同样必须加密落库（不能只保证插入路径）。
	var raw string
	require.NoError(t, db.Raw("SELECT api_key FROM sop_cli_credentials WHERE id = ?", first.ID).Scan(&raw).Error)
	require.NotContains(t, raw, "sk-second", "更新路径落库也必须是密文")

	// 空密钥 = 沿用旧密钥。
	third := &types.SopCLICredential{
		TenantID: 1, OwnerUserID: "u1", CLIKind: types.SopCLIKindZCode,
		BaseURL: "https://c.example",
	}
	require.NoError(t, repo.Upsert(ctx, third))
	got, err = repo.Get(ctx, 1, "u1", types.SopCLIKindZCode)
	require.NoError(t, err)
	require.Equal(t, "sk-second", got.APIKey, "空密钥不得抹掉已存密钥")
	require.Equal(t, "https://c.example", got.BaseURL)
}

// TestCredentialKeyNeverStoredInPlaintext 仓储写入后列里必须是密文，
// 且读回的明文只来自 AfterFind 解密。
func TestCredentialKeyNeverStoredInPlaintext(t *testing.T) {
	db := newSopCredentialTestDB(t)
	repo := NewSopCredentialRepo(db)
	ctx := context.Background()
	require.NoError(t, repo.Upsert(ctx, &types.SopCLICredential{
		TenantID: 1, OwnerUserID: "u1", CLIKind: types.SopCLIKindKimi, APIKey: "sk-plain-kimi",
	}))

	var raw string
	require.NoError(t, db.Raw("SELECT api_key FROM sop_cli_credentials").Scan(&raw).Error)
	require.NotContains(t, raw, "sk-plain-kimi")

	got, err := repo.Get(ctx, 1, "u1", types.SopCLIKindKimi)
	require.NoError(t, err)
	require.Equal(t, "sk-plain-kimi", got.APIKey)
}

// TestCredentialListByOwnerScoped ListByOwner 只返回本租户本用户的凭据。
//
// 唯一索引按迁移原文是 (owner_user_id, cli_kind)（不含 tenant_id：用户 id 全局唯一），
// 故跨租户复用同一 owner id 不构造（那会撞唯一键），这里用不同 owner 验证读取侧收口。
func TestCredentialListByOwnerScoped(t *testing.T) {
	db := newSopCredentialTestDB(t)
	repo := NewSopCredentialRepo(db)
	ctx := context.Background()
	for _, c := range []*types.SopCLICredential{
		{TenantID: 1, OwnerUserID: "u1", CLIKind: types.SopCLIKindZCode, APIKey: "k1"},
		{TenantID: 1, OwnerUserID: "u1", CLIKind: types.SopCLIKindClaude, APIKey: "k2"},
		{TenantID: 1, OwnerUserID: "u2", CLIKind: types.SopCLIKindZCode, APIKey: "k3"},
		{TenantID: 2, OwnerUserID: "u3", CLIKind: types.SopCLIKindZCode, APIKey: "k4"},
	} {
		require.NoError(t, repo.Upsert(ctx, c))
	}

	list, err := repo.ListByOwner(ctx, 1, "u1")
	require.NoError(t, err)
	require.Len(t, list, 2)
	require.Equal(t, types.SopCLIKindClaude, list[0].CLIKind, "按 cli_kind 升序")
	require.Equal(t, types.SopCLIKindZCode, list[1].CLIKind)

	other, err := repo.ListByOwner(ctx, 2, "u1")
	require.NoError(t, err)
	require.Empty(t, other, "跨租户不可见")

	_, err = repo.Get(ctx, 1, "u1", types.SopCLIKindOpenCode)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	_, err = repo.Get(ctx, 2, "u1", types.SopCLIKindZCode)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound, "跨租户不可见")
}

// TestCredentialUpsertRejectsUnknownKind 未知 CLI 种类在仓储层即拒绝（唯一写入口）。
func TestCredentialUpsertRejectsUnknownKind(t *testing.T) {
	db := newSopCredentialTestDB(t)
	repo := NewSopCredentialRepo(db)
	err := repo.Upsert(context.Background(), &types.SopCLICredential{
		TenantID: 1, OwnerUserID: "u1", CLIKind: "not-a-cli", APIKey: "k",
	})
	require.ErrorIs(t, err, types.ErrInvalidSopCredential)

	var count int64
	require.NoError(t, db.Model(&types.SopCLICredential{}).Count(&count).Error)
	require.Zero(t, count)
}
