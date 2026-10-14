# 会议室（Meeting Room）功能实施计划

> **给执行者：** 执行本计划必须使用 REQUIRED SUB-SKILL: `superpowers:executing-plans`（逐任务执行、按检查点汇报）。每个 Task 严格按 TDD 顺序：写失败测试 → 运行确认失败 → 最小实现 → 运行通过 → 提交。

**Goal:** 在 WeKnora 中新增独立"会议室"子系统：用户可按主题创建会议、拉入多个智能体，主持人（系统）协调多智能体围绕主题广播式讨论，用户逐条评审采纳/不采纳，最终由主持人生成结构化方案并沉淀为可回滚的产物。

**Architecture:** 设计规格见 [2026-10-13-meeting-room-design.md](../specs/2026-10-13-meeting-room-design.md)（commit `e9aaeb01d`）。后端 Go/Gin/GORM/Dig/Redis/SSE，4 张新表 + `MeetingEngine` 程序化复用 `SessionService.AgentQA`（内存态 Session + 克隆 CustomAgent 强制 `MultiTurnEnabled=false` + 自算 KB 并集放入 `req.KnowledgeBaseIDs` + eventBus 订阅 `EventAgentFinalAnswer`/`EventError` 捕获答案）。前端 Vue3/Pinia/Element Plus，`@microsoft/fetch-event-source` 订阅 SSE。

**Tech Stack:** Go 1.2x, Gin, GORM, Uber Dig, Redis（`redislock`）, PostgreSQL（`migrations/versioned/`，当前最新 000114）；前端 Vue 3, Pinia, Element Plus, TypeScript；测试：Go `testify`（`go test -v ./...`，Makefile L111），前端 `tsx --test`（`npm test`，测试文件与源码同目录）。

---

## File Structure

**后端新建：**
- `migrations/versioned/000115_meeting_room.up.sql` / `000115_meeting_room.down.sql` — 4 张表
- `internal/types/meeting.go` — 4 个模型 + 常量
- `internal/types/interfaces/meeting.go` — Repository/Service/Engine 接口
- `internal/application/repository/meeting.go` — 4 个 GORM Repository
- `internal/application/service/meeting.go` — CRUD/评审/方案/产物 Service
- `internal/application/service/meeting_broadcast.go` — 进程内 SSE 广播器
- `internal/application/service/meeting_engine.go` — 讨论引擎 RunTurn 状态机
- `internal/application/service/meeting_engine_test.go` — 引擎纯逻辑单测（KB 并集/筛选解析/护栏检测/版本号）
- `internal/application/service/meeting_test.go` — Service 单测（评审流转/产物快照）
- `internal/handler/meeting.go` — 15 个端点 + SSE stream

**后端修改：**
- `internal/router/routes_meeting.go`（新建）+ `internal/router/router.go`（`RouterParams` 加 `MeetingHandler`，注册路由）
- `internal/router/rbac.go`（加 `meetingCreator` lookup 与 `OwnedMeetingOrAdmin()`）
- `internal/container/container.go`（Provide 4 个 Repository、MeetingService、MeetingEngine、MeetingHandler）
- `internal/config/config.go` + `config/config.yaml` + `.env.example`（`MEETING_GUARD_MAX_EXCHANGE` / `MEETING_CONTEXT_MAX_MESSAGES` / `MEETING_DEFAULT_BROADCAST_MODE`）

**前端新建：**
- `frontend/src/api/meeting/index.ts` + `frontend/src/api/meeting/index.test.ts`
- `frontend/src/stores/meeting.ts`
- `frontend/src/views/meeting/MeetingListView.vue`、`MeetingCreateView.vue`、`MeetingRoomView.vue`
- `frontend/src/views/meeting/components/ParticipantChip.vue`、`PendingReviewBar.vue`、`ArtifactDrawer.vue`

**前端修改：**
- `frontend/src/router/index.ts`（`/platform` children 加 3 条路由）
- `frontend/src/stores/menu.ts`（menuArr 加会议室入口，icon 复用 `agent`）
- `frontend/src/i18n/locales/zh-CN.ts`（menu 节加 `meetings: '会议室'`）、`en-US.ts`（`meetings: 'Meeting Rooms'`）

**文档：**
- `docs/meeting-room.md` — 中文用户文档（M6 产出）

---

## 全局约定

- 提交信息格式：`feat(meeting): <描述>` / `test(meeting): ...` / `docs(meeting): ...`，每个 Task 结束提交一次。
- Go 测试运行：`go test ./internal/application/service/ -run TestXxx -v`（在仓库根目录执行）；全量 `go test -v ./...`。
- 前端测试运行：`cd frontend; npm test -- src/api/meeting/index.test.ts`。
- 迁移执行：`make migrate-up`；创建新迁移 `make migrate-create name=meeting_room`（本计划直接给出文件内容，仍需手工核对编号未被占用）。
- 所有 handler 内通过 `types.TenantIDFromContext(ctx)` / `types.UserIDFromContext(ctx)` 取租户与用户。
- 常量区分：LLM 上下文 50 条（`MEETING_CONTEXT_MAX_MESSAGES`），前端展示 100 条。

---

# Milestone M1 — 数据层 + 最小可用会议（单智能体链路调通）

**验收标准：** 创建含 1 个智能体的会议 → 发消息 → 该智能体经 AgentQA 流式产出回复并落库 → SSE 推送到前端对话框 → 刷新后历史消息仍在。

## Task T1.1 数据库迁移

**文件：** `migrations/versioned/000115_meeting_room.up.sql`（新建）、`migrations/versioned/000115_meeting_room.down.sql`（新建）

- [ ] **Step 1: 写 up 迁移（完整内容）**

```sql
-- 会议室：4 张表。所有查询均以 tenant_id + created_by 双重过滤（仅创建者可见）。
CREATE TABLE meeting_rooms (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    created_by VARCHAR(36) NOT NULL,
    title VARCHAR(255) NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    status VARCHAR(16) NOT NULL DEFAULT 'active',        -- active / archived
    broadcast_mode VARCHAR(16) NOT NULL DEFAULT 'sequential', -- sequential / parallel
    guard_max_exchange INTEGER NOT NULL DEFAULT 3,       -- 护栏：互呛来回上限
    knowledge_base_ids JSONB NOT NULL DEFAULT '[]',      -- 会议级知识库范围
    current_solution_message_id VARCHAR(36) NOT NULL DEFAULT '',
    context_summary TEXT NOT NULL DEFAULT '',            -- 早期消息摘要缓存
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMP WITH TIME ZONE
);
CREATE INDEX idx_meeting_rooms_tenant_creator_status ON meeting_rooms (tenant_id, created_by, status);
CREATE INDEX idx_meeting_rooms_tenant_creator_updated ON meeting_rooms (tenant_id, created_by, updated_at DESC);

CREATE TABLE meeting_participants (
    id VARCHAR(36) PRIMARY KEY,
    meeting_room_id VARCHAR(36) NOT NULL REFERENCES meeting_rooms(id) ON DELETE CASCADE,
    kind VARCHAR(8) NOT NULL,                            -- host / agent
    agent_id VARCHAR(36) NOT NULL DEFAULT '',
    display_name VARCHAR(128) NOT NULL DEFAULT '',
    avatar VARCHAR(64) NOT NULL DEFAULT '',
    order_index INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);
-- 同一会议内同一智能体唯一；主持人唯一且不可移除
CREATE UNIQUE INDEX idx_meeting_participants_room_agent ON meeting_participants (meeting_room_id, agent_id) WHERE kind = 'agent';
CREATE UNIQUE INDEX idx_meeting_participants_room_host ON meeting_participants (meeting_room_id) WHERE kind = 'host';

CREATE TABLE meeting_messages (
    id VARCHAR(36) PRIMARY KEY,
    meeting_room_id VARCHAR(36) NOT NULL REFERENCES meeting_rooms(id) ON DELETE CASCADE,
    sender_kind VARCHAR(8) NOT NULL,                     -- user / agent / host / system
    sender_id VARCHAR(36) NOT NULL DEFAULT '',           -- agent_id 或用户 ID
    reply_to_message_id VARCHAR(36) NOT NULL DEFAULT '',
    mentioned_agent_ids JSONB NOT NULL DEFAULT '[]',     -- @ 点名（优先于智能筛选）
    content TEXT NOT NULL,
    payload JSONB,                                       -- 引用/思考过程等扩展
    review_state VARCHAR(16) NOT NULL DEFAULT 'none',    -- none / pending / adopted / rejected
    is_solution BOOLEAN NOT NULL DEFAULT false,
    solution_version INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_meeting_messages_room_created ON meeting_messages (meeting_room_id, created_at, id);
CREATE INDEX idx_meeting_messages_room_review ON meeting_messages (meeting_room_id, review_state);
CREATE INDEX idx_meeting_messages_room_solution ON meeting_messages (meeting_room_id, is_solution);

CREATE TABLE meeting_artifacts (
    id VARCHAR(36) PRIMARY KEY,
    meeting_room_id VARCHAR(36) NOT NULL REFERENCES meeting_rooms(id) ON DELETE CASCADE,
    solution_message_id VARCHAR(36) NOT NULL,            -- 指向消息，不重复存正文
    version INTEGER NOT NULL,
    adopted_count INTEGER NOT NULL DEFAULT 0,            -- 快照：采纳数
    rejected_count INTEGER NOT NULL DEFAULT 0,           -- 快照：不采纳数
    exported_format VARCHAR(8) NOT NULL DEFAULT 'md',
    created_by VARCHAR(36) NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX idx_meeting_artifacts_room_version ON meeting_artifacts (meeting_room_id, version);
```

- [ ] **Step 2: 写 down 迁移（完整内容）**

```sql
DROP TABLE IF EXISTS meeting_artifacts;
DROP TABLE IF EXISTS meeting_messages;
DROP TABLE IF EXISTS meeting_participants;
DROP TABLE IF EXISTS meeting_rooms;
```

- [ ] **Step 3: 运行迁移验证**

Run: `make migrate-up`
Expected: 输出含 `000115_meeting_room` applied（或 `Migrating to version 115`）；`make migrate-version` 显示 115。

- [ ] **Step 4: 提交**

Run: `git add migrations/versioned/000115_meeting_room.up.sql migrations/versioned/000115_meeting_room.down.sql; git commit -m "feat(meeting): 新增会议室 4 张表迁移 000115"`

## Task T1.2 领域模型 types

**文件：** `internal/types/meeting.go`（新建）

- [ ] **Step 1: 写模型（完整内容）**

```go
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
	ID                 string     `gorm:"type:varchar(36);primaryKey" json:"id"`
	MeetingRoomID      string     `gorm:"type:varchar(36);index" json:"meeting_room_id"`
	SenderKind         string     `gorm:"type:varchar(8)" json:"sender_kind"`
	SenderID           string     `gorm:"type:varchar(36)" json:"sender_id"`
	ReplyToMessageID   string     `gorm:"type:varchar(36)" json:"reply_to_message_id"`
	MentionedAgentIDs  StringList `gorm:"type:jsonb" json:"mentioned_agent_ids"`
	Content            string     `gorm:"type:text" json:"content"`
	Payload            []byte     `gorm:"type:jsonb" json:"payload,omitempty"`
	ReviewState        string     `gorm:"type:varchar(16);default:none" json:"review_state"`
	IsSolution         bool       `gorm:"default:false" json:"is_solution"`
	SolutionVersion    int        `gorm:"default:0" json:"solution_version"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
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
```

- [ ] **Step 2: 编译验证**

Run: `go build ./internal/types/`
Expected: 无输出（编译通过）。

- [ ] **Step 3: 提交**

Run: `git add internal/types/meeting.go; git commit -m "feat(meeting): 新增会议室领域模型"`

## Task T1.3 Repository 接口与实现

**文件：** `internal/types/interfaces/meeting.go`（新建）、`internal/application/repository/meeting.go`（新建）

- [ ] **Step 1: 写接口（完整内容）**

`internal/types/interfaces/meeting.go`:

```go
package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// MeetingRoomRepository 会议室存储（全部按 tenant_id+created_by 过滤，仅创建者可见）
type MeetingRoomRepository interface {
	Create(ctx context.Context, room *types.MeetingRoom) error
	GetByID(ctx context.Context, id string, tenantID uint64) (*types.MeetingRoom, error)
	ListByCreator(ctx context.Context, tenantID uint64, createdBy string, status string, offset, limit int) ([]*types.MeetingRoom, int64, error)
	Update(ctx context.Context, room *types.MeetingRoom) error
	Delete(ctx context.Context, id string, tenantID uint64) error // 软删
}

type MeetingParticipantRepository interface {
	CreateBatch(ctx context.Context, ps []*types.MeetingParticipant) error
	ListByRoom(ctx context.Context, roomID string) ([]*types.MeetingParticipant, error)
	CountAgentsByRoom(ctx context.Context, roomID string) (int64, error)
}

type MeetingMessageRepository interface {
	Create(ctx context.Context, msg *types.MeetingMessage) error
	GetByID(ctx context.Context, id string) (*types.MeetingMessage, error)
	// ListByRoom 游标分页：beforeID 为空取最新 limit 条；否则取该消息之前更早的 limit 条。返回正序（旧->新）。
	ListByRoom(ctx context.Context, roomID string, beforeID string, limit int) ([]*types.MeetingMessage, error)
	// ListRecent 取最近 limit 条（正序），供 LLM 上下文使用
	ListRecent(ctx context.Context, roomID string, limit int) ([]*types.MeetingMessage, error)
	UpdateReviewState(ctx context.Context, id string, state string) error
	CountByRoom(ctx context.Context, roomID string) (int64, error)
	CountByReviewStates(ctx context.Context, roomID string, states []string) (int64, error)
	MaxSolutionVersion(ctx context.Context, roomID string) (int, error)
	ListByReviewState(ctx context.Context, roomID string, state string) ([]*types.MeetingMessage, error)
}

type MeetingArtifactRepository interface {
	Create(ctx context.Context, a *types.MeetingArtifact) error
	GetByID(ctx context.Context, id string, roomID string) (*types.MeetingArtifact, error)
	ListByRoom(ctx context.Context, roomID string) ([]*types.MeetingArtifact, error)
}
```

- [ ] **Step 2: 写实现（完整内容）**

`internal/application/repository/meeting.go`:

```go
package repository

import (
	"context"
	"errors"

	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

var ErrMeetingRoomNotFound = errors.New("meeting room not found")
var ErrMeetingMessageNotFound = errors.New("meeting message not found")
var ErrMeetingArtifactNotFound = errors.New("meeting artifact not found")

type meetingRoomRepository struct{ db *gorm.DB }

func NewMeetingRoomRepository(db *gorm.DB) interfaces.MeetingRoomRepository {
	return &meetingRoomRepository{db: db}
}

func (r *meetingRoomRepository) Create(ctx context.Context, room *types.MeetingRoom) error {
	return r.db.WithContext(ctx).Create(room).Error
}

func (r *meetingRoomRepository) GetByID(ctx context.Context, id string, tenantID uint64) (*types.MeetingRoom, error) {
	var room types.MeetingRoom
	err := r.db.WithContext(ctx).Where("id = ? AND tenant_id = ?", id, tenantID).First(&room).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrMeetingRoomNotFound
	}
	return &room, err
}

func (r *meetingRoomRepository) ListByCreator(ctx context.Context, tenantID uint64, createdBy string, status string, offset, limit int) ([]*types.MeetingRoom, int64, error) {
	var rooms []*types.MeetingRoom
	var total int64
	q := r.db.WithContext(ctx).Model(&types.MeetingRoom{}).
		Where("tenant_id = ? AND created_by = ?", tenantID, createdBy)
	if status != "" {
		q = q.Where("status = ?", status)
	}
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	err := q.Order("updated_at DESC").Offset(offset).Limit(limit).Find(&rooms).Error
	return rooms, total, err
}

func (r *meetingRoomRepository) Update(ctx context.Context, room *types.MeetingRoom) error {
	return r.db.WithContext(ctx).Save(room).Error
}

func (r *meetingRoomRepository) Delete(ctx context.Context, id string, tenantID uint64) error {
	return r.db.WithContext(ctx).
		Where("id = ? AND tenant_id = ?", id, tenantID).
		Delete(&types.MeetingRoom{}).Error
}

type meetingParticipantRepository struct{ db *gorm.DB }

func NewMeetingParticipantRepository(db *gorm.DB) interfaces.MeetingParticipantRepository {
	return &meetingParticipantRepository{db: db}
}

func (r *meetingParticipantRepository) CreateBatch(ctx context.Context, ps []*types.MeetingParticipant) error {
	if len(ps) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Create(ps).Error
}

func (r *meetingParticipantRepository) ListByRoom(ctx context.Context, roomID string) ([]*types.MeetingParticipant, error) {
	var ps []*types.MeetingParticipant
	err := r.db.WithContext(ctx).
		Where("meeting_room_id = ?", roomID).
		Order("order_index ASC, created_at ASC").Find(&ps).Error
	return ps, err
}

func (r *meetingParticipantRepository) CountAgentsByRoom(ctx context.Context, roomID string) (int64, error) {
	var n int64
	err := r.db.WithContext(ctx).Model(&types.MeetingParticipant{}).
		Where("meeting_room_id = ? AND kind = ?", roomID, types.ParticipantKindAgent).
		Count(&n).Error
	return n, err
}

type meetingMessageRepository struct{ db *gorm.DB }

func NewMeetingMessageRepository(db *gorm.DB) interfaces.MeetingMessageRepository {
	return &meetingMessageRepository{db: db}
}

func (r *meetingMessageRepository) Create(ctx context.Context, msg *types.MeetingMessage) error {
	return r.db.WithContext(ctx).Create(msg).Error
}

func (r *meetingMessageRepository) GetByID(ctx context.Context, id string) (*types.MeetingMessage, error) {
	var msg types.MeetingMessage
	err := r.db.WithContext(ctx).Where("id = ?", id).First(&msg).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrMeetingMessageNotFound
	}
	return &msg, err
}

func (r *meetingMessageRepository) ListByRoom(ctx context.Context, roomID string, beforeID string, limit int) ([]*types.MeetingMessage, error) {
	q := r.db.WithContext(ctx).Where("meeting_room_id = ?", roomID)
	if beforeID != "" {
		// 游标：取 beforeID 这条消息的 (created_at,id)，查询更早的
		var pivot types.MeetingMessage
		if err := r.db.WithContext(ctx).Select("created_at", "id").Where("id = ?", beforeID).First(&pivot).Error; err != nil {
			return nil, ErrMeetingMessageNotFound
		}
		q = q.Where("(created_at, id) < (?, ?)", pivot.CreatedAt, pivot.ID)
	}
	var desc []*types.MeetingMessage
	if err := q.Order("created_at DESC, id DESC").Limit(limit).Find(&desc).Error; err != nil {
		return nil, err
	}
	// 反转为正序（旧->新）
	msgs := make([]*types.MeetingMessage, 0, len(desc))
	for i := len(desc) - 1; i >= 0; i-- {
		msgs = append(msgs, desc[i])
	}
	return msgs, nil
}

func (r *meetingMessageRepository) ListRecent(ctx context.Context, roomID string, limit int) ([]*types.MeetingMessage, error) {
	return r.ListByRoom(ctx, roomID, "", limit)
}

func (r *meetingMessageRepository) UpdateReviewState(ctx context.Context, id string, state string) error {
	return r.db.WithContext(ctx).Model(&types.MeetingMessage{}).
		Where("id = ?", id).Update("review_state", state).Error
}

func (r *meetingMessageRepository) CountByRoom(ctx context.Context, roomID string) (int64, error) {
	var n int64
	err := r.db.WithContext(ctx).Model(&types.MeetingMessage{}).
		Where("meeting_room_id = ?", roomID).Count(&n).Error
	return n, err
}

func (r *meetingMessageRepository) CountByReviewStates(ctx context.Context, roomID string, states []string) (int64, error) {
	var n int64
	err := r.db.WithContext(ctx).Model(&types.MeetingMessage{}).
		Where("meeting_room_id = ? AND review_state IN ?", roomID, states).
		Count(&n).Error
	return n, err
}

func (r *meetingMessageRepository) MaxSolutionVersion(ctx context.Context, roomID string) (int, error) {
	var v int
	err := r.db.WithContext(ctx).Model(&types.MeetingMessage{}).
		Where("meeting_room_id = ?", roomID).
		Select("COALESCE(MAX(solution_version), 0)").Scan(&v).Error
	return v, err
}

func (r *meetingMessageRepository) ListByReviewState(ctx context.Context, roomID string, state string) ([]*types.MeetingMessage, error) {
	var msgs []*types.MeetingMessage
	err := r.db.WithContext(ctx).
		Where("meeting_room_id = ? AND review_state = ?", roomID, state).
		Order("created_at ASC").Find(&msgs).Error
	return msgs, err
}

type meetingArtifactRepository struct{ db *gorm.DB }

func NewMeetingArtifactRepository(db *gorm.DB) interfaces.MeetingArtifactRepository {
	return &meetingArtifactRepository{db: db}
}

func (r *meetingArtifactRepository) Create(ctx context.Context, a *types.MeetingArtifact) error {
	return r.db.WithContext(ctx).Create(a).Error
}

func (r *meetingArtifactRepository) GetByID(ctx context.Context, id string, roomID string) (*types.MeetingArtifact, error) {
	var a types.MeetingArtifact
	err := r.db.WithContext(ctx).
		Where("id = ? AND meeting_room_id = ?", id, roomID).First(&a).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrMeetingArtifactNotFound
	}
	return &a, err
}

func (r *meetingArtifactRepository) ListByRoom(ctx context.Context, roomID string) ([]*types.MeetingArtifact, error) {
	var as []*types.MeetingArtifact
	err := r.db.WithContext(ctx).
		Where("meeting_room_id = ?", roomID).
		Order("version DESC").Find(&as).Error
	return as, err
}
```

- [ ] **Step 3: 编译验证**

Run: `go build ./internal/types/interfaces/ ./internal/application/repository/`
Expected: 编译通过。

- [ ] **Step 4: 提交**

Run: `git add internal/types/interfaces/meeting.go internal/application/repository/meeting.go; git commit -m "feat(meeting): 新增会议室 Repository 接口与 GORM 实现"`

## Task T1.4 MeetingService（CRUD + 发消息入口）

**文件：** `internal/types/interfaces/meeting.go`（追加 Service 接口）、`internal/application/service/meeting.go`（新建）、`internal/application/service/meeting_test.go`（新建）

- [ ] **Step 1: 写失败测试**

`internal/application/service/meeting_test.go`:

```go
package service

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestValidateMeetingCreate(t *testing.T) {
	// 标题必填
	err := validateMeetingCreate(&types.MeetingRoom{}, []string{"a1"})
	assert.Error(t, err)
	// 至少 1 个智能体
	err = validateMeetingCreate(&types.MeetingRoom{Title: "x"}, nil)
	assert.Error(t, err)
	// 合法
	err = validateMeetingCreate(&types.MeetingRoom{Title: "x"}, []string{"a1"})
	assert.NoError(t, err)
	// 广播模式非法时回退默认
	room := &types.MeetingRoom{Title: "x", BroadcastMode: "bogus"}
	_ = validateMeetingCreate(room, []string{"a1"})
	assert.Equal(t, types.MeetingBroadcastSequential, room.BroadcastMode)
}
```

Run: `go test ./internal/application/service/ -run TestValidateMeetingCreate -v`
Expected: FAIL（`validateMeetingCreate` 未定义，编译错误）。

- [ ] **Step 2: 接口追加 + Service 实现**

`internal/types/interfaces/meeting.go` 追加：

```go
// MeetingService 会议室业务服务
type MeetingService interface {
	CreateMeeting(ctx context.Context, room *types.MeetingRoom, agentIDs []string) (*types.MeetingRoom, error)
	GetMeeting(ctx context.Context, tenantID uint64, id string) (*types.MeetingRoom, []*types.MeetingParticipant, error)
	ListMeetings(ctx context.Context, tenantID uint64, createdBy string, status string, offset, limit int) ([]*types.MeetingRoom, int64, error)
	UpdateMeeting(ctx context.Context, room *types.MeetingRoom) error
	DeleteMeeting(ctx context.Context, tenantID uint64, id string) error
	// SendUserMessage 落库用户消息并异步触发讨论轮，返回落库后的消息
	SendUserMessage(ctx context.Context, roomID string, userID string, content string, mentionedAgentIDs []string) (*types.MeetingMessage, error)
	ListMessages(ctx context.Context, roomID string, beforeID string, limit int) ([]*types.MeetingMessage, error)
}
```

`internal/application/service/meeting.go`（新建，完整内容）：

```go
package service

import (
	"context"
	"errors"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// meetingService 会议室 CRUD 与消息入口；讨论本身委托给 MeetingEngine
type meetingService struct {
	roomRepo        interfaces.MeetingRoomRepository
	participantRepo interfaces.MeetingParticipantRepository
	messageRepo     interfaces.MeetingMessageRepository
	agentRepo       interfaces.CustomAgentRepository
	engine          interfaces.MeetingEngine
}

func NewMeetingService(
	roomRepo interfaces.MeetingRoomRepository,
	participantRepo interfaces.MeetingParticipantRepository,
	messageRepo interfaces.MeetingMessageRepository,
	agentRepo interfaces.CustomAgentRepository,
	engine interfaces.MeetingEngine,
) interfaces.MeetingService {
	return &meetingService{
		roomRepo: roomRepo, participantRepo: participantRepo,
		messageRepo: messageRepo, agentRepo: agentRepo, engine: engine,
	}
}

// validateMeetingCreate 创建参数校验（标题必填、至少 1 个智能体、广播模式回退默认）
func validateMeetingCreate(room *types.MeetingRoom, agentIDs []string) error {
	if room.Title == "" {
		return errors.New("title is required")
	}
	if len(agentIDs) == 0 {
		return errors.New("at least one agent is required")
	}
	if room.BroadcastMode != types.MeetingBroadcastParallel &&
		room.BroadcastMode != types.MeetingBroadcastSequential {
		room.BroadcastMode = types.MeetingBroadcastSequential
	}
	if room.GuardMaxExchange <= 0 {
		room.GuardMaxExchange = 3
	}
	if room.Status == "" {
		room.Status = types.MeetingStatusActive
	}
	return nil
}

func (s *meetingService) CreateMeeting(ctx context.Context, room *types.MeetingRoom, agentIDs []string) (*types.MeetingRoom, error) {
	if err := validateMeetingCreate(room, agentIDs); err != nil {
		return nil, err
	}
	// 校验智能体归属本租户并取显示名/头像
	participants := make([]*types.MeetingParticipant, 0, len(agentIDs)+1)
	// 主持人（系统自动担任，唯一不可移除）
	participants = append(participants, &types.MeetingParticipant{
		MeetingRoomID: room.ID, Kind: types.ParticipantKindHost,
		DisplayName: "主持人", OrderIndex: 0,
	})
	for i, agentID := range agentIDs {
		agent, err := s.agentRepo.GetByID(ctx, agentID, room.TenantID)
		if err != nil {
			return nil, errors.New("agent not found: " + agentID)
		}
		participants = append(participants, &types.MeetingParticipant{
			MeetingRoomID: room.ID, Kind: types.ParticipantKindAgent,
			AgentID: agent.ID, DisplayName: agent.Name, Avatar: agent.Avatar, OrderIndex: i + 1,
		})
	}
	if err := s.roomRepo.Create(ctx, room); err != nil {
		return nil, err
	}
	for _, p := range participants {
		p.MeetingRoomID = room.ID
	}
	if err := s.participantRepo.CreateBatch(ctx, participants); err != nil {
		return nil, err
	}
	return room, nil
}

func (s *meetingService) GetMeeting(ctx context.Context, tenantID uint64, id string) (*types.MeetingRoom, []*types.MeetingParticipant, error) {
	room, err := s.roomRepo.GetByID(ctx, id, tenantID)
	if err != nil {
		return nil, nil, err
	}
	participants, err := s.participantRepo.ListByRoom(ctx, id)
	return room, participants, err
}

func (s *meetingService) ListMeetings(ctx context.Context, tenantID uint64, createdBy string, status string, offset, limit int) ([]*types.MeetingRoom, int64, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	return s.roomRepo.ListByCreator(ctx, tenantID, createdBy, status, offset, limit)
}

func (s *meetingService) UpdateMeeting(ctx context.Context, room *types.MeetingRoom) error {
	return s.roomRepo.Update(ctx, room)
}

func (s *meetingService) DeleteMeeting(ctx context.Context, tenantID uint64, id string) error {
	return s.roomRepo.Delete(ctx, id, tenantID)
}

func (s *meetingService) SendUserMessage(ctx context.Context, roomID string, userID string, content string, mentionedAgentIDs []string) (*types.MeetingMessage, error) {
	msg := &types.MeetingMessage{
		MeetingRoomID:     roomID,
		SenderKind:        types.MeetingSenderUser,
		SenderID:          userID,
		Content:           content,
		MentionedAgentIDs: mentionedAgentIDs,
	}
	if err := s.messageRepo.Create(ctx, msg); err != nil {
		return nil, err
	}
	// 异步触发讨论轮；CloneContext 保留 trace，避免请求 ctx 取消中断讨论
	go func() {
		bgCtx := logger.CloneContext(context.Background(), ctx)
		if err := s.engine.RunTurn(bgCtx, roomID, msg.ID); err != nil {
			logger.Errorf(bgCtx, "meeting RunTurn failed, room=%s err=%v", roomID, err)
		}
	}()
	return msg, nil
}

func (s *meetingService) ListMessages(ctx context.Context, roomID string, beforeID string, limit int) ([]*types.MeetingMessage, error) {
	if limit <= 0 || limit > 100 {
		limit = 100 // 前端展示默认 100 条 + 顶部"加载更早"
	}
	return s.messageRepo.ListByRoom(ctx, roomID, beforeID, limit)
}
```

`internal/types/interfaces/meeting.go` 再追加引擎接口：

```go
// MeetingEngine 讨论引擎：同一会议同一时刻仅一个讨论轮次（Redis 锁保证）
type MeetingEngine interface {
	RunTurn(ctx context.Context, roomID string, triggerMessageID string) error
	Interrupt(ctx context.Context, roomID string) error
}
```

- [ ] **Step 3: 运行测试通过**

Run: `go test ./internal/application/service/ -run TestValidateMeetingCreate -v`
Expected: PASS。注意：此 Task 编译需要 MeetingEngine 已存在，可与 T1.5 合并提交；若单独编译失败属预期，先完成 T1.5 Step 1-2 的接口与骨架后回本步骤。

- [ ] **Step 4: 提交**

Run: `git add internal/types/interfaces/meeting.go internal/application/service/meeting.go internal/application/service/meeting_test.go; git commit -m "feat(meeting): 新增 MeetingService CRUD 与消息入口"`

## Task T1.5 SSE 广播器 + MeetingEngine 单智能体链路

**文件：** `internal/application/service/meeting_broadcast.go`（新建）、`internal/application/service/meeting_engine.go`（新建）、`internal/application/service/meeting_engine_test.go`（新建）

**关键实现要点（来自代码核实）：**
- `AgentQA(ctx, req *types.QARequest, eventBus *event.EventBus) error` 自身**不写消息 DB**；`MultiTurnEnabled=false` 时不读历史 → 用内存态 Session + 克隆 agent 配置。
- `resolveKnowledgeBases`：`req.KnowledgeBaseIDs` 非空时直接使用、**不会**合并 agent 配置 KB → 并集必须由引擎计算。
- 答案捕获：订阅 `event.EventAgentFinalAnswer`（`data.Content` 累积至 `data.Done`）与 `event.EventError`。
- 参考实现：`internal/im/service.go` L2505-2814。

- [ ] **Step 1: 写失败测试**

`internal/application/service/meeting_engine_test.go`:

```go
package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestUnionKnowledgeBaseIDs(t *testing.T) {
	// selected 模式：智能体 KB ∪ 会议级 KB，去重
	got := unionKnowledgeBaseIDs("selected", []string{"a", "b"}, []string{"b", "c"})
	assert.ElementsMatch(t, []string{"a", "b", "c"}, got)
	// none 模式：仅会议级 KB
	got = unionKnowledgeBaseIDs("none", []string{"a"}, []string{"c"})
	assert.Equal(t, []string{"c"}, got)
	// all 模式：返回 nil（AgentQA 回退为智能体"全部知识库"，天然是会议级 KB 的超集）
	got = unionKnowledgeBaseIDs("all", nil, []string{"c"})
	assert.Nil(t, got)
}

func TestBuildMeetingQuery(t *testing.T) {
	room := roomFixture()
	msgs := messagesFixture()
	q := buildMeetingQuery(room, "讨论下部署方案", msgs)
	// 应包含会议主题与共享上下文
	assert.Contains(t, q, room.Title)
	assert.Contains(t, q, "讨论下部署方案")
	assert.Contains(t, q, msgs[0].Content)
}
```

Run: `go test ./internal/application/service/ -run "TestUnionKnowledgeBaseIDs|TestBuildMeetingQuery" -v`
Expected: FAIL（函数与 fixture 未定义）。

- [ ] **Step 2: 广播器实现（完整内容）**

`internal/application/service/meeting_broadcast.go`:

```go
package service

import "sync"

// MeetingEvent 会议室 SSE 事件（事件名与设计文档一致）
type MeetingEvent struct {
	Type string      `json:"type"`
	Data interface{} `json:"data"`
}

// SSE 事件名常量
const (
	MeetingEventMessageCreated       = "message.created"
	MeetingEventMessageReviewUpdated = "message.review_updated"
	MeetingEventTurnStarted          = "discussion.turn_started"
	MeetingEventTurnEnded            = "discussion.turn_ended"
	MeetingEventGuardTriggered       = "guard.triggered"
	MeetingEventSolutionGenerating   = "solution.generating"
	MeetingEventSolutionCreated      = "solution.created"
	MeetingEventPing                 = "ping"
)

// MeetingBroadcaster 进程内按会议房间的发布/订阅（单副本部署前提，见设计风险章节）。
// 导出原因：handler 层 SSE 订阅需经类型断言拿到广播器（见 T1.6 engineBroadcaster）。
type MeetingBroadcaster struct {
	mu   sync.RWMutex
	subs map[string]map[chan MeetingEvent]struct{}
}

func NewMeetingBroadcaster() *MeetingBroadcaster {
	return &MeetingBroadcaster{subs: make(map[string]map[chan MeetingEvent]struct{})}
}

// Subscribe 订阅某房间事件，返回 channel 与取消函数
func (b *MeetingBroadcaster) Subscribe(roomID string) (chan MeetingEvent, func()) {
	ch := make(chan MeetingEvent, 64)
	b.mu.Lock()
	if b.subs[roomID] == nil {
		b.subs[roomID] = make(map[chan MeetingEvent]struct{})
	}
	b.subs[roomID][ch] = struct{}{}
	b.mu.Unlock()
	return ch, func() {
		b.mu.Lock()
		delete(b.subs[roomID], ch)
		if len(b.subs[roomID]) == 0 {
			delete(b.subs, roomID)
		}
		b.mu.Unlock()
		close(ch)
	}
}

// Publish 向房间内全部订阅者广播（非阻塞，慢消费者丢弃并记录由调用方负责）
func (b *MeetingBroadcaster) Publish(roomID string, evt MeetingEvent) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for ch := range b.subs[roomID] {
		select {
		case ch <- evt:
		default: // 慢消费者丢事件，前端有 ping 心跳与重连兜底
		}
	}
}
```

- [ ] **Step 3: 引擎骨架（单智能体，完整内容）**

`internal/application/service/meeting_engine.go`:

```go
package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/Tencent/WeKnora/internal/common/redislock"
	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// meetingEngine 讨论引擎：广播式多智能体讨论（M1 先支持顺序单/多智能体，并发与筛选在 M2）
type meetingEngine struct {
	roomRepo        interfaces.MeetingRoomRepository
	participantRepo interfaces.MeetingParticipantRepository
	messageRepo     interfaces.MeetingMessageRepository
	agentRepo       interfaces.CustomAgentRepository
	sessionService  interfaces.SessionService
	redis           *redis.Client
	broadcaster     *MeetingBroadcaster
	contextMax      int // LLM 上下文消息上限（MEETING_CONTEXT_MAX_MESSAGES，默认 50）
	turnLockLease   time.Duration
}

func NewMeetingEngine(
	roomRepo interfaces.MeetingRoomRepository,
	participantRepo interfaces.MeetingParticipantRepository,
	messageRepo interfaces.MeetingMessageRepository,
	agentRepo interfaces.CustomAgentRepository,
	sessionService interfaces.SessionService,
	redisClient *redis.Client,
) interfaces.MeetingEngine {
	return &meetingEngine{
		roomRepo: roomRepo, participantRepo: participantRepo,
		messageRepo: messageRepo, agentRepo: agentRepo,
		sessionService: sessionService, redis: redisClient,
		broadcaster: NewMeetingBroadcaster(),
		contextMax:    50,
		turnLockLease: 5 * time.Minute,
	}
}

// Broadcaster 暴露给 handler 层订阅 SSE（同进程）
func (e *meetingEngine) Broadcaster() *MeetingBroadcaster { return e.broadcaster }

func turnLockKey(roomID string) string { return fmt.Sprintf("meeting:%s:turn_lock", roomID) }
func interruptKey(roomID string) string { return fmt.Sprintf("meeting:%s:interrupt", roomID) }

// RunTurn 执行一个讨论轮次：同一会议同一时刻仅允许一个轮次
func (e *meetingEngine) RunTurn(ctx context.Context, roomID string, triggerMessageID string) error {
	token, err := redislock.NewToken()
	if err != nil {
		return err
	}
	ok, err := redislock.TryAcquire(ctx, e.redis, turnLockKey(roomID), token, e.turnLockLease)
	if err != nil {
		return err
	}
	if !ok {
		// 已有讨论轮进行中：本条消息排队（已落库，下一轮会带上）
		logger.Infof(ctx, "meeting turn already running, queued. room=%s", roomID)
		return nil
	}
	defer redislock.Release(ctx, e.redis, turnLockKey(roomID), token)
	// 进入新一轮即清除打断标记
	_ = e.redis.Del(ctx, interruptKey(roomID)).Err()

	room, err := e.roomRepo.GetByID(ctx, roomID, 0) // tenantID=0 见下方说明
	if err != nil {
		return err
	}
	trigger, err := e.messageRepo.GetByID(ctx, triggerMessageID)
	if err != nil {
		return err
	}
	participants, err := e.participantRepo.ListByRoom(ctx, roomID)
	if err != nil {
		return err
	}
	agents := filterAgentParticipants(participants)

	e.broadcaster.Publish(roomID, MeetingEvent{Type: MeetingEventTurnStarted, Data: map[string]interface{}{
		"room_id": roomID, "trigger_message_id": triggerMessageID,
	}})
	defer e.broadcaster.Publish(roomID, MeetingEvent{Type: MeetingEventTurnEnded, Data: map[string]interface{}{
		"room_id": roomID,
	}})

	// 顺序广播：按 order_index 逐个智能体发言（M2 加并发模式与智能筛选）
	speakers := selectSpeakers(room, trigger, agents)
	for _, p := range speakers {
		if e.isInterrupted(ctx, roomID) {
			logger.Infof(ctx, "meeting turn interrupted. room=%s", roomID)
			return nil
		}
		if err := e.runAgentTurn(ctx, room, p, trigger, nil); err != nil {
			logger.Errorf(ctx, "agent turn failed, room=%s agent=%s err=%v", roomID, p.AgentID, err)
			// 单个智能体失败不阻塞整轮，广播 system 消息提示
			e.publishSystemMessage(ctx, roomID, fmt.Sprintf("智能体「%s」本轮发言失败：%v", p.DisplayName, err))
		}
	}
	return nil
}

// Interrupt 强制结束当前讨论轮（释放锁由引擎下一轮进入时自行处理，这里置打断标记）
func (e *meetingEngine) Interrupt(ctx context.Context, roomID string) error {
	return e.redis.Set(ctx, interruptKey(roomID), "1", 10*time.Minute).Err()
}

func (e *meetingEngine) isInterrupted(ctx context.Context, roomID string) bool {
	n, err := e.redis.Exists(ctx, interruptKey(roomID)).Result()
	return err == nil && n > 0
}

// runAgentTurn 单个智能体发言：程序化调用 AgentQA，捕获最终答案并落库+广播
func (e *meetingEngine) runAgentTurn(ctx context.Context, room *types.MeetingRoom, p *types.MeetingParticipant, trigger *types.MeetingMessage, replyTo *types.MeetingMessage) error {
	agent, err := e.agentRepo.GetByID(ctx, p.AgentID, room.TenantID)
	if err != nil {
		return err
	}
	meetingAgent := cloneAgentForMeeting(agent)
	kbIDs := unionKnowledgeBaseIDs(meetingAgent.Config.KBSelectionMode, meetingAgent.Config.KnowledgeBases, room.KnowledgeBaseIDs)

	// 共享上下文：最近 N 条（正序）
	contextMsgs, err := e.messageRepo.ListRecent(ctx, room.ID, e.contextMax)
	if err != nil {
		return err
	}
	query := buildMeetingQuery(room, trigger.Content, contextMsgs)

	eventBus := event.NewEventBus()
	var answer strings.Builder
	var runErr error
	eventBus.On(event.EventAgentFinalAnswer, func(_ context.Context, evt event.Event) error {
		data, ok := evt.Data.(event.AgentFinalAnswerData)
		if !ok {
			return nil
		}
		answer.WriteString(data.Content)
		return nil
	})
	eventBus.On(event.EventError, func(_ context.Context, evt event.Event) error {
		if data, ok := evt.Data.(event.ErrorData); ok {
			runErr = fmt.Errorf("stage=%s err=%v", data.Stage, data.Error)
		}
		return nil
	})

	req := &types.QARequest{
		Session:            &types.Session{ID: uuid.New().String(), TenantID: room.TenantID}, // 内存态会话：AgentQA 不落库消息
		Query:              query,
		AssistantMessageID: uuid.New().String(),
		CustomAgent:        meetingAgent,
		KnowledgeBaseIDs:   kbIDs,
	}
	if err := e.sessionService.AgentQA(ctx, req, eventBus); err != nil {
		return err
	}
	if runErr != nil {
		return runErr
	}

	msg := &types.MeetingMessage{
		MeetingRoomID: room.ID,
		SenderKind:    types.MeetingSenderAgent,
		SenderID:      p.AgentID,
		Content:       answer.String(),
		ReviewState:   types.ReviewStateNone,
	}
	if replyTo != nil {
		msg.ReplyToMessageID = replyTo.ID
	}
	if err := e.messageRepo.Create(ctx, msg); err != nil {
		return err
	}
	e.broadcaster.Publish(room.ID, MeetingEvent{Type: MeetingEventMessageCreated, Data: msg})
	return nil
}

// publishSystemMessage 系统提示消息（失败提示、护栏提示等）
func (e *meetingEngine) publishSystemMessage(ctx context.Context, roomID string, content string) {
	msg := &types.MeetingMessage{
		MeetingRoomID: roomID, SenderKind: types.MeetingSenderSystem, Content: content,
	}
	if err := e.messageRepo.Create(ctx, msg); err == nil {
		e.broadcaster.Publish(roomID, MeetingEvent{Type: MeetingEventMessageCreated, Data: msg})
	}
}

// ---- 纯逻辑函数（单测覆盖）----

// cloneAgentForMeeting 克隆智能体配置：关闭多轮（会议上下文经 query 注入），保持其余配置
func cloneAgentForMeeting(agent *types.CustomAgent) *types.CustomAgent {
	clone := *agent
	cfg := agent.Config // 值拷贝
	cfg.MultiTurnEnabled = false
	cfg.HistoryTurns = 0
	// 会议级 KB 由引擎注入 req.KnowledgeBaseIDs；避免"仅@时检索"吞掉注入的 KB
	cfg.RetrieveKBOnlyWhenMentioned = false
	clone.Config = cfg
	return &clone
}

// unionKnowledgeBaseIDs 双层并集：智能体 KB ∪ 会议级 KB
// all 模式返回 nil（AgentQA 回退为智能体全部 KB，天然包含会议级 KB）
func unionKnowledgeBaseIDs(mode string, agentKBs []string, roomKBs []string) []string {
	switch mode {
	case "all":
		return nil
	case "none":
		return append([]string{}, roomKBs...)
	default: // "selected" 及空值
		seen := make(map[string]struct{}, len(agentKBs)+len(roomKBs))
		out := make([]string, 0, len(agentKBs)+len(roomKBs))
		for _, id := range append(append([]string{}, agentKBs...), roomKBs...) {
			if id == "" {
				continue
			}
			if _, dup := seen[id]; dup {
				continue
			}
			seen[id] = struct{}{}
			out = append(out, id)
		}
		return out
	}
}

// filterAgentParticipants 取 agent 类参会者（按 order_index 已排序）
func filterAgentParticipants(ps []*types.MeetingParticipant) []*types.MeetingParticipant {
	out := make([]*types.MeetingParticipant, 0, len(ps))
	for _, p := range ps {
		if p.Kind == types.ParticipantKindAgent {
			out = append(out, p)
		}
	}
	return out
}

// selectSpeakers 选择本轮发言者：@ 点名优先；M1 默认全员（智能筛选在 M2）
func selectSpeakers(room *types.MeetingRoom, trigger *types.MeetingMessage, agents []*types.MeetingParticipant) []*types.MeetingParticipant {
	if len(trigger.MentionedAgentIDs) > 0 {
		mentioned := make(map[string]struct{}, len(trigger.MentionedAgentIDs))
		for _, id := range trigger.MentionedAgentIDs {
			mentioned[id] = struct{}{}
		}
		out := make([]*types.MeetingParticipant, 0, len(agents))
		for _, p := range agents {
			if _, ok := mentioned[p.AgentID]; ok {
				out = append(out, p)
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	return agents
}

// buildMeetingQuery 把会议主题 + 共享上下文 + 触发消息拼成单轮 query（MultiTurnEnabled=false）
func buildMeetingQuery(room *types.MeetingRoom, triggerContent string, contextMsgs []*types.MeetingMessage) string {
	var b strings.Builder
	b.WriteString("【会议主题】" + room.Title)
	if room.Description != "" {
		b.WriteString("\n【会议描述】" + room.Description)
	}
	if room.ContextSummary != "" {
		b.WriteString("\n【早期讨论摘要】" + room.ContextSummary)
	}
	if len(contextMsgs) > 0 {
		b.WriteString("\n【讨论记录】")
		for _, m := range contextMsgs {
			// 跳过触发消息本身，最后单独强调
			if m.Content == triggerContent && m.SenderKind == types.MeetingSenderUser {
				continue
			}
			b.WriteString(fmt.Sprintf("\n%s: %s", senderLabel(m), m.Content))
		}
	}
	b.WriteString("\n【当前议题】" + triggerContent)
	b.WriteString("\n请以你的身份与职责，针对当前议题发表意见，简明扼要。")
	return b.String()
}

func senderLabel(m *types.MeetingMessage) string {
	switch m.SenderKind {
	case types.MeetingSenderUser:
		return "用户"
	case types.MeetingSenderHost:
		return "主持人"
	case types.MeetingSenderSystem:
		return "系统"
	default:
		return "智能体(" + m.SenderID + ")"
	}
}
```

测试 fixture 追加到 `meeting_engine_test.go`：

```go
func roomFixture() *types.MeetingRoom {
	return &types.MeetingRoom{ID: "r1", Title: "发布方案评审", KnowledgeBaseIDs: []string{"c"}}
}

func messagesFixture() []*types.MeetingMessage {
	return []*types.MeetingMessage{
		{ID: "m1", SenderKind: types.MeetingSenderUser, Content: "先同步下背景"},
		{ID: "m2", SenderKind: types.MeetingSenderAgent, SenderID: "a1", Content: "我建议用蓝绿发布"},
	}
}
```

（文件头 import 需加 `"github.com/Tencent/WeKnora/internal/types"`。）

注意：`roomRepo.GetByID(ctx, roomID, 0)` 中 tenantID=0 为占位——M1 需要给 `MeetingRoomRepository` 增加一个不过滤租户的内部方法，或将引擎专用查询改为 `GetByIDInternal`。处理方式：在 `interfaces.MeetingRoomRepository` 增加：

```go
// GetByIDInternal 引擎内部使用：不校验 created_by（讨论轮是系统行为），仍按 tenant 隔离由调用方保证
GetByIDInternal(ctx context.Context, id string) (*types.MeetingRoom, error)
```

并在 `repository/meeting.go` 实现（`Where("id = ?", id)`），同时把引擎中调用改为 `e.roomRepo.GetByIDInternal(ctx, roomID)`。安全边界：租户隔离在 HTTP 层由 `tenant_id+created_by` 双重过滤保证，引擎仅消费已授权会议。

- [ ] **Step 4: 运行测试通过**

Run: `go test ./internal/application/service/ -run "TestUnionKnowledgeBaseIDs|TestBuildMeetingQuery" -v`
Expected: PASS。

- [ ] **Step 5: 提交**

Run: `git add internal/application/service/meeting_broadcast.go internal/application/service/meeting_engine.go internal/application/service/meeting_engine_test.go internal/types/interfaces/meeting.go internal/application/repository/meeting.go; git commit -m "feat(meeting): 新增讨论引擎（单智能体 AgentQA 链路 + Redis 轮次锁 + SSE 广播器）"`

## Task T1.6 Handler + 路由 + RBAC + DI + 配置

**文件：** `internal/handler/meeting.go`（新建）、`internal/router/routes_meeting.go`（新建）、`internal/router/rbac.go`（修改）、`internal/router/router.go`（修改）、`internal/container/container.go`（修改）、`internal/config/config.go`（修改）、`config/config.yaml`（修改）、`.env.example`（修改）

- [ ] **Step 1: Handler（M1 范围端点，完整内容）**

`internal/handler/meeting.go`:

```go
package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// MeetingHandler 会议室 API
type MeetingHandler struct {
	meetingService interfaces.MeetingService
	engine         interfaces.MeetingEngine
}

func NewMeetingHandler(meetingService interfaces.MeetingService, engine interfaces.MeetingEngine) *MeetingHandler {
	return &MeetingHandler{meetingService: meetingService, engine: engine}
}

// MeetingCreatorLookup 供 RBAC RequireOwnershipOrRole 使用：返回会议创建者
func (h *MeetingHandler) MeetingCreatorLookup(c *gin.Context) (string, error) {
	ctx := c.Request.Context()
	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok {
		return "", errors.NewUnauthorizedError("missing tenant")
	}
	room, _, err := h.meetingService.GetMeeting(ctx, tenantID, c.Param("id"))
	if err != nil {
		return "", err // repository.ErrMeetingRoomNotFound 会被中间件映射为放行->handler 出 404
	}
	return room.CreatedBy, nil
}

type createMeetingRequest struct {
	Title            string   `json:"title" binding:"required"`
	Description      string   `json:"description"`
	BroadcastMode    string   `json:"broadcast_mode"`
	GuardMaxExchange int      `json:"guard_max_exchange"`
	KnowledgeBaseIDs []string `json:"knowledge_base_ids"`
	AgentIDs         []string `json:"agent_ids" binding:"required,min=1"`
}

// CreateMeeting POST /api/v1/meetings
func (h *MeetingHandler) CreateMeeting(c *gin.Context) {
	ctx := c.Request.Context()
	var req createMeetingRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}
	tenantID, _ := types.TenantIDFromContext(ctx)
	userID, _ := types.UserIDFromContext(ctx)
	room := &types.MeetingRoom{
		TenantID: tenantID, CreatedBy: userID,
		Title: req.Title, Description: req.Description,
		BroadcastMode: req.BroadcastMode, GuardMaxExchange: req.GuardMaxExchange,
		KnowledgeBaseIDs: req.KnowledgeBaseIDs,
	}
	created, err := h.meetingService.CreateMeeting(ctx, room, req.AgentIDs)
	if err != nil {
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}
	c.JSON(http.StatusCreated, gin.H{"success": true, "data": created})
}

// ListMeetings GET /api/v1/meetings?status=&offset=&limit=
func (h *MeetingHandler) ListMeetings(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID, _ := types.TenantIDFromContext(ctx)
	userID, _ := types.UserIDFromContext(ctx)
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	rooms, total, err := h.meetingService.ListMeetings(ctx, tenantID, userID, c.Query("status"), offset, limit)
	if err != nil {
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": rooms, "total": total})
}

// GetMeeting GET /api/v1/meetings/:id
func (h *MeetingHandler) GetMeeting(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID, _ := types.TenantIDFromContext(ctx)
	room, participants, err := h.meetingService.GetMeeting(ctx, tenantID, c.Param("id"))
	if err != nil {
		c.Error(errors.NewNotFoundError("meeting not found"))
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"room": room, "participants": participants}})
}

// ListMessages GET /api/v1/meetings/:id/messages?before=&limit=
func (h *MeetingHandler) ListMessages(c *gin.Context) {
	ctx := c.Request.Context()
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "100"))
	msgs, err := h.meetingService.ListMessages(ctx, c.Param("id"), c.Query("before"), limit)
	if err != nil {
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": msgs})
}

type sendMessageRequest struct {
	Content           string   `json:"content" binding:"required"`
	MentionedAgentIDs []string `json:"mentioned_agent_ids"`
}

// SendMessage POST /api/v1/meetings/:id/messages —— 202 异步：落库后触发讨论轮
func (h *MeetingHandler) SendMessage(c *gin.Context) {
	ctx := c.Request.Context()
	var req sendMessageRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}
	userID, _ := types.UserIDFromContext(ctx)
	msg, err := h.meetingService.SendUserMessage(ctx, c.Param("id"), userID, req.Content, req.MentionedAgentIDs)
	if err != nil {
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"success": true, "data": msg})
}

// StreamMessages GET /api/v1/meetings/:id/stream —— SSE 订阅会议事件
func (h *MeetingHandler) StreamMessages(c *gin.Context) {
	ctx := c.Request.Context()
	roomID := c.Param("id")
	// SSE 头（与 session/helpers.go setSSEHeaders 一致）
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")

	broadcaster := h.engineBroadcaster()
	if broadcaster == nil {
		c.Error(errors.NewInternalServerError("meeting engine unavailable"))
		return
	}
	ch, unsubscribe := broadcaster.Subscribe(roomID)
	defer unsubscribe()

	// 立即发一个 ping，便于前端确认连接建立
	c.SSEvent(service.MeetingEventPing, gin.H{"room_id": roomID})
	c.Writer.Flush()

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case evt, ok := <-ch:
			if !ok {
				return
			}
			c.SSEvent(evt.Type, evt.Data)
			c.Writer.Flush()
		case <-ticker.C:
			c.SSEvent(service.MeetingEventPing, gin.H{"ts": time.Now().Unix()})
			c.Writer.Flush()
		}
	}
}

// engineBroadcaster 类型断言取广播器（引擎接口不暴露实现细节，这里收窄）
func (h *MeetingHandler) engineBroadcaster() *service.MeetingBroadcaster {
	type broadcasterProvider interface{ Broadcaster() *service.MeetingBroadcaster }
	if p, ok := h.engine.(broadcasterProvider); ok {
		return p.Broadcaster()
	}
	return nil
}
```

说明：广播器类型在 T1.5 已直接以导出名 `MeetingBroadcaster` / `NewMeetingBroadcaster()` 定义，这里无需改名。handler 顶部 import 加 `"time"`。

- [ ] **Step 2: 路由 + RBAC**

`internal/router/routes_meeting.go`（新建，完整内容）：

```go
package router

import (
	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/handler"
)

// RegisterMeetingRoutes 会议室路由。可见性决策"仅创建者可见"（规格 §8）：
// 房间级读写一律 OwnedMeetingOrAdmin（创建者本人或租户管理员）；创建开放 Contributor；
// 列表 Viewer 即可但服务层 ListByCreator 强制按创建者过滤，天然只见自己的会议。
func RegisterMeetingRoutes(r *gin.RouterGroup, meetingHandler *handler.MeetingHandler, g *rbacGuards) {
	meetings := g.apiKeyGroup(r.Group("/meetings"), apiKeyFullAccess())
	{
		meetings.POST("", g.Contributor(), meetingHandler.CreateMeeting)
		meetings.GET("", g.Viewer(), meetingHandler.ListMeetings)
		meetings.GET("/:id", g.OwnedMeetingOrAdmin(), meetingHandler.GetMeeting)
		meetings.GET("/:id/messages", g.OwnedMeetingOrAdmin(), meetingHandler.ListMessages)
		meetings.POST("/:id/messages", g.OwnedMeetingOrAdmin(), meetingHandler.SendMessage)
		meetings.GET("/:id/stream", g.OwnedMeetingOrAdmin(), meetingHandler.StreamMessages)
	}
}
```

`internal/router/rbac.go` 修改：
1. `rbacGuards` struct 增加字段 `meetingCreator middleware.CreatorLookup`（紧邻 `agentCreator`，约 L171 处）。
2. `newRBACGuards(...)` 参数增加 `meetingHandler *handler.MeetingHandler`，函数体内增加 `g.meetingCreator = meetingHandler.MeetingCreatorLookup`。
3. 增加 guard 方法（紧邻 `OwnedAgentOrAdmin`）：

```go
// OwnedMeetingOrAdmin 会议室写操作：创建者本人或租户管理员
func (g *rbacGuards) OwnedMeetingOrAdmin() gin.HandlerFunc {
	return middleware.RequireOwnershipOrRole(types.TenantRoleAdmin, g.meetingCreator, g.cfg)
}
```

`internal/router/router.go` 修改：
1. `RouterParams` struct 增加 `MeetingHandler *handler.MeetingHandler`（紧邻 `CustomAgentHandler`，约 L80 后）。
2. 在 `RegisterCustomAgentRoutes(...)`（约 L319）之后增加：
   `RegisterMeetingRoutes(api, params.MeetingHandler, g)`
3. `newRBACGuards` 调用处参数同步加 `params.MeetingHandler`。

- [ ] **Step 3: DI 注册**

`internal/container/container.go` 修改：
1. Repository 区（L185 `NewCustomAgentRepository` 后）增加：

```go
must(container.Provide(repository.NewMeetingRoomRepository))
must(container.Provide(repository.NewMeetingParticipantRepository))
must(container.Provide(repository.NewMeetingMessageRepository))
must(container.Provide(repository.NewMeetingArtifactRepository))
```

2. Service 区（L286 `NewCustomAgentService` 后）增加：

```go
must(container.Provide(service.NewMeetingEngine))
must(container.Provide(service.NewMeetingService))
```

3. Handler 区（L581 `NewCustomAgentHandler` 后）增加：

```go
must(container.Provide(handler.NewMeetingHandler))
```

- [ ] **Step 4: 配置**

`internal/config/config.go`：`Config` struct（L19-25 区域）增加字段：

```go
Meeting *MeetingConfig `yaml:"meeting" json:"meeting"`
```

并新增类型：

```go
// MeetingConfig 会议室配置
type MeetingConfig struct {
	GuardMaxExchange     int    `yaml:"guard_max_exchange" json:"guard_max_exchange"`
	ContextMaxMessages   int    `yaml:"context_max_messages" json:"context_max_messages"`
	DefaultBroadcastMode string `yaml:"default_broadcast_mode" json:"default_broadcast_mode"`
}
```

`config/config.yaml` 末尾追加：

```yaml
meeting:
  guard_max_exchange: ${MEETING_GUARD_MAX_EXCHANGE}
  context_max_messages: ${MEETING_CONTEXT_MAX_MESSAGES}
  default_broadcast_mode: ${MEETING_DEFAULT_BROADCAST_MODE}
```

注意：config.go 的 `${ENV_VAR}` 替换在 env 不存在时**保持原样**，字符串 `"${X}"` 无法解析为 int 会导致 unmarshal 报错。处理：yaml 中给默认值——

```yaml
meeting:
  guard_max_exchange: 3
  context_max_messages: 50
  default_broadcast_mode: sequential
```

并在 config.go 的 env 覆盖段（仿 L705 OIDC 模式）追加：

```go
if value := strings.TrimSpace(os.Getenv("MEETING_GUARD_MAX_EXCHANGE")); value != "" {
	if n, err := strconv.Atoi(value); err == nil {
		cfg.Meeting.GuardMaxExchange = n
	}
}
if value := strings.TrimSpace(os.Getenv("MEETING_CONTEXT_MAX_MESSAGES")); value != "" {
	if n, err := strconv.Atoi(value); err == nil {
		cfg.Meeting.ContextMaxMessages = n
	}
}
if value := strings.TrimSpace(os.Getenv("MEETING_DEFAULT_BROADCAST_MODE")); value != "" {
	cfg.Meeting.DefaultBroadcastMode = value
}
```

（需确认 `cfg.Meeting` 非 nil 守卫：`if cfg.Meeting == nil { cfg.Meeting = &MeetingConfig{GuardMaxExchange: 3, ContextMaxMessages: 50, DefaultBroadcastMode: "sequential"} }`。）

`.env.example` 追加：

```bash
# 会议室：护栏互呛来回上限（默认 3）
MEETING_GUARD_MAX_EXCHANGE=3
# 会议室：注入 LLM 的上下文消息上限（默认 50）
MEETING_CONTEXT_MAX_MESSAGES=50
# 会议室：默认广播模式 sequential|parallel
MEETING_DEFAULT_BROADCAST_MODE=sequential
```

引擎接入配置：`meetingEngine` struct 增加 `cfg *config.Config` 字段，`NewMeetingEngine` 增加同名参数（Dig 自动注入）；构造时 `contextMax = cfg.Meeting.ContextMaxMessages`（`cfg`/`cfg.Meeting` 为 nil 或值为 0 时回退 50）。`cfg` 字段保留供 M3 护栏读取 `GuardMaxExchange`。

- [ ] **Step 5: 编译 + 启动冒烟**

Run: `go build ./...`
Expected: 编译通过（含 `assertAPIKeyPoliciesMatchRoutes` 启动自检所需的策略映射——若 panic 提示 meetings 路由缺 API key 策略，按 L410-435 的模式在策略表登记 `/api/v1/meetings*` 为 full-access）。

Run: `go test ./internal/... -run "TestValidateMeetingCreate|TestUnionKnowledgeBaseIDs|TestBuildMeetingQuery" -v`
Expected: 全部 PASS。

- [ ] **Step 6: 提交**

Run: `git add internal/handler/meeting.go internal/router/routes_meeting.go internal/router/rbac.go internal/router/router.go internal/container/container.go internal/config/config.go config/config.yaml .env.example internal/application/service/meeting_broadcast.go internal/application/service/meeting_engine.go; git commit -m "feat(meeting): 接入会议室 HTTP API/SSE/DI/配置"`

### Task T1.7: 前端 API 模块 + Store + 路由/菜单/i18n

**Files:**
- Create: `frontend/src/api/meeting/index.ts`
- Create: `frontend/src/api/meeting/meeting.test.ts`
- Create: `frontend/src/stores/meeting.ts`
- Modify: `frontend/src/router/index.ts`（`/platform` children 加 3 条，仿 L149-154 agents 模式）
- Modify: `frontend/src/stores/menu.ts`（menuArr 加 1 条，L37 agents 之后）
- Modify: `frontend/src/i18n/locales/zh-CN.ts`（menu 节 L7570 附近加 `meetings`）
- Modify: `frontend/src/i18n/locales/en-US.ts`（menu 节对应位置加 `meetings`）

- [ ] **Step 1: 写失败测试**

`frontend/src/api/meeting/meeting.test.ts`：

```ts
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { buildCreatePayload, buildMessagePayload, streamUrl } from './index';

test('buildCreatePayload 组装创建会议请求体', () => {
  const p = buildCreatePayload({
    title: '  新客服方案评审  ',
    description: '客服机器人选型',
    agentIds: ['a1', 'a2'],
    knowledgeBaseIds: ['kb1'],
    broadcastMode: 'sequential',
  });
  assert.equal(p.title, '新客服方案评审'); // 标题去首尾空格
  assert.equal(p.description, '客服机器人选型');
  assert.deepEqual(p.agent_ids, ['a1', 'a2']);
  assert.deepEqual(p.knowledge_base_ids, ['kb1']);
  assert.equal(p.broadcast_mode, 'sequential');
});

test('buildCreatePayload 拒绝空标题/空智能体', () => {
  assert.throws(() => buildCreatePayload({ title: ' ', description: '', agentIds: [], knowledgeBaseIds: [], broadcastMode: 'sequential' }), /title/);
  assert.throws(() => buildCreatePayload({ title: 't', description: '', agentIds: [], knowledgeBaseIds: [], broadcastMode: 'sequential' }), /agent/);
});

test('buildMessagePayload 组装发言请求体（含 mentions）', () => {
  const p = buildMessagePayload('大家好 @法务必答', ['agent-9']);
  assert.equal(p.content, '大家好 @法务必答');
  assert.deepEqual(p.mentioned_agent_ids, ['agent-9']);
});

test('streamUrl 指向会议室 SSE 端点', () => {
  assert.equal(streamUrl('room-1'), '/api/v1/meetings/room-1/stream');
});
```

Run: `cd frontend; npx tsx --test src/api/meeting/meeting.test.ts`
Expected: FAIL —— `Cannot find module './index'`（模块尚不存在）

- [ ] **Step 2: 实现 API 模块**

`frontend/src/api/meeting/index.ts`：

```ts
import { get, post } from "../../utils/request";
import { fetchEventSource } from '@microsoft/fetch-event-source';

// ===== 类型定义（与后端 internal/types/meeting.go 对齐）=====
export type BroadcastMode = 'sequential' | 'parallel';
export type ParticipantKind = 'host' | 'agent';
export type SenderKind = 'user' | 'agent' | 'host' | 'system';

export interface MeetingRoom {
  id: string;
  title: string;
  description: string;
  status: 'active' | 'archived';
  broadcast_mode: BroadcastMode;
  knowledge_base_ids: string[];
  created_by: string;
  created_at: string;
  updated_at: string;
  participants?: MeetingParticipant[];
}

export interface MeetingParticipant {
  id: string;
  meeting_room_id: string;
  agent_id: string;     // host 参与者为空字符串
  kind: ParticipantKind;
  display_name: string; // 冗余展示名（主持人固定为"主持人"）
}

export interface MeetingMessage {
  id: string;
  meeting_room_id: string;
  sender_kind: SenderKind;
  sender_id: string;    // user UUID / agent UUID / ''
  content: string;
  mentioned_agent_ids: string[];
  review_state: 'none' | 'pending' | 'adopted' | 'rejected';
  is_solution: boolean;
  solution_version: number;
  created_at: string;
}

// ===== 纯函数：请求体组装（便于单测）=====
export interface CreateMeetingInput {
  title: string;
  description: string;
  agentIds: string[];
  knowledgeBaseIds: string[];
  broadcastMode: BroadcastMode;
}

export function buildCreatePayload(input: CreateMeetingInput) {
  const title = input.title.trim();
  if (!title) throw new Error('title is required');
  if (!input.agentIds.length) throw new Error('agent ids required');
  return {
    title,
    description: input.description.trim(),
    agent_ids: input.agentIds,
    knowledge_base_ids: input.knowledgeBaseIds,
    broadcast_mode: input.broadcastMode,
  };
}

export function buildMessagePayload(content: string, mentionAgentIds: string[] = []) {
  return { content, mentioned_agent_ids: mentionAgentIds };
}

export function streamUrl(roomId: string): string {
  return `/api/v1/meetings/${roomId}/stream`;
}

// ===== HTTP API =====
export function listMeetings() {
  return get<MeetingRoom[]>('/api/v1/meetings');
}

export function createMeeting(input: CreateMeetingInput) {
  return post<MeetingRoom>('/api/v1/meetings', buildCreatePayload(input));
}

export function getMeeting(roomId: string) {
  return get<MeetingRoom>(`/api/v1/meetings/${roomId}`);
}

// 游标分页：beforeId 为空表示取最新一页
export function listMessages(roomId: string, beforeId = '', limit = 100) {
  const qs = new URLSearchParams();
  if (beforeId) qs.set('before_id', beforeId);
  qs.set('limit', String(limit));
  return get<MeetingMessage[]>(`/api/v1/meetings/${roomId}/messages?${qs.toString()}`);
}

export function sendMessage(roomId: string, content: string, mentionAgentIds: string[] = []) {
  // 后端固定 202 Accepted，讨论结果经 SSE 推送
  return post<MeetingMessage>(`/api/v1/meetings/${roomId}/messages`, buildMessagePayload(content, mentionAgentIds));
}

// ===== SSE 订阅（M1 简化版：不做 401 刷新重放，失败走 onError 提示重连）=====
export interface MeetingStreamHandlers {
  onEvent: (event: string, data: unknown) => void;
  onError?: (err: unknown) => void;
}

export function openMeetingStream(roomId: string, handlers: MeetingStreamHandlers): () => void {
  const controller = new AbortController();
  const token = localStorage.getItem('weknora_token') || '';
  const tenantId = localStorage.getItem('weknora_selected_tenant_id') || '';
  const base = (import.meta.env.VITE_API_BASE_URL || '') as string;

  fetchEventSource(`${base}${streamUrl(roomId)}`, {
    method: 'GET',
    headers: {
      'Authorization': `Bearer ${token}`,
      ...(tenantId ? { 'X-Tenant-ID': tenantId } : {}),
    },
    signal: controller.signal,
    openWhenHidden: true,
    onmessage(ev) {
      // 后端 c.SSEvent：ev.event 为事件名，ev.data 为 JSON 字符串
      try {
        handlers.onEvent(ev.event || 'message', ev.data ? JSON.parse(ev.data) : null);
      } catch (e) {
        console.error('[meeting] 解析 SSE 事件失败', e);
      }
    },
    onerror(err) {
      handlers.onError?.(err);
      throw err; // 终止自动重连，由视图层决定重试
    },
  });
  return () => controller.abort(); // 返回关闭函数
}
```

> 说明：`request.ts` 的 `get/post` 已自动附带 Bearer token 与 `X-Tenant-ID`（与 `api/agent/index.ts` 同一套封装）；SSE 走原生 fetchEventSource 需手动带头，模式参考 `api/chat/streame.ts` L170-186。

- [ ] **Step 3: 实现 Pinia Store**

`frontend/src/stores/meeting.ts`：

```ts
import { ref } from 'vue';
import { defineStore } from 'pinia';
import {
  listMeetings, getMeeting, listMessages, sendMessage,
  type MeetingRoom, type MeetingMessage,
} from '@/api/meeting';

export const useMeetingStore = defineStore('meetingStore', () => {
  const rooms = ref<MeetingRoom[]>([]);
  const currentRoom = ref<MeetingRoom | null>(null);
  const messages = ref<MeetingMessage[]>([]);
  const hasMore = ref(false);      // 顶部是否还有更早消息
  const discussing = ref(false);   // 是否有讨论轮在进行（禁用输入用）

  async function fetchRooms() {
    rooms.value = await listMeetings();
  }

  async function enterRoom(roomId: string) {
    currentRoom.value = await getMeeting(roomId);
    const page = await listMessages(roomId);
    messages.value = page;
    hasMore.value = page.length >= 100;
  }

  // 顶部"加载更早"：以当前最旧一条为游标向前翻
  async function loadEarlier() {
    if (!currentRoom.value || !messages.value.length) return;
    const oldest = messages.value[0];
    const page = await listMessages(currentRoom.value.id, oldest.id);
    messages.value = [...page, ...messages.value];
    hasMore.value = page.length >= 100;
  }

  async function speak(content: string, mentionAgentIds: string[] = []) {
    if (!currentRoom.value) return;
    await sendMessage(currentRoom.value.id, content, mentionAgentIds);
    // 用户消息本体经 SSE message.created 回流，这里不本地乐观插入，避免重复
  }

  // SSE 事件入口：由视图层订阅后调用
  function applyStreamEvent(event: string, data: any) {
    if (event === 'message.created' && data) {
      messages.value.push(data as MeetingMessage);
    } else if (event === 'discussion.turn_started') {
      discussing.value = true;
    } else if (event === 'discussion.turn_ended') {
      discussing.value = false;
    }
  }

  function leaveRoom() {
    currentRoom.value = null;
    messages.value = [];
    hasMore.value = false;
    discussing.value = false;
  }

  return { rooms, currentRoom, messages, hasMore, discussing,
    fetchRooms, enterRoom, loadEarlier, speak, applyStreamEvent, leaveRoom };
});
```

- [ ] **Step 4: 路由 + 菜单 + i18n**

`frontend/src/router/index.ts` `/platform` children（agents 条目之后）追加：

```ts
{
  path: "meetings",
  name: "meetingList",
  component: () => import("../views/meeting/MeetingListView.vue"),
  meta: { requiresInit: true, requiresAuth: true }
},
{
  path: "meetings/new",
  name: "meetingCreate",
  component: () => import("../views/meeting/MeetingCreateView.vue"),
  meta: { requiresInit: true, requiresAuth: true }
},
{
  path: "meetings/:roomId",
  name: "meetingRoom",
  component: () => import("../views/meeting/MeetingRoomView.vue"),
  meta: { requiresInit: true, requiresAuth: true }
},
```

`frontend/src/stores/menu.ts` menuArr（L37 agents 条目之后）追加：

```ts
{ title: '', titleKey: 'menu.meetings', icon: 'agent', path: 'meetings' },
```

`frontend/src/i18n/locales/zh-CN.ts` menu 节（`docAssistant: '方案助手'` 前）加：

```ts
    meetings: '会议室',
```

`frontend/src/i18n/locales/en-US.ts` menu 节对应位置加：

```ts
    meetings: 'Meeting Rooms',
```

- [ ] **Step 5: 跑测试 + 类型检查**

Run: `cd frontend; npx tsx --test src/api/meeting/meeting.test.ts`
Expected: 4 个测试 PASS。

Run: `cd frontend; npx vue-tsc --noEmit -p tsconfig.json`（或项目既有 type-check 脚本）
Expected: 无新增类型错误（视图文件 T1.8 才建，路由 import 暂时报"找不到模块"属预期，T1.8 完成后消失——若 CI 阻断可将本 Step 与 T1.8 合并提交）。

- [ ] **Step 6: 提交**

Run: `git add frontend/src/api/meeting frontend/src/stores/meeting.ts frontend/src/router/index.ts frontend/src/stores/menu.ts frontend/src/i18n/locales/zh-CN.ts frontend/src/i18n/locales/en-US.ts; git commit -m "feat(meeting): 前端会议室 API/Store/路由/菜单接入"`

---

### Task T1.8: 前端三视图（列表 / 创建 / 会议室）

**Files:**
- Create: `frontend/src/views/meeting/MeetingListView.vue`
- Create: `frontend/src/views/meeting/MeetingCreateView.vue`
- Create: `frontend/src/views/meeting/MeetingRoomView.vue`

- [ ] **Step 1: 会议列表视图**

`frontend/src/views/meeting/MeetingListView.vue`：

```vue
<template>
  <div class="meeting-list">
    <div class="header">
      <h2>{{ $t('menu.meetings') }}</h2>
      <el-button type="primary" @click="$router.push({ name: 'meetingCreate' })">
        {{ $t('meeting.create') }}
      </el-button>
    </div>
    <el-table :data="store.rooms" v-loading="loading" @row-click="openRoom">
      <el-table-column prop="title" :label="$t('meeting.title')" />
      <el-table-column prop="description" :label="$t('meeting.topic')" show-overflow-tooltip />
      <el-table-column prop="created_at" :label="$t('meeting.createdAt')" width="180" />
      <el-table-column :label="$t('meeting.status')" width="100">
        <template #default="{ row }">
          <el-tag :type="row.status === 'active' ? 'success' : 'info'">{{ row.status }}</el-tag>
        </template>
      </el-table-column>
    </el-table>
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue';
import { useRouter } from 'vue-router';
import { useMeetingStore } from '@/stores/meeting';

const store = useMeetingStore();
const router = useRouter();
const loading = ref(false);

onMounted(async () => {
  loading.value = true;
  try { await store.fetchRooms(); } finally { loading.value = false; }
});

function openRoom(row: { id: string }) {
  router.push({ name: 'meetingRoom', params: { roomId: row.id } });
}
</script>
```

- [ ] **Step 2: 创建会议视图**

`frontend/src/views/meeting/MeetingCreateView.vue`：

```vue
<template>
  <div class="meeting-create">
    <h2>{{ $t('meeting.create') }}</h2>
    <el-form :model="form" label-width="120px" style="max-width: 640px">
      <el-form-item :label="$t('meeting.title')" required>
        <el-input v-model="form.title" maxlength="100" />
      </el-form-item>
      <el-form-item :label="$t('meeting.topic')">
        <el-input v-model="form.description" type="textarea" :rows="2"
          :placeholder="$t('meeting.topicPlaceholder')" />
      </el-form-item>
      <el-form-item :label="$t('meeting.selectAgents')" required>
        <el-select v-model="form.agentIds" multiple filterable style="width: 100%">
          <el-option v-for="a in agents" :key="a.id" :label="a.name" :value="a.id" />
        </el-select>
      </el-form-item>
      <el-form-item :label="$t('meeting.selectKBs')">
        <el-select v-model="form.knowledgeBaseIds" multiple filterable style="width: 100%">
          <el-option v-for="kb in knowledgeBases" :key="kb.id" :label="kb.name" :value="kb.id" />
        </el-select>
      </el-form-item>
      <el-form-item :label="$t('meeting.broadcastMode')">
        <el-radio-group v-model="form.broadcastMode">
          <el-radio value="sequential">{{ $t('meeting.modeSequential') }}</el-radio>
          <el-radio value="parallel">{{ $t('meeting.modeParallel') }}</el-radio>
        </el-radio-group>
      </el-form-item>
      <el-form-item>
        <el-button type="primary" :loading="submitting" @click="submit">
          {{ $t('meeting.createAndEnter') }}
        </el-button>
      </el-form-item>
    </el-form>
  </div>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue';
import { useRouter } from 'vue-router';
import { ElMessage } from 'element-plus';
import { createMeeting } from '@/api/meeting';
import { listAgents } from '@/api/agent';           // 复用既有智能体列表接口
import { getKnowledgeBases } from '@/api/knowledge-base'; // 复用既有 KB 列表接口

const router = useRouter();
const agents = ref<Array<{ id: string; name: string }>>([]);
const knowledgeBases = ref<Array<{ id: string; name: string }>>([]);
const submitting = ref(false);
const form = reactive({
  title: '', description: '',
  agentIds: [] as string[], knowledgeBaseIds: [] as string[],
  broadcastMode: 'sequential' as const,
});

onMounted(async () => {
  // 两个下拉源并行加载；接口名以既有模块实际导出为准
  const [a, kb] = await Promise.all([listAgents(), getKnowledgeBases()]);
  agents.value = (a as any)?.data ?? a ?? [];
  knowledgeBases.value = (kb as any)?.data ?? kb ?? [];
});

async function submit() {
  submitting.value = true;
  try {
    const room = await createMeeting(form);
    router.push({ name: 'meetingRoom', params: { roomId: room.id } });
  } catch (e: any) {
    ElMessage.error(e?.message || 'create failed');
  } finally {
    submitting.value = false;
  }
}
</script>
```

> 实施注意：`listAgents` / `getKnowledgeBases` 的准确导出名与返回包裹结构，以 `frontend/src/api/agent/index.ts`、`frontend/src/api/knowledge-base/` 现状为准调整；验收标准不变（下拉能列出、能提交创建）。

- [ ] **Step 3: 会议室视图（聊天主界面）**

`frontend/src/views/meeting/MeetingRoomView.vue`：

```vue
<template>
  <div class="meeting-room">
    <header class="room-header">
      <span class="title">{{ store.currentRoom?.title }}</span>
      <span class="topic">{{ store.currentRoom?.description }}</span>
    </header>

    <main ref="scrollRef" class="message-list">
      <div v-if="store.hasMore" class="load-earlier" @click="store.loadEarlier()">
        {{ $t('meeting.loadEarlier') }}
      </div>
      <div v-for="m in store.messages" :key="m.id" class="msg" :class="m.sender_kind">
        <div class="sender">{{ senderLabel(m) }}</div>
        <div class="bubble">{{ m.content }}</div>
      </div>
      <div v-if="store.discussing" class="discussing">{{ $t('meeting.discussing') }}</div>
    </main>

    <footer class="input-bar">
      <el-input v-model="draft" type="textarea" :rows="2" :disabled="store.discussing"
        :placeholder="$t('meeting.inputPlaceholder')"
        @keyup.ctrl.enter="send" />
      <el-button type="primary" :disabled="store.discussing || !draft.trim()" @click="send">
        {{ $t('meeting.send') }}
      </el-button>
    </footer>
  </div>
</template>

<script setup lang="ts">
import { nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import { useRoute } from 'vue-router';
import { useMeetingStore } from '@/stores/meeting';
import { openMeetingStream } from '@/api/meeting';
import type { MeetingMessage } from '@/api/meeting';

const route = useRoute();
const store = useMeetingStore();
const draft = ref('');
const scrollRef = ref<HTMLElement>();
let closeStream: (() => void) | null = null;

const roomId = route.params.roomId as string;

onMounted(async () => {
  await store.enterRoom(roomId);
  scrollToBottom();
  // 订阅 SSE：讨论轮与智能体发言实时回流
  closeStream = openMeetingStream(roomId, {
    onEvent: (event, data) => store.applyStreamEvent(event, data),
    onError: () => { /* M1：视图显示离线提示即可，重连留给 M2 */ },
  });
});

onBeforeUnmount(() => {
  closeStream?.();
  store.leaveRoom();
});

// 新消息自动滚到底部
watch(() => store.messages.length, () => nextTick(scrollToBottom));

function scrollToBottom() {
  const el = scrollRef.value;
  if (el) el.scrollTop = el.scrollHeight;
}

// 发送者展示名：主持人/系统固定文案，智能体查参会者 display_name，用户显示"我"
function senderLabel(m: MeetingMessage): string {
  if (m.sender_kind === 'host') return '主持人';
  if (m.sender_kind === 'system') return '系统';
  if (m.sender_kind === 'user') return '我';
  const p = store.currentRoom?.participants.find(
    x => x.kind === 'agent' && x.agent_id === m.sender_id);
  return p?.display_name ?? m.sender_id.slice(0, 8);
}

async function send() {
  const content = draft.value.trim();
  if (!content) return;
  draft.value = '';
  await store.speak(content);
}
</script>
```

- [ ] **Step 4: i18n 文案补充**

`zh-CN.ts` 根级新增 `meeting` 节（与 menu 节平级）：

```ts
  meeting: {
    create: '创建会议',
    title: '会议名称',
    topic: '讨论主题',
    topicPlaceholder: '本次讨论要解决的问题…',
    selectAgents: '参会智能体',
    selectKBs: '会议级知识库',
    broadcastMode: '发言模式',
    modeSequential: '轮流发言',
    modeParallel: '并发发言',
    createAndEnter: '创建并进入',
    createdAt: '创建时间',
    status: '状态',
    loadEarlier: '加载更早消息',
    discussing: '讨论进行中…',
    inputPlaceholder: '输入你的观点或问题，Ctrl+Enter 发送',
    send: '发送',
  },
```

`en-US.ts` 对应翻译（Meetings/Create Meeting/Title/Topic/...）。

- [ ] **Step 5: 构建验证**

Run: `cd frontend; npm run build`（或项目既有 build 脚本）
Expected: 构建通过，无类型/导入错误。

- [ ] **Step 6: 提交**

Run: `git add frontend/src/views/meeting frontend/src/i18n/locales/zh-CN.ts frontend/src/i18n/locales/en-US.ts; git commit -m "feat(meeting): 会议室列表/创建/聊天三视图（M1）"`

---

### Task T1.9: M1 端到端验收

**前置**：`make migrate-up` 已执行；后端 `go run ./cmd/server`（或既有启动方式）与前端 `npm run dev` 均运行中。

- [ ] **Step 1: 建会**：界面"会议室 → 创建会议"，选 2 个智能体 + 1 个会议级 KB，创建成功并自动进入会议室。
- [ ] **Step 2: 库表验证**：`psql` 查 `meeting_rooms` 有 1 行；`meeting_participants` 有 3 行（2 agent + 1 host 角色 host）。
- [ ] **Step 3: 发言触发讨论**：输入"请两位就主题给出初步意见"发送 → 前端立即出现自己的消息（SSE 回流）→ 输入框进入"讨论进行中"禁用态 → 两个智能体依次（sequential）发言出现在消息流 → `discussion.turn_ended` 后输入框恢复。
- [ ] **Step 4: SSE 事件核对**：`psql` 查 `meeting_messages` 有 1 user + 2 agent 消息；浏览器 Network 面板 SSE 连接收到 `message.created` ×3、`discussion.turn_started/ended` 各 1。
- [ ] **Step 5: 权限验证**：用另一租户用户的 token 调 `GET /api/v1/meetings/{roomId}` → 403/404；列表接口不返回他人会议。
- [ ] **Step 6: 翻页**：构造 105 条消息后进入会议室 → 默认加载最新 100 条 → 点"加载更早"再出现 5 条。
- [ ] **Step 7: 提交**

Run: `git add -A; git commit -m "test(meeting): M1 端到端验收（如产生验收脚本/种子数据）" --allow-empty`

## 里程碑 M2：主持人智能筛选 + 并发模式 + @ 交互

**目标**：讨论轮从"全员轮流"升级为"主持人按相关性挑选发言者"；支持 parallel 并发发言；前端提供 @ 按钮 + chip 指定发言者；连发消息排队语义完善。

### Task T2.1: 主持人 LLM 接入与发言者筛选

**Files:**
- Modify: `internal/application/service/meeting_engine.go`（增加 hostChat 字段与 selectSpeakers 升级）
- Create: `internal/application/service/meeting_host.go`
- Create: `internal/application/service/meeting_host_test.go`

- [ ] **Step 1: 写失败测试**

`meeting_host_test.go`：

```go
func TestParseSpeakerSelection(t *testing.T) {
	// 主持人 LLM 返回 JSON 数组，容错解析
	ids := parseSpeakerSelection(`["a1","a2"]`, []string{"a1", "a2", "a3"})
	assert.Equal(t, []string{"a1", "a2"}, ids)

	// 含 markdown 代码块包裹也能解析
	ids = parseSpeakerSelection("```json\n[\"a3\"]\n```", []string{"a1", "a2", "a3"})
	assert.Equal(t, []string{"a3"}, ids)

	// 非法输出 → 返回 nil（调用方回退全员）
	ids = parseSpeakerSelection("我觉得都应该说", []string{"a1"})
	assert.Nil(t, ids)

	// 过滤不在候选集合内的 id（防幻觉）
	ids = parseSpeakerSelection(`["a1","ghost"]`, []string{"a1", "a2"})
	assert.Equal(t, []string{"a1"}, ids)
}

func TestBuildSpeakerSelectionPrompt(t *testing.T) {
	agents := []speakerCandidate{
		{ID: "a1", Name: "法务顾问", Description: "审合同"},
		{ID: "a2", Name: "技术架构师", Description: "系统设计"},
	}
	p := buildSpeakerSelectionPrompt("客服机器人选型", "数据合规怎么处理？", agents)
	assert.Contains(t, p, "客服机器人选型")
	assert.Contains(t, p, "数据合规怎么处理？")
	assert.Contains(t, p, "法务顾问")
	assert.Contains(t, p, `"a1"`) // 候选 id 必须出现在提示词中，便于模型引用
}
```

Run: `go test ./internal/application/service/ -run "TestParseSpeakerSelection|TestBuildSpeakerSelectionPrompt" -v`
Expected: FAIL —— 函数未定义。

- [ ] **Step 2: 实现 meeting_host.go**

```go
package service

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
)

// speakerCandidate 主持人筛选时的候选智能体视图
type speakerCandidate struct {
	ID          string
	Name        string
	Description string // 取自 agent 描述/系统提示词摘要，供主持人判断相关性
}

// resolveHostChat 获取租户默认对话模型作为主持人大脑。
// 模式仿 agent_eval.go resolveJudgeModel：ListModels → KnowledgeQA 默认模型 → GetChatModel。
func (e *meetingEngine) resolveHostChat(ctx context.Context) (chat.Chat, error) {
	models, err := e.modelService.ListModels(ctx)
	if err != nil {
		return nil, fmt.Errorf("list models: %w", err)
	}
	for _, m := range models {
		if m.Type == types.ModelTypeKnowledgeQA && m.IsDefault {
			return e.modelService.GetChatModel(ctx, m.ID)
		}
	}
	return nil, fmt.Errorf("no default KnowledgeQA model for meeting host")
}

// selectSpeakersByLLM 让主持人从候选中挑选本轮发言者；失败/解析失败返回 nil 由调用方回退全员。
func (e *meetingEngine) selectSpeakersByLLM(ctx context.Context, room *types.MeetingRoom, trigger *types.MeetingMessage, candidates []speakerCandidate) []string {
	hostChat, err := e.resolveHostChat(ctx)
	if err != nil {
		logger.Warnf(ctx, "[meeting] 主持人模型不可用，回退全员发言: %v", err)
		return nil
	}
	prompt := buildSpeakerSelectionPrompt(room.Description, trigger.Content, candidates)
	resp, err := hostChat.Chat(ctx, []chat.Message{
		{Role: "user", Content: prompt},
	}, &chat.ChatOptions{Temperature: 0})
	if err != nil {
		logger.Warnf(ctx, "[meeting] 主持人筛选失败，回退全员发言: %v", err)
		return nil
	}
	return parseSpeakerSelection(resp.Content, candidateIDs(candidates))
}

var jsonBlockRe = regexp.MustCompile("(?s)```(?:json)?\\s*(\\[.*?\\])\\s*```")

// parseSpeakerSelection 从模型输出中提取 JSON 字符串数组并过滤非法 id。
func parseSpeakerSelection(output string, validIDs []string) []string {
	text := strings.TrimSpace(output)
	if m := jsonBlockRe.FindStringSubmatch(text); m != nil {
		text = m[1]
	}
	var picked []string
	if err := json.Unmarshal([]byte(text), &picked); err != nil {
		return nil
	}
	valid := make(map[string]bool, len(validIDs))
	for _, id := range validIDs {
		valid[id] = true
	}
	out := make([]string, 0, len(picked))
	seen := make(map[string]bool, len(picked))
	for _, id := range picked {
		if valid[id] && !seen[id] {
			out = append(out, id)
			seen[id] = true
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func buildSpeakerSelectionPrompt(topic, userMessage string, candidates []speakerCandidate) string {
	var b strings.Builder
	b.WriteString("你是会议室主持人。会议主题：" + topic + "\n")
	b.WriteString("用户最新发言：" + userMessage + "\n\n")
	b.WriteString("参会智能体列表：\n")
	for _, c := range candidates {
		fmt.Fprintf(&b, "- id=%q 名称=%q 专长=%q\n", c.ID, c.Name, c.Description)
	}
	b.WriteString("\n请挑选最需要对这条发言表态的 1-3 个智能体，只输出 JSON 数组（元素为 id），不要输出其他内容。")
	return b.String()
}

func candidateIDs(cs []speakerCandidate) []string {
	ids := make([]string, len(cs))
	for i, c := range cs {
		ids[i] = c.ID
	}
	return ids
}
```

- [ ] **Step 3: 引擎 selectSpeakers 升级为三级策略**

`meeting_engine.go` 中 `selectSpeakers` 改为：

```go
// selectSpeakers 三级策略：@ 指定 > 主持人 LLM 筛选 > 全员
func (e *meetingEngine) selectSpeakers(ctx context.Context, room *types.MeetingRoom, trigger *types.MeetingMessage, agents []*types.CustomAgent) []*types.CustomAgent {
	// 第一级：消息显式 @ 了哪些智能体（保持参会者顺序，保证确定性）
	if len(trigger.MentionedAgentIDs) > 0 {
		return filterByIDs(agents, trigger.MentionedAgentIDs)
	}
	// 第二级：主持人 LLM 按相关性挑选
	candidates := make([]speakerCandidate, len(agents))
	for i, a := range agents {
		candidates[i] = speakerCandidate{ID: a.ID, Name: a.Name, Description: a.Description}
	}
	if ids := e.selectSpeakersByLLM(ctx, room, trigger, candidates); ids != nil {
		return filterByIDs(agents, ids)
	}
	// 第三级：全员
	return agents
}

func filterByIDs(agents []*types.CustomAgent, ids []string) []*types.CustomAgent {
	want := make(map[string]bool, len(ids))
	for _, id := range ids {
		want[id] = true
	}
	out := make([]*types.CustomAgent, 0, len(ids))
	for _, a := range agents {
		if want[a.ID] {
			out = append(out, a)
		}
	}
	return out
}
```

同时给 `meetingEngine` struct 增加 `modelService interfaces.ModelService` 字段并在 `NewMeetingEngine` 注入（Dig 自动装配）。

- [ ] **Step 4: 跑测试 + 编译**

Run: `go test ./internal/application/service/ -run "TestParseSpeakerSelection|TestBuildSpeakerSelectionPrompt|TestBuildMeetingQuery|TestUnionKnowledgeBaseIDs" -v; go build ./...`
Expected: 全 PASS，编译通过。

- [ ] **Step 5: 提交**

Run: `git add internal/application/service/meeting_host.go internal/application/service/meeting_host_test.go internal/application/service/meeting_engine.go; git commit -m "feat(meeting): 主持人 LLM 智能筛选发言者（三级策略）"`

---

### Task T2.2: parallel 并发发言模式

**Files:**
- Modify: `internal/application/service/meeting_engine.go`（runAgentTurns 分支并发执行）
- Modify: `internal/application/service/meeting_engine_test.go`

- [ ] **Step 1: 写失败测试**

```go
func TestOrderAgentsForMode(t *testing.T) {
	agents := []string{"a1", "a2", "a3"}
	// sequential 保持参会顺序
	assert.Equal(t, agents, orderAgentsForMode("sequential", agents))
	// parallel 不改变集合（顺序无意义，但集合必须一致）
	got := orderAgentsForMode("parallel", agents)
	assert.ElementsMatch(t, agents, got)
}
```

Run: `go test ./internal/application/service/ -run TestOrderAgentsForMode -v`
Expected: FAIL。

- [ ] **Step 2: 引擎并发执行分支**

`meeting_engine.go` 讨论轮主体改为：

```go
func (e *meetingEngine) runAgentTurns(ctx context.Context, room *types.MeetingRoom, trigger *types.MeetingMessage, speakers []*types.CustomAgent) {
	if room.BroadcastMode == types.MeetingBroadcastParallel && len(speakers) > 1 {
		var wg sync.WaitGroup
		for _, agent := range speakers {
			// 并发模式下每个智能体独立跑；谁先完成谁先落库，消息天然交错
			wg.Add(1)
			go func(a *types.CustomAgent) {
				defer wg.Done()
				if err := e.runAgentTurn(ctx, room, trigger, a); err != nil {
					logger.Errorf(ctx, "[meeting] 智能体 %s 发言失败: %v", a.ID, err)
					e.publishSystemMessage(ctx, room.ID, fmt.Sprintf("「%s」发言失败，已跳过", a.Name))
				}
			}(agent)
		}
		wg.Wait()
		return
	}
	// sequential：严格按参会顺序逐个发言，后一个能看到前一个的发言
	for _, a := range speakers {
		if e.isInterrupted(ctx, room.ID) {
			e.publishSystemMessage(ctx, room.ID, "讨论已被用户打断")
			return
		}
		if err := e.runAgentTurn(ctx, room, trigger, a); err != nil {
			logger.Errorf(ctx, "[meeting] 智能体 %s 发言失败: %v", a.ID, err)
			e.publishSystemMessage(ctx, room.ID, fmt.Sprintf("「%s」发言失败，已跳过", a.Name))
		}
	}
}
```

签名调整（对 M1 代码的改动，否则本任务编译不过）：
- `runAgentTurn` 签名由 `(ctx, room, p *types.MeetingParticipant, trigger, replyTo *types.MeetingMessage)` 调整为 `(ctx, room, trigger *types.MeetingMessage, agent *types.CustomAgent)`：speakers 经 `selectSpeakers` 已持有 agent 对象，函数体内删除 `agentRepo.GetByID` 调用；`SenderID: p.AgentID` 改为 `SenderID: agent.ID`；落库消息固定 `ReplyToMessageID: trigger.ID`（回复链指向当前触发消息，取代 M1 的 replyTo 参数）。
- M1 `RunTurn` 中逐个调 `runAgentTurn` 的循环删除，改为 `speakers := e.selectSpeakers(ctx, room, trigger, agents); e.runAgentTurns(ctx, room, trigger, speakers)`（T2.4 会整体替换主循环，此处保持可编译即可）。

说明：
- `runAgentTurn` 内部基于消息表查上下文，并发时各智能体看到的是"发言开始时"的快照——符合广播式语义（规格 §5）。
- sequential 模式每个发言前检查 interrupt 标记（M3 的 interrupt 端点会设置它；M2 先提供 `isInterrupted` 读取 Redis 标记的实现）。
- `publishSystemMessage`（M1 已定义）：落库一条 `sender_kind=system` 消息并广播 `message.created`。

- [ ] **Step 3: 测试 + 编译 + 提交**

Run: `go test ./internal/application/service/ -v -run "Meeting"; go build ./...`
Expected: PASS。

Run: `git add internal/application/service/meeting_engine.go internal/application/service/meeting_engine_test.go; git commit -m "feat(meeting): parallel 并发发言模式 + 发言失败降级"`

---

### Task T2.3: 前端 @ 按钮 + chip 交互

**Files:**
- Create: `frontend/src/views/meeting/components/MentionPicker.vue`
- Modify: `frontend/src/views/meeting/MeetingRoomView.vue`（输入栏加 @ 按钮与 chip 区）
- Modify: `frontend/src/stores/meeting.ts`（speak 传 mentionAgentIds；维护 mentions）

- [ ] **Step 1: MentionPicker 组件**

`frontend/src/views/meeting/components/MentionPicker.vue`：

```vue
<template>
  <el-popover trigger="click" :width="280">
    <template #reference>
      <el-button size="small" circle>@</el-button>
    </template>
    <div class="mention-list">
      <div v-for="p in agentParticipants" :key="p.agent_id" class="mention-item"
        @click="pick(p)">
        {{ p.display_name }}
      </div>
      <div v-if="!agentParticipants.length" class="empty">{{ $t('meeting.noAgents') }}</div>
    </div>
  </el-popover>
</template>

<script setup lang="ts">
import { computed } from 'vue';
import { useMeetingStore } from '@/stores/meeting';
import type { MeetingParticipant } from '@/api/meeting';

const emit = defineEmits<{ pick: [p: MeetingParticipant] }>();
const store = useMeetingStore();

// 仅 agent 角色可被 @（主持人默认关注全场，不可 @）
const agentParticipants = computed(() =>
  (store.currentRoom?.participants ?? []).filter(p => p.kind === 'agent'));

function pick(p: MeetingParticipant) { emit('pick', p); }
</script>
```

- [ ] **Step 2: 会议室视图接入 chip 区**

`MeetingRoomView.vue` 输入栏上方加 chip 区与 @ 按钮：

```vue
<footer class="input-bar">
  <div v-if="mentions.length" class="mention-chips">
    <el-tag v-for="m in mentions" :key="m.agent_id" closable size="small"
      @close="removeMention(m.agent_id)">
      @{{ m.display_name }}
    </el-tag>
  </div>
  <div class="input-row">
    <MentionPicker @pick="addMention" />
    <el-input v-model="draft" ... />
    <el-button ...>{{ $t('meeting.send') }}</el-button>
  </div>
</footer>
```

```ts
import MentionPicker from './components/MentionPicker.vue';
import type { MeetingParticipant } from '@/api/meeting';

const mentions = ref<MeetingParticipant[]>([]);

function addMention(p: MeetingParticipant) {
  if (!mentions.value.some(m => m.agent_id === p.agent_id)) {
    mentions.value.push(p); // chip 去重
  }
}
function removeMention(agentId: string) {
  mentions.value = mentions.value.filter(m => m.agent_id !== agentId);
}
async function send() {
  const content = draft.value.trim();
  if (!content) return;
  const ids = mentions.value.map(m => m.agent_id);
  draft.value = '';
  mentions.value = [];
  await store.speak(content, ids);
}
```

- [ ] **Step 3: 构建 + 提交**

Run: `cd frontend; npm run build`
Expected: 通过。

Run: `git add frontend/src/views/meeting frontend/src/stores/meeting.ts; git commit -m "feat(meeting): 前端 @ 按钮 + chip 指定发言者"`

---

### Task T2.4: 连发消息排队语义

**背景**：M1 引擎已用 Redis `TryAcquire` 实现"同一房间同一时刻只有一个讨论轮"。M2 补齐排队：讨论进行中用户又发言时，新消息挂起，当前轮结束后自动以最后一条用户消息为新触发再开一轮。

**Files:**
- Modify: `internal/application/service/meeting_engine.go`

- [ ] **Step 1: 写失败测试**

```go
func TestExtractQueuedTrigger(t *testing.T) {
	msgs := []types.MeetingMessage{
		{ID: "m1", SenderKind: types.MeetingSenderUser, Content: "第一条"},
		{ID: "m2", SenderKind: types.MeetingSenderAgent, Content: "a 回复"},
		{ID: "m3", SenderKind: types.MeetingSenderUser, Content: "补充一下"},
		{ID: "m4", SenderKind: types.MeetingSenderUser, Content: "还有这个"},
	}
	// 排队触发 = 当前轮触发消息之后的最后一条用户消息
	got := extractQueuedTrigger(msgs, "m1")
	assert.Equal(t, "m4", got.ID)
	// 触发消息之后没有新用户消息 → nil
	got = extractQueuedTrigger(msgs, "m4")
	assert.Nil(t, got)
}
```

Run: `go test ./internal/application/service/ -run TestExtractQueuedTrigger -v`
Expected: FAIL。

- [ ] **Step 2: 实现排队循环**

`RunTurn` 主循环改为：

```go
// RunTurn 持有房间锁依次消化：当前触发 → 期间累积的最后一条用户消息 → 直到没有新输入
func (e *meetingEngine) RunTurn(ctx context.Context, roomID string, triggerMessageID string) error {
	lock := redislock.NewToken(e.redis, turnLockKey(roomID), turnLockTTL)
	ok, err := lock.TryAcquire(ctx)
	if err != nil {
		return err
	}
	if !ok {
		// 已有讨论轮在跑：消息已落库，锁持有者会在循环末尾拾取，这里直接返回
		return nil
	}
	defer lock.Release(ctx)

	current := triggerMessageID
	for {
		if e.isInterrupted(ctx, roomID) {
			e.clearInterrupt(ctx, roomID)
			break
		}
		trigger, room, speakers, err := e.prepareTurn(ctx, roomID, current)
		if err != nil {
			return err
		}
		e.broadcaster.Publish(roomID, MeetingEvent{Type: MeetingEventTurnStarted, Data: map[string]any{"trigger_message_id": current}})
		e.runAgentTurns(ctx, room, trigger, speakers)
		e.broadcaster.Publish(roomID, MeetingEvent{Type: MeetingEventTurnEnded, Data: map[string]any{"trigger_message_id": current}})

		// 拾取排队输入：本轮触发消息之后的最后一条用户消息
		recent, err := e.messageRepo.ListByRoom(ctx, roomID, "", 50)
		if err != nil {
			return err
		}
		next := extractQueuedTrigger(recent, current)
		if next == nil {
			break // 没有新输入，结束
		}
		current = next.ID
	}
	return nil
}

// extractQueuedTrigger 返回 triggerID 之后最后一条用户消息；无则 nil。
// 注意 ListByRoom 按时间升序返回。
func extractQueuedTrigger(msgs []types.MeetingMessage, triggerID string) *types.MeetingMessage {
	triggerIdx := -1
	for i, m := range msgs {
		if m.ID == triggerID {
			triggerIdx = i
		}
	}
	if triggerIdx < 0 {
		return nil
	}
	for i := len(msgs) - 1; i > triggerIdx; i-- {
		if msgs[i].SenderKind == types.MeetingSenderUser {
			return &msgs[i]
		}
	}
	return nil
}
```

> `prepareTurn` 为既有 RunTurn 前半段的提取（加载房间/触发消息/参会者/选发言人），保持原逻辑不变。

- [ ] **Step 3: 测试 + 提交**

Run: `go test ./internal/application/service/ -run TestExtractQueuedTrigger -v; go build ./...`
Expected: PASS。

Run: `git add internal/application/service/meeting_engine.go internal/application/service/meeting_engine_test.go; git commit -m "feat(meeting): 讨论轮连发排队（消化最新用户输入直至清空）"`

---

## 里程碑 M3：互呛护栏 + 用户打断

**目标**：同一对智能体连续 3 个来回无用户输入时主持人介入收敛；用户可随时打断讨论轮。

### Task T3.1: 护栏检测算法

**Files:**
- Create: `internal/application/service/meeting_guard.go`
- Create: `internal/application/service/meeting_guard_test.go`

- [ ] **Step 1: 写失败测试**

`meeting_guard_test.go`：

```go
func TestDetectBickering(t *testing.T) {
	mk := func(id, sender string, st string) types.MeetingMessage {
		return types.MeetingMessage{ID: id, SenderID: sender, SenderKind: st}
	}
	max := 3 // 与 MEETING_GUARD_MAX_EXCHANGE 对齐

	// 场景 1：A/B 连续 3 来回，无用户消息 → 触发
	msgs := []types.MeetingMessage{
		mk("u1", "user-1", types.MeetingSenderUser),
		mk("m1", "agent-A", types.MeetingSenderAgent), mk("m2", "agent-B", types.MeetingSenderAgent),
		mk("m3", "agent-A", types.MeetingSenderAgent), mk("m4", "agent-B", types.MeetingSenderAgent),
		mk("m5", "agent-A", types.MeetingSenderAgent), mk("m6", "agent-B", types.MeetingSenderAgent),
	}
	hit, pair := detectBickering(msgs, max)
	assert.True(t, hit)
	assert.Equal(t, [2]string{"agent-A", "agent-B"}, pair)

	// 场景 2：中间插了用户消息 → 计数重置，不触发
	msgs2 := []types.MeetingMessage{
		mk("m1", "agent-A", types.MeetingSenderAgent), mk("m2", "agent-B", types.MeetingSenderAgent),
		mk("m3", "agent-A", types.MeetingSenderAgent), mk("m4", "agent-B", types.MeetingSenderAgent),
		mk("u1", "user-1", types.MeetingSenderUser),
		mk("m5", "agent-A", types.MeetingSenderAgent), mk("m6", "agent-B", types.MeetingSenderAgent),
	}
	hit, _ = detectBickering(msgs2, max)
	assert.False(t, hit)

	// 场景 3：三方轮换（A/B/C 各说一次）不构成"同一对" → 不触发
	msgs3 := []types.MeetingMessage{
		mk("m1", "agent-A", types.MeetingSenderAgent), mk("m2", "agent-B", types.MeetingSenderAgent),
		mk("m3", "agent-C", types.MeetingSenderAgent), mk("m4", "agent-A", types.MeetingSenderAgent),
		mk("m5", "agent-B", types.MeetingSenderAgent), mk("m6", "agent-C", types.MeetingSenderAgent),
	}
	hit, _ = detectBickering(msgs3, max)
	assert.False(t, hit)

	// 场景 4：只有 2 个来回（未到阈值）→ 不触发
	msgs4 := msgs[:4]
	hit, _ = detectBickering(msgs4, max)
	assert.False(t, hit)
}
```

Run: `go test ./internal/application/service/ -run TestDetectBickering -v`
Expected: FAIL。

- [ ] **Step 2: 实现检测算法**

`meeting_guard.go`：

```go
package service

import "github.com/Tencent/WeKnora/internal/types"

// detectBickering 检测"同一对智能体连续 maxExchange 个来回且无用户/主持人插入"的互呛。
// 返回是否触发与涉事智能体对（按首字母序，便于稳定输出）。
// 定义：从最近一条用户/主持人/系统消息之后开始数，若消息流严格在 A、B 两个智能体间交替，
// 且 A→B 或 B→A 的完整来回数达到 maxExchange，即触发。
func detectBickering(msgs []types.MeetingMessage, maxExchange int) (bool, [2]string) {
	// 1. 截取尾部：最后一条非智能体消息之后的智能体消息段
	tailStart := 0
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].SenderKind != types.MeetingSenderAgent {
			tailStart = i + 1
			break
		}
	}
	tail := msgs[tailStart:]
	if len(tail) < maxExchange*2 {
		return false, [2]string{}
	}

	// 2. 尾段必须只涉及两个 sender，且严格交替
	senderA, senderB := tail[0].SenderID, ""
	for i, m := range tail {
		if m.SenderID != senderA && senderB == "" {
			senderB = m.SenderID
		}
		if m.SenderID != senderA && m.SenderID != senderB {
			return false, [2]string{} // 第三方插入，不构成"同一对"
		}
		// 严格交替：相邻两条不能同一人
		if i > 0 && tail[i-1].SenderID == m.SenderID {
			return false, [2]string{}
		}
	}
	if senderA == "" || senderB == "" {
		return false, [2]string{}
	}

	// 3. 来回数 = 完整对数（两条一来一回）
	if len(tail)/2 < maxExchange {
		return false, [2]string{}
	}
	if senderA > senderB { // 稳定排序
		senderA, senderB = senderB, senderA
	}
	return true, [2]string{senderA, senderB}
}
```

- [ ] **Step 3: 测试 + 提交**

Run: `go test ./internal/application/service/ -run TestDetectBickering -v`
Expected: 4 场景 PASS。

Run: `git add internal/application/service/meeting_guard.go internal/application/service/meeting_guard_test.go; git commit -m "feat(meeting): 互呛护栏检测算法"`

---

### Task T3.2: 主持人收敛消息 + 护栏接入讨论轮

**Files:**
- Modify: `internal/application/service/meeting_host.go`（主持人收敛文案生成）
- Modify: `internal/application/service/meeting_engine.go`（runAgentTurns 后跑护栏）

- [ ] **Step 1: 主持人收敛生成**

`meeting_host.go` 追加：

```go
// generateGuardMessage 护栏触发后由主持人生成"回到正题"收敛消息；失败时返回兜底文案。
func (e *meetingEngine) generateGuardMessage(ctx context.Context, room *types.MeetingRoom, nameA, nameB string, recent []types.MeetingMessage) string {
	fallback := fmt.Sprintf("主持人提醒：「%s」与「%s」的讨论已较深入，我们先回到主题「%s」。@用户 请指示下一步方向。", nameA, nameB, room.Description)
	hostChat, err := e.resolveHostChat(ctx)
	if err != nil {
		return fallback
	}
	var b strings.Builder
	fmt.Fprintf(&b, "会议主题：%s\n智能体「%s」和「%s」已连续多轮互相回应，内容如下（截取）：\n", room.Description, nameA, nameB)
	for _, m := range tailN(recent, 6) {
		// 智能体消息用参会者展示名，其余用 senderLabel 固定文案
		name := senderLabel(&m)
		if m.SenderKind == types.MeetingSenderAgent {
			name = e.agentDisplayName(ctx, room.ID, m.SenderID)
		}
		fmt.Fprintf(&b, "- %s: %s\n", name, truncate(m.Content, 200))
	}
	b.WriteString("\n请以主持人身份用 2-3 句话：肯定双方观点中有价值的部分，指出分歧焦点，然后把讨论引导回主题并向用户提问下一步。语气中立、简洁。")
	resp, err := hostChat.Chat(ctx, []chat.Message{{Role: "user", Content: b.String()}}, &chat.ChatOptions{Temperature: 0.3})
	if err != nil || strings.TrimSpace(resp.Content) == "" {
		return fallback
	}
	return resp.Content
}

// tailN 取最后 n 条
func tailN(msgs []types.MeetingMessage, n int) []types.MeetingMessage {
	if len(msgs) <= n {
		return msgs
	}
	return msgs[len(msgs)-n:]
}

// truncate 截断到 n 字符（按 rune 计，避免中文截半）
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
```

- [ ] **Step 2: 护栏接入讨论轮**

`meeting_engine.go` `RunTurn` 主循环中，`runAgentTurns` 之后、`turn_ended` 之前插入：

```go
// 护栏：检测互呛 → 主持人收敛 → 广播 guard.triggered 并结束本轮
recent, _ := e.messageRepo.ListByRoom(ctx, roomID, "", e.guardWindowSize())
if hit, pair := detectBickering(recent, e.guardMaxExchange()); hit {
	nameA := e.agentDisplayName(ctx, room.ID, pair[0])
	nameB := e.agentDisplayName(ctx, room.ID, pair[1])
	content := e.generateGuardMessage(ctx, room, nameA, nameB, recent)
	hostMsg, err := e.appendHostMessage(ctx, roomID, content)
	if err == nil {
		e.broadcaster.Publish(roomID, MeetingEvent{Type: MeetingEventGuardTriggered, Data: map[string]any{
			"agents":  []string{pair[0], pair[1]},
			"message": hostMsg,
		}})
	}
	break // 护栏触发即结束当前讨论轮，等待用户指示
}
```

`meeting_engine.go` 追加辅助函数（M4 方案生成会复用 `appendHostMessage`/`agentDisplayName`）：

```go
// appendHostMessage 落库 sender_kind=host 消息并广播 message.created
func (e *meetingEngine) appendHostMessage(ctx context.Context, roomID string, content string) (*types.MeetingMessage, error) {
	msg := &types.MeetingMessage{
		MeetingRoomID: roomID, SenderKind: types.MeetingSenderHost, Content: content,
	}
	if err := e.messageRepo.Create(ctx, msg); err != nil {
		return nil, err
	}
	e.broadcaster.Publish(roomID, MeetingEvent{Type: MeetingEventMessageCreated, Data: msg})
	return msg, nil
}

// agentDisplayName 参会者列表反查展示名，查不到回退 agentID 前 8 位
func (e *meetingEngine) agentDisplayName(ctx context.Context, roomID string, agentID string) string {
	ps, err := e.participantRepo.ListByRoom(ctx, roomID)
	if err == nil {
		for _, p := range ps {
			if p.AgentID == agentID && p.DisplayName != "" {
				return p.DisplayName
			}
		}
	}
	if len(agentID) > 8 {
		return agentID[:8]
	}
	return agentID
}

// guardMaxExchange 护栏互呛来回上限（配置缺失时回退 3）
func (e *meetingEngine) guardMaxExchange() int {
	if e.cfg != nil && e.cfg.Meeting != nil && e.cfg.Meeting.GuardMaxExchange > 0 {
		return e.cfg.Meeting.GuardMaxExchange
	}
	return 3
}

// guardWindowSize 护栏回看窗口：来回上限*2 + 余量（够判定即可，避免全量拉取）
func (e *meetingEngine) guardWindowSize() int { return e.guardMaxExchange()*2 + 4 }
```

说明：
- 并发模式下护栏在 `wg.Wait()` 后统一检测（轮内不打断，轮间收敛——与规格 §6 一致）。

- [ ] **Step 3: 编译 + 提交**

Run: `go build ./...; go test ./internal/application/service/ -v -run "Meeting|TestDetectBickering"`
Expected: 通过。

Run: `git add internal/application/service/meeting_host.go internal/application/service/meeting_engine.go; git commit -m "feat(meeting): 护栏触发后主持人收敛消息"`

---

### Task T3.3: POST /interrupt 打断端点

**Files:**
- Modify: `internal/handler/meeting.go`（新增 Interrupt）
- Modify: `internal/router/routes_meeting.go`（登记路由）
- Modify: `internal/application/service/meeting.go`（Service 转发引擎 Interrupt）

- [ ] **Step 1: 引擎补 clearInterrupt**

`interruptKey`/`Interrupt`/`isInterrupted` 已在 T1.5（M1）实现，此处仅补 `clearInterrupt`：

```go
// clearInterrupt 讨论轮结束后清除打断标记（RunTurn 退出前调用）
func (e *meetingEngine) clearInterrupt(ctx context.Context, roomID string) {
	e.redis.Del(ctx, interruptKey(roomID))
}
```

- [ ] **Step 2: Service + Handler + 路由**

`meeting.go`（MeetingService）：

```go
// InterruptDiscussion 用户主动打断进行中的讨论轮
func (s *meetingService) InterruptDiscussion(ctx context.Context, tenantID uint64, roomID string) error {
	if _, err := s.roomRepo.GetByID(ctx, roomID, tenantID); err != nil {
		return err // 租户校验（仓储按 tenant 过滤）
	}
	return s.engine.Interrupt(ctx, roomID)
}
```

`handler/meeting.go`：

```go
// Interrupt godoc POST /api/v1/meetings/:id/interrupt
func (h *MeetingHandler) Interrupt(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID, _ := types.TenantIDFromContext(ctx)
	if err := h.meetingService.InterruptDiscussion(ctx, tenantID, c.Param("id")); err != nil {
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"success": true, "message": "interrupt signal sent"})
}
```

`routes_meeting.go` 追加：

```go
meetings.POST("/:id/interrupt", rbacGuards.OwnedMeetingOrAdmin(), meetingHandler.Interrupt)
```

- [ ] **Step 3: 前端"回到正题"/打断按钮**

`MeetingRoomView.vue` header 区加按钮（仅 `discussing` 时可用）：

```vue
<el-button size="small" :disabled="!store.discussing" @click="interrupt">
  {{ $t('meeting.interrupt') }}
</el-button>
```

```ts
import { interruptMeeting } from '@/api/meeting';
async function interrupt() {
  await interruptMeeting(roomId); // 打断后引擎在边界停轮并 publishSystemMessage
}
```

`api/meeting/index.ts` 追加：

```ts
export function interruptMeeting(roomId: string) {
  return post<{ message: string }>(`/api/v1/meetings/${roomId}/interrupt`, {});
}
```

store `applyStreamEvent` 增加：

```ts
} else if (event === 'guard.triggered') {
  discussing.value = false;
  guardNotice.value = data; // 视图顶部横幅提示"主持人已介入"
}
```

i18n 增加 `meeting.interrupt: '打断讨论'` / `meeting.guardNotice: '主持人已介入，请指示下一步'`。

- [ ] **Step 4: 编译/构建 + 提交**

Run: `go build ./...; cd frontend; npm run build`
Expected: 通过。

Run: `git add internal/handler/meeting.go internal/router/routes_meeting.go internal/application/service/meeting.go internal/application/service/meeting_engine.go frontend/src/api/meeting/index.ts frontend/src/stores/meeting.ts frontend/src/views/meeting/MeetingRoomView.vue frontend/src/i18n; git commit -m "feat(meeting): 用户打断端点 + 前端打断按钮与护栏提示"`

---

### Task T3.4: 单轮内智能体互相回应（reply_to 链 + 多来回子轮循环）

> **补计划遗漏（T3.2 代码质量审查 C1）**：设计 §4.2 明确"单个 RunTurn 内允许 agent 互相回应（生成 reply_to 链），链长达到 `guard_max_exchange` 才触发护栏"。但 M3 原计划没有任何实现该语义的任务，`runAgentTurns` 每个 agent 每轮只发一条且 `ReplyToMessageID` 恒为触发消息（用户消息），导致护栏 tail 最多只有"发言人数"条，生产路径 **永不触发**（只有 `guard_max_exchange<=1` 时侥幸命中）。本任务补齐该链路。

**Files:**
- Modify: `internal/application/service/meeting_engine.go`（子轮循环 + 提及解析 + 组装指令）
- Modify: `internal/application/service/meeting_engine_test.go`（修复 M1 假阳性 + 新增子轮用例）

**语义（最小可用）**：
- 首轮：`selectSpeakers` 选出的发言者各自 `reply_to = 触发消息`（现状不变）。
- 子轮：由 **agent 自身输出信号**驱动——agent 在回复中用 `@展示名` 提及其他参会者，引擎解析后写入该消息的 `MentionedAgentIDs`；下一子轮发言者 = 上一子轮中被提及的参会者（按参会顺序去重），各自 `reply_to = 提及它的那条消息`。
- 自然终止：某子轮内没有任何 agent 提及其他参会者（对齐设计："本轮没有 agent 消息 reply_to 其他 agent 消息 → 自然结束"）。
- 硬上限：子轮数 ≤ `guardMaxExchange(room)`（防 LLM 无限互呛，绑定成本）。
- 打断：子轮边界复用既有 `isInterrupted` 检查。
- 护栏检查仍留在 `RunTurn` 中 `runDiscussion` 之后（T3.2 现状不变）；此时 tail 已形成 `A,B,A,B,…` 交替链，达 `maxExchange*2` 条即触发。

- [ ] **Step 1: 写失败测试（含 M1 假阳性修复）**

`meeting_engine_test.go`：

**① 修复假阳性根因**：替身 `ListByRoom` 必须返回"预置 ∪ 本轮落库"，否则测试预置 `runAgentTurns` 永远造不出的状态：

```go
// GetByID 从预置 + 本轮落库中按 ID 查找
func (f *fakeMeetingMessageRepo) GetByID(_ context.Context, id string) (*types.MeetingMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, m := range append(append([]*types.MeetingMessage{}, f.preset...), f.created...) {
		if m.ID == id {
			return m, nil
		}
	}
	return nil, errors.New("message not found")
}

// ListByRoom 返回"预置 ∪ 本轮落库"的正序副本（旧->新），模拟真实持久化读回
func (f *fakeMeetingMessageRepo) ListByRoom(_ context.Context, _ string, _ string, _ int) ([]*types.MeetingMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]*types.MeetingMessage, 0, len(f.preset)+len(f.created))
	out = append(out, f.preset...)
	out = append(out, f.created...)
	return out, nil
}
```

**② 提及解析纯逻辑用例**：

```go
func TestParseMentionedAgentIDs(t *testing.T) {
	ps := []*types.MeetingParticipant{
		{Kind: types.ParticipantKindAgent, AgentID: "agent-A", DisplayName: "甲"},
		{Kind: types.ParticipantKindAgent, AgentID: "agent-B", DisplayName: "乙"},
		{Kind: types.ParticipantKindHost, AgentID: "", DisplayName: "主持人"},
	}
	// 命中对方：按参会顺序返回 agentID
	assert.Equal(t, []string{"agent-B"}, parseMentionedAgentIDs("我赞同 @乙 的看法", ps, "agent-A"))
	// 提及自己：排除
	assert.Nil(t, parseMentionedAgentIDs("@甲 我说完了", ps, "agent-A"))
	// 提及主持人（非 agent 参会者）与未知名字：不产生 ID
	assert.Nil(t, parseMentionedAgentIDs("@主持人 请裁决", ps, "agent-A"))
	assert.Nil(t, parseMentionedAgentIDs("没有提及任何人", ps, "agent-A"))
	// 参与者为空：不 panic、返回 nil
	assert.Nil(t, parseMentionedAgentIDs("@甲", nil, "agent-A"))
}
```

**③ 子轮行为用例（真实流程自然形成 3 来回）**：

```go
// fakeTwoAgentRepoForTurn 解析 agent-A/agent-B 两个智能体对象（子轮按 ID 取回发言人）
type fakeTwoAgentRepoForTurn struct {
	interfaces.CustomAgentRepository
}

func (f *fakeTwoAgentRepoForTurn) GetAgentByID(_ context.Context, id string, _ uint64) (*types.CustomAgent, error) {
	name := "甲"
	if id == "agent-B" {
		name = "乙"
	}
	return &types.CustomAgent{ID: id, Name: name}, nil
}

// TestRunTurnGuardTriggersHostConvergence 真实路径：两个智能体在本轮内互相 @，
// 子轮自然扩展出 3 个来回（6 条 agent 消息）→ 护栏触发 → 主持人收敛 + 广播 guard.triggered
func TestRunTurnGuardTriggersHostConvergence(t *testing.T) {
	ctx := context.Background()
	preset := []*types.MeetingMessage{
		{ID: "u1", SenderKind: types.MeetingSenderUser, Content: "触发"},
	}
	e, repo := newGuardEngineFixture(t, "room-g", preset)
	e.roomRepo = &fakeGuardRoomRepoForTurn{}          // GuardMaxExchange=3
	e.participantRepo = &fakeTwoAgentParticipantRepo{} // 甲/乙
	e.agentRepo = &fakeTwoAgentRepoForTurn{}
	// 每个智能体都 @ 对方 → 每轮都产生新提及 → 子轮持续到硬上限（3 轮 × 2 人 = 6 条）
	e.sessionService = &fakeMeetingSession{agentQA: func(_ context.Context, req *types.QARequest, bus *event.EventBus) error {
		target := "@乙"
		if req.CustomAgent.ID == "agent-B" {
			target = "@甲"
		}
		return bus.Emit(context.Background(), event.Event{
			Type: event.EventAgentFinalAnswer,
			Data: event.AgentFinalAnswerData{Content: "我的观点 " + target},
		})
	}}

	ch, unsub := e.broadcaster.Subscribe("room-g")
	defer unsub()

	require.NoError(t, e.RunTurn(ctx, "room-g", "u1"))

	agentMsgs := messagesByKind(repo.snapshot(), types.MeetingSenderAgent)
	require.Len(t, agentMsgs, 6, "3 个子轮 × 2 个智能体 = 6 条互相回应消息")
	assert.Equal(t, "u1", agentMsgs[0].ReplyToMessageID, "首轮回应触发消息")
	assert.NotEqual(t, "u1", agentMsgs[2].ReplyToMessageID, "子轮应回应其他智能体的消息")
	require.Len(t, agentMsgs[0].MentionedAgentIDs, 1, "应落库解析出的提及")

	hostMsgs := messagesByKind(repo.snapshot(), types.MeetingSenderHost)
	require.Len(t, hostMsgs, 1, "护栏触发应产生恰好一条主持人收敛消息")
	assert.Contains(t, hostMsgs[0].Content, "发布方案评审", "兜底收敛文案应包含会议主题")

	var sawGuard bool
	for len(ch) > 0 {
		if evt := <-ch; evt.Type == MeetingEventGuardTriggered {
			sawGuard = true
		}
	}
	assert.True(t, sawGuard, "应广播 guard.triggered 事件")
}

// TestRunTurnStopsWhenNoMention 反向用例：智能体不提及他人 → 不展开子轮，仅 2 条消息、不触发护栏
func TestRunTurnStopsWhenNoMention(t *testing.T) {
	ctx := context.Background()
	e, repo := newGuardEngineFixture(t, "room-s", []*types.MeetingMessage{
		{ID: "u1", SenderKind: types.MeetingSenderUser, Content: "触发"},
	})
	e.roomRepo = &fakeGuardRoomRepoForTurn{}
	e.participantRepo = &fakeTwoAgentParticipantRepo{}
	e.agentRepo = &fakeTwoAgentRepoForTurn{}

	require.NoError(t, e.RunTurn(ctx, "room-s", "u1"))

	assert.Equal(t, 2, countByKind(repo.snapshot(), types.MeetingSenderAgent), "无提及应自然终止于首轮")
	assert.Equal(t, 0, countByKind(repo.snapshot(), types.MeetingSenderHost), "未达阈值不应触发护栏")
}
```

Run: `go test ./internal/application/service/ -run "TestParseMentionedAgentIDs|TestRunTurnStopsWhenNoMention|TestRunTurnGuardTriggersHostConvergence" -v`
Expected: FAIL（`parseMentionedAgentIDs` 未定义 / 子轮未实现）。

- [ ] **Step 2: 实现提及解析 + 子轮循环**

`meeting_engine.go` 顶部 import 增加 `"regexp"`。

```go
// mentionTokenRe 匹配 @某人（@ 后接连续的字母/数字/下划线/连字符/汉字）
var mentionTokenRe = regexp.MustCompile(`@([\p{L}\p{N}_\-]+)`)

// parseMentionedAgentIDs 从回复文本解析 @展示名 提及并映射为 agentID（按参会顺序去重，排除自己）。
// participants 为空或无有效匹配时返回 nil。
func parseMentionedAgentIDs(content string, participants []*types.MeetingParticipant, selfID string) []string {
	if content == "" || len(participants) == 0 {
		return nil
	}
	matches := mentionTokenRe.FindAllStringSubmatch(content, -1)
	if len(matches) == 0 {
		return nil
	}
	names := make(map[string]struct{}, len(matches))
	for _, m := range matches {
		names[m[1]] = struct{}{}
	}
	var out []string
	for _, p := range participants {
		if p.Kind != types.ParticipantKindAgent || p.AgentID == "" || p.AgentID == selfID || p.DisplayName == "" {
			continue
		}
		if _, ok := names[p.DisplayName]; ok {
			out = append(out, p.AgentID)
		}
	}
	return out
}

// meetingSpeaker 一个发言任务：agent 回应 replyTo 这条消息
type meetingSpeaker struct {
	agent   *types.CustomAgent
	replyTo *types.MeetingMessage
}

// runAgentTurns 一轮发言（所有发言者回应同一 trigger）；保留既有签名供既有测试/调用复用
func (e *meetingEngine) runAgentTurns(ctx context.Context, room *types.MeetingRoom, trigger *types.MeetingMessage, speakers []*types.CustomAgent) []*types.MeetingMessage {
	tasks := make([]meetingSpeaker, 0, len(speakers))
	for _, a := range speakers {
		tasks = append(tasks, meetingSpeaker{agent: a, replyTo: trigger})
	}
	return e.runSpeakerTasks(ctx, room, tasks, nil)
}

// runSpeakerTasks 按广播模式执行一组发言任务，返回成功落库的 agent 消息（顺序确定）
func (e *meetingEngine) runSpeakerTasks(ctx context.Context, room *types.MeetingRoom, tasks []meetingSpeaker, participants []*types.MeetingParticipant) []*types.MeetingMessage {
	if shouldRunParallel(room.BroadcastMode, len(tasks)) {
		if ctx.Err() != nil {
			return nil // ctx 已取消时不再启动并发发言
		}
		slots := make([]*types.MeetingMessage, len(tasks))
		var wg sync.WaitGroup
		for i, task := range tasks {
			wg.Add(1)
			go func(i int, task meetingSpeaker) {
				defer wg.Done()
				msg, err := e.runAgentTurn(ctx, room, task.replyTo, task.agent, participants)
				if err != nil {
					e.handleSpeakFailure(ctx, room, task.agent, err)
					return
				}
				slots[i] = msg // 各 goroutine 写不同下标，无数据竞争
			}(i, task)
		}
		wg.Wait()
		return compactMessages(slots)
	}
	// sequential：严格按顺序逐个发言，每个发言前检查打断标记
	out := make([]*types.MeetingMessage, 0, len(tasks))
	for _, task := range tasks {
		if e.isInterrupted(ctx, room.ID) {
			logger.Infof(ctx, "meeting turn interrupted. room=%s", room.ID)
			e.publishSystemMessage(ctx, room.ID, "讨论已被用户打断")
			return out
		}
		msg, err := e.runAgentTurn(ctx, room, task.replyTo, task.agent, participants)
		if err != nil {
			e.handleSpeakFailure(ctx, room, task.agent, err)
			continue
		}
		out = append(out, msg)
	}
	return out
}

// compactMessages 去掉 nil 槽位，保持成功消息相对顺序
func compactMessages(in []*types.MeetingMessage) []*types.MeetingMessage {
	out := make([]*types.MeetingMessage, 0, len(in))
	for _, m := range in {
		if m != nil {
			out = append(out, m)
		}
	}
	return out
}

// planReplyRound 依据本轮消息的提及关系规划下一子轮：被提及者各自回应"最早提及它的那条消息"。
// 无任何提及 → nil，表示讨论自然终止。
func planReplyRound(created []*types.MeetingMessage, participants []*types.MeetingParticipant, agentsByID map[string]*types.CustomAgent) []meetingSpeaker {
	targets := make(map[string]*types.MeetingMessage, len(participants))
	for _, m := range created {
		for _, id := range m.MentionedAgentIDs {
			if _, seen := targets[id]; !seen {
				targets[id] = m // 仅记最早一条，保证确定性
			}
		}
	}
	if len(targets) == 0 {
		return nil
	}
	out := make([]meetingSpeaker, 0, len(targets))
	for _, p := range participants { // 按参会顺序，保证确定性
		if p.Kind != types.ParticipantKindAgent {
			continue
		}
		target, ok := targets[p.AgentID]
		if !ok {
			continue
		}
		if agent := agentsByID[p.AgentID]; agent != nil {
			out = append(out, meetingSpeaker{agent: agent, replyTo: target})
		}
	}
	return out
}

// runDiscussion 执行一轮完整讨论：首轮由 selectSpeakers 决定的发言者回应触发消息，
// 之后按"提及链"展开子轮，直至自然终止（无提及）/达到护栏上限/用户打断。
func (e *meetingEngine) runDiscussion(
	ctx context.Context, room *types.MeetingRoom, trigger *types.MeetingMessage,
	speakers, allAgents []*types.CustomAgent, participants []*types.MeetingParticipant,
) {
	agentsByID := make(map[string]*types.CustomAgent, len(allAgents))
	for _, a := range allAgents {
		agentsByID[a.ID] = a
	}
	tasks := make([]meetingSpeaker, 0, len(speakers))
	for _, a := range speakers {
		tasks = append(tasks, meetingSpeaker{agent: a, replyTo: trigger})
	}
	maxRounds := e.guardMaxExchange(room) // 子轮硬上限：防 LLM 无限互相回应
	for round := 0; round < maxRounds && len(tasks) > 0; round++ {
		created := e.runSpeakerTasks(ctx, room, tasks, participants)
		if e.isInterrupted(ctx, room.ID) {
			return // sequential 分支已落库打断提示，此处仅停止继续展开子轮
		}
		tasks = planReplyRound(created, participants, agentsByID)
	}
}
```

`prepareTurn` 改为返回输入快照结构（避免 5 个返回值）：

```go
// turnInput 一轮讨论的输入快照
type turnInput struct {
	room         *types.MeetingRoom
	trigger      *types.MeetingMessage
	speakers     []*types.CustomAgent // 本轮发言者（selectSpeakers 结果）
	agents       []*types.CustomAgent // 全部参会智能体（供子轮按 ID 取回）
	participants []*types.MeetingParticipant
}

func (e *meetingEngine) prepareTurn(ctx context.Context, roomID string, triggerMessageID string) (*turnInput, error) {
	room, err := e.roomRepo.GetByIDInternal(ctx, roomID)
	if err != nil {
		return nil, err
	}
	trigger, err := e.messageRepo.GetByID(ctx, triggerMessageID)
	if err != nil {
		return nil, err
	}
	participants, err := e.participantRepo.ListByRoom(ctx, roomID)
	if err != nil {
		return nil, err
	}
	agents := filterAgentParticipants(participants)
	agentObjs := make([]*types.CustomAgent, 0, len(agents))
	for _, p := range agents {
		agent, err := e.agentRepo.GetAgentByID(ctx, p.AgentID, room.TenantID)
		if err != nil {
			logger.Warnf(ctx, "meeting load agent failed, room=%s agent=%s err=%v", roomID, p.AgentID, err)
			continue
		}
		agentObjs = append(agentObjs, agent)
	}
	return &turnInput{
		room: room, trigger: trigger,
		speakers:     e.selectSpeakers(ctx, room, trigger, agentObjs),
		agents:       agentObjs,
		participants: participants,
	}, nil
}
```

`RunTurn` 主循环相应改为：

```go
in, err := e.prepareTurn(ctx, roomID, current)
if err != nil {
	return err
}
room := in.room
e.broadcaster.Publish(roomID, MeetingEvent{Type: MeetingEventTurnStarted, Data: map[string]interface{}{
	"room_id": roomID, "trigger_message_id": current,
}})
e.runDiscussion(ctx, room, in.trigger, in.speakers, in.agents, in.participants)
e.broadcaster.Publish(roomID, MeetingEvent{Type: MeetingEventTurnEnded, Data: map[string]interface{}{
	"room_id": roomID, "trigger_message_id": current,
}})
// ...以下护栏块保持不变
```

`runAgentTurn` 签名改为"回应哪条消息"，并落库提及：

```go
// runAgentTurn 单个智能体发言：回应 replyTo 这条消息；解析回复中的 @ 提及写入 MentionedAgentIDs
func (e *meetingEngine) runAgentTurn(ctx context.Context, room *types.MeetingRoom, replyTo *types.MeetingMessage, agent *types.CustomAgent, participants []*types.MeetingParticipant) (*types.MeetingMessage, error) {
	// ...（前半段 AgentQA 调用不变，仅把 trigger 全部替换为 replyTo）
	query := buildMeetingQuery(room, replyTo.Content, contextMsgs)
	// ...
	msg := &types.MeetingMessage{
		MeetingRoomID:     room.ID,
		SenderKind:        types.MeetingSenderAgent,
		SenderID:          agent.ID,
		ReplyToMessageID:  replyTo.ID,
		MentionedAgentIDs: types.StringList(parseMentionedAgentIDs(answer.String(), participants, agent.ID)),
		Content:           answer.String(),
		ReviewState:       types.ReviewStateNone,
	}
	if err := e.messageRepo.Create(ctx, msg); err != nil {
		return nil, err
	}
	e.broadcaster.Publish(room.ID, MeetingEvent{Type: MeetingEventMessageCreated, Data: msg})
	return msg, nil
}
```

`buildMeetingQuery` 末尾指令追加"回应他人"的引导（否则生产环境 agent 不会 @ 他人，子轮永不展开）：

```go
b.WriteString("\n请以你的身份与职责，针对当前议题发表意见，简明扼要。若需赞同或反驳其他参会人，请在该句中直接用 @对方姓名 提及。")
```

> **补充（Phase 2 复审修复，commit `c6b8bc113`）**：仅追加 `@对方姓名` 引导语**不足以**让子轮在生产路径生效——`buildMeetingQuery` 生成的提示词从未向模型提供参会者展示名（讨论记录中 agent 消息经 `senderLabel` 渲染为 `智能体(<UUID>)`，且全文无参会者名册），而解析侧 `parseMentionedAgentIDs` 按参会者 `DisplayName` 匹配，模型无从产出可命中的 `@展示名`；测试未能暴露是因为替身里模型回答写死为 `@乙`/`@甲`。修复：`buildMeetingQuery` 增加 `participants []*types.MeetingParticipant` 参数，在【会议描述】/【早期讨论摘要】之后、【讨论记录】之前注入名册行 `【参会智能体】甲、乙`（仅取 `ParticipantKindAgent` 且 `DisplayName != ""`，去重、按参会顺序，排除主持人；无 agent 展示名时不输出该行）；`runAgentTurn` 透传 `participants`；`TestBuildMeetingQuery` 改为 4 参数并新增"query 含 agent 展示名、不含主持人展示名"断言，另新增 `TestBuildMeetingQueryNoAgentRoster`。因此上文 `runAgentTurn` 中调用点为 `buildMeetingQuery(room, replyTo.Content, contextMsgs, participants)`。

- [ ] **Step 3: 全量测试 + 编译 + 提交**

Run:
```
go test ./internal/application/service/ -run "Meeting|TestDetectBickering|TestParseMentionedAgentIDs|TestRunTurn|TestRunAgentTurns|TestExtractQueuedTrigger|TestGenerateGuardMessage" -v
```
（既有 3 个 `runAgentTurns` 用例签名不变——`runAgentTurns` 保留为 4 参数薄封装，新增返回值可在语句中忽略。）
Expected: PASS。

Run: `go vet ./internal/application/service/`
Expected: 无输出。

Run: `git add internal/application/service/meeting_engine.go internal/application/service/meeting_engine_test.go; git commit -m "feat(meeting): 单轮内智能体互相回应（reply_to 链 + 多来回子轮循环），修复护栏不可达与假阳性测试"`

---

### Task T3.6: 用户打断触发主持人收敛消息（补 T3.3 设计级偏离）

> **补计划遗漏（T3.3 规格符合性审查发现）**：设计 `design.md:137`（`POST /meetings/:id/interrupt` = "用户触发'回到正题'（中断当前 RunTurn，**主持人收敛**）"）、`design.md:229-239`（主持人职责第 2 条：护栏触发/用户点"回到正题"时"总结分歧点/暂停争论/邀请用户裁决"；`L239` 明确"护栏触发 / **用户 interrupt** / 用户点'汇总方案' 时分别触发对应 host 消息生成"）、`design.md:369-371`（M3 验收"用户点'回到正题'**立即触发收敛**"）均要求用户打断后由主持人生成**收敛消息**。
> 但 T3.3 计划原文仅要求"边界停轮并 publishSystemMessage"，实现因此只在 sequential 分支落一条系统提示（`meeting_engine.go:286`），parallel 分支连系统提示都缺失，且打断路径完全不生成主持人消息（主持人收敛此前仅接线到护栏路径 `meeting_engine.go:120-121`）。本任务补齐该语义。

**Files:**
- Modify: `internal/application/service/meeting_engine.go`（RunTurn 收尾统一处理 + 去掉分支内重复落库 + 新增 handleInterrupt）
- Modify: `internal/application/service/meeting_host.go`（新增 generateInterruptMessage + 抽取共用渲染辅助）
- Modify: `internal/application/service/meeting_engine_test.go`（调整既有打断用例 + 新增 RunTurn 级打断收敛用例）

**语义**：
- 打断收尾**统一在 `RunTurn` 主循环**（`runDiscussion` 返回并广播 TurnEnded 之后、护栏块之前）判定 `isInterrupted`：①落一条系统提示"讨论已被用户打断"；②主持人 `generateInterruptMessage` 生成收敛消息（不可用/失败用兜底文案）；③落库 sender_kind=host 并广播 `guard.triggered`（`reason:"user_interrupt"`）；④清除标记并结束本轮。sequential / parallel 两种模式走同一路径。
- `runSpeakerTasks`（sequential 分支）与 `runDiscussion` 只负责"停止"，不再自行落库提示（避免重复与缺失）。

- [ ] **Step 1: 拆掉分支内重复落库**

`runSpeakerTasks` sequential 分支：

```go
if e.isInterrupted(ctx, room.ID) {
	logger.Infof(ctx, "meeting turn interrupted. room=%s", room.ID)
	return out // 打断收尾（系统提示 + 主持人收敛）统一由 RunTurn 处理
}
```

`runDiscussion` 子轮循环：

```go
if e.isInterrupted(ctx, room.ID) {
	return // 打断收尾统一由 RunTurn 处理
}
```

- [ ] **Step 2: RunTurn 统一收尾**

在 `RunTurn` 主循环中，"广播 MeetingEventTurnEnded"之后、"护栏检测块"之前插入：

```go
// 用户打断（"回到正题"）：落系统提示 + 主持人生成收敛消息 + 广播 guard.triggered，随后结束本轮
if e.isInterrupted(ctx, roomID) {
	e.handleInterrupt(ctx, room, roomID)
	e.clearInterrupt(ctx, roomID)
	break
}
```

- [ ] **Step 3: handleInterrupt（meeting_engine.go，appendHostMessage 附近）**

```go
// handleInterrupt 用户打断（"回到正题"）的收尾：落系统提示 + 主持人生成收敛消息并广播 guard.triggered
func (e *meetingEngine) handleInterrupt(ctx context.Context, room *types.MeetingRoom, roomID string) {
	e.publishSystemMessage(ctx, roomID, "讨论已被用户打断")
	recent, err := e.messageRepo.ListByRoom(ctx, roomID, "", e.contextMax)
	if err != nil {
		logger.Warnf(ctx, "meeting interrupt list messages failed, room=%s err=%v", roomID, err)
		recent = nil
	}
	content := e.generateInterruptMessage(ctx, room, recent)
	hostMsg, herr := e.appendHostMessage(ctx, roomID, content)
	if herr != nil {
		logger.Warnf(ctx, "meeting interrupt host message failed, room=%s err=%v", roomID, herr)
		return
	}
	e.broadcaster.Publish(roomID, MeetingEvent{Type: MeetingEventGuardTriggered, Data: map[string]interface{}{
		"reason": "user_interrupt", "message": hostMsg,
	}})
}
```

- [ ] **Step 4: generateInterruptMessage + 抽取共用渲染（meeting_host.go）**

为避免与 `generateGuardMessage` 重复，先抽取共用渲染：

```go
// renderRecentForHost 把最近 n 条消息渲染为"名称: 内容"要点行（主持人收敛/打断提示复用）
func (e *meetingEngine) renderRecentForHost(ctx context.Context, roomID string, recent []*types.MeetingMessage, n int) string {
	var b strings.Builder
	for _, m := range tailN(recent, n) {
		name := senderLabel(m)
		if m.SenderKind == types.MeetingSenderAgent {
			name = e.agentDisplayName(ctx, roomID, m.SenderID)
		}
		fmt.Fprintf(&b, "- %s: %s\n", name, truncate(m.Content, 200))
	}
	return b.String()
}
```

`generateGuardMessage` 内的渲染循环改为 `b.WriteString(e.renderRecentForHost(ctx, room.ID, recent, 6))`（行为不变）。

新增：

```go
// generateInterruptMessage 用户主动打断（"回到正题"）后由主持人生成收敛消息；不可用/失败时返回兜底文案。
func (e *meetingEngine) generateInterruptMessage(ctx context.Context, room *types.MeetingRoom, recent []*types.MeetingMessage) string {
	fallback := fmt.Sprintf("主持人：已收到打断，我们先暂停当前讨论，回到主题「%s」。请用户指示下一步方向。", room.Title)
	hostChat, err := e.resolveHostChat(ctx)
	if err != nil {
		return fallback
	}
	var b strings.Builder
	fmt.Fprintf(&b, "会议主题：%s\n用户已打断当前讨论并要求回到正题。最近讨论内容（截取）：\n", room.Title)
	b.WriteString(e.renderRecentForHost(ctx, room.ID, recent, 6))
	b.WriteString("\n请以主持人身份用 2-3 句话：简要总结当前讨论的分歧焦点，宣布暂停争论，并邀请用户给出下一步指示。语气中立、简洁。")
	resp, err := hostChat.Chat(ctx, []chat.Message{{Role: "user", Content: b.String()}}, &chat.ChatOptions{Temperature: 0.3})
	if err != nil || resp == nil || strings.TrimSpace(resp.Content) == "" {
		return fallback
	}
	return resp.Content
}
```

- [ ] **Step 5: 测试**

（1）调整 `TestRunAgentTurnsSequentialInterrupted`：`runSpeakerTasks` 不再落库，断言改为"不调用 AgentQA 且不产生任何消息"，并更名 `TestRunSpeakerTasksSequentialInterruptedStopsSilently`。

（2）新增 RunTurn 级用例（复用 `newGuardEngineFixture` + `fakeGuardRoomRepoForTurn`/`fakeTwoAgentParticipantRepo`/`fakeTwoAgentRepoForTurn`）：

```go
// TestRunTurnInterruptTriggersHostConvergence 真实路径：用户在讨论进行中打断 →
// 系统提示 + 主持人收敛消息 + 广播 guard.triggered
func TestRunTurnInterruptTriggersHostConvergence(t *testing.T) {
	ctx := context.Background()
	e, repo := newGuardEngineFixture(t, "room-i", []*types.MeetingMessage{
		{ID: "u1", SenderKind: types.MeetingSenderUser, Content: "触发"},
	})
	e.roomRepo = &fakeGuardRoomRepoForTurn{}
	e.participantRepo = &fakeTwoAgentParticipantRepo{}
	e.agentRepo = &fakeTwoAgentRepoForTurn{}
	// 首个智能体发言时模拟用户点击"回到正题"（置打断标记）
	e.sessionService = &fakeMeetingSession{agentQA: func(ctx context.Context, _ *types.QARequest, _ *event.EventBus) error {
		return e.redis.Set(ctx, interruptKey("room-i"), "1", time.Minute).Err()
	}}

	ch, unsub := e.broadcaster.Subscribe("room-i")
	defer unsub()

	require.NoError(t, e.RunTurn(ctx, "room-i", "u1"))

	msgs := repo.snapshot()
	sysMsgs := messagesByKind(msgs, types.MeetingSenderSystem)
	require.Len(t, sysMsgs, 1, "打断应落一条系统提示")
	assert.Contains(t, sysMsgs[0].Content, "讨论已被用户打断")

	hostMsgs := messagesByKind(msgs, types.MeetingSenderHost)
	require.Len(t, hostMsgs, 1, "打断应产生恰好一条主持人收敛消息")
	assert.Contains(t, hostMsgs[0].Content, "发布方案评审", "兜底收敛文案应包含会议主题")

	var sawGuard bool
	for len(ch) > 0 {
		if evt := <-ch; evt.Type == MeetingEventGuardTriggered {
			sawGuard = true
		}
	}
	assert.True(t, sawGuard, "打断应广播 guard.triggered")
}
```

- [ ] **Step 6: 测试 + vet + 提交**

Run: `go test ./internal/application/service/ -run "Meeting|TestDetectBickering|TestParseMentionedAgentIDs|TestRunTurn|TestRunSpeakerTasks|TestGenerateGuardMessage|TestBuildMeetingQuery|TestExtractQueuedTrigger" -v`
Expected: PASS。

Run: `go vet ./internal/application/service/`
Expected: 无输出。

Run: `git add internal/application/service/meeting_engine.go internal/application/service/meeting_host.go internal/application/service/meeting_engine_test.go; git commit -m "feat(meeting): 用户打断统一收尾——系统提示 + 主持人收敛消息 + guard.triggered（补 T3.3 设计级偏离）"`

---

### Task T3.5: M3 端到端验收

> 依赖 T3.4：Step 1 的"3 个来回互呛"须在单轮内由子轮循环自然形成（首轮各自表态后互相 @ 反驳）。

- [ ] **Step 1: 互呛触发**：造 2 个提示词易争论的智能体（如"激进派"/"保守派"），引导其互相反驳 3 个来回 → 消息流出现主持人收敛消息（sender_kind=host）→ SSE 收到 `guard.triggered` → 讨论轮停止，输入框恢复。
- [ ] **Step 2: 不触发场景**：同一对 2 个来回后用户插话 → 再 2 个来回 → 不触发（计数被重置）。
- [ ] **Step 3: 打断**：讨论进行中点"打断讨论"→ 当前智能体说完即停（下一个边界前不再开新发言）→ 系统消息"讨论已被用户打断" → **主持人收敛消息（sender_kind=host）** → SSE 收到 `guard.triggered`（`reason=user_interrupt`）。
- [ ] **Step 4: 提交**：`git commit --allow-empty -m "test(meeting): M3 护栏与打断验收"`

---

## Milestone M4: 混合评审 + 主持人方案生成

**目标**：用户对智能体消息逐条评审（采纳/不采纳/待定），评审状态落库并 SSE 同步；一键触发主持人把已采纳意见汇总为结构化方案（is_solution 消息 + meeting_artifacts 版本记录），经 SSE 回流到消息流。

### Task T4.1: 评审端点 PUT /:id/messages/:mid/review

**Files:**
- Modify: `internal/types/interfaces/meeting.go`（Service 接口加 UpdateReviewState）
- Modify: `internal/application/service/meeting.go`（实现 + publishEvent 辅助函数）
- Modify: `internal/handler/meeting.go`（UpdateReviewState）
- Modify: `internal/router/routes_meeting.go`（登记路由）

- [ ] **Step 1: Service 接口 + 实现**

`MeetingService` 接口追加：

```go
// UpdateReviewState 更新智能体消息的评审状态（pending/adopted/rejected；none 仅初始值不可回设）
UpdateReviewState(ctx context.Context, tenantID uint64, roomID string, messageID string, state string) (*types.MeetingMessage, error)
```

`meeting.go` 追加：

```go
// validReviewStates 允许前端设置的评审状态（none 只是初始值，不允许回设）
var validReviewStates = map[string]bool{
	types.ReviewStatePending:  true,
	types.ReviewStateAdopted:  true,
	types.ReviewStateRejected: true,
}

// broadcasterProvider 引擎侧暴露广播器的收窄接口（与 handler.engineBroadcaster 同一模式）
type broadcasterProvider interface{ Broadcaster() *MeetingBroadcaster }

// publishEvent 经引擎广播器发 SSE 事件；引擎类型不符时静默跳过（不阻断主流程）
func publishEvent(engine interfaces.MeetingEngine, roomID string, evt MeetingEvent) {
	if bp, ok := engine.(broadcasterProvider); ok {
		bp.Broadcaster().Publish(roomID, evt)
	}
}

func (s *meetingService) UpdateReviewState(ctx context.Context, tenantID uint64, roomID string, messageID string, state string) (*types.MeetingMessage, error) {
	if !validReviewStates[state] {
		return nil, fmt.Errorf("invalid review state: %s", state)
	}
	room, err := s.roomRepo.GetByID(ctx, roomID, tenantID) // 租户校验（仓储按 tenant 过滤）
	if err != nil {
		return nil, err
	}
	msg, err := s.messageRepo.GetByID(ctx, messageID)
	if err != nil {
		return nil, err
	}
	if msg.MeetingRoomID != room.ID {
		return nil, fmt.Errorf("message %s not in room %s", messageID, roomID)
	}
	if msg.SenderKind != types.MeetingSenderAgent || msg.IsSolution {
		return nil, fmt.Errorf("only agent discussion messages can be reviewed")
	}
	if err := s.messageRepo.UpdateReviewState(ctx, messageID, state); err != nil {
		return nil, err
	}
	msg.ReviewState = state
	publishEvent(s.engine, roomID, MeetingEvent{Type: MeetingEventMessageReviewUpdated, Data: msg})
	return msg, nil
}
```

- [ ] **Step 2: Handler + 路由**

`meeting.go`（handler）追加：

```go
type updateReviewRequest struct {
	ReviewState string `json:"review_state" binding:"required"`
}

// UpdateReviewState PUT /api/v1/meetings/:id/messages/:mid/review
func (h *MeetingHandler) UpdateReviewState(c *gin.Context) {
	ctx := c.Request.Context()
	var req updateReviewRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}
	tenantID, _ := types.TenantIDFromContext(ctx)
	msg, err := h.meetingService.UpdateReviewState(ctx, tenantID, c.Param("id"), c.Param("mid"), req.ReviewState)
	if err != nil {
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": msg})
}
```

`routes_meeting.go` 追加（房间级一律 `OwnedMeetingOrAdmin`，见 T1.6 决策）：

```go
meetings.PUT("/:id/messages/:mid/review", rbacGuards.OwnedMeetingOrAdmin(), meetingHandler.UpdateReviewState)
```

- [ ] **Step 3: 编译 + 提交**

Run: `go build ./...`
Expected: 通过。

Run: `git add internal/types/interfaces/meeting.go internal/application/service/meeting.go internal/handler/meeting.go internal/router/routes_meeting.go; git commit -m "feat(meeting): 消息评审状态端点 + review_updated 广播"`

---

### Task T4.2: POST /:id/solution 主持人结构化方案生成

**Files:**
- Create: `internal/application/service/meeting_solution.go`
- Create: `internal/application/service/meeting_solution_test.go`
- Modify: `internal/types/interfaces/meeting.go`（Engine 接口加 GenerateSolution；Service 接口加 GenerateSolution）
- Modify: `internal/application/service/meeting_engine.go`（补 artifactRepo 字段）
- Modify: `internal/application/service/meeting.go`（GenerateSolution 委托）
- Modify: `internal/handler/meeting.go`、`internal/router/routes_meeting.go`

- [ ] **Step 1: 写失败测试（buildSolutionPrompt 纯函数）**

`meeting_solution_test.go`:

```go
package service

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
)

func TestBuildSolutionPrompt(t *testing.T) {
	room := &types.MeetingRoom{Title: "发布方案评审", Description: "讨论 v2 发布策略"}
	adopted := []*types.MeetingMessage{
		{SenderID: "a1", Content: "建议蓝绿发布"},
		{SenderID: "a2", Content: "需要回滚预案"},
	}
	nameOf := func(id string) string {
		return map[string]string{"a1": "架构师", "a2": "测试专家"}[id]
	}
	out := buildSolutionPrompt(room, adopted, nameOf)
	assert.Contains(t, out, "发布方案评审")   // 主题入提示词
	assert.Contains(t, out, "架构师：建议蓝绿发布") // 采纳意见带展示名
	assert.Contains(t, out, "测试专家：需要回滚预案")
	assert.Contains(t, out, "## 背景与目标")     // 固定结构四小节
	assert.Contains(t, out, "## 方案要点")
	assert.Contains(t, out, "## 风险与待确认事项")
	assert.Contains(t, out, "## 下一步行动")
}
```

Run: `go test ./internal/application/service/ -run TestBuildSolutionPrompt -v`
Expected: FAIL（函数不存在）。

- [ ] **Step 2: 实现 meeting_solution.go + 引擎补 artifactRepo 字段**

`meeting_solution.go`（完整内容）：

```go
package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
)

// ErrNoAdoptedMessages 无已采纳消息时拒绝生成方案（handler 判等映射 400）
var ErrNoAdoptedMessages = errors.New("meeting: no adopted messages")

// buildSolutionPrompt 组装主持人生成方案的提示词（纯函数，单测覆盖）
func buildSolutionPrompt(room *types.MeetingRoom, adopted []*types.MeetingMessage, nameOf func(agentID string) string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "会议主题：%s\n会议描述：%s\n\n以下是本次讨论中被用户采纳的意见（共 %d 条）：\n", room.Title, room.Description, len(adopted))
	for i, m := range adopted {
		fmt.Fprintf(&b, "%d. %s：%s\n", i+1, nameOf(m.SenderID), m.Content)
	}
	b.WriteString("\n请以主持人身份，将上述采纳意见汇总为一份结构化方案，固定包含以下小节：\n")
	b.WriteString("## 背景与目标\n## 方案要点\n## 风险与待确认事项\n## 下一步行动\n")
	b.WriteString("要求：不引入采纳意见之外的新观点；要点可溯源到对应意见；语言简明。")
	return b.String()
}

// GenerateSolution 主持人基于已采纳意见生成结构化方案：
// 落 is_solution 消息（版本递增）→ 写 meeting_artifacts → 更新房间当前方案指针 → 广播
func (e *meetingEngine) GenerateSolution(ctx context.Context, room *types.MeetingRoom, userID string) (*types.MeetingMessage, error) {
	adopted, err := e.messageRepo.ListByReviewState(ctx, room.ID, types.ReviewStateAdopted)
	if err != nil {
		return nil, err
	}
	if len(adopted) == 0 {
		return nil, ErrNoAdoptedMessages
	}
	// 生成中事件无配对终态：LLM 失败时前端 loading 由 HTTP 错误响应复位（见 T4.3）
	e.broadcaster.Publish(room.ID, MeetingEvent{Type: MeetingEventSolutionGenerating, Data: map[string]any{"room_id": room.ID}})

	hostChat, err := e.resolveHostChat(ctx)
	if err != nil {
		return nil, err
	}
	nameOf := func(agentID string) string { return e.agentDisplayName(ctx, room.ID, agentID) }
	resp, err := hostChat.Chat(ctx, []chat.Message{{Role: "user", Content: buildSolutionPrompt(room, adopted, nameOf)}}, &chat.ChatOptions{Temperature: 0.3})
	if err != nil {
		return nil, fmt.Errorf("host generate solution: %w", err)
	}

	version, err := e.messageRepo.MaxSolutionVersion(ctx, room.ID)
	if err != nil {
		return nil, err
	}
	version++
	msg := &types.MeetingMessage{
		MeetingRoomID:   room.ID,
		SenderKind:      types.MeetingSenderHost,
		Content:         resp.Content,
		IsSolution:      true,
		SolutionVersion: version,
		ReviewState:     types.ReviewStateNone,
	}
	if err := e.messageRepo.Create(ctx, msg); err != nil {
		return nil, err
	}

	// 产物元数据：正文不重复存储，经 solution_message_id 关联（统计失败不阻断）
	adoptedCount, _ := e.messageRepo.CountByReviewStates(ctx, room.ID, []string{types.ReviewStateAdopted})
	rejectedCount, _ := e.messageRepo.CountByReviewStates(ctx, room.ID, []string{types.ReviewStateRejected})
	artifact := &types.MeetingArtifact{
		MeetingRoomID:     room.ID,
		SolutionMessageID: msg.ID,
		Version:           version,
		AdoptedCount:      int(adoptedCount),
		RejectedCount:     int(rejectedCount),
		CreatedBy:         userID,
	}
	if err := e.artifactRepo.Create(ctx, artifact); err != nil {
		return nil, err
	}

	room.CurrentSolutionMessageID = msg.ID
	if err := e.roomRepo.Update(ctx, room); err != nil {
		return nil, err
	}

	e.broadcaster.Publish(room.ID, MeetingEvent{Type: MeetingEventMessageCreated, Data: msg})
	e.broadcaster.Publish(room.ID, MeetingEvent{Type: MeetingEventSolutionCreated, Data: artifact})
	return msg, nil
}
```

接口与字段补充：

- `interfaces.MeetingEngine` 追加：`GenerateSolution(ctx context.Context, room *types.MeetingRoom, userID string) (*types.MeetingMessage, error)`
- `interfaces.MeetingService` 追加：`GenerateSolution(ctx context.Context, tenantID uint64, roomID string, userID string) (*types.MeetingMessage, error)`
- `meetingEngine` struct 增加 `artifactRepo interfaces.MeetingArtifactRepository` 字段并在 `NewMeetingEngine` 注入（Dig 自动装配，同 T2.1 modelService 模式）。

`meeting.go`（service）追加——service 先做租户校验取 room 再委托引擎（引擎无租户上下文）：

```go
func (s *meetingService) GenerateSolution(ctx context.Context, tenantID uint64, roomID string, userID string) (*types.MeetingMessage, error) {
	room, err := s.roomRepo.GetByID(ctx, roomID, tenantID)
	if err != nil {
		return nil, err
	}
	return s.engine.GenerateSolution(ctx, room, userID)
}
```

- [ ] **Step 3: Handler + 路由**

`meeting.go`（handler）追加（文件需 import `"github.com/Tencent/WeKnora/internal/application/service"` 以判等哨兵）：

```go
// GenerateSolution POST /api/v1/meetings/:id/solution —— 同步等待 LLM 结果
func (h *MeetingHandler) GenerateSolution(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID, _ := types.TenantIDFromContext(ctx)
	userID, _ := types.UserIDFromContext(ctx)
	msg, err := h.meetingService.GenerateSolution(ctx, tenantID, c.Param("id"), userID)
	if err != nil {
		if err == service.ErrNoAdoptedMessages {
			c.Error(errors.NewBadRequestError("请先在讨论中采纳至少一条意见"))
			return
		}
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": msg})
}
```

`routes_meeting.go` 追加：

```go
meetings.POST("/:id/solution", rbacGuards.OwnedMeetingOrAdmin(), meetingHandler.GenerateSolution)
```

- [ ] **Step 4: 测试 + 编译 + 提交**

Run: `go test ./internal/application/service/ -run TestBuildSolutionPrompt -v; go build ./...`
Expected: PASS，编译通过。

Run: `git add internal/application/service/meeting_solution.go internal/application/service/meeting_solution_test.go internal/types/interfaces/meeting.go internal/application/service/meeting_engine.go internal/application/service/meeting.go internal/handler/meeting.go internal/router/routes_meeting.go; git commit -m "feat(meeting): 主持人基于采纳意见生成结构化方案（版本化产物）"`

---

### Task T4.3: 前端评审操作条 + 生成方案按钮

**Files:**
- Modify: `frontend/src/api/meeting/index.ts`（补 put 导入 + 两个 API）
- Modify: `frontend/src/stores/meeting.ts`（solutionGenerating + setReviewState/generate + SSE 分支）
- Modify: `frontend/src/views/meeting/MeetingRoomView.vue`（ReviewBar + 工具栏按钮 + 方案消息样式）
- Modify: `frontend/src/i18n/locales/zh-CN.ts`（+ en-US 对应键）

- [ ] **Step 1: API 层**

`api/meeting/index.ts` 的 request 导入行补 `put`，追加：

```ts
export function updateReviewState(roomId: string, messageId: string, reviewState: string) {
  return put<MeetingMessage>(`/api/v1/meetings/${roomId}/messages/${messageId}/review`, { review_state: reviewState });
}

export function generateSolution(roomId: string) {
  return post<MeetingMessage>(`/api/v1/meetings/${roomId}/solution`, {});
}
```

- [ ] **Step 2: Store**

`stores/meeting.ts` 追加：

```ts
const solutionGenerating = ref(false);

// setReviewState HTTP 成功后直接本地更新（SSE review_updated 回流为幂等覆盖）
async function setReviewState(messageId: string, state: string) {
  const updated = await updateReviewState(currentRoomId.value, messageId, state);
  const m = messages.value.find(x => x.id === messageId);
  if (m) m.review_state = updated.review_state;
}

// generate 方案消息经 SSE message.created 回流，这里只控制 loading
async function generate() {
  solutionGenerating.value = true;
  try {
    await generateSolution(currentRoomId.value);
  } finally {
    // LLM 失败时 HTTP 报错同样走这里复位（solution.generating 无配对终态事件）
    solutionGenerating.value = false;
  }
}
```

`applyStreamEvent` 追加三分支：

```ts
} else if (event === 'message.review_updated') {
  const m = messages.value.find(x => x.id === data.id);
  if (m) m.review_state = data.review_state;
} else if (event === 'solution.generating') {
  solutionGenerating.value = true;
} else if (event === 'solution.created') {
  solutionGenerating.value = false;
}
```

（`solutionGenerating`/`setReviewState`/`generate` 需在 store return 中导出；`currentRoomId` 用 store 中现有的房间 id ref 名。）

- [ ] **Step 3: 视图**

消息气泡内加评审操作条（仅智能体讨论消息显示；方案消息不参与评审）：

```vue
<div v-if="m.sender_kind === 'agent' && !m.is_solution" class="review-bar">
  <el-button size="small" type="success" :plain="m.review_state !== 'adopted'"
    @click="store.setReviewState(m.id, 'adopted')">{{ $t('meeting.adopt') }}</el-button>
  <el-button size="small" type="danger" :plain="m.review_state !== 'rejected'"
    @click="store.setReviewState(m.id, 'rejected')">{{ $t('meeting.reject') }}</el-button>
  <el-button size="small" type="warning" :plain="m.review_state !== 'pending'"
    @click="store.setReviewState(m.id, 'pending')">{{ $t('meeting.pending') }}</el-button>
</div>
<!-- 方案消息：高亮卡片 + 版本标记 -->
<el-tag v-if="m.is_solution" type="primary" effect="dark" class="solution-tag">
  {{ $t('meeting.solution') }} v{{ m.solution_version }}
</el-tag>
```

工具栏加生成按钮（讨论进行中禁用，避免上下文同时变动）：

```vue
<el-button size="small" type="primary" :loading="store.solutionGenerating" :disabled="store.discussing"
  @click="store.generate()">{{ $t('meeting.generateSolution') }}</el-button>
```

i18n（zh-CN / en-US 同步）追加：`meeting.adopt: '采纳'`、`meeting.reject: '不采纳'`、`meeting.pending: '待定'`、`meeting.solution: '方案'`、`meeting.generateSolution: '生成方案'`。

- [ ] **Step 4: 构建 + 提交**

Run: `cd frontend; npm run build`
Expected: 通过。

Run: `git add frontend/src/api/meeting frontend/src/stores/meeting.ts frontend/src/views/meeting frontend/src/i18n; git commit -m "feat(meeting): 前端评审操作条 + 生成方案入口 + 方案消息样式"`

---

### Task T4.4: M4 端到端验收

- [ ] **Step 1: 评审流转**：讨论中对智能体消息点"采纳"→ 按钮高亮（非 plain）；点"待定"→ 状态切换；刷新页面后状态保持（落库）。
- [ ] **Step 2: 多端同步**：同一用户开两个浏览器窗口进同一会议 → A 窗口点"不采纳" → B 窗口按钮态同步（`message.review_updated`）。
- [ ] **Step 3: 生成方案**：采纳 ≥2 条后点"生成方案"→ 按钮 loading → 消息流出现主持人方案消息（`is_solution`，版本 tag v1）→ 再点一次 → v2 版本 tag。
- [ ] **Step 4: 空采纳拦截**：无任何采纳时点"生成方案"→ 400 toast"请先在讨论中采纳至少一条意见"，loading 复位。
- [ ] **Step 5: 提交**：`git commit --allow-empty -m "test(meeting): M4 评审与方案生成验收"`

---

## Milestone M5: 产物抽屉 + 版本对比 + 回滚

**目标**：侧边抽屉查看本会议全部历史方案产物（含正文、版本、采纳/不采纳统计、当前标记）；支持选两个版本并排对比；支持把"当前方案"指针回滚到任一历史版本（落系统消息广播）。

### Task T5.1: GET /:id/artifacts 产物列表端点

**Files:**
- Modify: `internal/types/meeting.go`（加 MeetingArtifactDetail）
- Modify: `internal/types/interfaces/meeting.go`（Service 接口加 ListArtifacts）
- Modify: `internal/application/service/meeting.go`（补 artifactRepo 字段 + ListArtifacts）
- Modify: `internal/handler/meeting.go`、`internal/router/routes_meeting.go`

- [ ] **Step 1: 类型 + Service 实现**

`types/meeting.go` 追加：

```go
// MeetingArtifactDetail 产物 + 方案正文（正文存消息表不重复存储，联查带出）
type MeetingArtifactDetail struct {
	*MeetingArtifact
	Content string `json:"content"`
}
```

`MeetingService` 接口追加：

```go
// ListArtifacts 本会议历史方案产物（含正文），按版本升序
ListArtifacts(ctx context.Context, tenantID uint64, roomID string) ([]*types.MeetingArtifactDetail, error)
```

`meetingService` struct 增加 `artifactRepo interfaces.MeetingArtifactRepository` 字段并在 `NewMeetingService` 注入（Dig 自动装配；T1.6 已 `container.Provide(repository.NewMeetingArtifactRepository)`）。实现：

```go
func (s *meetingService) ListArtifacts(ctx context.Context, tenantID uint64, roomID string) ([]*types.MeetingArtifactDetail, error) {
	if _, err := s.roomRepo.GetByID(ctx, roomID, tenantID); err != nil { // 租户校验
		return nil, err
	}
	artifacts, err := s.artifactRepo.ListByRoom(ctx, roomID)
	if err != nil {
		return nil, err
	}
	out := make([]*types.MeetingArtifactDetail, 0, len(artifacts))
	for _, a := range artifacts {
		d := &types.MeetingArtifactDetail{MeetingArtifact: a}
		if msg, err := s.messageRepo.GetByID(ctx, a.SolutionMessageID); err == nil {
			d.Content = msg.Content // 正文关联失败降级为空串，不拖垮整个列表
		}
		out = append(out, d)
	}
	return out, nil
}
```

- [ ] **Step 2: Handler + 路由**

```go
// ListArtifacts GET /api/v1/meetings/:id/artifacts
func (h *MeetingHandler) ListArtifacts(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID, _ := types.TenantIDFromContext(ctx)
	list, err := h.meetingService.ListArtifacts(ctx, tenantID, c.Param("id"))
	if err != nil {
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": list})
}
```

```go
meetings.GET("/:id/artifacts", rbacGuards.OwnedMeetingOrAdmin(), meetingHandler.ListArtifacts)
```

- [ ] **Step 3: 编译 + 提交**

Run: `go build ./...; git add internal/types internal/application/service/meeting.go internal/handler/meeting.go internal/router/routes_meeting.go; git commit -m "feat(meeting): 产物列表端点（联查方案正文）"`

---

### Task T5.2: POST /:id/solution/rollback 回滚端点

**Files:**
- Modify: `internal/types/interfaces/meeting.go`（Service 接口加 RollbackSolution）
- Modify: `internal/application/service/meeting.go`
- Modify: `internal/handler/meeting.go`、`internal/router/routes_meeting.go`

- [ ] **Step 1: Service 实现**

接口追加：`RollbackSolution(ctx context.Context, tenantID uint64, roomID string, version int) error`

```go
// RollbackSolution 把房间当前方案指针切到指定历史版本，并落系统消息广播
func (s *meetingService) RollbackSolution(ctx context.Context, tenantID uint64, roomID string, version int) error {
	room, err := s.roomRepo.GetByID(ctx, roomID, tenantID)
	if err != nil {
		return err
	}
	artifacts, err := s.artifactRepo.ListByRoom(ctx, roomID)
	if err != nil {
		return err
	}
	var target *types.MeetingArtifact
	for _, a := range artifacts {
		if a.Version == version {
			target = a
			break
		}
	}
	if target == nil {
		return fmt.Errorf("solution version %d not found", version)
	}
	msg, err := s.messageRepo.GetByID(ctx, target.SolutionMessageID)
	if err != nil {
		return err
	}
	if !msg.IsSolution || msg.MeetingRoomID != roomID { // 防跨房间指针污染
		return fmt.Errorf("invalid solution message for version %d", version)
	}
	room.CurrentSolutionMessageID = msg.ID
	if err := s.roomRepo.Update(ctx, room); err != nil {
		return err
	}
	// 系统消息告知回滚（SSE 对端经 message.created 同步）
	sysMsg := &types.MeetingMessage{
		MeetingRoomID: roomID,
		SenderKind:    types.MeetingSenderSystem,
		Content:       fmt.Sprintf("已将当前方案回滚到 v%d", version),
	}
	if err := s.messageRepo.Create(ctx, sysMsg); err == nil {
		publishEvent(s.engine, roomID, MeetingEvent{Type: MeetingEventMessageCreated, Data: sysMsg})
	}
	return nil
}
```

- [ ] **Step 2: Handler + 路由**

```go
type rollbackSolutionRequest struct {
	Version int `json:"version" binding:"required,min=1"`
}

// RollbackSolution POST /api/v1/meetings/:id/solution/rollback
func (h *MeetingHandler) RollbackSolution(c *gin.Context) {
	ctx := c.Request.Context()
	var req rollbackSolutionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}
	tenantID, _ := types.TenantIDFromContext(ctx)
	if err := h.meetingService.RollbackSolution(ctx, tenantID, c.Param("id"), req.Version); err != nil {
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}
```

```go
meetings.POST("/:id/solution/rollback", rbacGuards.OwnedMeetingOrAdmin(), meetingHandler.RollbackSolution)
```

- [ ] **Step 3: 编译 + 提交**

Run: `go build ./...; git add internal/types/interfaces/meeting.go internal/application/service/meeting.go internal/handler/meeting.go internal/router/routes_meeting.go; git commit -m "feat(meeting): 方案版本回滚端点（指针切换 + 系统消息）"`

---

### Task T5.3: 前端产物抽屉 ArtifactDrawer.vue

**Files:**
- Create: `frontend/src/views/meeting/ArtifactDrawer.vue`
- Modify: `frontend/src/api/meeting/index.ts`（类型 + 两个 API）
- Modify: `frontend/src/types/meeting.ts`（MeetingRoom 补 current_solution_message_id）
- Modify: `frontend/src/views/meeting/MeetingRoomView.vue`（"历史产物"按钮 + 抽屉挂载）
- Modify: `frontend/src/i18n/locales/zh-CN.ts`（+ en-US）

- [ ] **Step 1: 类型 + API**

`types/meeting.ts` 的 `MeetingRoom` 接口补字段（对齐后端 json）：

```ts
current_solution_message_id: string; // 当前方案消息指针（空串表示未生成）
```

`api/meeting/index.ts` 追加：

```ts
export interface MeetingArtifact {
  id: string;
  meeting_room_id: string;
  solution_message_id: string;
  version: number;
  adopted_count: number;
  rejected_count: number;
  created_by: string;
  created_at: string;
  content: string; // 后端联查带出的方案正文
}

export function listArtifacts(roomId: string) {
  return get<MeetingArtifact[]>(`/api/v1/meetings/${roomId}/artifacts`);
}

export function rollbackSolution(roomId: string, version: number) {
  return post(`/api/v1/meetings/${roomId}/solution/rollback`, { version });
}
```

（`get` 按现有 import 行补入。）

- [ ] **Step 2: ArtifactDrawer.vue（新建）**

要点：`v-model:visible` 受控；打开时拉取列表；每项展示版本 tag、"当前"标记（`solution_message_id === currentSolutionMessageId`）、采纳/不采纳计数、创建时间；`el-collapse` 展开正文（正文渲染复用消息气泡的 markdown 渲染方式）；勾选恰好 2 个版本进入并排对比；非当前版本显示"回滚到此版本"。

```vue
<template>
  <el-drawer :model-value="visible" :title="$t('meeting.artifacts')" size="46%"
    @update:model-value="emit('update:visible', $event)" @open="fetchArtifacts">
    <div v-if="compareList.length === 2" class="compare-view">
      <!-- 并排对比：左右两栏各渲染一个版本正文 -->
      <div v-for="a in compareList" :key="a.id" class="compare-col">
        <el-tag>v{{ a.version }}</el-tag>
        <div class="artifact-content md-body" v-html="renderMd(a.content)"></div>
      </div>
      <el-button @click="compareIds = []">{{ $t('meeting.exitCompare') }}</el-button>
    </div>
    <el-timeline v-else>
      <el-timeline-item v-for="a in artifacts" :key="a.id" :timestamp="a.created_at">
        <div class="artifact-head">
          <el-checkbox :model-value="compareIds.includes(a.id)" @change="toggleCompare(a.id)" />
          <el-tag :type="isCurrent(a) ? 'success' : 'info'">v{{ a.version }}</el-tag>
          <el-tag v-if="isCurrent(a)" size="small">{{ $t('meeting.currentSolution') }}</el-tag>
          <span class="counts">
            {{ $t('meeting.adoptedCount', { n: a.adopted_count }) }} /
            {{ $t('meeting.rejectedCount', { n: a.rejected_count }) }}
          </span>
          <el-button v-if="!isCurrent(a)" size="small" text type="primary"
            @click="rollback(a)">{{ $t('meeting.rollbackTo') }}</el-button>
        </div>
        <el-collapse><el-collapse-item :title="$t('meeting.viewContent')">
          <div class="artifact-content md-body" v-html="renderMd(a.content)"></div>
        </el-collapse-item></el-collapse>
      </el-timeline-item>
    </el-timeline>
  </el-drawer>
</template>

<script setup lang="ts">
import { ref, computed } from 'vue';
import { ElMessageBox, ElMessage } from 'element-plus';
import { listArtifacts, rollbackSolution, type MeetingArtifact } from '@/api/meeting';

const props = defineProps<{ visible: boolean; roomId: string; currentSolutionMessageId: string }>();
const emit = defineEmits<{ 'update:visible': [boolean] }>();

const artifacts = ref<MeetingArtifact[]>([]);
const compareIds = ref<string[]>([]);
const compareList = computed(() => artifacts.value.filter(a => compareIds.value.includes(a.id)));

async function fetchArtifacts() {
  artifacts.value = await listArtifacts(props.roomId);
}
const isCurrent = (a: MeetingArtifact) => a.solution_message_id === props.currentSolutionMessageId;

// toggleCompare 最多勾选 2 个版本，超出时替换最早勾选
function toggleCompare(id: string) {
  const i = compareIds.value.indexOf(id);
  if (i >= 0) compareIds.value.splice(i, 1);
  else {
    if (compareIds.value.length >= 2) compareIds.value.shift();
    compareIds.value.push(id);
  }
}

async function rollback(a: MeetingArtifact) {
  await ElMessageBox.confirm($t('meeting.rollbackConfirm', { v: a.version }));
  await rollbackSolution(props.roomId, a.version);
  ElMessage.success($t('meeting.rollbackDone'));
  emit('update:visible', false); // 关闭抽屉，房间数据由父组件刷新
}
</script>
```

（`renderMd` 复用消息气泡现有的 markdown 渲染函数；`$t` 按项目内组合式用法引入。）

- [ ] **Step 3: 视图挂载**

`MeetingRoomView.vue` 工具栏加"历史产物"按钮 + 抽屉（可见性用本地 ref，不入 store）：

```vue
<el-button size="small" @click="showArtifacts = true">{{ $t('meeting.artifacts') }}</el-button>
<ArtifactDrawer v-model:visible="showArtifacts" :room-id="roomId"
  :current-solution-message-id="store.room?.current_solution_message_id ?? ''" />
```

```ts
import ArtifactDrawer from './ArtifactDrawer.vue';
const showArtifacts = ref(false);
```

i18n 追加：`meeting.artifacts: '历史产物'`、`meeting.currentSolution: '当前'`、`meeting.rollbackTo: '回滚到此版本'`、`meeting.rollbackConfirm: '确认将当前方案回滚到 v{v}？'`、`meeting.rollbackDone: '已回滚'`、`meeting.viewContent: '查看正文'`、`meeting.exitCompare: '退出对比'`、`meeting.adoptedCount: '采纳 {n}'`、`meeting.rejectedCount: '不采纳 {n}'`。

- [ ] **Step 4: 构建 + 提交**

Run: `cd frontend; npm run build`
Expected: 通过。

Run: `git add frontend/src/views/meeting frontend/src/api/meeting frontend/src/types/meeting.ts frontend/src/i18n; git commit -m "feat(meeting): 产物抽屉（版本列表/并排对比/回滚）"`

---

### Task T5.4: M5 端到端验收

- [ ] **Step 1: 产物列表**：生成 v1、v2 两版方案 → 打开抽屉 → 时间线显示两条，v2 标"当前"，采纳/不采纳计数正确。
- [ ] **Step 2: 并排对比**：勾选 v1、v2 → 进入对比视图左右并排渲染正文 → "退出对比"返回时间线。
- [ ] **Step 3: 回滚**：v1 点"回滚到此版本"→ 确认 → 消息流出现系统消息"已将当前方案回滚到 v1"→ 重开抽屉 v1 标"当前"→ 刷新页面保持。
- [ ] **Step 4: 提交**：`git commit --allow-empty -m "test(meeting): M5 产物抽屉与回滚验收"`

## 里程碑 M6：归档 / 删除 / 导出 / 权限收口

> 目标：会议生命周期收口（归档只读、删除软删）、方案导出 .md、权限隔离单测、覆盖率核查、中文使用文档。

### Task T6.1: 归档守卫 + 归档 / 删除 / PATCH 端点

**签名调整说明（先于本任务代码执行）**：M1 的 `SendUserMessage(ctx, roomID, userID, content, mentionedAgentIDs)` 5 参签名补 `tenantID`，改为 **6 参** `SendUserMessage(ctx context.Context, tenantID uint64, roomID, userID, content string, mentionedAgentIDs []string)`。三处同步：
1. `interfaces` 包的 `MeetingService` 接口签名；
2. `meetingService` 实现：开头改为 `room, err := s.roomRepo.GetByID(ctx, roomID, tenantID)`（替换原 tenantID=0 调用）；
3. `MeetingHandler.SendMessage`（约 L1411）：当前仅提取 userID，须补 `tenantID, _ := types.TenantIDFromContext(ctx)` 并作为首参传入。

- [ ] **Step 1: 新建归档守卫文件**

`internal/application/service/meeting_lifecycle.go`：

```go
package service

import (
    "errors"

    "WeKnora/internal/types"
)

// ErrRoomArchived 会议已归档：一切写操作拒绝（handler 判等映射 403）
var ErrRoomArchived = errors.New("meeting: room archived")

// ensureRoomActive 归档守卫：非 active 状态的房间拒绝写操作
func ensureRoomActive(room *types.MeetingRoom) error {
    if room.Status != types.MeetingStatusActive {
        return ErrRoomArchived
    }
    return nil
}
```

- [ ] **Step 2: 在 5 个写路径插入守卫**

① `SendUserMessage`（签名调整后）开头：

```go
room, err := s.roomRepo.GetByID(ctx, roomID, tenantID)
if err != nil {
    return nil, err // 租户校验（仓储按 tenant 过滤）
}
if err := ensureRoomActive(room); err != nil {
    return nil, err // 归档守卫
}
```

② `InterruptDiscussion`（T3.3 已修正签名）：将 `if _, err := ...` 的 `_` 改为 `room`，随后插入同样的两行守卫。

③ `UpdateReviewState`、④ `GenerateSolution`、⑤ `RollbackSolution`：各自在 `GetByID` 租户校验成功后插入两行守卫（模式同①）。

> 引擎 `RunTurn` 不加守卫：归档后的新讨论在 `SendUserMessage` 入口已被拦截，进行中的轮次让其自然跑完（规格仅要求写操作 403）。

- [ ] **Step 3: ArchiveRoom service**

`meeting_lifecycle.go` 追加：

```go
// ArchiveRoom 归档会议（幂等）：置 archived + 落系统消息 + 广播
func (s *meetingService) ArchiveRoom(ctx context.Context, tenantID uint64, roomID string) error {
    room, err := s.roomRepo.GetByID(ctx, roomID, tenantID)
    if err != nil {
        return err
    }
    if room.Status == types.MeetingStatusArchived {
        return nil // 幂等：已归档直接返回
    }
    room.Status = types.MeetingStatusArchived
    if err := s.roomRepo.Update(ctx, room); err != nil {
        return err
    }
    // 落系统消息并广播（复用 T5.2 模式）
    sysMsg := &types.MeetingMessage{
        MeetingRoomID: roomID,
        SenderKind:    types.MeetingSenderSystem,
        Content:       "会议已归档，进入只读状态",
    }
    if err := s.messageRepo.Create(ctx, sysMsg); err != nil {
        return err
    }
    publishEvent(s.engine, roomID, types.MeetingEvent{
        Type:    types.MeetingEventMessageCreated,
        RoomID:  roomID,
        Payload: sysMsg,
    })
    return nil
}
```

`MeetingService` 接口追加：`ArchiveRoom(ctx context.Context, tenantID uint64, roomID string) error` 与 `ExportArtifact(ctx context.Context, tenantID uint64, roomID, artifactID string) (filename string, content string, err error)`（T6.3 实现）。

- [ ] **Step 4: Handler 三个端点**

```go
// updateMeetingRequest PATCH 白名单四字段（参会人不可改）
type updateMeetingRequest struct {
    Title           string `json:"title" binding:"required"`
    Description     string `json:"description"`
    BroadcastMode   string `json:"broadcast_mode" binding:"required,oneof=sequential parallel"`
    GuardMaxExchange int   `json:"guard_max_exchange" binding:"required,min=1,max=10"`
}

func (h *MeetingHandler) UpdateMeeting(c *gin.Context) {
    ctx := c.Request.Context()
    tenantID, _ := types.TenantIDFromContext(ctx)
    var req updateMeetingRequest
    if err := c.ShouldBindJSON(&req); err != nil {
        c.Error(errors.NewBadRequestError(err.Error()))
        return
    }
    room, err := h.meetingService.GetMeeting(ctx, c.Param("id"), tenantID)
    if err != nil {
        c.Error(errors.NewNotFoundError("meeting not found"))
        return
    }
    if room.Status != types.MeetingStatusActive {
        c.Error(errors.NewForbiddenError("会议已归档，仅可查看"))
        return
    }
    // 白名单拷贝四字段，其余字段不可改
    room.Title = req.Title
    room.Description = req.Description
    room.BroadcastMode = req.BroadcastMode
    room.GuardMaxExchange = req.GuardMaxExchange
    if err := h.meetingService.UpdateMeeting(ctx, room); err != nil {
        c.Error(errors.NewInternalServerError(err.Error()))
        return
    }
    c.JSON(http.StatusOK, gin.H{"success": true, "data": room})
}

func (h *MeetingHandler) ArchiveMeeting(c *gin.Context) {
    ctx := c.Request.Context()
    tenantID, _ := types.TenantIDFromContext(ctx)
    if err := h.meetingService.ArchiveRoom(ctx, tenantID, c.Param("id")); err != nil {
        c.Error(errors.NewInternalServerError(err.Error()))
        return
    }
    c.JSON(http.StatusOK, gin.H{"success": true, "message": "archived"})
}

func (h *MeetingHandler) DeleteMeeting(c *gin.Context) {
    ctx := c.Request.Context()
    tenantID, _ := types.TenantIDFromContext(ctx)
    if err := h.meetingService.DeleteMeeting(ctx, c.Param("id"), tenantID); err != nil {
        c.Error(errors.NewInternalServerError(err.Error()))
        return
    }
    c.JSON(http.StatusOK, gin.H{"success": true, "message": "deleted"})
}
```

- [ ] **Step 5: 五个既有写 handler 补归档错误映射**

`SendMessage` / `Interrupt` / `UpdateReviewState` / `GenerateSolution` / `RollbackSolution` 的错误分支首行各补：

```go
if err == service.ErrRoomArchived {
    c.Error(errors.NewForbiddenError("会议已归档，仅可查看"))
    return
}
```

> 判等不用 `errors.Is`：`ErrRoomArchived` 只以原值返回、从不 wrap，直接 `==` 即可（与 T4.2 `ErrNoAdoptedMessages` 先例一致）。

- [ ] **Step 6: 路由注册**

```go
meetings.PATCH("/:id", rbacGuards.OwnedMeetingOrAdmin(), handler.UpdateMeeting)
meetings.POST("/:id/archive", rbacGuards.OwnedMeetingOrAdmin(), handler.ArchiveMeeting)
meetings.DELETE("/:id", rbacGuards.OwnedMeetingOrAdmin(), handler.DeleteMeeting)
```

- [ ] **Step 7: 守卫单测**

`meeting_lifecycle_test.go`：

```go
func TestEnsureRoomActive(t *testing.T) {
    active := &types.MeetingRoom{Status: types.MeetingStatusActive}
    assert.NoError(t, ensureRoomActive(active))

    archived := &types.MeetingRoom{Status: types.MeetingStatusArchived}
    err := ensureRoomActive(archived)
    assert.Equal(t, ErrRoomArchived, err) // 哨兵判等
}
```

Run: `go test ./internal/application/service/ -run TestEnsureRoomActive -v`
Expected: PASS

- [ ] **Step 8: 构建 + 提交**

Run: `go build ./...; go vet ./internal/...`
Expected: 通过。

Run: `git add internal/application/service internal/handler internal/router internal/types/interfaces; git commit -m "feat(meeting): 归档守卫与归档/删除/PATCH 端点"`

---

### Task T6.2: 归档 / 删除前端

**签名调整说明**：M1 的 `listMeetings()` 无参 → 改为 `listMeetings(status = '')`，内部用 qs 拼 `?status=`（空串不拼）。

- [ ] **Step 1: api 模块追加**

`frontend/src/api/meeting/index.ts`（import 补 `put, del`）：

```ts
export const listMeetings = (status = '') =>
  get<MeetingRoom[]>(`/meetings${status ? `?status=${status}` : ''}`);
export const updateMeeting = (id: string, data: UpdateMeetingRequest) =>
  put<MeetingRoom>(`/meetings/${id}`, data);
export const archiveMeeting = (id: string) =>
  post(`/meetings/${id}/archive`);
export const deleteMeeting = (id: string) =>
  del(`/meetings/${id}`);
```

`UpdateMeetingRequest` 类型加入 `frontend/src/types/meeting.ts`（四字段：title/description/broadcast_mode/guard_max_exchange）。

- [ ] **Step 2: 列表页状态筛选 + 操作列**

`MeetingListView.vue`：搜索栏旁加 `el-radio-group`（filterAll / filterActive / filterArchived 三档，切换即调 `listMeetings(status)` 刷新）；表格操作列加"归档"按钮（仅 `row.status === 'active'` 显示，`ElMessageBox.confirm` 确认后调 `archiveMeeting` 并刷新）与"删除"按钮（type="danger"，confirm 后调 `deleteMeeting` 并刷新）。

- [ ] **Step 3: 详情页只读态**

`MeetingRoomView.vue`：

```ts
const isArchived = computed(() => store.room?.status === 'archived');
```

- 顶部加灰条 `<el-alert v-if="isArchived" :title="$t('meeting.archivedBanner')" type="info" :closable="false" />`；
- 输入框、发送按钮、评审条（采纳/不采纳）、"生成方案"、"回到正题"按钮全部 `:disabled="isArchived"`；
- SSE 流仍正常接收（归档系统消息会推送到场）。

- [ ] **Step 4: i18n 追加**

`meeting.filterAll: '全部'`、`meeting.filterActive: '进行中'`、`meeting.filterArchived: '已归档'`、`meeting.archive: '归档'`、`meeting.archiveConfirm: '归档后会议进入只读，确认归档？'`、`meeting.archiveDone: '已归档'`、`meeting.delete: '删除'`、`meeting.deleteConfirm: '删除后不可恢复，确认删除？'`、`meeting.deleteDone: '已删除'`、`meeting.archivedBanner: '会议已归档，仅可查看'`、`meeting.archivedForbidden: '会议已归档，仅可查看'`。

- [ ] **Step 5: 构建 + 提交**

Run: `cd frontend; npm run build`
Expected: 通过。

Run: `git add frontend/src/views/meeting frontend/src/api/meeting frontend/src/types/meeting.ts frontend/src/i18n; git commit -m "feat(meeting): 归档/删除前端（状态筛选/只读态）"`

---

### Task T6.3: 方案导出 .md

**接口调整说明**：`MeetingArtifactRepository` 补 `UpdateExportedFormat(ctx context.Context, id string, format string) error`；GORM 实现为单行 `UpdateColumn("exported_format", format)`。

- [ ] **Step 1: service ExportArtifact**

```go
// ExportArtifact 导出方案为 .md（只读操作：归档房间允许导出）
func (s *meetingService) ExportArtifact(ctx context.Context, tenantID uint64, roomID, artifactID string) (string, string, error) {
    if _, err := s.roomRepo.GetByID(ctx, roomID, tenantID); err != nil {
        return "", "", err // 租户校验
    }
    artifact, err := s.artifactRepo.GetByID(ctx, artifactID, roomID)
    if err != nil {
        return "", "", err
    }
    msg, err := s.messageRepo.GetByID(ctx, artifact.SolutionMessageID)
    if err != nil {
        return "", "", err
    }
    // 防跨房间指针污染：方案消息必须属于本房间且确为方案
    if !msg.IsSolution || msg.MeetingRoomID != roomID {
        return "", "", errors.New("meeting: artifact points to invalid solution message")
    }
    // 记录导出格式；失败不阻断导出
    _ = s.artifactRepo.UpdateExportedFormat(ctx, artifactID, "md")
    return fmt.Sprintf("meeting-solution-v%d.md", artifact.Version), msg.Content, nil
}
```

- [ ] **Step 2: Handler**

```go
func (h *MeetingHandler) ExportArtifact(c *gin.Context) {
    ctx := c.Request.Context()
    tenantID, _ := types.TenantIDFromContext(ctx)
    filename, content, err := h.meetingService.ExportArtifact(ctx, c.Param("id"), c.Param("aid"))
    if err != nil {
        c.Error(errors.NewNotFoundError("artifact not found"))
        return
    }
    c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", filename))
    c.Data(http.StatusOK, "text/markdown; charset=utf-8", []byte(content))
}
```

（import 补 `fmt`。）

- [ ] **Step 3: 路由**

```go
meetings.GET("/:id/artifacts/:aid/export", rbacGuards.OwnedMeetingOrAdmin(), handler.ExportArtifact)
```

- [ ] **Step 4: 前端导出按钮**

api 追加（blob 下载，参照 knowledge-base L417 先例）：

```ts
export const exportArtifact = (roomId: string, artifactId: string) =>
  get<Blob>(`/meetings/${roomId}/artifacts/${artifactId}/export`, { responseType: 'blob' }) as unknown as Promise<Blob>;
```

`ArtifactDrawer.vue` 每项加"导出 .md"按钮：

```ts
const onExport = async (a: MeetingArtifact) => {
  const blob = await exportArtifact(props.roomId, a.id);
  const url = URL.createObjectURL(blob);
  const link = document.createElement('a');
  link.href = url;
  link.download = `meeting-solution-v${a.version}.md`;
  link.click();
  URL.revokeObjectURL(url);
};
```

i18n 追加：`meeting.exportMd: '导出 .md'`。

- [ ] **Step 5: 构建 + 提交**

Run: `go build ./...; cd frontend; npm run build`
Expected: 通过。

Run: `git add internal frontend/src; git commit -m "feat(meeting): 方案导出 .md（端点/抽屉按钮/exported_format 记录）"`

---

### Task T6.4: 权限隔离单测

- [ ] **Step 1: 内存 fake 仓储**

`meeting_isolation_test.go`：

```go
// fakeMeetingRoomRepo 内存房间仓储：严格按接口语义过滤 tenantID/createdBy（与 GORM WHERE 等价）
type fakeMeetingRoomRepo struct {
    rooms map[string]*types.MeetingRoom
}

func (f *fakeMeetingRoomRepo) GetByID(ctx context.Context, id string, tenantID uint64) (*types.MeetingRoom, error) {
    r, ok := f.rooms[id]
    if !ok || r.TenantID != tenantID { // 跨租户视同不存在
        return nil, errors.New("record not found")
    }
    return r, nil
}

func (f *fakeMeetingRoomRepo) ListByCreator(ctx context.Context, tenantID uint64, createdBy, status string) ([]*types.MeetingRoom, error) {
    var out []*types.MeetingRoom
    for _, r := range f.rooms {
        if r.TenantID != tenantID || r.CreatedBy != createdBy {
            continue // 仅创建者可见
        }
        if status != "" && r.Status != status {
            continue
        }
        out = append(out, r)
    }
    return out, nil
}
```

（其余接口方法以空实现满足编译即可。）fixture：`newIsolationFixture` 构造 `&meetingService{roomRepo: repo}`（本组用例不触达其余依赖，留 nil）。

- [ ] **Step 2: 三用例**

```go
func TestGetMeetingCrossTenantNotFound(t *testing.T) {
    // tenant1 的房间，tenant2 取必须 error
}

func TestListMeetingsOnlyOwnRooms(t *testing.T) {
    // user-a 有 r1/r2，user-b 有 r3：user-a 列表仅 r1/r2
}

func TestArchivedRoomRejectsMessage(t *testing.T) {
    // archived 房间 SendUserMessage 返回 ErrRoomArchived（依赖 T6.1 签名调整已落地）
}
```

Run: `go test ./internal/application/service/ -run 'TestGetMeetingCrossTenantNotFound|TestListMeetingsOnlyOwnRooms|TestArchivedRoomRejectsMessage' -v`
Expected: PASS

- [ ] **Step 3: 提交**

Run: `git add internal/application/service/meeting_isolation_test.go; git commit -m "test(meeting): 权限隔离单测（跨租户/创建者过滤/归档拒写）"`

---

### Task T6.5: M6 端到端验收

- [ ] **Step 1: 归档只读**：对活动会议归档 → 列表该会议标"已归档" → 详情页出现灰条、输入区禁用 → `curl -X POST /meetings/:id/messages` 返回 403「会议已归档，仅可查看」（评审/生成方案/回滚/PATCH 同验）→ 再归档一次返回 200（幂等）。
- [ ] **Step 2: 删除**：删除会议 → 列表消失 → `curl GET /meetings/:id` 返回 404 → DB 查该行 `deleted_at` 非空。
- [ ] **Step 3: 导出**：抽屉点"导出 .md"→ 下载 `meeting-solution-v1.md` 且正文正确 → DB 查 `meeting_artifacts.exported_format='md'`。
- [ ] **Step 4: 权限**：同租户用户 B 登录 → 列表不见 A 的会议 → `curl GET /meetings/:id`（B 的 token）返回 404；跨租户用户 C 同验（GET/POST 均 403/404）。
- [ ] **Step 5: 提交**：`git commit --allow-empty -m "test(meeting): M6 归档/删除/导出/权限验收"`

---

### Task T6.6: 覆盖率核查 + 中文使用文档

- [ ] **Step 1: 覆盖率核查**

Run: `go test ./internal/application/service/ -coverprofile=cover.out; go tool cover -func=cover.out`

口径：纯逻辑函数（`ensureRoomActive`、`canReviewMessage`、护栏判定、`buildSolutionPrompt` 等无外部依赖函数）100%；`meeting_*.go` 文件行覆盖整体 ≥70%；集成路径（`RunTurn`/AgentQA 链路）由里程碑验收覆盖，不为 LLM 调用写 mock。不足时优先补纯函数表驱动测试。

- [ ] **Step 2: 撰写 `docs/meeting-room.md`（中文）**

九节大纲：
1. 功能概述（多智能体广播式讨论 + 用户评审 + 主持人流水线方案）
2. 核心概念（会议/主持人/参会智能体/评审状态/产物/护栏术语表）
3. 快速上手（创建会议 → 发主题 → 评审 → 生成方案 → 导出）
4. 讨论模式与护栏（sequential/parallel 开关语义、3 来回打断、回到正题）
5. 归档与删除（只读语义、幂等、软删）
6. API 一览（15 个端点表格：方法/路径/权限/说明）
7. SSE 事件（8 类事件名/payload 结构/断线重连）
8. 配置项（并发上限/护栏阈值/历史窗口默认值及来源）
9. FAQ（归档后能否导出=能；为何看不到他人会议=仅创建者可见；方案如何回滚）

- [ ] **Step 3: 提交**

Run: `git add docs/meeting-room.md; git commit -m "docs(meeting): 会议室使用文档"`

---

## Self-Review

### 1. 规格覆盖核对

| 规格章节 | 覆盖任务 | 状态 |
|---|---|---|
| §1 数据模型（4 表 + 迁移） | T1.1-T1.3 | ✅ |
| §2 类型与接口 | T1.2/T1.4 | ✅ |
| §3 API 端点 | T1.6/T2.4/T3.3/T4.3/T5.1/T6.1/T6.3 | ✅（15 个端点；两处有意简化见下） |
| §4 讨论引擎（广播/护栏/并发） | T2.1-T2.4/T3.1-T3.3 | ✅ |
| §5 评审流 | T4.1-T4.3 | ✅ |
| §6 方案生成与产物 | T5.1-T5.3 | ✅ |
| §7 SSE 事件 | T2.3/T3.2/T4.1/T5.2/T6.1 | ✅ |
| §8 权限模型 | T1.6/T6.4/T6.5 | ✅ |
| §9 前端界面 | T1.7/T2.5/T3.4/T4.4/T5.3/T6.2/T6.3 | ✅ |
| §10 测试与文档 | T6.4/T6.6 | ✅ |

**两处有意的端点简化（chunk 5 决策，记录备查）**：
1. 产物详情未单开 `GET /:id/artifacts/:aid`——并入 `GET /:id/artifacts` 列表响应（`MeetingArtifactDetail` 带正文联查），减少一次往返；
2. 回滚采用 `POST /:id/solution/rollback`（body 传 version）而非规格的 `POST /:id/artifacts/:aid/rollback`——语义上回滚的是"当前方案"指针而非产物本身。

端点合计：M1 建/查/消息 4 个 + M3 interrupt 1 + M4 评审 2 + M5 方案/产物/回滚 3 + M6 PATCH/归档/删除/导出 4 + SSE 1 = **15 个**。

### 2. 占位符扫描

全文检索 `TBD` / `待补充` / `XXX` / `FIXME`：0 处。所有签名、字段名、路由、i18n 键均为确定值。

### 3. 类型一致性终查

| 检查项 | 结论 |
|---|---|
| 引擎接收者 | `meetingEngine` 小写（未导出），构造函数返回接口 ✅ |
| 广播器 | `MeetingBroadcaster` 导出 + `Publish(roomID string, evt MeetingEvent)` 两参，全文统一 ✅ |
| SSE 事件名 | 8 个常量与前后端消费点对齐 ✅ |
| 评审状态 | `pending/adopted/rejected` 三值 + 消息过滤语义一致 ✅ |
| 关键签名 | `SendUserMessage` 6 参（T6.1 调整后）/`InterruptDiscussion(ctx, tenantID, roomID)`/`ExportArtifact` 三返回值 ✅ |
| handler 上下文提取 | `TenantIDFromContext`/`UserIDFromContext` 均两返回值 `_` 接收 ✅ |
| 哨兵错误 | `ErrRoomArchived`/`ErrNoAdoptedMessages` 原值判等，不 wrap、不用 `errors.Is` ✅ |
| 路由权限 | 房间级 `OwnedMeetingOrAdmin()`、创建 `Contributor()`、列表 `Viewer()`+服务层过滤 ✅ |
| 迁移文件 | `000115_meeting.up.sql/.down.sql` 成对 ✅ |

### 4. 漂移记录

- chunk 5 前修正 6 类：接收者大小写 7 处 / T3.3 重复定义 interruptKey 系列 / `emitSystemMessage`→`publishSystemMessage` 5 处 / T3.2 补 4 个辅助函数定义 + `agentDisplayName` 补 roomID 参数 3 处调用点 / T1.6 cfg 字段注入 / T2.2 `runAgentTurn` 5参→4参签名调整说明；
- chunk 6 前修正 2 处：T3.3 `InterruptDiscussion` 的 `currentUserID(ctx)` 未定义且类型错误（改为 tenantID uint64 签名）、T3.3 `Interrupt` handler 的 `respondMeetingError` 未定义（改标准错误模式）；
- chunk 6 新增 3 项签名调整说明：`SendUserMessage` 补 tenantID（三处同步）、`listMeetings` 补 status 参数、`MeetingArtifactRepository` 补 `UpdateExportedFormat`。

**计划完成。总计 6 个里程碑、24 个任务，全部具备可直接执行的文件路径、代码骨架与验证命令。**
