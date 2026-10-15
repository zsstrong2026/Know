package types

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ---- 常量 ----

const (
	MeetingStatusActive   = "active"
	MeetingStatusArchived = "archived"

	MeetingBroadcastSequential = "sequential"
	MeetingBroadcastParallel   = "parallel"

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
	ID                       string         `gorm:"type:varchar(36);primaryKey" json:"id"`
	TenantID                 uint64         `gorm:"index" json:"tenant_id"`
	CreatedBy                string         `gorm:"type:varchar(36);index" json:"created_by"`
	Title                    string         `gorm:"type:varchar(255)" json:"title"`
	Description              string         `gorm:"type:text" json:"description"`
	Status                   string         `gorm:"type:varchar(16);default:active" json:"status"`
	BroadcastMode            string         `gorm:"type:varchar(16);default:sequential" json:"broadcast_mode"`
	GuardMaxExchange         int            `gorm:"default:3" json:"guard_max_exchange"`
	KnowledgeBaseIDs         StringList     `gorm:"type:jsonb" json:"knowledge_base_ids"`
	CurrentSolutionMessageID string         `gorm:"type:varchar(36)" json:"current_solution_message_id"`
	ContextSummary           string         `gorm:"type:text" json:"context_summary"`
	CreatedAt                time.Time      `json:"created_at"`
	UpdatedAt                time.Time      `json:"updated_at"`
	DeletedAt                gorm.DeletedAt `gorm:"index" json:"-"`
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
	ReviewState       string     `gorm:"type:varchar(16);default:none" json:"review_state"`
	IsSolution        bool       `gorm:"default:false" json:"is_solution"`
	SolutionVersion   int        `gorm:"default:0" json:"solution_version"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

func (MeetingMessage) TableName() string { return "meeting_messages" }

func (m *MeetingMessage) BeforeCreate(_ *gorm.DB) error {
	if m.ID == "" {
		m.ID = uuid.New().String()
	}
	return nil
}

// MeetingArtifact 方案产物（正文在 solution_message_id 指向的消息里）
type MeetingArtifact struct {
	ID                string    `gorm:"type:varchar(36);primaryKey" json:"id"`
	MeetingRoomID     string    `gorm:"type:varchar(36);index" json:"meeting_room_id"`
	SolutionMessageID string    `gorm:"type:varchar(36)" json:"solution_message_id"`
	Version           int       `json:"version"`
	AdoptedCount      int       `json:"adopted_count"`
	RejectedCount     int       `json:"rejected_count"`
	ExportedFormat    string    `gorm:"type:varchar(8)" json:"exported_format"`
	CreatedBy         string    `gorm:"type:varchar(36)" json:"created_by"`
	CreatedAt         time.Time `json:"created_at"`
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
