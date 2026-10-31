package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

// writableShareLookup mimics CheckTenantKBPermission: editor shares are capped
// at viewer for a tenant Viewer caller.
type writableShareLookup map[string]types.OrgMemberRole

func (l writableShareLookup) CheckTenantKBPermission(
	_ context.Context, kbID string, _ uint64, role types.TenantRole,
) (types.OrgMemberRole, bool, error) {
	permission, ok := l[kbID]
	if ok && role == types.TenantRoleViewer {
		permission = types.OrgRoleViewer
	}
	return permission, ok, nil
}

// writableKBLookup 是 KBVisibilityLookup 的测试替身：map[kbID]visibility。
// 没列出的 KB 返回 workspace 语义（非私有），与旧行为一致。
type writableKBLookup map[string]string

func (l writableKBLookup) GetKnowledgeBaseByID(
	_ context.Context, id string,
) (*types.KnowledgeBase, error) {
	visibility := types.KBVisibilityWorkspace
	if v, ok := l[id]; ok {
		visibility = v
	}
	return &types.KnowledgeBase{
		ID: id, TenantID: 42, Visibility: visibility,
		IsSystem: visibility == types.KBVisibilityPrivate,
	}, nil
}

func TestKBWritableIDs(t *testing.T) {
	targets := types.SearchTargets{
		{KnowledgeBaseID: "own", TenantID: 42},
		{KnowledgeBaseID: "shared-editor", TenantID: 7},
		{KnowledgeBaseID: "shared-viewer", TenantID: 7},
		{KnowledgeBaseID: "agent-scope", TenantID: 7},
	}
	shares := writableShareLookup{"shared-editor": types.OrgRoleEditor, "shared-viewer": types.OrgRoleViewer}
	kbs := writableKBLookup{}
	caller := func(role types.TenantRole, userID string) context.Context {
		return types.WithCaller(context.Background(), types.Caller{TenantID: 42, UserID: userID, Role: role})
	}
	apiKey := func(scope types.TenantAPIKeyScope) context.Context {
		return types.WithTenantAPIKeyScope(caller(types.TenantRoleViewer, "system-42"), scope)
	}

	require.Equal(t, []string{"own", "shared-editor"},
		kbWritableIDs(caller(types.TenantRoleContributor, "u"), shares, kbs, targets, true))
	require.Equal(t, []string{"own"}, kbWritableIDs(caller(types.TenantRoleContributor, ""), shares, kbs, targets, true),
		"callers without a user do not expand through org shares")

	// A tenant Viewer (also the IM / embed / MCP endpoint principals) is
	// read-only, like on the HTTP write routes; with RBAC enforcement off the
	// role check only logs, but a share never grants a Viewer more than read.
	require.Empty(t, kbWritableIDs(caller(types.TenantRoleViewer, "u"), shares, kbs, targets, true))
	require.Equal(t, []string{"own"}, kbWritableIDs(caller(types.TenantRoleViewer, "u"), shares, kbs, targets, false))

	// Scoped API keys write only with the ingest capability.
	chatOnly := types.TenantAPIKeyScope{Capabilities: types.StringArray{string(types.APIKeyCapabilityChat)}}
	require.Empty(t, kbWritableIDs(apiKey(chatOnly), shares, kbs, targets, true))
	ingest := types.TenantAPIKeyScope{Capabilities: types.StringArray{string(types.APIKeyCapabilityIngest)}}
	require.Equal(t, []string{"own"}, kbWritableIDs(apiKey(ingest), shares, kbs, targets, true))
}

// 私有 KB 即使同租户、即使是本人，也不进可写集合 —— 写入必须走显式 grant。
func TestKBWritableIDsExcludesPrivateKB(t *testing.T) {
	targets := types.SearchTargets{{KnowledgeBaseID: "own-work-kb", TenantID: 42}}
	kbs := writableKBLookup{"own-work-kb": types.KBVisibilityPrivate}
	ctx := types.WithCaller(context.Background(), types.Caller{
		TenantID: 42, UserID: "u", Role: types.TenantRoleContributor,
	})
	require.Empty(t, kbWritableIDs(ctx, writableShareLookup{}, kbs, targets, true))
}
