package handler

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/application/service/workbench"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
)

// WorkbenchAIHandler 工作台内联 AI 的 HTTP 入口（§11.3）。
//
// 帧格式与其余 SSE 端点不同：这里用 `event: delta|done|error` + 单行 JSON data，
// 而不是统一的 `event: message`。原因是内联车道的前端只用 fetch + ReadableStream，
// 不经过 axios 拦截器，事件名要能直接分辨“增量 / 结束 / 中断”。
type WorkbenchAIHandler struct {
	service *workbench.AIService
}

// NewWorkbenchAIHandler 构造 handler。
func NewWorkbenchAIHandler(service *workbench.AIService) *WorkbenchAIHandler {
	return &WorkbenchAIHandler{service: service}
}

// Inline POST /api/v1/workbench/ai/inline
//
// stream=false 时返回普通 JSON，用于测试与不支持 SSE 的客户端；
// 否则先开流再写帧——所以“无模型 / 超长 / 条目不存在”必须在开流前判掉。
func (h *WorkbenchAIHandler) Inline(c *gin.Context) {
	var req types.InlineAIRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(apperrors.NewValidationError("Invalid request data").WithDetails(err.Error()))
		return
	}

	stream, err := h.service.InlineStream(c.Request.Context(), req)
	if err != nil {
		h.fail(c, err)
		return
	}

	// 非流式：消费完整个通道再一次性返回，语义与 SSE 分支完全一致。
	if !req.Stream {
		h.collect(c, stream)
		return
	}

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
	c.Status(http.StatusOK)

	text := strings.Builder{}
	for resp := range stream {
		switch resp.ResponseType {
		case types.ResponseTypeAnswer:
			if !resp.Done && resp.Content != "" {
				text.WriteString(resp.Content)
				if !writeInlineEvent(c, "delta", gin.H{"text": resp.Content}) {
					return
				}
				continue
			}
			if resp.Done {
				writeInlineEvent(c, "done", gin.H{
					"text":     text.String(),
					"model_id": resp.Data["model_id"],
					"agent_id": resp.Data["agent_id"],
					"usage":    resp.Usage,
				})
			}
		case types.ResponseTypeError:
			writeInlineEvent(c, "error", gin.H{
				"code":    int(apperrors.ErrWorkbenchStreamBroken),
				"message": resp.Content,
			})
			return
		}
	}
}

// collect 消费完整条流，返回等价于聚合结果的 JSON（§11.3 stream=false）。
func (h *WorkbenchAIHandler) collect(c *gin.Context, stream <-chan types.StreamResponse) {
	text := strings.Builder{}
	result := types.InlineAIResult{}
	for resp := range stream {
		switch resp.ResponseType {
		case types.ResponseTypeAnswer:
			if resp.Content != "" {
				text.WriteString(resp.Content)
			}
			if resp.Done {
				if v, ok := resp.Data["model_id"].(string); ok {
					result.ModelID = v
				}
				if v, ok := resp.Data["agent_id"].(string); ok {
					result.AgentID = v
				}
				result.Usage = resp.Usage
			}
		case types.ResponseTypeError:
			c.Error(apperrors.NewWorkbenchStreamBroken(resp.Content))
			return
		}
	}
	result.Text = text.String()
	c.JSON(http.StatusOK, gin.H{"success": true, "data": result})
}

// Ask POST /api/v1/workbench/ai/ask
//
// 帧格式与内联车道一致（delta / done / error），另外多一个 references 帧先行发出，
// 让前端能边渲染来源边等正文（§11.8）。
func (h *WorkbenchAIHandler) Ask(c *gin.Context) {
	var req types.AskAIRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(apperrors.NewValidationError("Invalid request data").WithDetails(err.Error()))
		return
	}
	stream, err := h.service.AskStream(c.Request.Context(), req)
	if err != nil {
		h.fail(c, err)
		return
	}

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
	c.Status(http.StatusOK)

	text := strings.Builder{}
	for resp := range stream {
		switch resp.ResponseType {
		case types.ResponseTypeReferences:
			writeInlineEvent(c, "references", gin.H{"references": resp.Data["references"]})
		case types.ResponseTypeAnswer:
			if !resp.Done && resp.Content != "" {
				text.WriteString(resp.Content)
				if !writeInlineEvent(c, "delta", gin.H{"text": resp.Content}) {
					return
				}
				continue
			}
			if resp.Done {
				writeInlineEvent(c, "done", gin.H{
					"text":     text.String(),
					"model_id": resp.Data["model_id"],
					"agent_id": resp.Data["agent_id"],
					"usage":    resp.Usage,
				})
			}
		case types.ResponseTypeError:
			writeInlineEvent(c, "error", gin.H{
				"code":    int(apperrors.ErrWorkbenchStreamBroken),
				"message": resp.Content,
			})
			return
		}
	}
}

// writeInlineEvent 写一帧并 flush；返回 false 表示连接已断。
func writeInlineEvent(c *gin.Context, event string, data gin.H) bool {
	payload, err := json.Marshal(data)
	if err != nil {
		return false
	}
	if _, err := c.Writer.WriteString("event: " + event + "\ndata: " + string(payload) + "\n\n"); err != nil {
		return false
	}
	c.Writer.Flush()
	return c.Request.Context().Err() == nil
}

// fail 统一错误出口：2400/2402/2408 在开流前返回普通 JSON。
func (h *WorkbenchAIHandler) fail(c *gin.Context, err error) {
	if _, ok := apperrors.IsAppError(err); ok {
		c.Error(err)
		return
	}
	c.Error(apperrors.NewInternalServerError("Failed to process workbench AI request").
		WithDetails(err.Error()))
}
