package middleware

import (
	"context"
	"net/http"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestTenantOptionalAPISurface(t *testing.T) {
	tests := []struct {
		method string
		path   string
		want   bool
	}{
		{http.MethodGet, "/api/v1/auth/me", true},
		{http.MethodPut, "/api/v1/auth/me", true},
		{http.MethodPut, "/api/v1/auth/me/preferences", true},
		{http.MethodPost, "/api/v1/tenants", true},
		{http.MethodGet, "/api/v1/me/invitations", true},
		{http.MethodPost, "/api/v1/me/invitations/12/accept", true},
		// 用户级工作台通知：被移出最后一个 workspace 后仍要能读（§5.7）。
		{http.MethodGet, "/api/v1/me/workbench-notices", true},
		{http.MethodPost, "/api/v1/me/workbench-notices/read-all", true},
		{http.MethodPost, "/api/v1/me/workbench-notices/n-1/read", true},
		{http.MethodGet, "/api/v1/knowledge-bases", false},
		{http.MethodGet, "/api/v1/tenants", false},
		// 离开预览需要租户统计，不能被放行。
		{http.MethodGet, "/api/v1/workbench/leave-preview", false},
	}
	for _, tt := range tests {
		if got := isTenantOptionalAPI(tt.path, tt.method); got != tt.want {
			t.Errorf("isTenantOptionalAPI(%s %s) = %v, want %v", tt.method, tt.path, got, tt.want)
		}
	}
}

func TestResolveFirstMembershipTarget(t *testing.T) {
	members := newFakeMemberService()
	members.seedActive("tenantless-user", 42, types.TenantRoleViewer)
	tenants := &fakeTenantService{tenant: &types.Tenant{ID: 42}}

	got := resolveFirstMembershipTarget(
		context.Background(),
		&types.User{ID: "tenantless-user"},
		members,
		tenants,
	)
	if got != 42 {
		t.Fatalf("resolved tenant = %d, want 42", got)
	}
}
