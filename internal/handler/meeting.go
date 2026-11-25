package handler

import (
	"encoding/json"
	stderrors "errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	apprepo "github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
)

// MeetingHandler 会议室 API
type MeetingHandler struct {
	meetingService interfaces.MeetingService
	engine         interfaces.MeetingEngine
}

func NewMeetingHandler(meetingService interfaces.MeetingService, engine interfaces.MeetingEngine) *MeetingHandler {
	return &MeetingHandler{meetingService: meetingService, engine: engine}
}

// ensureMeetingAccess 校验房间存在且归属当前租户；不存在/越权时写 404 并返回 false。
// guard 在 Admin/API-key/EnableRBAC=false 时会跳过 creator lookup，
// 故消息/SSE 等子资源端点必须自行做租户归属校验，避免跨租户读写。
func (h *MeetingHandler) ensureMeetingAccess(c *gin.Context, tenantID uint64) bool {
	if _, _, err := h.meetingService.GetMeeting(c.Request.Context(), tenantID, c.Param("id")); err != nil {
		c.Error(errors.NewNotFoundError("meeting not found"))
		return false
	}
	return true
}

type createMeetingRequest struct {
	Title                     string   `json:"title" binding:"required"`
	Description               string   `json:"description"`
	Mode                      string   `json:"mode" binding:"omitempty,oneof=hosted free"`
	BroadcastMode             string   `json:"broadcast_mode"`
	RoutingMode               string   `json:"routing_mode"`
	GuardMaxExchange          int      `json:"guard_max_exchange"`
	HostEveryNRounds          int      `json:"host_every_n_rounds"`
	ConsensusDetectionEnabled bool     `json:"consensus_detection_enabled"`
	AgendaEnabled             bool     `json:"agenda_enabled"`
	AdmissionMode             string   `json:"admission_mode"`
	KnowledgeBaseIDs          []string `json:"knowledge_base_ids"`
	WebSearchEnabled          bool     `json:"web_search_enabled"`
	AgentIDs                  []string `json:"agent_ids" binding:"required,min=1"`
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
		Title: req.Title, Description: req.Description, Mode: req.Mode,
		BroadcastMode: req.BroadcastMode, GuardMaxExchange: req.GuardMaxExchange,
		RoutingMode: req.RoutingMode, HostEveryNRounds: req.HostEveryNRounds,
		ConsensusDetectionEnabled: req.ConsensusDetectionEnabled,
		AgendaEnabled:             req.AgendaEnabled,
		AdmissionMode:             req.AdmissionMode,
		KnowledgeBaseIDs:          types.StringList(req.KnowledgeBaseIDs),
		WebSearchEnabled:          req.WebSearchEnabled,
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
		// 仅"不存在"映射 404；其它异常映射 500 通用文案并记录日志，避免泄露内部错误
		if stderrors.Is(err, apprepo.ErrMeetingRoomNotFound) {
			c.Error(errors.NewNotFoundError("meeting not found"))
			return
		}
		logger.ErrorWithFields(ctx, err, map[string]interface{}{"meeting_id": c.Param("id")})
		c.Error(errors.NewInternalServerError("Failed to load meeting"))
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"room": room, "participants": participants}})
}

// updateMeetingRequest PATCH 白名单：标题/描述可改，其余治理字段仅保留给内部调试。
// 用户侧只暴露标题、描述与参会人；讨论规则由创建时的内置默认统一决定。
type updateMeetingRequest struct {
	Title         string `json:"title" binding:"required"`
	Description   string `json:"description"`
	BroadcastMode string `json:"broadcast_mode" binding:"omitempty,oneof=sequential parallel"`
	RoutingMode   string `json:"routing_mode" binding:"omitempty,oneof=host-assign mention free"`
	// 指针类型区分「未传」（nil，保留原值）与显式设置
	GuardMaxExchange *int `json:"guard_max_exchange" binding:"omitempty,min=1,max=10"`
	// 指针类型区分「未传」（nil，保留原值）与显式 0（关闭主持人介入）
	HostEveryNRounds *int `json:"host_every_n_rounds" binding:"omitempty,min=0,max=10"`
	// 裁判 + 共识检测开关：指针区分「未传」（保留原值）与显式设置
	ConsensusDetectionEnabled *bool `json:"consensus_detection_enabled"`
	// 结构性治理开关：指针区分「未传」与显式设置
	AgendaEnabled *bool `json:"agenda_enabled"`
	// 发言准入模式：off / observe / enforce（空 = 保留原值）
	AdmissionMode string `json:"admission_mode" binding:"omitempty,oneof=off observe enforce"`
	// ResetRules 旧会议显式升级：把治理参数重置为当前内置默认（保留标题/描述/成员/状态）
	ResetRules bool `json:"reset_rules"`
}

// UpdateMeeting PATCH /api/v1/meetings/:id —— 白名单更新，归档房间只读
func (h *MeetingHandler) UpdateMeeting(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID, _ := types.TenantIDFromContext(ctx)
	var req updateMeetingRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}
	room, _, err := h.meetingService.GetMeeting(ctx, tenantID, c.Param("id"))
	if err != nil {
		c.Error(errors.NewNotFoundError("meeting not found"))
		return
	}
	// 仅 archived 只读；awaiting_confirmation 允许改设置（详情弹窗在待确认态可编辑，见 M2）
	if room.Status == types.MeetingStatusArchived {
		c.Error(errors.NewForbiddenError("会议已归档，仅可查看"))
		return
	}
	// 白名单拷贝：标题/描述始终可改；治理字段仅在显式传入时覆盖
	room.Title = req.Title
	room.Description = req.Description
	if req.ResetRules {
		service.ApplyMeetingRuleDefaults(room)
	}
	if req.BroadcastMode != "" {
		room.BroadcastMode = req.BroadcastMode
	}
	if req.RoutingMode != "" {
		room.RoutingMode = req.RoutingMode
	}
	if req.GuardMaxExchange != nil {
		room.GuardMaxExchange = *req.GuardMaxExchange
	}
	// 介入间隔为可选字段：未传（nil）时保留原值，显式 0 表示关闭
	if req.HostEveryNRounds != nil {
		room.HostEveryNRounds = *req.HostEveryNRounds
	}
	// 共识检测开关为可选字段：未传（nil）时保留原值
	if req.ConsensusDetectionEnabled != nil {
		room.ConsensusDetectionEnabled = *req.ConsensusDetectionEnabled
	}
	// 结构性治理开关与准入模式：未传时保留原值
	if req.AgendaEnabled != nil {
		room.AgendaEnabled = *req.AgendaEnabled
	}
	if req.AdmissionMode != "" {
		room.AdmissionMode = req.AdmissionMode
	}
	if err := h.meetingService.UpdateMeeting(ctx, room); err != nil {
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": room})
}

type replaceParticipantsRequest struct {
	AgentIDs []string `json:"agent_ids"`
}

// ReplaceParticipants PUT /api/v1/meetings/:id/participants —— 全量替换参会智能体
func (h *MeetingHandler) ReplaceParticipants(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID, _ := types.TenantIDFromContext(ctx)
	var req replaceParticipantsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}
	participants, err := h.meetingService.ReplaceParticipants(ctx, tenantID, c.Param("id"), req.AgentIDs)
	if err != nil {
		if err == service.ErrRoomArchived {
			c.Error(errors.NewForbiddenError("会议已归档，仅可查看"))
			return
		}
		if err == service.ErrTurnRunning {
			c.Error(errors.NewConflictError("讨论进行中，暂不能修改参会智能体"))
			return
		}
		if err == service.ErrNoAgents {
			c.Error(errors.NewBadRequestError("至少保留一个参会智能体"))
			return
		}
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": participants})
}

// ConfirmFlow POST /api/v1/meetings/:id/confirm —— 确认开场流程（幂等置 active + 自动开跑首轮）
func (h *MeetingHandler) ConfirmFlow(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID, _ := types.TenantIDFromContext(ctx)
	if err := h.meetingService.ConfirmFlow(ctx, tenantID, c.Param("id")); err != nil {
		if err == service.ErrRoomArchived {
			c.Error(errors.NewForbiddenError("会议已归档，仅可查看"))
			return
		}
		logger.ErrorWithFields(ctx, err, map[string]interface{}{"meeting_id": c.Param("id")})
		c.Error(errors.NewInternalServerError("Failed to confirm meeting flow"))
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"success": true, "message": "confirmed"})
}

// ListAgenda GET /api/v1/meetings/:id/agenda —— 议程项列表（按 seq 升序）
func (h *MeetingHandler) ListAgenda(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID, _ := types.TenantIDFromContext(ctx)
	items, err := h.meetingService.ListAgenda(ctx, tenantID, c.Param("id"))
	if err != nil {
		if stderrors.Is(err, apprepo.ErrMeetingRoomNotFound) {
			c.Error(errors.NewNotFoundError("meeting not found"))
			return
		}
		logger.ErrorWithFields(ctx, err, map[string]interface{}{"meeting_id": c.Param("id")})
		c.Error(errors.NewInternalServerError("Failed to load agenda"))
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": items})
}

type advanceAgendaRequest struct {
	// ItemID 为空表示推进当前活跃项
	ItemID string `json:"item_id"`
}

// AdvanceAgenda POST /api/v1/meetings/:id/agenda/advance —— 人工推进议程
func (h *MeetingHandler) AdvanceAgenda(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID, _ := types.TenantIDFromContext(ctx)
	var req advanceAgendaRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}
	items, err := h.meetingService.AdvanceAgenda(ctx, tenantID, c.Param("id"), req.ItemID)
	if err != nil {
		switch {
		case stderrors.Is(err, service.ErrRoomArchived):
			c.Error(errors.NewForbiddenError("会议已归档，仅可查看"))
		case stderrors.Is(err, service.ErrTurnRunning):
			c.Error(errors.NewConflictError("讨论进行中，暂不能推进议程"))
		case stderrors.Is(err, apprepo.ErrMeetingRoomNotFound):
			c.Error(errors.NewNotFoundError("meeting not found"))
		default:
			c.Error(errors.NewBadRequestError(err.Error()))
		}
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": items})
}

type appendAgendaRequest struct {
	// Title 新议题标题（必填）
	Title string `json:"title"`
	// Goal 该议题要产出的结论（可空）
	Goal string `json:"goal"`
}

// AppendAgenda POST /api/v1/meetings/:id/agenda/append —— 追加新议题并开启讨论
func (h *MeetingHandler) AppendAgenda(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID, _ := types.TenantIDFromContext(ctx)
	userID, _ := types.UserIDFromContext(ctx)
	var req appendAgendaRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}
	items, err := h.meetingService.AppendAgenda(ctx, tenantID, c.Param("id"), userID, req.Title, req.Goal)
	if !h.writeAgendaError(c, err, "暂不能追加议题") {
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": items})
}

type reopenAgendaRequest struct {
	// ItemID 要恢复讨论的议题 ID（必填）
	ItemID string `json:"item_id"`
}

// ReopenAgenda POST /api/v1/meetings/:id/agenda/reopen —— 恢复指定议题继续讨论
func (h *MeetingHandler) ReopenAgenda(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID, _ := types.TenantIDFromContext(ctx)
	var req reopenAgendaRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}
	if req.ItemID == "" {
		c.Error(errors.NewBadRequestError("请指定要恢复的议题"))
		return
	}
	items, err := h.meetingService.ReopenAgenda(ctx, tenantID, c.Param("id"), req.ItemID)
	if !h.writeAgendaError(c, err, "暂不能恢复议题讨论") {
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": items})
}

// writeAgendaError 议程类接口的统一错误映射；返回 true 表示 err 为 nil（可继续返回成功）。
func (h *MeetingHandler) writeAgendaError(c *gin.Context, err error, conflictMsg string) bool {
	switch {
	case err == nil:
		return true
	case stderrors.Is(err, service.ErrRoomArchived):
		c.Error(errors.NewForbiddenError("会议已归档，仅可查看"))
	case stderrors.Is(err, service.ErrTurnRunning):
		c.Error(errors.NewConflictError(conflictMsg))
	case stderrors.Is(err, apprepo.ErrMeetingRoomNotFound):
		c.Error(errors.NewNotFoundError("meeting not found"))
	case stderrors.Is(err, service.ErrAgendaDisabled):
		c.Error(errors.NewBadRequestError("本会议未启用议程"))
	case stderrors.Is(err, service.ErrAgendaFull):
		c.Error(errors.NewBadRequestError("议题数量已达上限，请先结束本议题"))
	case stderrors.Is(err, service.ErrAgendaItemNotFound):
		c.Error(errors.NewBadRequestError("指定的议题不存在"))
	default:
		c.Error(errors.NewBadRequestError(err.Error()))
	}
	return false
}

// ArchiveMeeting POST /api/v1/meetings/:id/archive —— 幂等归档
func (h *MeetingHandler) ArchiveMeeting(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID, _ := types.TenantIDFromContext(ctx)
	if err := h.meetingService.ArchiveRoom(ctx, tenantID, c.Param("id")); err != nil {
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "archived"})
}

// DeleteMeeting DELETE /api/v1/meetings/:id —— 软删
func (h *MeetingHandler) DeleteMeeting(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID, _ := types.TenantIDFromContext(ctx)
	if err := h.meetingService.DeleteMeeting(ctx, tenantID, c.Param("id")); err != nil {
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "deleted"})
}

// ExportArtifact GET /api/v1/meetings/:id/artifacts/:aid/export —— 下载产物 .md
// （solution 为方案正文；generated 为生成时 agent 最终回答。只读，归档房间可导出）
func (h *MeetingHandler) ExportArtifact(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID, _ := types.TenantIDFromContext(ctx)
	filename, content, err := h.meetingService.ExportArtifact(ctx, tenantID, c.Param("id"), c.Param("aid"))
	if err != nil {
		c.Error(errors.NewNotFoundError("artifact not found"))
		return
	}
	// 中文标题需 RFC 2184 编码，否则浏览器下载会丢文件名
	c.Header("Content-Disposition", exportContentDisposition(filename))
	c.Data(http.StatusOK, "text/markdown; charset=utf-8", []byte(content))
}

// ListMessages GET /api/v1/meetings/:id/messages?before=&limit=
func (h *MeetingHandler) ListMessages(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID, _ := types.TenantIDFromContext(ctx)
	if !h.ensureMeetingAccess(c, tenantID) {
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "100"))
	msgs, err := h.meetingService.ListMessages(ctx, c.Param("id"), c.Query("before"), limit)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{"meeting_id": c.Param("id")})
		c.Error(errors.NewInternalServerError("Failed to list meeting messages"))
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": msgs})
}

// meetingEventDTO 事件响应体：payload 以原始 JSON 下发。
// MeetingEvent.Payload 是 []byte，直接序列化会被编码成 base64，前端无法读取结构化字段。
type meetingEventDTO struct {
	ID            string          `json:"id"`
	MeetingRoomID string          `json:"meeting_room_id"`
	Phase         string          `json:"phase"`
	Type          string          `json:"type"`
	ActorKind     string          `json:"actor_kind"`
	ActorID       string          `json:"actor_id"`
	Summary       string          `json:"summary"`
	Payload       json.RawMessage `json:"payload"`
	CreatedAt     time.Time       `json:"created_at"`
}

// ListMeetingEvents GET /api/v1/meetings/:id/events?limit=100 —— 阶段与事件时间线（正序）
func (h *MeetingHandler) ListMeetingEvents(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID, _ := types.TenantIDFromContext(ctx)
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "100"))
	events, err := h.meetingService.ListEvents(ctx, tenantID, c.Param("id"), limit)
	if err != nil {
		if stderrors.Is(err, apprepo.ErrMeetingRoomNotFound) {
			c.Error(errors.NewNotFoundError("meeting not found"))
			return
		}
		logger.ErrorWithFields(ctx, err, map[string]interface{}{"meeting_id": c.Param("id")})
		c.Error(errors.NewInternalServerError("Failed to list meeting events"))
		return
	}
	items := make([]meetingEventDTO, 0, len(events))
	for _, ev := range events {
		payload := ev.Payload
		if len(payload) == 0 {
			payload = []byte("{}") // 存量/异常数据兜底，保证前端拿到的始终是对象
		}
		items = append(items, meetingEventDTO{
			ID: ev.ID, MeetingRoomID: ev.MeetingRoomID, Phase: ev.Phase, Type: ev.Type,
			ActorKind: ev.ActorKind, ActorID: ev.ActorID, Summary: ev.Summary,
			Payload: payload, CreatedAt: ev.CreatedAt,
		})
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"items": items}})
}

type sendMessageRequest struct {
	Content           string   `json:"content" binding:"required"`
	MentionedAgentIDs []string `json:"mentioned_agent_ids"`
	// 「调整流程」标记：仅待确认状态下生效，主持人据此重出开场
	AdjustFlow bool `json:"adjust_flow"`
}

// SendMessage POST /api/v1/meetings/:id/messages —— 202 异步：落库后触发讨论轮
func (h *MeetingHandler) SendMessage(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID, _ := types.TenantIDFromContext(ctx)
	if !h.ensureMeetingAccess(c, tenantID) {
		return
	}
	var req sendMessageRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}
	userID, _ := types.UserIDFromContext(ctx)
	msg, err := h.meetingService.SendUserMessage(ctx, tenantID, c.Param("id"), userID, req.Content, req.MentionedAgentIDs, req.AdjustFlow)
	if err != nil {
		if err == service.ErrRoomArchived {
			c.Error(errors.NewForbiddenError("会议已归档，仅可查看"))
			return
		}
		logger.ErrorWithFields(ctx, err, map[string]interface{}{"meeting_id": c.Param("id")})
		c.Error(errors.NewInternalServerError("Failed to send meeting message"))
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"success": true, "data": msg})
}

// Interrupt POST /api/v1/meetings/:id/interrupt
func (h *MeetingHandler) Interrupt(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID, _ := types.TenantIDFromContext(ctx)
	if !h.ensureMeetingAccess(c, tenantID) {
		return
	}
	if err := h.meetingService.InterruptDiscussion(ctx, tenantID, c.Param("id")); err != nil {
		if err == service.ErrRoomArchived {
			c.Error(errors.NewForbiddenError("会议已归档，仅可查看"))
			return
		}
		logger.ErrorWithFields(ctx, err, map[string]interface{}{"meeting_id": c.Param("id")})
		c.Error(errors.NewInternalServerError("Failed to interrupt meeting"))
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"success": true, "data": gin.H{"room_id": c.Param("id"), "interrupted": true}})
}

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
		if err == service.ErrRoomArchived {
			c.Error(errors.NewForbiddenError("会议已归档，仅可查看"))
			return
		}
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": msg})
}

// GenerateSolution POST /api/v1/meetings/:id/solution —— 同步等待 LLM 结果
func (h *MeetingHandler) GenerateSolution(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID, _ := types.TenantIDFromContext(ctx)
	userID, _ := types.UserIDFromContext(ctx)
	msg, err := h.meetingService.GenerateSolution(ctx, tenantID, c.Param("id"), userID)
	if err != nil {
		if err == service.ErrRoomArchived {
			c.Error(errors.NewForbiddenError("会议已归档，仅可查看"))
			return
		}
		if err == service.ErrNoDiscussionMessages {
			c.Error(errors.NewBadRequestError("还没有讨论内容，暂无法生成方案"))
			return
		}
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": msg})
}

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

type requestArtifactRequest struct {
	KindType    string `json:"kind_type" binding:"required"`
	Title       string `json:"title"`
	Requirement string `json:"requirement"`
}

// writeArtifactError 产物端点统一错误映射（按服务层哨兵判等）
func (h *MeetingHandler) writeArtifactError(c *gin.Context, err error) {
	switch {
	case err == service.ErrRoomArchived:
		c.Error(errors.NewForbiddenError("会议已归档，仅可查看"))
	case err == service.ErrArtifactBusy:
		c.Error(errors.NewConflictError("已有产物正在生成，请稍候再试"))
	case err == service.ErrInvalidArtifactKind, err == service.ErrNotRegenerable,
		err == service.ErrNoDiscussionMessages:
		c.Error(errors.NewBadRequestError(err.Error()))
	case stderrors.Is(err, apprepo.ErrMeetingRoomNotFound), stderrors.Is(err, apprepo.ErrMeetingArtifactNotFound):
		// 会议不存在/跨租户与产物不存在统一 404：与守卫契约及读路径语义保持一致，避免回传内部错误原文
		c.Error(errors.NewNotFoundError("meeting or artifact not found"))
	default:
		c.Error(errors.NewInternalServerError(err.Error()))
	}
}

// RequestArtifact POST /api/v1/meetings/:id/artifacts —— 202 异步生成（终态经 SSE 推送）
func (h *MeetingHandler) RequestArtifact(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID, _ := types.TenantIDFromContext(ctx)
	userID, _ := types.UserIDFromContext(ctx)
	var req requestArtifactRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}
	a, err := h.meetingService.RequestArtifact(ctx, tenantID, c.Param("id"), userID, req.KindType, req.Title, req.Requirement)
	if err != nil {
		h.writeArtifactError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"success": true, "data": a})
}

// RegenerateArtifact POST /api/v1/meetings/:id/artifacts/:aid/regenerate —— 202 按原参数重跑
func (h *MeetingHandler) RegenerateArtifact(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID, _ := types.TenantIDFromContext(ctx)
	userID, _ := types.UserIDFromContext(ctx)
	a, err := h.meetingService.RegenerateArtifact(ctx, tenantID, c.Param("id"), userID, c.Param("aid"))
	if err != nil {
		h.writeArtifactError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"success": true, "data": a})
}

// GetArtifactDetail GET /api/v1/meetings/:id/artifacts/:aid —— 产物详情（含正文）
func (h *MeetingHandler) GetArtifactDetail(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID, _ := types.TenantIDFromContext(ctx)
	d, err := h.meetingService.GetArtifactDetail(ctx, tenantID, c.Param("id"), c.Param("aid"))
	if err != nil {
		if stderrors.Is(err, apprepo.ErrMeetingArtifactNotFound) || stderrors.Is(err, apprepo.ErrMeetingRoomNotFound) {
			c.Error(errors.NewNotFoundError("artifact not found"))
			return
		}
		logger.ErrorWithFields(ctx, err, map[string]interface{}{"meeting_id": c.Param("id")})
		c.Error(errors.NewInternalServerError("Failed to load artifact"))
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": d})
}

// DownloadArtifact GET /api/v1/meetings/:id/artifacts/:aid/download
// —— 302 跳转既有会话产物下载端点：后台会话属主即会议创建者，
// 鉴权/租户存储后端解析/tombstone 检查全部复用（spec §2.3 下载复用）。
func (h *MeetingHandler) DownloadArtifact(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID, _ := types.TenantIDFromContext(ctx)
	location, err := h.meetingService.ResolveArtifactDownload(ctx, tenantID, c.Param("id"), c.Param("aid"))
	if err != nil {
		// 不可下载/源消息或文件缺失统一 404，避免泄露内部失败细节
		c.Error(errors.NewNotFoundError("artifact not found"))
		return
	}
	c.Redirect(http.StatusFound, location)
}

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
		if err == service.ErrRoomArchived {
			c.Error(errors.NewForbiddenError("会议已归档，仅可查看"))
			return
		}
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// UploadAttachment POST /api/v1/meetings/:id/attachments —— 上传会议文件（异步解析）
func (h *MeetingHandler) UploadAttachment(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID, _ := types.TenantIDFromContext(ctx)
	// 与聊天附件一致：限制请求体大小，避免超大文件打满内存/磁盘
	maxBytes := secutils.GetMaxFileSizeMB()*1024*1024 + 1024*1024
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes)
	fileHeader, err := c.FormFile("file")
	if err != nil {
		c.Error(errors.NewBadRequestError(fmt.Sprintf("invalid attachment upload: %v", err)))
		return
	}
	file, err := fileHeader.Open()
	if err != nil {
		c.Error(errors.NewBadRequestError("failed to open attachment"))
		return
	}
	defer file.Close()

	doc, err := h.meetingService.UploadAttachment(
		ctx, tenantID, c.Param("id"),
		fileHeader.Filename, fileHeader.Header.Get("Content-Type"), fileHeader.Size, file,
	)
	if err != nil {
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"success": true, "data": doc})
}

// ListAttachments GET /api/v1/meetings/:id/attachments —— 本会议文件与解析状态
func (h *MeetingHandler) ListAttachments(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID, _ := types.TenantIDFromContext(ctx)
	docs, err := h.meetingService.ListAttachments(ctx, tenantID, c.Param("id"))
	if err != nil {
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": docs})
}

// DeleteAttachment DELETE /api/v1/meetings/:id/attachments/:aid
func (h *MeetingHandler) DeleteAttachment(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID, _ := types.TenantIDFromContext(ctx)
	if err := h.meetingService.DeleteAttachment(ctx, tenantID, c.Param("id"), c.Param("aid")); err != nil {
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// StreamMessages GET /api/v1/meetings/:id/stream —— SSE 订阅会议事件
func (h *MeetingHandler) StreamMessages(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID, _ := types.TenantIDFromContext(ctx)
	if !h.ensureMeetingAccess(c, tenantID) {
		return
	}
	roomID := c.Param("id")
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

	c.SSEvent(service.MeetingEventPing, gin.H{"room_id": roomID})
	c.Writer.Flush()

	// 心跳 ticker：SSE 长连接保活，避免网关/代理因空闲断开
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
	type broadcasterProvider interface {
		Broadcaster() *service.MeetingBroadcaster
	}
	if p, ok := h.engine.(broadcasterProvider); ok {
		return p.Broadcaster()
	}
	return nil
}
