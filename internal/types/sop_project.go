package types

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ---- 常量：项目知识库归属 ----
// project 随项目删除（自建库）；linked 为改绑的既有库，删除项目仅解绑；
// pm 为 PM 工作站迁移过来的库，同样仅解绑。
const (
	SopKBOwnerProject = "project"
	SopKBOwnerLinked  = "linked"
	SopKBOwnerPM      = "pm"
)

// SopProjectRepo 项目绑定的 Git 仓库（存 sop_projects.repos 的 JSON 数组）
type SopProjectRepo struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	URL     string `json:"url"`
	Branch  string `json:"branch"`
	Default bool   `json:"default"`
}

// SopProject 项目：事项编号前缀（code）+ 编号游标（NextItemNo）+ 项目知识库与仓库绑定。
type SopProject struct {
	ID                     string `gorm:"type:varchar(36);primaryKey" json:"id"`
	TenantID               uint64 `gorm:"index" json:"-"`
	OwnerUserID            string `gorm:"type:varchar(36)" json:"owner_user_id"`
	Name                   string `gorm:"type:varchar(200)" json:"name"`
	Description            string `gorm:"type:text" json:"description"`
	Code                   string `gorm:"type:varchar(10)" json:"code"`
	NextItemNo             int    `gorm:"default:0" json:"next_item_no"`
	KBID                   string `gorm:"type:varchar(36)" json:"kb_id"`
	KBOwner                string `gorm:"type:varchar(16);default:project" json:"kb_owner"`
	BriefKnowledgeID       string `gorm:"type:varchar(36)" json:"brief_knowledge_id"`
	BriefRenderedSeq       int64  `gorm:"default:0" json:"brief_rendered_seq"`
	DefaultTemplateID      string `gorm:"type:varchar(36)" json:"default_template_id"`
	DefaultExecutor        string `gorm:"type:varchar(64);default:platform_agent" json:"default_executor"`
	OnRunSuccessItemStatus string `gorm:"type:varchar(32)" json:"on_run_success_item_status"`
	// 与迁移列默认值一致：新建时未显式指定即开启通知（gorm 会把列默认值回填到结构体），
	// 更新时显式 false 照常落库（Save 全列写入，不受零值跳过影响）。
	NotifyOnGateOrFail bool             `gorm:"not null;default:true" json:"notify_on_gate_or_fail"`
	WorkspaceRoot      string           `gorm:"type:varchar(500)" json:"workspace_root"`
	Repos              []SopProjectRepo `gorm:"serializer:json" json:"repos"`
	Status             string           `gorm:"type:varchar(16);default:active" json:"status"`
	DomainData         map[string]any   `gorm:"serializer:json" json:"domain_data"`
	CreatedAt          time.Time        `json:"created_at"`
	UpdatedAt          time.Time        `json:"updated_at"`
	DeletedAt          gorm.DeletedAt   `gorm:"index" json:"-"`
}

func (SopProject) TableName() string { return "sop_projects" }

// BeforeCreate 补主键与时间戳；JSON 列补默认值，避免空值被驱动写成 NULL。
func (p *SopProject) BeforeCreate(_ *gorm.DB) error {
	if p.ID == "" {
		p.ID = uuid.NewString()
	}
	now := time.Now()
	if p.CreatedAt.IsZero() {
		p.CreatedAt = now
	}
	if p.UpdatedAt.IsZero() {
		p.UpdatedAt = now
	}
	if p.Repos == nil {
		p.Repos = []SopProjectRepo{}
	}
	if p.DomainData == nil {
		p.DomainData = map[string]any{}
	}
	return nil
}

// sopCodeRe 项目编号前缀：2–10 位字母数字且字母开头（与界面提示一致）。
var sopCodeRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]{1,9}$`)

// ValidateSopProjectCode 校验项目编号前缀。
func ValidateSopProjectCode(code string) error {
	if !sopCodeRe.MatchString(code) {
		return fmt.Errorf("编号前缀须为 2–10 位字母数字且字母开头")
	}
	return nil
}

// sopSSHRepoRe 匹配 git@host:path 形式的 SSH 地址。
var sopSSHRepoRe = regexp.MustCompile(`^[\w.-]+@[\w.-]+:[\w./-]+$`)

// ValidateSopRepoURL 校验仓库地址：只接受 HTTPS 或 SSH（不接受明文 http 与本地路径）。
func ValidateSopRepoURL(u string) error {
	if strings.HasPrefix(u, "https://") && len(u) > len("https://") {
		return nil
	}
	if strings.HasPrefix(u, "ssh://") || sopSSHRepoRe.MatchString(u) {
		return nil
	}
	return fmt.Errorf("仅支持 HTTPS 或 SSH Git 地址")
}

// NormalizeSopRepos 去重（按 URL）并补齐 id / 名称 / 分支，第一个仓库强制为默认。
func NormalizeSopRepos(in []SopProjectRepo) []SopProjectRepo {
	seen := map[string]bool{}
	out := make([]SopProjectRepo, 0, len(in))
	for _, r := range in {
		if seen[r.URL] {
			continue
		}
		seen[r.URL] = true
		if r.ID == "" {
			r.ID = uuid.NewString()
		}
		if r.Name == "" {
			base := r.URL[strings.LastIndexAny(r.URL, "/:")+1:]
			r.Name = strings.TrimSuffix(base, ".git")
		}
		if r.Branch == "" {
			r.Branch = "main"
		}
		r.Default = len(out) == 0 // 第一个自动默认，其余强制 false
		out = append(out, r)
	}
	return out
}
