package handler

import (
	stderrors "errors"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"

	apprepo "github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
)

// UploadRequirementAttachment POST /api/v1/pm/requirements/:id/attachments
// multipart/form-data，字段名 file；支持图片与常见文档。
func (h *PMHandler) UploadRequirementAttachment(c *gin.Context) {
	// 请求体上限略大于单文件上限，超限时 FormFile 直接报错
	c.Request.Body = http.MaxBytesReader(
		c.Writer, c.Request.Body, types.PMRequirementAttachmentMaxSize+(1<<20),
	)
	fileHeader, err := c.FormFile("file")
	if err != nil {
		c.Error(errors.NewBadRequestError("file is required"))
		return
	}
	if fileHeader.Size > types.PMRequirementAttachmentMaxSize {
		pmFail(c, service.ErrRequirementAttachmentTooLarge, nil, "")
		return
	}
	f, err := fileHeader.Open()
	if err != nil {
		c.Error(errors.NewBadRequestError("file is required"))
		return
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		c.Error(errors.NewBadRequestError("failed to read file"))
		return
	}

	attachment, err := h.requirementService.AddAttachment(
		c.Request.Context(), pmTenantID(c), pmUserID(c), c.Param("id"),
		fileHeader.Filename, fileHeader.Header.Get("Content-Type"), data,
	)
	if err != nil {
		pmFail(c, err, apprepo.ErrPMRequirementNotFound, "requirement not found")
		return
	}
	c.JSON(http.StatusCreated, gin.H{"success": true, "data": attachment})
}

// ListRequirementAttachments GET /api/v1/pm/requirements/:id/attachments
func (h *PMHandler) ListRequirementAttachments(c *gin.Context) {
	list, err := h.requirementService.ListAttachments(
		c.Request.Context(), pmTenantID(c), c.Param("id"),
	)
	if err != nil {
		pmFail(c, err, apprepo.ErrPMRequirementNotFound, "requirement not found")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": list})
}

// DeleteRequirementAttachment DELETE /api/v1/pm/requirements/:id/attachments/:attachment_id
func (h *PMHandler) DeleteRequirementAttachment(c *gin.Context) {
	err := h.requirementService.DeleteAttachment(
		c.Request.Context(), pmTenantID(c), c.Param("id"), c.Param("attachment_id"),
	)
	if err != nil {
		pmFailAttachment(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// DownloadRequirementAttachment GET /api/v1/pm/requirements/:id/attachments/:attachment_id/download
func (h *PMHandler) DownloadRequirementAttachment(c *gin.Context) {
	attachment, err := h.requirementService.GetAttachmentFile(
		c.Request.Context(), pmTenantID(c), c.Param("id"), c.Param("attachment_id"),
	)
	if err != nil {
		pmFailAttachment(c, err)
		return
	}
	c.Header("Content-Disposition", exportContentDisposition(attachment.FileName))
	c.Data(http.StatusOK, attachment.ContentType, attachment.Data)
}

// pmFailAttachment 附件类错误的统一出口：附件不存在与需求不存在都按 404 处理
func pmFailAttachment(c *gin.Context, err error) {
	if stderrors.Is(err, apprepo.ErrPMRequirementAttachmentNotFound) {
		c.Error(errors.NewNotFoundError("attachment not found"))
		return
	}
	pmFail(c, err, apprepo.ErrPMRequirementNotFound, "requirement not found")
}
