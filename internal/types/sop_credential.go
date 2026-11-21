package types

import (
	"errors"
	"fmt"
	"time"

	"github.com/Tencent/WeKnora/internal/utils"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ---- 常量：CLI 种类（10 种；值即适配器的 cli_kind，落库后不再改写）----
const (
	SopCLIKindOpenCode  = "opencode"
	SopCLIKindCursor    = "cursor-agent"
	SopCLIKindAGY       = "agy"
	SopCLIKindKimi      = "kimi"
	SopCLIKindPi        = "pi"
	SopCLIKindDSH       = "dsh"
	SopCLIKindZCode     = "zcode"
	SopCLIKindClaude    = "claude"
	SopCLIKindCodeBuddy = "codebuddy"
	SopCLIKindCodex     = "codex"
)

// SopCLIKinds 是全部 CLI 种类的固定清单（顺序即前端下拉顺序）。
var SopCLIKinds = []string{
	SopCLIKindOpenCode, SopCLIKindCursor, SopCLIKindAGY, SopCLIKindKimi, SopCLIKindPi,
	SopCLIKindDSH, SopCLIKindZCode, SopCLIKindClaude, SopCLIKindCodeBuddy, SopCLIKindCodex,
}

// IsSopCLIKind 判断 kind 是否为本平台支持的 CLI 种类。
func IsSopCLIKind(kind string) bool {
	for _, k := range SopCLIKinds {
		if k == kind {
			return true
		}
	}
	return false
}

// ErrInvalidSopCredential 凭据入参非法（未知 cli_kind 等）；调用方用 errors.Is 映射 400。
var ErrInvalidSopCredential = errors.New("sop credential: invalid")

// SopCLICredential 是「用户 × CLI」的凭据：同一租户内一人一 CLI 只能有一份。
//
// APIKey 加密落库：BeforeSave 以 AES-256-GCM（utils.EncryptAESGCM）加密后写列，
// AfterFind 用宽松解密（DecryptStoredSecretLenient）还原明文——密钥轮换/缺失时
// 只是该行降级为空（UI 显示「未配置」并要求重填），不拖垮整表读取；兼容历史明文行。
// json:"-" 保证凭据永远不会随 API 响应出网（DTO 另行输出尾 4 位）。
type SopCLICredential struct {
	ID          string `gorm:"type:varchar(36);primaryKey" json:"id"`
	TenantID    uint64 `gorm:"index" json:"-"`
	OwnerUserID string `gorm:"type:varchar(36)" json:"owner_user_id"`
	CLIKind     string `gorm:"type:varchar(32)" json:"cli_kind"`
	// APIKey 明文只存在于内存；落库密文见 BeforeSave。
	APIKey       string         `gorm:"type:text" json:"-"`
	BaseURL      string         `gorm:"type:varchar(500)" json:"base_url"`
	DefaultModel string         `gorm:"type:varchar(200)" json:"default_model"`
	Extra        map[string]any `gorm:"serializer:json" json:"extra"`
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
	DeletedAt    gorm.DeletedAt `gorm:"index" json:"-"`
}

func (SopCLICredential) TableName() string { return "sop_cli_credentials" }

// BeforeCreate 补主键与时间戳；Extra 为 nil 时补空 map（serializer 列写 NULL 与
// 列默认 '{}' 不一致，读回也拿不到可用集合）。
func (c *SopCLICredential) BeforeCreate(_ *gorm.DB) error {
	if c.ID == "" {
		c.ID = uuid.NewString()
	}
	now := time.Now()
	if c.CreatedAt.IsZero() {
		c.CreatedAt = now
	}
	if c.UpdatedAt.IsZero() {
		c.UpdatedAt = now
	}
	if c.Extra == nil {
		c.Extra = map[string]any{}
	}
	return nil
}

// BeforeSave 把明文 APIKey 加密后写列（创建与更新共用；Utils 对已带 enc:v1: 前缀的
// 值原样返回，因此重复保存不会二次加密）。
//
// 加密失败必须中断写入：静默落明文等于把凭据泄漏进可查询列（照 TenantAPIKey.BeforeSave）。
func (c *SopCLICredential) BeforeSave(tx *gorm.DB) error {
	if c.APIKey == "" {
		return nil
	}
	key := utils.GetAESKey()
	if key == nil {
		// 未配置密钥的部署保持既有降级口径：按原值落库（与 model.go 的 Value() 一致）。
		return nil
	}
	encrypted, err := utils.EncryptAESGCM(c.APIKey, key)
	if err != nil {
		return fmt.Errorf("encrypt sop_cli_credentials.api_key (kind=%s): %w", c.CLIKind, err)
	}
	tx.Statement.SetColumn("api_key", encrypted)
	return nil
}

// AfterFind 宽松解密：失败时把该行当作「未配置」而不是让整次查询失败。
func (c *SopCLICredential) AfterFind(_ *gorm.DB) error {
	if plain, ok := utils.DecryptStoredSecretLenient(c.APIKey); ok {
		c.APIKey = plain
	} else {
		c.APIKey = ""
	}
	return nil
}

// AfterSave 把 BeforeSave 写进列后残留在结构体上的密文换回明文。
//
// BeforeSave 是通过 SetColumn 改列值的，同一块内存里的 APIKey 会留下 enc:v1: 密文；
// 若不还原，调用方拿着刚 Save 过的结构体去注入 env 就会把密文当密钥下发。
// 与 model.go 的讲解口径一致：内存里永远只有明文，密文只存在于数据库列。
func (c *SopCLICredential) AfterSave(_ *gorm.DB) error {
	return c.AfterFind(nil)
}
