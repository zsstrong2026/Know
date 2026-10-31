package access

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

// privateKB 构造一个属于 tenant 1、创建者为 owner 的私有知识库。
func privateKB(owner string) *types.KnowledgeBase {
	return &types.KnowledgeBase{
		ID:         "kb-private",
		TenantID:   1,
		CreatorID:  owner,
		Visibility: types.KBVisibilityPrivate,
	}
}

// asCaller 在给定上下文上覆盖调用者身份（租户 + 用户 + 角色）。
func asCaller(ctx context.Context, tenantID uint64, userID string, role types.TenantRole) context.Context {
	return types.WithCaller(ctx, types.Caller{TenantID: tenantID, UserID: userID, Role: role})
}

func TestCanSeeKB(t *testing.T) {
	base := context.Background()
	apiKeyCtx := types.WithTenantAPIKeyScope(base, types.TenantAPIKeyScope{KeyID: 9, FullAccess: true})

	tests := []struct {
		name string
		ctx  context.Context
		kb   *types.KnowledgeBase
		want bool
	}{
		{
			name: "nil knowledge base",
			ctx:  asCaller(base, 1, "owner", types.TenantRoleContributor),
			kb:   nil,
			want: false,
		},
		{
			name: "empty id",
			ctx:  asCaller(base, 1, "owner", types.TenantRoleContributor),
			kb:   &types.KnowledgeBase{TenantID: 1, CreatorID: "owner", Visibility: types.KBVisibilityPrivate},
			want: false,
		},
		{
			name: "workspace kb visible to api key",
			ctx:  apiKeyCtx,
			kb:   &types.KnowledgeBase{ID: "kb", TenantID: 1, Visibility: types.KBVisibilityWorkspace},
			want: true,
		},
		{
			name: "private kb visible to owner",
			ctx:  asCaller(base, 1, "owner", types.TenantRoleContributor),
			kb:   privateKB("owner"),
			want: true,
		},
		{
			name: "private kb hidden from other member of same tenant",
			ctx:  asCaller(base, 1, "colleague", types.TenantRoleContributor),
			kb:   privateKB("owner"),
			want: false,
		},
		{
			name: "private kb hidden from admin",
			ctx:  asCaller(base, 1, "boss", types.TenantRoleAdmin),
			kb:   privateKB("owner"),
			want: false,
		},
		{
			name: "private kb hidden from other tenant",
			ctx:  asCaller(base, 2, "owner", types.TenantRoleAdmin),
			kb:   privateKB("owner"),
			want: false,
		},
		{
			name: "private kb hidden from api key with full access",
			ctx:  apiKeyCtx,
			kb:   privateKB("owner"),
			want: false,
		},
		{
			name: "private kb hidden from synthetic user",
			ctx:  asCaller(base, 1, "system-1", types.TenantRoleAdmin),
			kb:   privateKB("owner"),
			want: false,
		},
		{
			name: "private kb hidden when caller user is empty",
			ctx:  asCaller(base, 1, "", types.TenantRoleAdmin),
			kb:   privateKB(""),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, CanSeeKB(tt.ctx, tt.kb))
		})
	}
}

func TestCanSeeKBAllowsTaskGrant(t *testing.T) {
	kb := privateKB("owner")
	// 后台 worker 没有真实调用者，只能凭任务授权继续处理。
	taskCtx, err := WithKBTaskWrite(context.Background(), kb, kb.TenantID)
	require.NoError(t, err)
	require.True(t, CanSeeKB(taskCtx, kb), "task grant must survive visibility check")
	require.True(t, HasTaskKBGrant(taskCtx, kb.ID, kb.TenantID))

	require.False(t, HasTaskKBGrant(context.Background(), kb.ID, kb.TenantID))
	require.False(t, HasTaskKBGrant(taskCtx, "", kb.TenantID))
	require.False(t, HasTaskKBGrant(taskCtx, kb.ID, 0))
}

// TestResolveKBPrivateHidesExistence 确认所有不可见身份拿到的都是 NotFound，
// 而不是 Forbidden，以免差异化的错误泄漏私有知识库的存在性。
func TestResolveKBPrivateHidesExistence(t *testing.T) {
	kb := privateKB("owner")

	tests := []struct {
		name   string
		ctx    context.Context
		caller types.Caller
		want   error
	}{
		{
			name:   "owner resolves",
			ctx:    asCaller(context.Background(), 1, "owner", types.TenantRoleContributor),
			caller: types.Caller{TenantID: 1, UserID: "owner", Role: types.TenantRoleContributor},
			want:   nil,
		},
		{
			name:   "other member gets not found",
			ctx:    asCaller(context.Background(), 1, "colleague", types.TenantRoleContributor),
			caller: types.Caller{TenantID: 1, UserID: "colleague", Role: types.TenantRoleContributor},
			want:   ErrNotFound,
		},
		{
			name:   "admin gets not found",
			ctx:    asCaller(context.Background(), 1, "boss", types.TenantRoleAdmin),
			caller: types.Caller{TenantID: 1, UserID: "boss", Role: types.TenantRoleAdmin},
			want:   ErrNotFound,
		},
		{
			name: "api key gets not found",
			ctx: types.WithTenantAPIKeyScope(
				asCaller(context.Background(), 1, "system-1", types.TenantRoleAdmin),
				types.TenantAPIKeyScope{KeyID: 1, FullAccess: true},
			),
			caller: types.Caller{TenantID: 1, UserID: "system-1", Role: types.TenantRoleAdmin},
			want:   ErrNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveKB(tt.ctx, KBRequest{Caller: tt.caller}, kb, types.OrgRoleViewer, nil, nil)
			if tt.want != nil {
				require.ErrorIs(t, err, tt.want)
				require.Nil(t, got)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, got)
		})
	}
}
