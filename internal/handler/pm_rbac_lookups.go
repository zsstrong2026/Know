package handler

import (
	"errors"

	"github.com/gin-gonic/gin"

	apprepo "github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
)

// PMProjectCreatorLookup resolves :id -> PMProject.CreatedBy，供
// middleware.RequireOwnershipOrRole 使用。项目不存在（含跨租户）时返回
// ErrResourceNotFound，让中间件放行、由 handler 统一发 404。
func (h *PMHandler) PMProjectCreatorLookup(c *gin.Context) (string, error) {
	id := c.Param("id")
	if id == "" {
		return "", errors.New("missing :id param for pm project creator lookup")
	}
	ctx := c.Request.Context()
	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok {
		return "", errors.New("workspace context missing")
	}
	project, err := h.projectService.GetProject(ctx, tenantID, id)
	if err != nil {
		if errors.Is(err, apprepo.ErrPMProjectNotFound) {
			return "", middleware.ErrResourceNotFound
		}
		return "", err
	}
	if project == nil {
		return "", middleware.ErrResourceNotFound
	}
	return project.CreatedBy, nil
}

// 编译期断言：签名漂移时立即暴露
var _ middleware.CreatorLookup = (*PMHandler)(nil).PMProjectCreatorLookup
