package types

import (
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// sopCredTestAESKey 是 32 字节密钥（GetAESKey 只接受该长度）：加密测试必须
// 真的加解密，不能在无密钥的降级路径上空过。
const sopCredTestAESKey = "0123456789abcdef0123456789abcdef"

// newSopCredTestDB 建独立 SQLite 内存库并 AutoMigrate 凭据表。
//
// SYSTEM_AES_KEY 用 t.Setenv 注入（测试结束自动还原），因此 BeforeSave/AfterFind
// 走真实 AES-GCM 路径。
func newSopCredTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	t.Setenv("SYSTEM_AES_KEY", sopCredTestAESKey)
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, db.AutoMigrate(&SopCLICredential{}))
	return db
}

// TestCredentialEncryptRoundtrip 落库必须是密文，读回必须解密还原明文
// （凭据在 executor 侧要以明文注入 env）。
func TestCredentialEncryptRoundtrip(t *testing.T) {
	db := newSopCredTestDB(t)
	c := &SopCLICredential{TenantID: 1, OwnerUserID: "u1", CLIKind: SopCLIKindZCode, APIKey: "sk-secret-123"}
	require.NoError(t, db.Create(c).Error)
	require.NotEmpty(t, c.ID, "BeforeCreate 必须补 uuid")
	require.NotContains(t, c.APIKey, "enc:v1:", "加密只写库，不得污染内存中的结构体（AfterSave 还原明文）")

	var raw string
	require.NoError(t, db.Raw("SELECT api_key FROM sop_cli_credentials WHERE id = ?", c.ID).Scan(&raw).Error)
	require.NotContains(t, raw, "sk-secret-123", "落库必须是密文")

	var got SopCLICredential
	require.NoError(t, db.First(&got, "id = ?", c.ID).Error)
	require.Equal(t, "sk-secret-123", got.APIKey, "AfterFind 必须解密还原")
}

// TestCredentialLegacyPlaintextLoads 兼容升级前的明文行：无 enc:v1: 前缀时按原值返回。
func TestCredentialLegacyPlaintextLoads(t *testing.T) {
	db := newSopCredTestDB(t)
	require.NoError(t, db.Exec(
		"INSERT INTO sop_cli_credentials (id, tenant_id, owner_user_id, cli_kind, api_key) VALUES (?, 1, 'u1', ?, ?)",
		"legacy-1", SopCLIKindClaude, "plain-legacy-key",
	).Error)

	var got SopCLICredential
	require.NoError(t, db.First(&got, "id = ?", "legacy-1").Error)
	require.Equal(t, "plain-legacy-key", got.APIKey)
}

// TestSopCLIKindsList 十种 CLI 常量与清单一一对应，顺序即前端下拉顺序。
func TestSopCLIKindsList(t *testing.T) {
	require.Equal(t, []string{
		SopCLIKindOpenCode, SopCLIKindCursor, SopCLIKindAGY, SopCLIKindKimi, SopCLIKindPi,
		SopCLIKindDSH, SopCLIKindZCode, SopCLIKindClaude, SopCLIKindCodeBuddy, SopCLIKindCodex,
	}, SopCLIKinds)
	require.Len(t, SopCLIKinds, 10)
	require.Equal(t, "opencode", SopCLIKindOpenCode)
	require.Equal(t, "cursor-agent", SopCLIKindCursor)
	require.Equal(t, "codebuddy", SopCLIKindCodeBuddy)
	for _, k := range SopCLIKinds {
		require.Truef(t, IsSopCLIKind(k), "清单里的 %s 必须被 IsSopCLIKind 识别", k)
	}
	require.False(t, IsSopCLIKind("unknown-cli"))
}
