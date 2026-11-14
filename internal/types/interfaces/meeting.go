package interfaces

import (
	"context"
	"io"

	"github.com/Tencent/WeKnora/internal/types"
)

// MeetingRoomRepository 会议室存储（全部按 tenant_id+created_by 过滤，仅创建者可见）
type MeetingRoomRepository interface {
	Create(ctx context.Context, room *types.MeetingRoom) error
	GetByID(ctx context.Context, id string, tenantID uint64) (*types.MeetingRoom, error)
	// GetByIDInternal 引擎内部使用：不校验 created_by（讨论轮是系统行为），仍按 tenant 隔离由调用方保证
	GetByIDInternal(ctx context.Context, id string) (*types.MeetingRoom, error)
	ListByCreator(ctx context.Context, tenantID uint64, createdBy string, status string, offset, limit int) ([]*types.MeetingRoom, int64, error)
	Update(ctx context.Context, room *types.MeetingRoom) error
	Delete(ctx context.Context, id string, tenantID uint64) error // 软删
}

type MeetingParticipantRepository interface {
	CreateBatch(ctx context.Context, ps []*types.MeetingParticipant) error
	ListByRoom(ctx context.Context, roomID string) ([]*types.MeetingParticipant, error)
	CountAgentsByRoom(ctx context.Context, roomID string) (int64, error)
	// DeleteAgentsByRoom 全量替换前置清理：仅删 agent 类参会者，host 记录始终保留
	DeleteAgentsByRoom(ctx context.Context, roomID string) error
}

type MeetingMessageRepository interface {
	Create(ctx context.Context, msg *types.MeetingMessage) error
	GetByID(ctx context.Context, id string) (*types.MeetingMessage, error)
	// ListByRoom 游标分页：beforeID 为空取最新 limit 条；否则取该消息之前更早的 limit 条。返回正序（旧->新）。
	ListByRoom(ctx context.Context, roomID string, beforeID string, limit int) ([]*types.MeetingMessage, error)
	// ListRecent 取最近 limit 条（正序），供 LLM 上下文使用
	ListRecent(ctx context.Context, roomID string, limit int) ([]*types.MeetingMessage, error)
	// ListByAgendaItem 取某议程项下最近 limit 条消息（正序），供按议题聚焦上下文
	ListByAgendaItem(ctx context.Context, roomID string, agendaItemID string, limit int) ([]*types.MeetingMessage, error)
	UpdateReviewState(ctx context.Context, id string, state string) error
	CountByRoom(ctx context.Context, roomID string) (int64, error)
	CountByReviewStates(ctx context.Context, roomID string, states []string) (int64, error)
	MaxSolutionVersion(ctx context.Context, roomID string) (int, error)
	ListByReviewState(ctx context.Context, roomID string, state string) ([]*types.MeetingMessage, error)
}

// MeetingAgendaRepository 议程项存储（按房间隔离，seq 唯一）
type MeetingAgendaRepository interface {
	// ReplaceByRoom 全量替换某房间的议程（开场澄清重出时重建）
	ReplaceByRoom(ctx context.Context, roomID string, items []*types.MeetingAgendaItem) error
	ListByRoom(ctx context.Context, roomID string) ([]*types.MeetingAgendaItem, error)
	// Active 返回当前讨论中的议程项；无 active 时返回 nil
	Active(ctx context.Context, roomID string) (*types.MeetingAgendaItem, error)
	Update(ctx context.Context, item *types.MeetingAgendaItem) error
	// Create 追加单个议程项：seq 由仓储按房间内 MAX(seq)+1 分配（规避 UNIQUE(room,seq) 冲突）
	Create(ctx context.Context, item *types.MeetingAgendaItem) error
}

type MeetingArtifactRepository interface {
	Create(ctx context.Context, a *types.MeetingArtifact) error
	GetByID(ctx context.Context, id string, roomID string) (*types.MeetingArtifact, error)
	ListByRoom(ctx context.Context, roomID string) ([]*types.MeetingArtifact, error)
	// UpdateExportedFormat 记录产物的导出格式（如 md）
	UpdateExportedFormat(ctx context.Context, id string, format string) error
	Update(ctx context.Context, a *types.MeetingArtifact) error
	// MaxVersionByRoom 房间内产物最大版本号（solution 与 generated 共用序列，规避 UNIQUE(room,version) 冲突）
	MaxVersionByRoom(ctx context.Context, roomID string) (int, error)
}

// MeetingService 会议室业务服务
type MeetingService interface {
	CreateMeeting(ctx context.Context, room *types.MeetingRoom, agentIDs []string) (*types.MeetingRoom, error)
	GetMeeting(ctx context.Context, tenantID uint64, id string) (*types.MeetingRoom, []*types.MeetingParticipant, error)
	ListMeetings(ctx context.Context, tenantID uint64, createdBy string, status string, offset, limit int) ([]*types.MeetingRoom, int64, error)
	UpdateMeeting(ctx context.Context, room *types.MeetingRoom) error
	DeleteMeeting(ctx context.Context, tenantID uint64, id string) error
	// SendUserMessage 落库用户消息并异步触发讨论轮，返回落库后的消息（归档房间拒绝）。
	// adjustFlow=true 且房间待确认时：不激活、不讨论，转由主持人按该意见重出开场（M3）
	SendUserMessage(ctx context.Context, tenantID uint64, roomID string, userID string, content string, mentionedAgentIDs []string, adjustFlow bool) (*types.MeetingMessage, error)
	ListMessages(ctx context.Context, roomID string, beforeID string, limit int) ([]*types.MeetingMessage, error)
	// InterruptDiscussion 用户主动打断进行中的讨论轮
	InterruptDiscussion(ctx context.Context, tenantID uint64, roomID string) error
	// UpdateReviewState 更新智能体消息的评审状态（pending/adopted/rejected；none 仅初始值不可回设）
	UpdateReviewState(ctx context.Context, tenantID uint64, roomID string, messageID string, state string) (*types.MeetingMessage, error)
	// GenerateSolution 主持人基于讨论记录（含议程进度）生成结构化方案（service 先做租户校验再委托引擎）
	GenerateSolution(ctx context.Context, tenantID uint64, roomID string, userID string) (*types.MeetingMessage, error)
	// ListArtifacts 本会议历史方案产物（含正文），按版本升序
	ListArtifacts(ctx context.Context, tenantID uint64, roomID string) ([]*types.MeetingArtifactDetail, error)
	// RollbackSolution 把房间当前方案指针切回指定历史版本
	RollbackSolution(ctx context.Context, tenantID uint64, roomID string, version int) error
	// ReplaceParticipants 全量替换参会智能体：主持人不可移除、至少 1 个 agent、
	// 讨论进行中 409（ErrTurnRunning）、归档只读；awaiting_confirmation 允许
	ReplaceParticipants(ctx context.Context, tenantID uint64, roomID string, agentIDs []string) ([]*types.MeetingParticipant, error)
	// ConfirmFlow 确认开场流程：幂等置 active，并以最近一条开场消息为触发自动开跑首轮
	ConfirmFlow(ctx context.Context, tenantID uint64, roomID string) error
	// ArchiveRoom 归档会议（幂等）：置 archived + 落系统消息 + 广播
	ArchiveRoom(ctx context.Context, tenantID uint64, roomID string) error
	// ExportArtifact 导出方案为 .md，返回文件名与正文（只读操作，归档房间允许导出；T6.3 实现）
	ExportArtifact(ctx context.Context, tenantID uint64, roomID string, artifactID string) (string, string, error)
	// RequestArtifact 发起产物生成（202 异步）：校验 + 落 generating 行 + 委托引擎后台执行
	RequestArtifact(ctx context.Context, tenantID uint64, roomID string, userID string, kindType, title, requirement string) (*types.MeetingArtifact, error)
	// RegenerateArtifact 按原参数重新生成指定 generated 产物（202 异步，沿用原行与版本号）
	RegenerateArtifact(ctx context.Context, tenantID uint64, roomID string, userID string, artifactID string) (*types.MeetingArtifact, error)
	// GetArtifactDetail 产物详情（solution 联查会议消息正文；generated 联查后台会话消息正文）
	GetArtifactDetail(ctx context.Context, tenantID uint64, roomID string, artifactID string) (*types.MeetingArtifactDetail, error)
	// ResolveArtifactDownload 解析 generated 产物下载跳转地址（后台会话消息的产物索引）
	ResolveArtifactDownload(ctx context.Context, tenantID uint64, roomID string, artifactID string) (string, error)
	// ListAgenda 本会议议程项（按 seq 升序；未启用议程时返回空列表）
	ListAgenda(ctx context.Context, tenantID uint64, roomID string) ([]*types.MeetingAgendaItem, error)
	// AdvanceAgenda 人工推进议程：把指定议程项标记完成（不再自动激活下一项，由用户决策后续动作）
	AdvanceAgenda(ctx context.Context, tenantID uint64, roomID string, itemID string) ([]*types.MeetingAgendaItem, error)
	// AppendAgenda 追加新议题并激活之，同时异步开启新一轮讨论
	AppendAgenda(ctx context.Context, tenantID uint64, roomID string, userID string, title string, goal string) ([]*types.MeetingAgendaItem, error)
	// ReopenAgenda 恢复指定议题继续讨论（当前活跃项回退为待讨论）
	ReopenAgenda(ctx context.Context, tenantID uint64, roomID string, itemID string) ([]*types.MeetingAgendaItem, error)
	// UploadAttachment 上传会议文件：复用会话临时文档的解析链路，作用域隔离到本会议。
	// 解析在后台异步进行，返回 uploaded/processing 状态，前端轮询列表获取进度。
	UploadAttachment(ctx context.Context, tenantID uint64, roomID, fileName, mimeType string, fileSize int64, reader io.Reader) (*types.TemporaryDocument, error)
	// ListAttachments 本会议已上传文件（含解析状态；解析正文不下发）
	ListAttachments(ctx context.Context, tenantID uint64, roomID string) ([]*types.TemporaryDocument, error)
	// DeleteAttachment 删除本会议文件（归档房间拒绝）
	DeleteAttachment(ctx context.Context, tenantID uint64, roomID, attachmentID string) error
}

// MeetingEngine 讨论引擎：同一会议同一时刻仅一个讨论轮次（Redis 锁保证）
type MeetingEngine interface {
	RunTurn(ctx context.Context, roomID string, triggerMessageID string) error
	Interrupt(ctx context.Context, roomID string) error
	// GenerateSolution 主持人生成结构化方案（引擎无租户上下文，room 由调用方校验后传入）
	GenerateSolution(ctx context.Context, room *types.MeetingRoom, userID string) (*types.MeetingMessage, error)
	// IsTurnRunning 查询讨论轮锁是否被持有（参会人变更 409 预检用）
	IsTurnRunning(ctx context.Context, roomID string) (bool, error)
	// GenerateOpening 主持人生成开场消息（主题分析 + 建议流程）并落库广播；
	// feedbackMessageID 非空 = 按用户「调整流程」意见重出开场
	GenerateOpening(ctx context.Context, room *types.MeetingRoom, feedbackMessageID string) error
	// IsArtifactRunning 查询房间产物生成锁是否被持有（生成 409 预检用）
	IsArtifactRunning(ctx context.Context, roomID string) (bool, error)
	// RunArtifactGeneration 产物生成主链路（互斥 → 后台会话 → AgentQA → 采集 → 回写 → 广播）
	RunArtifactGeneration(ctx context.Context, room *types.MeetingRoom, artifact *types.MeetingArtifact) error
	// ActiveAgendaItem 当前活跃议程项（未启用议程或无活跃项时返回 nil，不触发推进）
	ActiveAgendaItem(ctx context.Context, room *types.MeetingRoom) (*types.MeetingAgendaItem, error)
	// ListAgenda 房间全部议程项（按 seq 升序；未启用议程时返回空）
	ListAgenda(ctx context.Context, room *types.MeetingRoom) ([]*types.MeetingAgendaItem, error)
	// AppendAgendaItem 追加新议题；无活跃议题时新项即为活跃项
	AppendAgendaItem(ctx context.Context, room *types.MeetingRoom, title string, goal string) ([]*types.MeetingAgendaItem, error)
	// ReopenAgenda 把指定议题恢复为活跃项（其他活跃项回退为待讨论）
	ReopenAgenda(ctx context.Context, room *types.MeetingRoom, itemID string) ([]*types.MeetingAgendaItem, error)
}
