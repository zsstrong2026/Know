package router

import (
	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/handler"
)

// RegisterMeetingRoutes 会议室路由。可见性"仅创建者可见"：
// 房间级读写一律 OwnedMeetingOrAdmin；创建开放 Contributor；列表 Viewer 即可（服务层按创建者过滤）。
func RegisterMeetingRoutes(r *gin.RouterGroup, meetingHandler *handler.MeetingHandler, g *rbacGuards) {
	meetings := g.apiKeyGroup(r.Group("/meetings"), apiKeyFullAccess())
	{
		meetings.POST("", g.Contributor(), meetingHandler.CreateMeeting)
		meetings.GET("", g.Viewer(), meetingHandler.ListMeetings)
		meetings.GET("/:id", g.OwnedMeetingOrAdmin(), meetingHandler.GetMeeting)
		meetings.GET("/:id/messages", g.OwnedMeetingOrAdmin(), meetingHandler.ListMessages)
		meetings.POST("/:id/messages", g.OwnedMeetingOrAdmin(), meetingHandler.SendMessage)
		meetings.POST("/:id/interrupt", g.OwnedMeetingOrAdmin(), meetingHandler.Interrupt)
		meetings.PUT("/:id/messages/:mid/review", g.OwnedMeetingOrAdmin(), meetingHandler.UpdateReviewState)
		meetings.POST("/:id/solution", g.OwnedMeetingOrAdmin(), meetingHandler.GenerateSolution)
		meetings.GET("/:id/stream", g.OwnedMeetingOrAdmin(), meetingHandler.StreamMessages)
	}
}
