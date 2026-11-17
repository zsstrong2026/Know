package types

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"gorm.io/gorm"
)

// 工作台模块常量（§8.1）。
const (
	WorkbenchKindCanvas   = "canvas"
	WorkbenchKindDatabase = "database"

	WorkbenchSourceDoc  = "doc"
	WorkbenchSourceItem = "item"

	WorkbenchArchivePending = "pending"
	WorkbenchArchiveActive  = "active"
	WorkbenchArchiveDeleted = "deleted"

	WorkbenchNoticeRemoved  = "membership_removed"
	WorkbenchNoticeRestored = "membership_restored"
	WorkbenchNoticeCleanup  = "cleanup_warning"
	WorkbenchNoticeArchive  = "archive_failed"

	// Page / Edgeless 双形态（§10.14）。空串表示“未显式设置”，
	// 读取时按 kind 兜底为 edgeless；数据库里存 NULL，不存空串。
	WorkbenchModePage     = "page"
	WorkbenchModeEdgeless = "edgeless"
)

// 条目可见性。§3.2 DDL 的 workbench_items.visibility 默认 'private'。
const (
	WorkbenchVisibilityPrivate   = "private"
	WorkbenchVisibilityWorkspace = "workspace"
)

// 反向链接目标类型。§3.2 DDL 注释：item | doc | knowledge。
const (
	WorkbenchTargetItem      = "item"
	WorkbenchTargetDoc       = "doc"
	WorkbenchTargetKnowledge = "knowledge"
)

// 孤儿数据原因。§3.2 DDL 注释：left | removed | account_deleted。
const (
	WorkbenchOrphanReasonLeft           = "left"
	WorkbenchOrphanReasonRemoved        = "removed"
	WorkbenchOrphanReasonAccountDeleted = "account_deleted"
)

// P0-4 用 vendored BlockSuite 实际快照测出后再改；初始 1，只增不减。
const SupportedEditorSchemaVersion = 1

// 条目初始快照版本，与 §3.2 DDL 的 snapshot_version DEFAULT 0 一致。
const WorkbenchSnapshotVersionInitial = 0

// 分页与体积上限（§7.1、§7.4、§11/§12 汇总表）。
const (
	WorkbenchItemsDefaultLimit    = 30
	WorkbenchItemsMaxLimit        = 100
	WorkbenchVersionsDefaultLimit = 50
	WorkbenchVersionsMaxLimit     = 100
	WorkbenchNoticesDefaultLimit  = 20
	WorkbenchNoticesMaxLimit      = 100
	WorkbenchSearchDefaultLimit   = 20
	WorkbenchSearchMaxLimit       = 50
	// Journals 单月超过 500 条时截断并置 truncated（§7.11）。
	WorkbenchJournalMonthMaxEntries = 500

	WorkbenchMaxYDocBytes     = 8 << 20   // Y.Doc 快照上限 8MB
	WorkbenchMaxTextBytes     = 256 << 10 // text_content 上限 256KB
	WorkbenchMaxArchiveBytes  = 6 << 20   // 归档 Markdown 上限 6MB
	WorkbenchMaxArchiveParts  = 20        // 单次归档 part 数上限
	WorkbenchMaxAssetBytes    = 20 << 20  // 单文件资产上限 20MB
	WorkbenchMaxImageBytes    = 10 << 20  // 单张图片上限 10MB
	WorkbenchMaxAssetsPerItem = 200       // 单条目资产数上限
	// 列表卡片缩略图：前端截取编辑器视口后压缩上传，只用于封面展示。
	WorkbenchMaxThumbnailBytes = 512 << 10 // 单张缩略图上限 512KB
)

// 条目标题上限（trim 后按 rune 计，§7.3）。
const WorkbenchItemTitleMaxRunes = 200

// DefaultModeForKind 是 current_mode 为 NULL 时的唯一兜底口径，
// 服务层、handler、前端都必须走它，不要各自写 if kind == "canvas"。
func DefaultModeForKind(kind string) string { return WorkbenchModeEdgeless }

// 工作台域错误。前两个的错误文本是规格固定值，不得改动。
var (
	ErrWorkbenchVersionConflict = errors.New("workbench: version conflict")
	ErrArchiveInProgress        = errors.New("workbench: archive in progress")

	ErrInvalidDateOnly                     = errors.New("workbench: invalid date")
	ErrWorkbenchInvalidCalendarField       = errors.New("workbench: invalid calendar field")
	ErrWorkbenchInvalidKind                = errors.New("workbench: invalid item kind")
	ErrWorkbenchInvalidTitle               = errors.New("workbench: invalid item title")
	ErrWorkbenchInvalidVisibility          = errors.New("workbench: invalid item visibility")
	ErrWorkbenchInvalidMode                = errors.New("workbench: invalid current mode")
	ErrWorkbenchInvalidSnapshotVersion     = errors.New("workbench: invalid snapshot version")
	ErrWorkbenchInvalidEditorSchemaVersion = errors.New("workbench: invalid editor schema version")
	ErrWorkbenchInvalidJournalDate         = errors.New("workbench: invalid journal date")
	ErrWorkbenchInvalidBaseVersion         = errors.New("workbench: invalid base version")
	ErrWorkbenchInvalidClientID            = errors.New("workbench: client_id is required")
	// ErrWorkbenchInvalidCursor 表示分页游标无法解码或格式不符。
	ErrWorkbenchInvalidCursor = errors.New("workbench: invalid cursor")

	// 模板（§9.16）：名称/描述/来源/配额/体积各有独立哨兵，便于 handler 映射到 400/2402。
	ErrWorkbenchInvalidTemplateName        = errors.New("workbench: invalid template name")
	ErrWorkbenchInvalidTemplateDescription = errors.New("workbench: invalid template description")
	ErrWorkbenchInvalidTemplateSource      = errors.New("workbench: invalid template source item")
	ErrWorkbenchTemplateQuotaExceeded      = errors.New("workbench: template quota exceeded")
	ErrWorkbenchTemplateEmpty              = errors.New("workbench: template snapshot is empty")
	ErrWorkbenchTemplateTooLarge           = errors.New("workbench: template snapshot too large")

	// ErrWorkbenchTooManyAssetIDs 表示一次上报的资产 id 数量超过单条目上限。
	ErrWorkbenchTooManyAssetIDs = errors.New("workbench: too many asset ids")
)

// DateOnly 表示“本地日历日”，只有 YYYY-MM-DD，没有时刻也没有时区。
// 它存在的唯一目的就是阻止时区换算：任何把它转成 time.Time 再格式化回来的代码都是 bug。
//
// 规格把该类型放在 internal/types/date_only.go；本任务只新增 workbench.go，
// 故内联于此，行为与规格逐条一致。
type DateOnly struct {
	Valid bool   // false 表示 SQL NULL（= 不是 Journal）
	Date  string // Valid 为 true 时必然是 ^\d{4}-\d{2}-\d{2}$
}

// NewDateOnly 不做校验，调用方先走 ParseDateOnly。
func NewDateOnly(s string) DateOnly { return DateOnly{Valid: true, Date: s} }

// ParseDateOnly 校验格式 + 真实日历日；失败返回 ErrInvalidDateOnly。
func ParseDateOnly(s string) (DateOnly, error) {
	if len(s) != 10 || s[4] != '-' || s[7] != '-' {
		return DateOnly{}, fmt.Errorf("%w: %q", ErrInvalidDateOnly, s)
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return DateOnly{}, fmt.Errorf("%w: %q", ErrInvalidDateOnly, s)
	}
	// 再回写一次，挡住 2026-2-1 这类宽松格式与任何归一化。
	if t.Format("2006-01-02") != s {
		return DateOnly{}, fmt.Errorf("%w: %q", ErrInvalidDateOnly, s)
	}
	return DateOnly{Valid: true, Date: s}, nil
}

// String 在 Valid 为 false 时返回 ""。
func (d DateOnly) String() string {
	if !d.Valid {
		return ""
	}
	return d.Date
}

// IsZero 等价于 !Valid。
func (d DateOnly) IsZero() bool { return !d.Valid }

// Scan 同时接受 PG 驱动的 time.Time、SQLite 驱动的 []byte/string 与 nil。
func (d *DateOnly) Scan(value interface{}) error {
	switch v := value.(type) {
	case nil:
		*d = DateOnly{}
		return nil
	case string:
		parsed, err := ParseDateOnly(v)
		if err != nil {
			return err
		}
		*d = parsed
		return nil
	case []byte:
		return d.Scan(string(v))
	case time.Time:
		// 驱动已经把 date 解析成 time.Time；用 UTC 分量格式化成日期字符串。
		// 这里**不是**在做时区换算——PG 的 date 列没有时区，驱动给的是 00:00:00 UTC。
		*d = DateOnly{Valid: true, Date: v.UTC().Format("2006-01-02")}
		return nil
	default:
		return fmt.Errorf("DateOnly: unsupported scan type %T", value)
	}
}

// Value 实现 driver.Valuer：Valid 为 false → nil，否则返回 "YYYY-MM-DD"。
func (d DateOnly) Value() (driver.Value, error) {
	if !d.Valid {
		return nil, nil
	}
	return d.Date, nil
}

// MarshalJSON 有值时输出字符串，NULL 时输出 null（前端只认这两种形状）。
func (d DateOnly) MarshalJSON() ([]byte, error) {
	if !d.Valid {
		return []byte("null"), nil
	}
	return json.Marshal(d.Date)
}

// UnmarshalJSON null → Valid=false；字符串 → ParseDateOnly。
func (d *DateOnly) UnmarshalJSON(b []byte) error {
	if d == nil {
		return errors.New("DateOnly: UnmarshalJSON on nil pointer")
	}
	s := strings.TrimSpace(string(b))
	if s == "null" || s == `""` {
		*d = DateOnly{}
		return nil
	}
	var raw string
	if err := json.Unmarshal(b, &raw); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidDateOnly, err)
	}
	parsed, err := ParseDateOnly(raw)
	if err != nil {
		return err
	}
	*d = parsed
	return nil
}

// ParseMonth 供月视图查询使用：ParseMonth("2026-10") → ("2026-10-01", "2026-10-31", nil)。
// 返回 YYYY-MM-DD 字符串（与全链路 string-only 一致），供 BETWEEN 直接比较；
// 月末用日历计算，不写死 31。
func ParseMonth(s string) (start, end string, err error) {
	if len(s) != 7 || s[4] != '-' {
		return "", "", fmt.Errorf("%w: month must be YYYY-MM, got %q", ErrWorkbenchInvalidCalendarField, s)
	}
	t, err := time.Parse("2006-01", s)
	if err != nil {
		return "", "", fmt.Errorf("%w: month must be YYYY-MM, got %q", ErrWorkbenchInvalidCalendarField, s)
	}
	// day=0 表示上个月最后一天，即本月月末。
	last := time.Date(t.Year(), t.Month()+1, 0, 0, 0, 0, 0, time.UTC)
	return t.Format("2006-01-02"), last.Format("2006-01-02"), nil
}

// WorkbenchItem 工作台条目（画布 / 数据库），按 tenant_id + owner_id 隔离。
type WorkbenchItem struct {
	ID                  string  `json:"id" gorm:"type:varchar(36);primaryKey"`
	TenantID            uint64  `json:"tenant_id"`
	OwnerID             string  `json:"owner_id" gorm:"type:varchar(36);index"`
	Kind                string  `json:"kind" gorm:"type:varchar(16);not null"`
	Title               string  `json:"title" gorm:"type:text;not null"`
	Icon                string  `json:"icon" gorm:"type:text"`
	FolderID            *string `json:"folder_id" gorm:"type:varchar(36)"`
	YDoc                []byte  `json:"-" gorm:"column:ydoc;type:bytea"`
	TextContent         string  `json:"-" gorm:"column:text_content;type:text"`
	SnapshotVersion     int     `json:"snapshot_version" gorm:"not null;default:0"`
	EditorSchemaVersion int     `json:"-" gorm:"not null;default:1"`
	Visibility          string  `json:"visibility" gorm:"type:varchar(16);not null;default:'private'"`
	IsFavorite          bool    `json:"is_favorite" gorm:"not null;default:false"`
	// 路线 1/2 新增（§3.2 的 000139）。两列都可空，存量行为 NULL，不需要回填。
	JournalDate  DateOnly       `json:"journal_date" gorm:"column:journal_date;type:date"`
	CurrentMode  *string        `json:"current_mode" gorm:"type:varchar(16)"` // page | edgeless | NULL
	LastOpenedAt *time.Time     `json:"last_opened_at"`
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
	DeletedAt    gorm.DeletedAt `json:"-" gorm:"index"`
	// 列表封面缩略图：图片字节只在单独的缩略图接口读取，
	// 列表只带版本号，避免每次翻页都传几十张图。
	Thumbnail            []byte `json:"-" gorm:"column:thumbnail;type:bytea"`
	ThumbnailContentType string `json:"-" gorm:"column:thumbnail_content_type;type:varchar(32)"`
	ThumbnailVersion     int    `json:"thumbnail_version" gorm:"column:thumbnail_version;not null;default:0"`
	// 只读展示字段：只在列表查询时批量填充，不落库；创建/更新/详情响应不返回（详情用 archives/state）。
	ArchiveStatus   string `json:"archive_status,omitempty" gorm:"-"`
	ArchivedVersion int    `json:"archived_version,omitempty" gorm:"-"`
}

// IsCanvas 判断是否为画布条目。
func (it *WorkbenchItem) IsCanvas() bool {
	return it != nil && it.Kind == WorkbenchKindCanvas
}

// IsDatabase 判断是否为数据库条目。
func (it *WorkbenchItem) IsDatabase() bool {
	return it != nil && it.Kind == WorkbenchKindDatabase
}

// EnsureDefaults 兜底可空默认值，保持幂等；不猜测 kind（kind 由 Validate 校验）。
func (it *WorkbenchItem) EnsureDefaults() {
	if it == nil {
		return
	}
	if it.Visibility == "" {
		it.Visibility = WorkbenchVisibilityPrivate
	}
	if it.EditorSchemaVersion <= 0 {
		it.EditorSchemaVersion = SupportedEditorSchemaVersion
	}
	// snapshot_version 只增不减，初始值为 0；负值视为脏数据归零。
	if it.SnapshotVersion < WorkbenchSnapshotVersionInitial {
		it.SnapshotVersion = WorkbenchSnapshotVersionInitial
	}
}

// Validate 校验条目自身可独立判定的不变量（§3.5），归属校验留给服务层。
func (it *WorkbenchItem) Validate() error {
	if it == nil {
		return ErrWorkbenchInvalidKind
	}
	if it.Kind != WorkbenchKindCanvas && it.Kind != WorkbenchKindDatabase {
		return fmt.Errorf("%w: %q", ErrWorkbenchInvalidKind, it.Kind)
	}
	n := utf8.RuneCountInString(strings.TrimSpace(it.Title))
	if n == 0 || n > WorkbenchItemTitleMaxRunes {
		return fmt.Errorf("%w: %d runes", ErrWorkbenchInvalidTitle, n)
	}
	if it.SnapshotVersion < WorkbenchSnapshotVersionInitial {
		return fmt.Errorf("%w: %d", ErrWorkbenchInvalidSnapshotVersion, it.SnapshotVersion)
	}
	if it.EditorSchemaVersion < 1 || it.EditorSchemaVersion > SupportedEditorSchemaVersion {
		return fmt.Errorf("%w: %d", ErrWorkbenchInvalidEditorSchemaVersion, it.EditorSchemaVersion)
	}
	switch it.Visibility {
	case WorkbenchVisibilityPrivate, WorkbenchVisibilityWorkspace:
	default:
		return fmt.Errorf("%w: %q", ErrWorkbenchInvalidVisibility, it.Visibility)
	}
	if it.CurrentMode != nil && *it.CurrentMode != WorkbenchModePage && *it.CurrentMode != WorkbenchModeEdgeless {
		return fmt.Errorf("%w: %q", ErrWorkbenchInvalidMode, *it.CurrentMode)
	}
	// Journals 只包含画布条目（§3.5 不变量 12），日期必须是真实日历日。
	if it.JournalDate.Valid {
		if it.Kind != WorkbenchKindCanvas {
			return fmt.Errorf("%w: kind %q", ErrWorkbenchInvalidJournalDate, it.Kind)
		}
		if _, err := ParseDateOnly(it.JournalDate.Date); err != nil {
			return err
		}
	}
	return nil
}

// ToSummary 投影为列表/详情用的元数据，绝不携带 YDoc 与 text_content。
func (it *WorkbenchItem) ToSummary() *WorkbenchItemSummary {
	if it == nil {
		return nil
	}
	s := &WorkbenchItemSummary{
		ID:               it.ID,
		Kind:             it.Kind,
		Title:            it.Title,
		Icon:             it.Icon,
		FolderID:         it.FolderID,
		SnapshotVersion:  it.SnapshotVersion,
		Visibility:       it.Visibility,
		IsFavorite:       it.IsFavorite,
		JournalDate:      it.JournalDate,
		LastOpenedAt:     it.LastOpenedAt,
		CreatedAt:        it.CreatedAt,
		UpdatedAt:        it.UpdatedAt,
		ThumbnailVersion: it.ThumbnailVersion,
		ArchiveStatus:    it.ArchiveStatus,
		ArchivedVersion:  it.ArchivedVersion,
	}
	if it.CurrentMode != nil {
		mode := *it.CurrentMode
		s.CurrentMode = &mode
	}
	return s
}

// WorkbenchItemSummary 是条目的元数据投影（规格未单独定义，供列表/详情复用）。
type WorkbenchItemSummary struct {
	ID              string     `json:"id"`
	Kind            string     `json:"kind"`
	Title           string     `json:"title"`
	Icon            string     `json:"icon"`
	FolderID        *string    `json:"folder_id"`
	SnapshotVersion int        `json:"snapshot_version"`
	Visibility      string     `json:"visibility"`
	IsFavorite      bool       `json:"is_favorite"`
	JournalDate     DateOnly   `json:"journal_date"`
	CurrentMode     *string    `json:"current_mode"`
	LastOpenedAt    *time.Time `json:"last_opened_at"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
	// 大于 0 表示服务端已有封面缩略图，前端据此决定是否请求图片。
	ThumbnailVersion int    `json:"thumbnail_version"`
	ArchiveStatus    string `json:"archive_status,omitempty"`
	ArchivedVersion  int    `json:"archived_version,omitempty"`
}

// WorkbenchItemVersion 条目历史快照；列表不返回 Y.Doc 正文。
type WorkbenchItemVersion struct {
	ID                  string `json:"-" gorm:"type:varchar(36);primaryKey"`
	ItemID              string `json:"-" gorm:"type:varchar(36);index"`
	TenantID            uint64 `json:"-"`
	OwnerID             string `json:"-" gorm:"type:varchar(36)"`
	Version             int    `json:"version"`
	EditorSchemaVersion int    `json:"editor_schema_version"`
	YDoc                []byte `json:"-" gorm:"column:ydoc;type:bytea"`
	TextContent         string `json:"-" gorm:"column:text_content;type:text"`
	// 迁移里没有 size_bytes 列：它只在 ListVersions 里由
	// COALESCE(length(ydoc), 0) 投影到 WorkbenchVersionSummary。
	// 因此这里必须 `-`（完全忽略）；若写成 `->`（只读），GORM 会把
	// size_bytes 放进 SELECT 列表，查询直接报列不存在。
	SizeBytes int `json:"size_bytes" gorm:"-"`
	// created_by 记录写入该版本的调用者。单写者模型下恒等于 OwnerID，
	// 保留它是为了日后排查“清理任务写的检查点”和“用户写的检查点”。
	CreatedBy string    `json:"-" gorm:"type:varchar(36)"`
	CreatedAt time.Time `json:"created_at"`
}

// SaveWorkbenchContentRequest 保存内容契约；YDoc 为 base64 字符串。
type SaveWorkbenchContentRequest struct {
	BaseVersion         int    `json:"base_version"`
	EditorSchemaVersion int    `json:"editor_schema_version"`
	YDoc                string `json:"ydoc"`
	TextContent         string `json:"text_content"`
	ClientID            string `json:"client_id"`
	Force               bool   `json:"force"`
	Checkpoint          bool   `json:"checkpoint"`
}

// Validate 校验保存请求的服务端约束（§8.3），体积与 base64 解码在服务层做。
func (r *SaveWorkbenchContentRequest) Validate() error {
	if r == nil {
		return ErrWorkbenchInvalidBaseVersion
	}
	if r.BaseVersion < 0 {
		return fmt.Errorf("%w: %d", ErrWorkbenchInvalidBaseVersion, r.BaseVersion)
	}
	if strings.TrimSpace(r.ClientID) == "" {
		return ErrWorkbenchInvalidClientID
	}
	if r.EditorSchemaVersion < 1 || r.EditorSchemaVersion > SupportedEditorSchemaVersion {
		return fmt.Errorf("%w: %d", ErrWorkbenchInvalidEditorSchemaVersion, r.EditorSchemaVersion)
	}
	return nil
}

// SaveWorkbenchContentResult 保存结果。
type SaveWorkbenchContentResult struct {
	SnapshotVersion     int       `json:"snapshot_version"`
	EditorSchemaVersion int       `json:"editor_schema_version"`
	Checkpointed        bool      `json:"checkpointed"`
	UpdatedAt           time.Time `json:"updated_at"`
}

// WorkbenchItemContent 内容读取响应（YDoc 为 base64 字符串）。
type WorkbenchItemContent struct {
	ID                  string    `json:"id"`
	SnapshotVersion     int       `json:"snapshot_version"`
	EditorSchemaVersion int       `json:"editor_schema_version"`
	YDoc                string    `json:"ydoc"`
	TextContent         string    `json:"text_content"`
	UpdatedAt           time.Time `json:"updated_at"`
}

// WorkbenchVersionSummary 版本列表项，不含正文。
type WorkbenchVersionSummary struct {
	Version             int       `json:"version"`
	EditorSchemaVersion int       `json:"editor_schema_version"`
	SizeBytes           int       `json:"size_bytes"`
	CreatedAt           time.Time `json:"created_at"`
}

// WorkbenchSearchHit 跨画布/文档/归档的搜索命中。
type WorkbenchSearchHit struct {
	Type      string    `json:"type"` // item | document | knowledge
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Snippet   string    `json:"snippet"`
	UpdatedAt time.Time `json:"updated_at"`
}

// WorkbenchSearchResult 搜索结果。
type WorkbenchSearchResult struct {
	Hits []*WorkbenchSearchHit `json:"hits"`
}

// WorkbenchFolder 工作台文件夹。
type WorkbenchFolder struct {
	ID        string  `json:"id" gorm:"type:varchar(36);primaryKey"`
	TenantID  uint64  `json:"-"`
	OwnerID   string  `json:"-"`
	Name      string  `json:"name"`
	ParentID  *string `json:"parent_id"`
	SortOrder int     `json:"sort_order"`
	// ItemCount 是文件夹直属的未删除条目数，由 ListFolders 聚合填充，不落库。
	ItemCount int       `json:"item_count" gorm:"-"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	// DDL 有 deleted_at；删除文件夹是软删，子文件夹与条目上移到父级（不级联删）。
	DeletedAt gorm.DeletedAt `json:"-" gorm:"index"`
}

// WorkbenchFolderMoveRequest 拖拽后的目标位置（P4-2）。
//
// parent_id 为空或 null 都表示根层级；position 是「目标父级下排除自己后」的插入下标，
// 越界由服务端夹到 [0, n]，前端不必自己算边界。
type WorkbenchFolderMoveRequest struct {
	ParentID *string `json:"parent_id"`
	Position int     `json:"position"`
}

// WorkbenchFolderRequest 新建/更新文件夹请求。
type WorkbenchFolderRequest struct {
	Name     string  `json:"name"`
	ParentID *string `json:"parent_id"`
}

// WorkbenchArchive 归档映射行；用 Status 表示删除，不带 gorm.DeletedAt。
type WorkbenchArchive struct {
	ID            string    `json:"id"`
	TenantID      uint64    `json:"-"`
	OwnerID       string    `json:"-"`
	KBID          string    `json:"kb_id"`
	KnowledgeID   string    `json:"knowledge_id"`
	SourceType    string    `json:"source_type"`
	SourceID      string    `json:"source_id"`
	SourceVersion int       `json:"source_version"`
	PartNo        int       `json:"part_no"`
	PartCount     int       `json:"part_count"`
	Title         string    `json:"title"`
	Status        string    `json:"status"`
	ArchivedAt    time.Time `json:"archived_at"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// WorkbenchArchivePart 单个归档分片的状态。
type WorkbenchArchivePart struct {
	PartNo          int    `json:"part_no"`
	PartCount       int    `json:"part_count"`
	Title           string `json:"title"`
	KnowledgeID     string `json:"knowledge_id"`
	ParseStatus     string `json:"parse_status"`
	SourceVersion   int    `json:"source_version"`
	KnowledgeExists bool   `json:"knowledge_exists"`
}

// WorkbenchArchiveState 归档状态聚合。
type WorkbenchArchiveState struct {
	SourceType      string                  `json:"source_type"`
	SourceID        string                  `json:"source_id"`
	CurrentVersion  *int                    `json:"current_version"`
	ArchivedVersion *int                    `json:"archived_version"`
	Status          string                  `json:"status"`
	StaleReason     string                  `json:"stale_reason,omitempty"`
	KBID            string                  `json:"kb_id"`
	Parts           []*WorkbenchArchivePart `json:"parts"`
	UpdatedAt       *time.Time              `json:"updated_at"`
}

// WorkbenchArchiveRequest 显式归档/刷新归档请求。
type WorkbenchArchiveRequest struct {
	SourceType  string `json:"source_type"`
	SourceID    string `json:"source_id"`
	BaseVersion int    `json:"base_version"`
	Markdown    string `json:"markdown"`
	Force       bool   `json:"force"`
}

// WorkbenchArchiveDeleteRequest 删除归档请求。
type WorkbenchArchiveDeleteRequest struct {
	SourceType      string `json:"source_type"`
	SourceID        string `json:"source_id"`
	DeleteKnowledge bool   `json:"delete_knowledge"`
}

// workbench_item_links 在 P1 的迁移里就建表（避免 P4 再改已发布迁移），
// 但 P1–P3 只读不写；写入与反向链接 UI 属 P4-2。
type WorkbenchItemLink struct {
	ID            string    `json:"id" gorm:"type:varchar(36);primaryKey"`
	TenantID      uint64    `json:"-"`
	OwnerID       string    `json:"-"`
	SourceItemID  string    `json:"source_item_id"`
	TargetType    string    `json:"target_type"` // item | doc | knowledge
	TargetID      string    `json:"target_id"`
	SourceBlockID string    `json:"source_block_id"`
	CreatedAt     time.Time `json:"created_at"`
}

// WorkbenchNotice 用户级通知（origin tenant 在 payload 中）。
type WorkbenchNotice struct {
	ID        string          `json:"id"`
	UserID    string          `json:"-"`
	TenantID  uint64          `json:"tenant_id"`
	Kind      string          `json:"kind"`
	DedupeKey string          `json:"-"`
	Payload   json.RawMessage `json:"payload" gorm:"type:jsonb"`
	ReadAt    *time.Time      `json:"read_at"`
	CreatedAt time.Time       `json:"created_at"`
}

// WorkbenchOrphan 成员离开后的 30 天保留标记。
type WorkbenchOrphan struct {
	ID          string    `json:"id"`
	TenantID    uint64    `json:"tenant_id"`
	UserID      string    `json:"user_id"`
	Reason      string    `json:"reason"`
	SuspendedAt time.Time `json:"suspended_at"`
	PurgeAfter  time.Time `json:"purge_after"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// WorkbenchKbCapabilities 工作 KB 的检索能力。
type WorkbenchKbCapabilities struct {
	Vector  bool `json:"vector"`
	Keyword bool `json:"keyword"`
}

// WorkbenchKbSummary 工作 KB 摘要（bootstrap 里的投影）。
type WorkbenchKbSummary struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	IsSystem         bool   `json:"is_system"`
	SystemKey        string `json:"system_key"`
	Visibility       string `json:"visibility"`
	EmbeddingModelID string `json:"embedding_model_id"`
	// KnowledgeCount 是工作库里的真实文档数（含归档与导入），
	// 与 counts.items（墨匣条目数）不是一回事，前端展示时不可混用。
	KnowledgeCount int64                   `json:"knowledge_count"`
	Capabilities   WorkbenchKbCapabilities `json:"capabilities"`
}

// WorkbenchBootstrapCounts 工作台聚合计数。
type WorkbenchBootstrapCounts struct {
	Items        int64 `json:"items"`
	Folders      int64 `json:"folders"`
	Documents    int64 `json:"documents"`
	ArchiveParts int64 `json:"archive_parts"`
}

// WorkbenchBootstrapLimits 工作台配额上限。
type WorkbenchBootstrapLimits struct {
	ItemContentBytes  int64 `json:"item_content_bytes"`
	ArchiveTotalBytes int64 `json:"archive_total_bytes"`
	ArchiveParts      int   `json:"archive_parts"`
}

// WorkbenchFeatureToggles 是下发给前端的发布门禁状态（§9.1）。
//
// 前端据此隐藏入口；关闭时服务端也不会注册对应路由，两者必须同时生效，
// 所以开关状态随 bootstrap 一起下发，避免前端硬编码。
type WorkbenchFeatureToggles struct {
	Templates bool `json:"templates"`
	Journals  bool `json:"journals"`
	AI        bool `json:"ai"`
	DualMode  bool `json:"dual_mode"`
}

// WorkbenchBootstrap 工作台启动聚合响应。
type WorkbenchBootstrap struct {
	WorkKB             WorkbenchKbSummary       `json:"work_kb"`
	NeedsSemanticIndex bool                     `json:"needs_semantic_index"`
	Counts             WorkbenchBootstrapCounts `json:"counts"`
	UnreadNotices      int                      `json:"unread_notices"`
	Limits             WorkbenchBootstrapLimits `json:"limits"`
	Features           WorkbenchFeatureToggles  `json:"features"`
	ServerTime         time.Time                `json:"server_time"`
}

// WorkbenchItemFilter 列表查询条件；owner 由服务端从 ctx 注入。
type WorkbenchItemFilter struct {
	TenantID uint64
	OwnerID  string
	Kind     string
	// ExcludeKind 非空时排除该类型。「最近」用它挡掉事项（kind=database）：
	// 事项是全局单例视图，不是普通条目，只在「事项」列表里出现。
	ExcludeKind string
	FolderID    *string
	Favorite    *bool
	Journal     *bool // 非 nil 且为 true 时只返回 journal_date IS NOT NULL 的条目
	// TagID 非空时只返回打了该标签的条目（P4-2）。
	TagID string
	// Sort 列表排序（B5）：空/updated = 最近更新，created = 创建时间，title = 名称。
	// 排序键会写进 cursor，切换排序必须重置分页。
	Sort    string
	Trashed bool
	Query   string
	Cursor  string
	Limit   int
}

// 列表排序取值（§9.5）。
const (
	WorkbenchSortUpdated = "updated"
	WorkbenchSortCreated = "created"
	WorkbenchSortTitle   = "title"
)

// NormalizeWorkbenchItemSort 把排序取值收敛到白名单；未知值（含空）退回「最近更新」。
// 仓储的 ORDER BY 与 cursor 编码必须用同一个归一化结果，否则翻页会串页。
func NormalizeWorkbenchItemSort(sort string) string {
	switch sort {
	case WorkbenchSortCreated, WorkbenchSortTitle:
		return sort
	default:
		return WorkbenchSortUpdated
	}
}

// WorkbenchItemPage cursor 分页响应。
type WorkbenchItemPage struct {
	Items      []*WorkbenchItem `json:"items"`
	NextCursor string           `json:"next_cursor"`
}

// CreateWorkbenchItemRequest 新建条目请求。
type CreateWorkbenchItemRequest struct {
	Kind     string  `json:"kind"`
	Title    string  `json:"title"`
	FolderID *string `json:"folder_id"`
	// 可选：从日历某天新建时由前端直接带上。缺省与 "" 等价，都表示不钉日历。
	JournalDate *string `json:"journal_date,omitempty"`
	// 可选：page | edgeless。缺省存 NULL，读取时按 kind 兜底（DefaultModeForKind）。
	CurrentMode *string `json:"current_mode,omitempty"`
}

// UpdateWorkbenchItemRequest 更新条目请求（指针字段表示“是否提供”）。
type UpdateWorkbenchItemRequest struct {
	Title      *string `json:"title,omitempty"`
	Icon       *string `json:"icon,omitempty"`
	FolderID   *string `json:"folder_id,omitempty"`
	MoveToRoot bool    `json:"move_to_root,omitempty"`
	IsFavorite *bool   `json:"is_favorite,omitempty"`
	// 三态：nil = 不改；指向 "" = 取消钉（写 NULL）；指向 "YYYY-MM-DD" = 钉/改期。
	// 与 MoveToRoot 那种附加布尔不同，这里必须用指针三态，因为拖拽改期和取消钉共用一个端点。
	JournalDate *string `json:"journal_date,omitempty"`
	// nil = 不改；"page" / "edgeless" = 设置。不提供“清回 NULL”。
	CurrentMode *string `json:"current_mode,omitempty"`
}

// Journals 月历（§7.11）。只含 kind='canvas' 的条目，与数据库的 Calendar 视图（§10.8）无关。
type WorkbenchJournalEntry struct {
	ID              string    `json:"id"`
	Title           string    `json:"title"`
	Icon            string    `json:"icon,omitempty"`
	JournalDate     DateOnly  `json:"journal_date"`
	CurrentMode     string    `json:"current_mode"` // 已按 DefaultModeForKind 兜底，不返回 null
	UpdatedAt       time.Time `json:"updated_at"`
	ArchiveStatus   string    `json:"archive_status"`   // §6.1 的计算态
	ArchivedVersion int       `json:"archived_version"` // 各 part source_version 最小值；无映射为 0
}

// WorkbenchJournalMonth 月视图响应。
type WorkbenchJournalMonth struct {
	Month     string                   `json:"month"`
	Entries   []*WorkbenchJournalEntry `json:"entries"`
	Truncated bool                     `json:"truncated,omitempty"`
}

// 某天的 Journal（§7.11.4）。0 条是合法结果，不是 404。
type WorkbenchJournalDay struct {
	Date    DateOnly                 `json:"date"`
	Entries []*WorkbenchJournalEntry `json:"entries"`
}

// WorkbenchExportLink 离开工作台前的导出入口。
type WorkbenchExportLink struct {
	Label string `json:"label"`
	Href  string `json:"href"`
}

// WorkbenchLeavePreview 离队前的数据统计。
type WorkbenchLeavePreview struct {
	KBCount        int64                  `json:"kb_count"`
	KnowledgeCount int64                  `json:"knowledge_count"`
	ItemCount      int64                  `json:"item_count"`
	DocCount       int64                  `json:"doc_count"`
	PurgeAfterDays int                    `json:"purge_after_days"`
	ExportLinks    []*WorkbenchExportLink `json:"export_links"`
}

// WorkbenchReindexResult 语义索引重建结果。
type WorkbenchReindexResult struct {
	Queued  int    `json:"queued"`
	ModelID string `json:"model_id"`
}

// WorkbenchCleanupStats 清理任务统计。
type WorkbenchCleanupStats struct {
	OrphansPurged   int `json:"orphans_purged"`
	VersionsDeleted int `json:"versions_deleted"`
	TrashPurged     int `json:"trash_purged"`
	NoticesCreated  int `json:"notices_created"`
	AssetsPurged    int `json:"assets_purged"`
}

// WorkbenchAsset 条目资产（§12.1）；storage_path 与 content_hash 不外泄。
type WorkbenchAsset struct {
	ID           string         `json:"id" gorm:"type:varchar(36);primaryKey"`
	TenantID     uint64         `json:"-" gorm:"not null"`
	OwnerID      string         `json:"-" gorm:"type:varchar(36);not null"`
	ItemID       string         `json:"item_id" gorm:"type:varchar(36);not null"`
	StoragePath  string         `json:"-" gorm:"type:text;not null"`
	OriginalName string         `json:"original_name" gorm:"type:text;not null"`
	MimeType     string         `json:"mime_type" gorm:"type:varchar(128);not null"`
	ContentHash  string         `json:"-" gorm:"type:varchar(64)"`
	SizeBytes    int64          `json:"size_bytes" gorm:"not null;default:0"`
	LastSeenAt   *time.Time     `json:"-"`
	CreatedAt    time.Time      `json:"created_at"`
	DeletedAt    gorm.DeletedAt `json:"-"`
}

// WorkbenchItemAsset 是 WorkbenchAsset 的别名，语义为“条目资产”。
type WorkbenchItemAsset = WorkbenchAsset

// WorkbenchAssetTouchRequest 是“刷新资产引用时间”的请求体（§12.2）。
//
// 服务端不解析 Y.Doc，因此“资产是否仍被引用”只能由客户端在打开文档后上报。
// 上限 200 与单条目资产数上限一致，避免一次请求拖垮同一条目。
type WorkbenchAssetTouchRequest struct {
	AssetIDs []string `json:"asset_ids"`
}

// Validate 校验请求体；空列表是合法输入（等价于无操作）。
func (r *WorkbenchAssetTouchRequest) Validate() error {
	if len(r.AssetIDs) > WorkbenchMaxAssetsPerItem {
		return ErrWorkbenchTooManyAssetIDs
	}
	return nil
}

// TableName 显式绑定 000138 建的表名。
//
// GORM 默认把 WorkbenchAsset 复数化成 workbench_assets，
// 而迁移里实际叫 workbench_item_assets —— 不改这里，
// P1-2 第一次查询就会出现 "no such table: workbench_assets"。
func (*WorkbenchAsset) TableName() string { return "workbench_item_assets" }

// WorkbenchVersionConflictError 携带当前版本号与更新时间，供 handler 组装 2401。
type WorkbenchVersionConflictError struct {
	CurrentVersion int
	UpdatedAt      time.Time
}

func (e *WorkbenchVersionConflictError) Error() string {
	return ErrWorkbenchVersionConflict.Error()
}

func (e *WorkbenchVersionConflictError) Unwrap() error {
	return ErrWorkbenchVersionConflict
}

// NewVersionConflictError 只用于 service/repository 内部传递。
func NewVersionConflictError(currentVersion int, updatedAt time.Time) error {
	return &WorkbenchVersionConflictError{CurrentVersion: currentVersion, UpdatedAt: updatedAt}
}
