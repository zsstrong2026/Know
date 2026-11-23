// sop_templates.go 是流程模板的 HTTP 端点：草稿增删改、发布（白话校验）、
// 副本、发布历史与单条快照。
//
// 关键口径：
//   - 内置模板只读：修改路径是「复制副本」（POST /copy），handler 把只读哨兵映射 400；
//   - 发布校验失败逐条白话返回，前端直接展示，不需要翻译；
//   - 保存草稿时门禁命令由服务端按定义重算，不信任前端/AI 传来的值。
package handler

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/application/service/sop"
	"github.com/Tencent/WeKnora/internal/types"
)

// sopTemplatePayload 新建/保存草稿的请求体。
type sopTemplatePayload struct {
	ProjectID      string               `json:"project_id"`
	Name           *string              `json:"name"`
	Description    *string              `json:"description"`
	MetricCategory *string              `json:"metric_category"`
	Definition     *types.SopDefinition `json:"definition"`
}

// CreateTemplate 新建草稿模板。
func (h *SOPHandler) CreateTemplate(c *gin.Context) {
	tenantID := sopTenantID(c)
	if tenantID == 0 || h.templates == nil {
		return
	}
	var req sopTemplatePayload
	if !sopBindJSON(c, &req) {
		return
	}
	in := sop.TemplateInput{ProjectID: req.ProjectID}
	if req.Name != nil {
		in.Name = *req.Name
	}
	if req.Description != nil {
		in.Description = *req.Description
	}
	if req.MetricCategory != nil {
		in.MetricCategory = *req.MetricCategory
	}
	if req.Definition != nil {
		in.Definition = *req.Definition
	}
	tpl, err := h.templates.Create(c.Request.Context(), tenantID, sopUserID(c), in)
	if err != nil {
		sopFail(c, err, "项目不存在")
		return
	}
	c.JSON(http.StatusCreated, gin.H{"success": true, "data": tpl})
}

// ListTemplates 列可用模板（本租户全局 + 指定项目 + 内置）。
func (h *SOPHandler) ListTemplates(c *gin.Context) {
	tenantID := sopTenantID(c)
	if tenantID == 0 || h.templates == nil {
		return
	}
	list, err := h.templates.List(c.Request.Context(), tenantID, c.Query("project_id"))
	if err != nil {
		sopFail(c, err, "")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": list})
}

// GetTemplate 模板详情。
func (h *SOPHandler) GetTemplate(c *gin.Context) {
	tenantID := sopTenantID(c)
	if tenantID == 0 || h.templates == nil {
		return
	}
	tpl, err := h.templates.Get(c.Request.Context(), tenantID, c.Param("id"))
	if err != nil {
		sopFail(c, err, "模板不存在")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": tpl})
}

// UpdateTemplate 保存草稿（内置 400）。只改定义与元信息，发布态与归属列由服务端独占。
func (h *SOPHandler) UpdateTemplate(c *gin.Context) {
	tenantID := sopTenantID(c)
	if tenantID == 0 || h.templates == nil {
		return
	}
	tpl, err := h.templates.Get(c.Request.Context(), tenantID, c.Param("id"))
	if err != nil {
		sopFail(c, err, "模板不存在")
		return
	}
	var req sopTemplatePayload
	if !sopBindJSON(c, &req) {
		return
	}
	if req.Name != nil {
		tpl.Name = *req.Name
	}
	if req.Description != nil {
		tpl.Description = *req.Description
	}
	if req.MetricCategory != nil {
		tpl.MetricCategory = *req.MetricCategory
	}
	if req.Definition != nil {
		tpl.Definition = *req.Definition
	}
	if err := h.templates.Save(c.Request.Context(), tenantID, tpl); err != nil {
		sopAbort(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": tpl})
}

// DeleteTemplate 软删模板（内置 400）。
func (h *SOPHandler) DeleteTemplate(c *gin.Context) {
	tenantID := sopTenantID(c)
	if tenantID == 0 || h.templates == nil {
		return
	}
	if err := h.templates.Delete(c.Request.Context(), tenantID, c.Param("id")); err != nil {
		sopAbort(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"deleted": true}})
}

// PublishTemplate 发布：校验失败返回逐条白话清单（400）。
func (h *SOPHandler) PublishTemplate(c *gin.Context) {
	tenantID := sopTenantID(c)
	if tenantID == 0 || h.templates == nil {
		return
	}
	rev, err := h.templates.Publish(c.Request.Context(), tenantID, c.Param("id"), sopUserID(c))
	if err != nil {
		sopAbort(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": rev})
}

// CopyTemplate 复制出独立草稿（内置模板的唯一修改路径）。
func (h *SOPHandler) CopyTemplate(c *gin.Context) {
	tenantID := sopTenantID(c)
	if tenantID == 0 || h.templates == nil {
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if !sopBindJSON(c, &req) {
		return
	}
	cp, err := h.templates.Copy(c.Request.Context(), tenantID, c.Param("id"), strings.TrimSpace(req.Name))
	if err != nil {
		sopAbort(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"success": true, "data": cp})
}

// ListTemplateRevisions 发布历史（revision 降序）。
func (h *SOPHandler) ListTemplateRevisions(c *gin.Context) {
	tenantID := sopTenantID(c)
	if tenantID == 0 || h.templates == nil {
		return
	}
	list, err := h.templates.ListRevisions(c.Request.Context(), tenantID, c.Param("id"))
	if err != nil {
		sopAbort(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": list})
}

// GetTemplateRevision 单条冻结快照。
func (h *SOPHandler) GetTemplateRevision(c *gin.Context) {
	tenantID := sopTenantID(c)
	if tenantID == 0 || h.templates == nil {
		return
	}
	rev, err := h.templates.GetRevision(c.Request.Context(), tenantID, c.Param("id"))
	if err != nil {
		sopFail(c, err, "发布记录不存在")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": rev})
}
