package access

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

// privateKBLookup 只把一个指定 id 标记为私有，其余按 workspace 处理。
type privateKBLookup struct {
	privateID   string
	tenantID    uint64
	creatorID   string
	unloadedIDs map[string]bool
}

func (l privateKBLookup) GetKnowledgeBaseByID(
	_ context.Context, id string,
) (*types.KnowledgeBase, error) {
	if l.unloadedIDs[id] {
		return nil, nil
	}
	kb := &types.KnowledgeBase{
		ID:         id,
		TenantID:   l.tenantID,
		Visibility: types.KBVisibilityWorkspace,
	}
	if id == l.privateID {
		kb.Visibility = types.KBVisibilityPrivate
		kb.IsSystem = true
		kb.SystemKey = types.KBSystemKeyWork
		kb.CreatorID = l.creatorID
	}
	return kb, nil
}

// 私有 KB 的可见性必须在 own-tenant 快捷放行之前生效：
// 同租户的普通成员、Admin、API Key 都拿不到 grant，一律 NotFound 语义。
func TestKBPermissionsHidesPrivateKBFromTenantMembers(t *testing.T) {
	lookup := privateKBLookup{privateID: "work", tenantID: 1, creatorID: "owner"}
	cases := []struct {
		name   string
		caller types.Caller
	}{
		{name: "same tenant other member", caller: types.Caller{TenantID: 1, UserID: "other", Role: types.TenantRoleContributor}},
		{name: "same tenant admin", caller: types.Caller{TenantID: 1, UserID: "admin", Role: types.TenantRoleAdmin}},
		{name: "another tenant", caller: types.Caller{TenantID: 2, UserID: "owner", Role: types.TenantRoleOwner}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := types.WithCaller(context.Background(), tc.caller)
			allowed, err := NewKBPermissions(ctx, nil, lookup).Check("work", 1, types.OrgRoleViewer)
			require.NoError(t, err)
			require.False(t, allowed, "私有 KB 对非创建者不可见")
		})
	}
}

// 创建者本人可以读到自己的私有 KB（读写权限仍按既有 grant 规则）。
func TestKBPermissionsAllowsPrivateKBOwner(t *testing.T) {
	lookup := privateKBLookup{privateID: "work", tenantID: 1, creatorID: "owner"}
	ctx := types.WithCaller(context.Background(), types.Caller{
		TenantID: 1, UserID: "owner", Role: types.TenantRoleContributor,
	})
	allowed, err := NewKBPermissions(ctx, nil, lookup).Check("work", 1, types.OrgRoleViewer)
	require.NoError(t, err)
	require.True(t, allowed)
}

// 租户 API Key 即使 full-access、即使带上了该 KB id，也读不到私有 KB。
func TestKBPermissionsRejectsAPIKeyOnPrivateKB(t *testing.T) {
	lookup := privateKBLookup{privateID: "work", tenantID: 1, creatorID: "owner"}
	ctx := types.WithTenantAPIKeyScope(
		types.WithCaller(context.Background(), types.Caller{
			TenantID: 1, UserID: "system-1", Role: types.TenantRoleAdmin,
		}),
		types.TenantAPIKeyScope{FullAccess: true, KnowledgeBaseIDs: types.StringArray{"work"}},
	)
	allowed, err := NewKBPermissions(ctx, nil, lookup).Check("work", 1, types.OrgRoleViewer)
	require.NoError(t, err)
	require.False(t, allowed)
}

// 没有 KB 查询能力时必须拒绝，而不是退化成“放行”。
// 这条保证任何忘记注入 lookup 的调用点都不会悄悄绕过私有可见性。
func TestKBPermissionsNilLookupDenies(t *testing.T) {
	ctx := types.WithCaller(context.Background(), types.Caller{
		TenantID: 1, UserID: "owner", Role: types.TenantRoleOwner,
	})
	allowed, err := NewKBPermissions(ctx, nil, nil).Check("any", 1, types.OrgRoleViewer)
	require.NoError(t, err)
	require.False(t, allowed)
}

// CheckKB 直接接收已加载对象，行为必须与 Check 一致。
func TestKBPermissionsCheckKBMirrorsCheck(t *testing.T) {
	ctx := types.WithCaller(context.Background(), types.Caller{
		TenantID: 1, UserID: "other", Role: types.TenantRoleContributor,
	})
	privateKB := &types.KnowledgeBase{
		ID: "work", TenantID: 1, Visibility: types.KBVisibilityPrivate, CreatorID: "owner",
	}
	permissions := NewKBPermissions(ctx, nil, privateKBLookup{privateID: "work", tenantID: 1, creatorID: "owner"})
	allowed, err := permissions.CheckKB(privateKB, types.OrgRoleViewer)
	require.NoError(t, err)
	require.False(t, allowed)

	sameIDAllowed, err := permissions.Check("work", 1, types.OrgRoleViewer)
	require.NoError(t, err)
	require.Equal(t, allowed, sameIDAllowed)
}

// 同一请求内重复 Check 同一个 KB 只查一次库（cache 生效）。
func TestKBPermissionsCachesLookupPerRequest(t *testing.T) {
	counter := &countingKBLookup{}
	ctx := types.WithCaller(context.Background(), types.Caller{
		TenantID: 1, UserID: "u", Role: types.TenantRoleContributor,
	})
	permissions := NewKBPermissions(ctx, nil, counter)
	for i := 0; i < 5; i++ {
		_, err := permissions.Check("kb", 1, types.OrgRoleViewer)
		require.NoError(t, err)
	}
	require.Equal(t, 1, counter.calls, "同一 KBPermissions 实例内应命中缓存")
}

type countingKBLookup struct{ calls int }

func (c *countingKBLookup) GetKnowledgeBaseByID(
	_ context.Context, id string,
) (*types.KnowledgeBase, error) {
	c.calls++
	return &types.KnowledgeBase{ID: id, TenantID: 1, Visibility: types.KBVisibilityWorkspace}, nil
}
