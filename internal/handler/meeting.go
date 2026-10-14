package handler

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/errors"
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
		return "", err
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
		KnowledgeBaseIDs: types.StringList(req.KnowledgeBaseIDs),
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
