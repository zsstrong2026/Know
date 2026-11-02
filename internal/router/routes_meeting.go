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
		meetings.PATCH("/:id", g.OwnedMeetingOrAdmin(), meetingHandler.UpdateMeeting)
		meetings.PUT("/:id/participants", g.OwnedMeetingOrAdmin(), meetingHandler.ReplaceParticipants)
		meetings.POST("/:id/confirm", g.OwnedMeetingOrAdmin(), meetingHandler.ConfirmFlow)
		meetings.POST("/:id/archive", g.OwnedMeetingOrAdmin(), meetingHandler.ArchiveMeeting)
		meetings.DELETE("/:id", g.OwnedMeetingOrAdmin(), meetingHandler.DeleteMeeting)
		meetings.GET("/:id/messages", g.OwnedMeetingOrAdmin(), meetingHandler.ListMessages)
		meetings.GET("/:id/agenda", g.OwnedMeetingOrAdmin(), meetingHandler.ListAgenda)
		meetings.POST("/:id/agenda/advance", g.OwnedMeetingOrAdmin(), meetingHandler.AdvanceAgenda)
		meetings.POST("/:id/agenda/append", g.OwnedMeetingOrAdmin(), meetingHandler.AppendAgenda)
		meetings.POST("/:id/agenda/reopen", g.OwnedMeetingOrAdmin(), meetingHandler.ReopenAgenda)
		meetings.POST("/:id/messages", g.OwnedMeetingOrAdmin(), meetingHandler.SendMessage)
		meetings.POST("/:id/interrupt", g.OwnedMeetingOrAdmin(), meetingHandler.Interrupt)
		meetings.PUT("/:id/messages/:mid/review", g.OwnedMeetingOrAdmin(), meetingHandler.UpdateReviewState)
		meetings.POST("/:id/solution", g.OwnedMeetingOrAdmin(), meetingHandler.GenerateSolution)
		meetings.GET("/:id/artifacts", g.OwnedMeetingOrAdmin(), meetingHandler.ListArtifacts)
		meetings.GET("/:id/artifacts/:aid/export", g.OwnedMeetingOrAdmin(), meetingHandler.ExportArtifact)
		meetings.POST("/:id/artifacts", g.OwnedMeetingOrAdmin(), meetingHandler.RequestArtifact)
		meetings.GET("/:id/artifacts/:aid", g.OwnedMeetingOrAdmin(), meetingHandler.GetArtifactDetail)
		meetings.GET("/:id/artifacts/:aid/download", g.OwnedMeetingOrAdmin(), meetingHandler.DownloadArtifact)
		meetings.POST("/:id/artifacts/:aid/regenerate", g.OwnedMeetingOrAdmin(), meetingHandler.RegenerateArtifact)
		meetings.POST("/:id/solution/rollback", g.OwnedMeetingOrAdmin(), meetingHandler.RollbackSolution)
		meetings.GET("/:id/stream", g.OwnedMeetingOrAdmin(), meetingHandler.StreamMessages)
	}
}
