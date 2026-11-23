// sop_admin.go 是 SOP 工作台的管理端点：审批中心聚合、项目触发配置与历史、
// CLI 凭据（加密落库、脱敏出网）、PM 迁移归档摘要。
//
// 三条口径：
//   - 凭据永不回显明文：响应只给尾 4 位；密文由模型 BeforeSave 负责；
//   - 触发配置的校验以 types.ValidateSopTriggerConfig 为准（cron 最短 5 分钟、
//     per_run_limit 1–10），错误原文是给用户看的中文/字段级原因；
//   - 审批中心只读：处置仍走运行/审批各端点。
package handler

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/types"
)

// ---- 审批中心 ----

// ListApprovals 审批中心三源聚合（SOP 关口 + 智能体挂起 + 自动化待审批）。
func (h *SOPHandler) ListApprovals(c *gin.Context) {
	tenantID := sopTenantID(c)
	if tenantID == 0 || h.approvals == nil {
		return
	}
	entries, err := h.approvals.ListPending(c.Request.Context(), tenantID)
	if err != nil {
		sopAbort(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": entries})
}

// ---- 触发配置 ----

// GetTriggerConfig 取项目的触发配置（未配置时返回默认体，前端表单直接可编辑）。
func (h *SOPHandler) GetTriggerConfig(c *gin.Context) {
	tenantID := sopTenantID(c)
	if tenantID == 0 || h.triggers == nil {
		return
	}
	cfg, err := h.triggers.GetConfig(c.Request.Context(), tenantID, c.Param("id"))
	if err != nil {
		if err == gorm.ErrRecordNotFound || strings.Contains(err.Error(), "not found") {
			// 未配置不是错误：给一份默认草稿（interval/daily9/10 条/未启用）。
			c.JSON(http.StatusOK, gin.H{"success": true, "data": types.SopTriggerConfig{
				ProjectID: c.Param("id"), Mode: types.SopTriggerModeInterval,
				IntervalKey: "daily9", PerRunLimit: 10, Sources: "all",
			}})
			return
		}
		sopAbort(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": cfg})
}

// sopTriggerPayload 触发配置请求体（字段与模型对齐，前端表单直传）。
type sopTriggerPayload struct {
	Mode               string   `json:"mode"`
	IntervalKey        string   `json:"interval_key"`
	Cron               string   `json:"cron"`
	Timezone           string   `json:"timezone"`
	DefaultTemplateID  string   `json:"default_template_id"`
	PreferItemTemplate bool     `json:"prefer_item_template"`
	Sources            string   `json:"sources"`
	Statuses           []string `json:"statuses"`
	PerRunLimit        int      `json:"per_run_limit"`
	Enabled            bool     `json:"enabled"`
}

// PutTriggerConfig 保存触发配置：先过 types 校验，再 upsert。
func (h *SOPHandler) PutTriggerConfig(c *gin.Context) {
	tenantID := sopTenantID(c)
	if tenantID == 0 || h.triggers == nil {
		return
	}
	var req sopTriggerPayload
	if !sopBindJSON(c, &req) {
		return
	}
	cfg := &types.SopTriggerConfig{
		TenantID: tenantID, ProjectID: c.Param("id"),
		Mode: req.Mode, IntervalKey: req.IntervalKey, Cron: req.Cron, Timezone: req.Timezone,
		DefaultTemplateID: req.DefaultTemplateID, PreferItemTemplate: req.PreferItemTemplate,
		Sources: req.Sources, Statuses: req.Statuses, PerRunLimit: req.PerRunLimit,
		Enabled: req.Enabled,
	}
	types.NormalizeSopTriggerConfig(cfg)
	if err := types.ValidateSopTriggerConfig(cfg); err != nil {
		sopAbort(c, sopClientError{msg: translateTriggerError(err)})
		return
	}
	if err := h.triggers.UpsertConfig(c.Request.Context(), cfg); err != nil {
		sopAbort(c, sopClientError{msg: translateTriggerError(err)})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": cfg})
}

// translateTriggerError 把 types 的字段级校验错误翻成用户能看懂的中文。
//
// 校验错误原文是英文（如 "per_run_limit must be between 1 and 10"），前端不该
// 直接展示英文；这里按关键片段翻译，未命中保留原文（便于排查）。
func translateTriggerError(err error) string {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "per_run_limit"):
		return "单轮派发条数必须在 1–10 之间"
	case strings.Contains(msg, "minimum is") || strings.Contains(msg, "fires every"):
		return "cron 触发间隔太短，最短 5 分钟一次"
	case strings.Contains(msg, "cron mode needs"):
		return "cron 模式需要填写表达式"
	case strings.Contains(msg, "cannot parse cron"):
		return "cron 表达式无法解析，请检查格式"
	case strings.Contains(msg, "TZ prefix"):
		return "时区请填在时区字段里，不要写进 cron 表达式"
	case strings.Contains(msg, "interval_key"):
		return "请选择合法的触发频率"
	case strings.Contains(msg, "unknown timezone"):
		return "时区无法识别"
	case strings.Contains(msg, "status"):
		return "触发状态筛选含不认识的取值"
	case strings.Contains(msg, "source"):
		return "来源筛选含不认识的取值"
	case strings.Contains(msg, "mode"):
		return "触发方式只能是按频率或 cron"
	}
	return msg
}

// ListTriggerLogs 触发历史（created_at 倒序分页）。
func (h *SOPHandler) ListTriggerLogs(c *gin.Context) {
	tenantID := sopTenantID(c)
	if tenantID == 0 || h.triggers == nil {
		return
	}
	cfg, err := h.triggers.GetConfig(c.Request.Context(), tenantID, c.Param("id"))
	if err != nil {
		sopFail(c, err, "该项目还没有触发配置")
		return
	}
	logs, err := h.triggers.ListLogs(c.Request.Context(), tenantID, cfg.ID,
		atoiOr(c.Query("page"), 0), atoiOr(c.Query("page_size"), 0))
	if err != nil {
		sopAbort(c, err)
		return
	}
	if logs == nil {
		logs = []types.SopTriggerLog{}
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": logs})
}

// ---- CLI 凭据 ----

// ListCredentials 列当前用户的 CLI 凭据（脱敏）。
func (h *SOPHandler) ListCredentials(c *gin.Context) {
	tenantID := sopTenantID(c)
	if tenantID == 0 || h.credSvc == nil {
		return
	}
	list, err := h.credSvc.ListMasked(c.Request.Context(), tenantID, sopUserID(c))
	if err != nil {
		sopAbort(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": list})
}

// PutCredential 按 cli_kind upsert 凭据（api_key 可留空表示沿用旧密钥）。
func (h *SOPHandler) PutCredential(c *gin.Context) {
	tenantID := sopTenantID(c)
	if tenantID == 0 || h.credSvc == nil {
		return
	}
	var req struct {
		Name    string `json:"name"`
		APIKey  string `json:"api_key"`
		BaseURL string `json:"base_url"`
		Model   string `json:"model"`
	}
	if !sopBindJSON(c, &req) {
		return
	}
	view, err := h.credSvc.Upsert(c.Request.Context(), tenantID, sopUserID(c),
		c.Param("kind"), req.Name, req.APIKey, req.BaseURL, req.Model)
	if err != nil {
		sopAbort(c, sopClientError{msg: translateCredentialError(err)})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": view})
}

// DeleteCredential 删除某 CLI 种类的凭据。
func (h *SOPHandler) DeleteCredential(c *gin.Context) {
	tenantID := sopTenantID(c)
	if tenantID == 0 || h.credSvc == nil {
		return
	}
	if err := h.credSvc.Delete(c.Request.Context(), tenantID, sopUserID(c),
		c.Param("kind")); err != nil {
		sopFail(c, err, "凭据不存在")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"deleted": true}})
}

// CLIStatus 本机 CLI 安装状态（含版本与安装指引）。
func (h *SOPHandler) CLIStatus(c *gin.Context) {
	if sqlTenant := sopTenantID(c); sqlTenant == 0 || h.credSvc == nil {
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": h.credSvc.Status(c.Request.Context())})
}

// translateCredentialError 把凭据校验错误翻成中文。
func translateCredentialError(err error) string {
	msg := err.Error()
	if strings.Contains(msg, "unknown cli_kind") {
		return "不支持的本地 CLI 类型"
	}
	return msg
}

// ---- PM 归档摘要 ----

// PMArchiveSummary PM→SOP 迁移的归档计数（项目/事项/运行/附件）。
func (h *SOPHandler) PMArchiveSummary(c *gin.Context) {
	tenantID := sopTenantID(c)
	if tenantID == 0 || h.archive == nil {
		return
	}
	summary, err := h.archive.Summary(c.Request.Context(), tenantID)
	if err != nil {
		sopAbort(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": summary})
}
