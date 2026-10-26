package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	apprepo "github.com/Tencent/WeKnora/internal/application/repository"
)

// GetTask GET /api/v1/pm/tasks/:task_id
// 异步生成进度轮询入口，跨租户按 404 处理
func (h *PMHandler) GetTask(c *gin.Context) {
	task, err := h.taskService.GetTask(c.Request.Context(), pmTenantID(c), c.Param("task_id"))
	if err != nil {
		pmFail(c, err, apprepo.ErrPMTaskNotFound, "task not found")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": task})
}
