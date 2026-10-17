package handler

import (
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
	BroadcastMode             string   `json:"broadcast_mode"`
	RoutingMode               string   `json:"routing_mode"`
	GuardMaxExchange          int      `json:"guard_max_exchange"`
	HostEveryNRounds          int      `json:"host_every_n_rounds"`
	ConsensusDetectionEnabled bool     `json:"consensus_detection_enabled"`
	KnowledgeBaseIDs          []string `json:"knowledge_base_ids"`
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
		Title: req.Title, Description: req.Description,
		BroadcastMode: req.BroadcastMode, GuardMaxExchange: req.GuardMaxExchange,
		RoutingMode: req.RoutingMode, HostEveryNRounds: req.HostEveryNRounds,
		ConsensusDetectionEnabled: req.ConsensusDetectionEnabled,
		KnowledgeBaseIDs:          types.StringList(req.KnowledgeBaseIDs),
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

// updateMeetingRequest PATCH 白名单六字段（参会人不可改）
type updateMeetingRequest struct {
	Title            string `json:"title" binding:"required"`
	Description      string `json:"description"`
	BroadcastMode    string `json:"broadcast_mode" binding:"required,oneof=sequential parallel"`
	RoutingMode      string `json:"routing_mode" binding:"omitempty,oneof=host-assign mention free"`
	GuardMaxExchange int    `json:"guard_max_exchange" binding:"required,min=1,max=10"`
	// 指针类型区分「未传」（nil，保留原值）与显式 0（关闭主持人介入）
	HostEveryNRounds *int `json:"host_every_n_rounds" binding:"omitempty,min=0,max=10"`
	// 裁判 + 共识检测开关：指针区分「未传」（保留原值）与显式设置
	ConsensusDetectionEnabled *bool `json:"consensus_detection_enabled"`
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
	// 白名单拷贝六字段，其余字段不可改
	room.Title = req.Title
	room.Description = req.Description
	room.BroadcastMode = req.BroadcastMode
	// 路由模式为可选字段：未传时保留原值
	if req.RoutingMode != "" {
		room.RoutingMode = req.RoutingMode
	}
	room.GuardMaxExchange = req.GuardMaxExchange
	// 介入间隔为可选字段：未传（nil）时保留原值，显式 0 表示关闭
	if req.HostEveryNRounds != nil {
		room.HostEveryNRounds = *req.HostEveryNRounds
	}
	// 共识检测开关为可选字段：未传（nil）时保留原值
	if req.ConsensusDetectionEnabled != nil {
		room.ConsensusDetectionEnabled = *req.ConsensusDetectionEnabled
	}
	if err := h.meetingService.UpdateMeeting(ctx, room); err != nil {
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": room})
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

// ExportArtifact GET /api/v1/meetings/:id/artifacts/:aid/export —— 下载方案 .md（只读，归档房间可导出）
func (h *MeetingHandler) ExportArtifact(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID, _ := types.TenantIDFromContext(ctx)
	filename, content, err := h.meetingService.ExportArtifact(ctx, tenantID, c.Param("id"), c.Param("aid"))
	if err != nil {
		c.Error(errors.NewNotFoundError("artifact not found"))
		return
	}
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", filename))
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

type sendMessageRequest struct {
	Content           string   `json:"content" binding:"required"`
	MentionedAgentIDs []string `json:"mentioned_agent_ids"`
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
	msg, err := h.meetingService.SendUserMessage(ctx, tenantID, c.Param("id"), userID, req.Content, req.MentionedAgentIDs)
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
		if err == service.ErrNoAdoptedMessages {
			c.Error(errors.NewBadRequestError("请先在讨论中采纳至少一条意见"))
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
