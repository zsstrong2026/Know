package types

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ---- 常量 ----

const (
	MeetingStatusActive               = "active"
	MeetingStatusAwaitingConfirmation = "awaiting_confirmation" // 创建后待确认开场流程；直接打字或 confirm 后转 active
	MeetingStatusArchived             = "archived"

	MeetingBroadcastSequential = "sequential"
	MeetingBroadcastParallel   = "parallel"

	// 发言权路由模式：host-assign 主持人指派 / mention 提及驱动 / free 自由抢麦
	MeetingRoutingHostAssign = "host-assign"
	MeetingRoutingMention    = "mention"
	MeetingRoutingFree       = "free"

	// 会议模式：hosted 主持模式（默认，主持人开场分析并控场）/ free 自由模式（无主持人，智能体自组织讨论）
	MeetingModeHosted = "hosted"
	MeetingModeFree   = "free"

	// 主持人介入间隔上限（子轮数）：超过该值视为非法/关闭，防止过于稀疏或异常配置
	MeetingHostEveryNRoundsMax = 10

	// 议程项状态：pending 待讨论 / active 讨论中 / done 已覆盖 / skipped 跳过
	MeetingAgendaPending = "pending"
	MeetingAgendaActive  = "active"
	MeetingAgendaDone    = "done"
	MeetingAgendaSkipped = "skipped"

	// 发言准入模式：off 关闭 / observe 只记录 / enforce 不通过时要求重写一次（不丢弃发言）
	MeetingAdmissionOff     = "off"
	MeetingAdmissionObserve = "observe"
	MeetingAdmissionEnforce = "enforce"

	// 发言准入结果（落库便于观测与调参）
	MeetingAdmissionNone      = ""
	MeetingAdmissionPassed    = "passed"
	MeetingAdmissionObserved  = "observed"
	MeetingAdmissionRewritten = "rewritten"

	ParticipantKindHost  = "host"
	ParticipantKindAgent = "agent"

	MeetingSenderUser   = "user"
	MeetingSenderAgent  = "agent"
	MeetingSenderHost   = "host"
	MeetingSenderSystem = "system"

	ReviewStateNone     = "none"
	ReviewStatePending  = "pending"
	ReviewStateAdopted  = "adopted"
	ReviewStateRejected = "rejected"
)

// ---- 产物（generated）常量（M5）----

const (
	// MeetingArtifactKindSolution 历史方案汇总产物（solution_message_id 指向会议消息）
	MeetingArtifactKindSolution = "solution"
	// MeetingArtifactKindGenerated 用户指定形态生成的产物（字节存后台会话的 MessageArtifact）
	MeetingArtifactKindGenerated = "generated"

	MeetingArtifactStatusGenerating = "generating"
	MeetingArtifactStatusReady      = "ready"
	MeetingArtifactStatusFailed     = "failed"

	// MeetingArtifactSessionMarker 会议产物后台会话的隐藏标记前缀（聊天侧栏过滤，仿 skill_maintenance/agent_eval）
	MeetingArtifactSessionMarker = "meeting_artifact:"
)

// 9 种产物形态（本期硬编码集合，spec §1 G4；方案为首个形态，取代原独立的「生成方案」入口）
const (
	MeetingArtifactTypeSolution       = "solution"
	MeetingArtifactTypeReportMarkdown = "report_markdown"
	MeetingArtifactTypeMinutes        = "minutes"
	MeetingArtifactTypeTodoList       = "todo_list"
	MeetingArtifactTypeDocx           = "docx"
	MeetingArtifactTypeXlsx           = "xlsx"
	MeetingArtifactTypePptx           = "pptx"
	MeetingArtifactTypeHtmlReport     = "html_report"
	MeetingArtifactTypeChartImage     = "chart_image"
)

var validMeetingArtifactTypes = map[string]bool{
	MeetingArtifactTypeSolution:       true,
	MeetingArtifactTypeReportMarkdown: true,
	MeetingArtifactTypeMinutes:        true,
	MeetingArtifactTypeTodoList:       true,
	MeetingArtifactTypeDocx:           true,
	MeetingArtifactTypeXlsx:           true,
	MeetingArtifactTypePptx:           true,
	MeetingArtifactTypeHtmlReport:     true,
	MeetingArtifactTypeChartImage:     true,
}

// ValidMeetingArtifactType 形态白名单校验（handler/service 共用）
func ValidMeetingArtifactType(t string) bool { return validMeetingArtifactTypes[t] }

// IsMeetingArtifactSessionDescription 判断会话描述是否为会议产物后台会话标记（侧栏隐藏判定用）
func IsMeetingArtifactSessionDescription(desc string) bool {
	return strings.HasPrefix(desc, MeetingArtifactSessionMarker)
}

// NormalizeMeetingArtifact 读取路径兼容归一：历史行 kind=""→solution、status=""→ready
func NormalizeMeetingArtifact(a *MeetingArtifact) *MeetingArtifact {
	if a.Kind == "" {
		a.Kind = MeetingArtifactKindSolution
	}
	if a.Status == "" {
		a.Status = MeetingArtifactStatusReady
	}
	return a
}

// StringList 是 jsonb 字符串数组列（[]string <-> jsonb），仿 CustomAgentConfig 的 Valuer/Scanner 模式
type StringList []string

func (s StringList) Value() (driver.Value, error) {
	if s == nil {
		return "[]", nil
	}
	b, err := json.Marshal(s)
	return string(b), err
}

func (s *StringList) Scan(src interface{}) error {
	switch v := src.(type) {
	case []byte:
		return json.Unmarshal(v, s)
	case string:
		return json.Unmarshal([]byte(v), s)
	case nil:
		*s = []string{}
		return nil
	}
	return errors.New("unsupported type for StringList")
}

// MeetingRoom 会议室
type MeetingRoom struct {
	ID          string `gorm:"type:varchar(36);primaryKey" json:"id"`
	TenantID    uint64 `gorm:"index" json:"tenant_id"`
	CreatedBy   string `gorm:"type:varchar(36);index" json:"created_by"`
	Title       string `gorm:"type:varchar(255)" json:"title"`
	Description string `gorm:"type:text" json:"description"`
	// Mode 会议模式：hosted 主持模式（默认）/ free 自由模式（无主持人，智能体自主提问与讨论）
	Mode string `gorm:"type:varchar(16);default:hosted" json:"mode"`
	// TopicBrief 主持人澄清后收敛的一句话主题；澄清未完成或用户跳过时为空（讨论回退到 Title+Description）
	TopicBrief       string `gorm:"type:text" json:"topic_brief"`
	Status           string `gorm:"type:varchar(32);default:active" json:"status"`
	BroadcastMode    string `gorm:"type:varchar(16);default:sequential" json:"broadcast_mode"`
	RoutingMode      string `gorm:"type:varchar(32);default:host-assign" json:"routing_mode"`
	GuardMaxExchange int    `gorm:"default:3" json:"guard_max_exchange"`
	// 主持人介入间隔（子轮数）：每完成 N 轮主持人总结并重定向；0 表示不介入
	HostEveryNRounds int `gorm:"default:0" json:"host_every_n_rounds"`
	// 裁判 + 共识检测：开启后每个子轮结束由独立裁判判定是否收敛，收敛即落共识消息并终止讨论
	ConsensusDetectionEnabled bool       `gorm:"default:false" json:"consensus_detection_enabled"`
	KnowledgeBaseIDs          StringList `gorm:"type:jsonb" json:"knowledge_base_ids"`
	// AgendaEnabled 结构性治理总闸：开启后主持人开场拆解子议题，讨论按议题组织上下文
	// 并对每条发言做准入判定（否则退回"全量上下文 + 仅提示词约束"的旧行为）
	AgendaEnabled bool `gorm:"default:false" json:"agenda_enabled"`
	// AdmissionMode 发言准入模式：off / observe / enforce，见 MeetingAdmission* 常量
	AdmissionMode string `gorm:"type:varchar(16);default:off" json:"admission_mode"`
	// WebSearchEnabled 会议级联网搜索总闸（默认关闭）：开启后强制全部参会智能体启用联网检索（需系统已配置搜索服务）
	WebSearchEnabled         bool   `gorm:"default:false" json:"web_search_enabled"`
	CurrentSolutionMessageID string `gorm:"type:varchar(36)" json:"current_solution_message_id"`
	ContextSummary           string `gorm:"type:text" json:"context_summary"`
	// ArtifactSessionID 懒创建的产物生成后台会话（空 = 尚未生成过产物）；
	// 会话带隐藏标记不出现在聊天侧栏，属主为会议创建者
	ArtifactSessionID string         `gorm:"type:varchar(36)" json:"artifact_session_id"`
	CreatedAt         time.Time      `json:"created_at"`
	UpdatedAt         time.Time      `json:"updated_at"`
	DeletedAt         gorm.DeletedAt `gorm:"index" json:"-"`
}

func (MeetingRoom) TableName() string { return "meeting_rooms" }

func (r *MeetingRoom) BeforeCreate(_ *gorm.DB) error {
	if r.ID == "" {
		r.ID = uuid.New().String()
	}
	return nil
}

// MeetingParticipant 参会者（主持人唯一不可移除）
type MeetingParticipant struct {
	ID            string    `gorm:"type:varchar(36);primaryKey" json:"id"`
	MeetingRoomID string    `gorm:"type:varchar(36);index" json:"meeting_room_id"`
	Kind          string    `gorm:"type:varchar(8)" json:"kind"`
	AgentID       string    `gorm:"type:varchar(36)" json:"agent_id"`
	DisplayName   string    `gorm:"type:varchar(128)" json:"display_name"`
	Avatar        string    `gorm:"type:varchar(64)" json:"avatar"`
	OrderIndex    int       `json:"order_index"`
	CreatedAt     time.Time `json:"created_at"`
}

func (MeetingParticipant) TableName() string { return "meeting_participants" }

func (p *MeetingParticipant) BeforeCreate(_ *gorm.DB) error {
	if p.ID == "" {
		p.ID = uuid.New().String()
	}
	return nil
}

// MeetingMessage 会议消息
type MeetingMessage struct {
	ID                string     `gorm:"type:varchar(36);primaryKey" json:"id"`
	MeetingRoomID     string     `gorm:"type:varchar(36);index" json:"meeting_room_id"`
	SenderKind        string     `gorm:"type:varchar(8)" json:"sender_kind"`
	SenderID          string     `gorm:"type:varchar(36)" json:"sender_id"`
	ReplyToMessageID  string     `gorm:"type:varchar(36)" json:"reply_to_message_id"`
	MentionedAgentIDs StringList `gorm:"type:jsonb" json:"mentioned_agent_ids"`
	Content           string     `gorm:"type:text" json:"content"`
	Payload           []byte     `gorm:"type:jsonb" json:"payload,omitempty"`
	// AgendaItemID 发言归属的议程项（空 = 未归属，如开场澄清或议程外追问）
	AgendaItemID    string `gorm:"type:varchar(36);index" json:"agenda_item_id"`
	ReviewState     string `gorm:"type:varchar(16);default:none" json:"review_state"`
	IsSolution      bool   `gorm:"default:false" json:"is_solution"`
	SolutionVersion int    `gorm:"default:0" json:"solution_version"`
	IsConsensus     bool   `gorm:"default:false" json:"is_consensus"`
	// AdmissionState 发言准入判定结果（passed/observed/rewritten；空 = 未判定或关闭）
	AdmissionState string `gorm:"type:varchar(16);default:''" json:"admission_state"`
	// AdmissionNote 准入未通过的原因（enforce 模式下用于要求重写；便于运维调参）
	AdmissionNote string    `gorm:"type:text" json:"admission_note,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

func (MeetingMessage) TableName() string { return "meeting_messages" }

func (m *MeetingMessage) BeforeCreate(_ *gorm.DB) error {
	if m.ID == "" {
		m.ID = uuid.New().String()
	}
	return nil
}

// MeetingAgendaItem 议程项：主持人开场拆解出的一个子议题。
// 讨论按议程项组织上下文（只带本议题记录 + 其他议题一句话摘要），
// 从而避免"全量历史灌入"导致的链式漂移。
type MeetingAgendaItem struct {
	ID            string `gorm:"type:varchar(36);primaryKey" json:"id"`
	MeetingRoomID string `gorm:"type:varchar(36);index:idx_meeting_agenda_room_order" json:"meeting_room_id"`
	// Seq 议程顺序（0 起），同一房间唯一
	Seq int `gorm:"index:idx_meeting_agenda_room_order" json:"seq"`
	// Title 子议题标题（一句话）
	Title string `gorm:"type:varchar(255);not null" json:"title"`
	// Goal 该议题要产出的结论（可判定）
	Goal string `gorm:"type:text" json:"goal"`
	// OwnerAgentIDs 建议负责的智能体（空 = 由主持人按相关性指派）
	OwnerAgentIDs StringList `gorm:"type:jsonb" json:"owner_agent_ids"`
	// KnowledgeBaseIDs 该议题建议检索的知识库（空 = 沿用会议级知识库）
	KnowledgeBaseIDs StringList `gorm:"type:jsonb" json:"knowledge_base_ids"`
	Status           string     `gorm:"type:varchar(16);default:pending" json:"status"`
	// Summary 该议题已达成的小结（由主持人收敛时写入，供后续议题作为上下文）
	Summary   string    `gorm:"type:text" json:"summary"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (MeetingAgendaItem) TableName() string { return "meeting_agenda_items" }

func (a *MeetingAgendaItem) BeforeCreate(_ *gorm.DB) error {
	if a.ID == "" {
		a.ID = uuid.New().String()
	}
	return nil
}

// MeetingArtifact 方案产物（正文在 solution_message_id 指向的消息里）
type MeetingArtifact struct {
	ID                string `gorm:"type:varchar(36);primaryKey" json:"id"`
	MeetingRoomID     string `gorm:"type:varchar(36);index" json:"meeting_room_id"`
	SolutionMessageID string `gorm:"type:varchar(36)" json:"solution_message_id"`
	Version           int    `json:"version"`
	AdoptedCount      int    `json:"adopted_count"`
	RejectedCount     int    `json:"rejected_count"`
	ExportedFormat    string `gorm:"type:varchar(8)" json:"exported_format"`
	// ---- 以下为产物扩展（迁移 000128；solution 行保持零值）----
	Kind            string    `gorm:"type:varchar(16)" json:"kind"`       // solution / generated（历史行读取时归一为 solution）
	KindType        string    `gorm:"type:varchar(16)" json:"kind_type"`  // 8 种形态之一（generated 用）
	Title           string    `gorm:"type:varchar(255)" json:"title"`     // 展示标题
	Requirement     string    `gorm:"type:text" json:"requirement"`       // 用户补充要求（regenerate 回放）
	FileName        string    `gorm:"type:varchar(255)" json:"file_name"` // 交付文件名（generated 文件形态）
	FileType        string    `gorm:"type:varchar(32)" json:"file_type"`  // MIME/类型描述
	FileSize        int64     `gorm:"default:0" json:"file_size"`         // 字节数
	URL             string    `gorm:"type:varchar(512)" json:"url"`       // 存储引用（provider://path），下载走会话端点
	ContentHash     string    `gorm:"type:varchar(64)" json:"content_hash"`
	Status          string    `gorm:"type:varchar(16)" json:"status"`            // generating / ready / failed
	Error           string    `gorm:"type:text" json:"error"`                    // 生成失败原因
	SourceMessageID string    `gorm:"type:varchar(36)" json:"source_message_id"` // 生成产物的后台会话消息 ID
	CreatedBy       string    `gorm:"type:varchar(36)" json:"created_by"`
	CreatedAt       time.Time `json:"created_at"`
}

func (MeetingArtifact) TableName() string { return "meeting_artifacts" }

func (a *MeetingArtifact) BeforeCreate(_ *gorm.DB) error {
	if a.ID == "" {
		a.ID = uuid.New().String()
	}
	return nil
}

// MeetingArtifactDetail 产物 + 方案正文（正文存消息表不重复存储，联查带出）
type MeetingArtifactDetail struct {
	*MeetingArtifact
	Content string `json:"content"`
}
