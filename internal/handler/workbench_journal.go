package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/application/service/workbench"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
)

// WorkbenchJournalHandler 工作台 Journals（画布条目的日期视图）的 HTTP 入口。
//
// 只读：新建/改期复用条目端点（POST /items 带 journal_date、
// PUT /items/:id 带 journal_date），不在这里开第二套写接口。
type WorkbenchJournalHandler struct {
	service *workbench.Service
}

// NewWorkbenchJournalHandler 构造 handler。
func NewWorkbenchJournalHandler(service *workbench.Service) *WorkbenchJournalHandler {
	return &WorkbenchJournalHandler{service: service}
}

// GetMonth GET /api/v1/workbench/journals/month?month=YYYY-MM
//
// month 缺省时取服务器当前月；非法格式返回 2411（details.field=month）。
func (h *WorkbenchJournalHandler) GetMonth(c *gin.Context) {
	month, err := h.service.GetJournalsMonth(c.Request.Context(), c.Query("month"))
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": month})
}

// GetDay GET /api/v1/workbench/journals/:date
//
// 0 条是合法结果（这天还没写），返回空数组而不是 404。
func (h *WorkbenchJournalHandler) GetDay(c *gin.Context) {
	day, err := h.service.GetJournalDay(c.Request.Context(), c.Param("date"))
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": day})
}

// fail 统一错误出口。
func (h *WorkbenchJournalHandler) fail(c *gin.Context, err error) {
	if _, ok := apperrors.IsAppError(err); ok {
		c.Error(err)
		return
	}
	c.Error(apperrors.NewInternalServerError("Failed to process workbench journal request").
		WithDetails(err.Error()))
}
