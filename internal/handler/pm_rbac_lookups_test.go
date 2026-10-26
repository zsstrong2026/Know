package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	apprepo "github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// stubPMProjectService 只桩 GetProject；其余方法沿用内嵌接口，
// 一旦 lookup 越界调用其它方法会 nil panic，能立刻暴露契约漂移。
type stubPMProjectService struct {
	interfaces.PMProjectService
	get func(ctx context.Context, tenantID uint64, id string) (*types.PMProject, error)
}

func (s *stubPMProjectService) GetProject(
	ctx context.Context, tenantID uint64, id string,
) (*types.PMProject, error) {
	return s.get(ctx, tenantID, id)
}

// newPMLookupCtx 构造 /pm/projects/:id 形态的 gin.Context
func newPMLookupCtx(t *testing.T, tenantID uint64, id string) *gin.Context {
	t.Helper()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/x", nil)
	c.Request = c.Request.WithContext(
		context.WithValue(c.Request.Context(), types.TenantIDContextKey, tenantID),
	)
	c.Params = gin.Params{{Key: "id", Value: id}}
	return c
}

func TestPMProjectCreatorLookupNotFound(t *testing.T) {
	// 资源不存在必须翻译成中间件哨兵，由中间件放行、handler 统一发 404
	h := &PMHandler{projectService: &stubPMProjectService{
		get: func(_ context.Context, _ uint64, _ string) (*types.PMProject, error) {
			return nil, apprepo.ErrPMProjectNotFound
		},
	}}
	creator, err := h.PMProjectCreatorLookup(newPMLookupCtx(t, 1, "p-1"))
	if !errors.Is(err, middleware.ErrResourceNotFound) {
		t.Fatalf("expected ErrResourceNotFound, got %v", err)
	}
	if creator != "" {
		t.Fatalf("not-found lookup must return empty creator, got %q", creator)
	}
}

func TestPMProjectCreatorLookupOK(t *testing.T) {
	h := &PMHandler{projectService: &stubPMProjectService{
		get: func(_ context.Context, tenantID uint64, id string) (*types.PMProject, error) {
			if tenantID != 1 || id != "p-1" {
				t.Fatalf("unexpected lookup args tenant=%d id=%q", tenantID, id)
			}
			return &types.PMProject{ID: id, TenantID: tenantID, CreatedBy: "u-creator"}, nil
		},
	}}
	creator, err := h.PMProjectCreatorLookup(newPMLookupCtx(t, 1, "p-1"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if creator != "u-creator" {
		t.Fatalf("expected creator=u-creator, got %q", creator)
	}
}
