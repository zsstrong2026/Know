package access

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// RequireKBWrite consumes an explicit operation grant. Tenant ownership and
// role checks belong to the entry point that resolved the grant; an execution
// tenant alone, or a shared-agent/read grant, never authorizes a mutation.
func RequireKBWrite(ctx context.Context, kb *types.KnowledgeBase) error {
	if kb == nil || kb.ID == "" || kb.TenantID == 0 {
		return ErrNotFound
	}
	// 私有知识库对不可见调用者按不存在处理，不进入后续能力判定。
	if !CanSeeKB(ctx, kb) {
		return ErrNotFound
	}
	if scope, ok := types.TenantAPIKeyScopeFromContext(ctx); ok && !scope.FullAccess &&
		!scope.HasCapability(types.APIKeyCapabilityIngest) {
		return ErrForbidden
	}
	if !HasKBGrant(ctx, kb.ID, kb.TenantID, types.OrgRoleEditor) {
		return ErrForbidden
	}
	return nil
}

// WithKBTaskWrite is for trusted workers continuing an admitted operation.
// The worker supplies a server-loaded KB and verifies its child-resource
// bindings before any side effects. It grants only this KB, preserves the
// original caller (including an absent caller), and never creates an Admin.
// This is a task execution grant, not a way to authorize an incoming request.
//
// 因此私有知识库的 task 授权只能由已通过 CanSeeKB 的请求或任务延续产生，
// 不能在入口处凭空构造，否则会绕过私有可见性判定。
func WithKBTaskWrite(ctx context.Context, kb *types.KnowledgeBase, expectedTenant uint64) (context.Context, error) {
	if kb == nil || kb.ID == "" || expectedTenant == 0 || kb.TenantID != expectedTenant {
		return ctx, ErrForbidden
	}
	ctx = types.WithExecutionTenant(ctx, expectedTenant)
	grant := kbGrant{
		caller:     types.CallerFromContext(ctx),
		kbID:       kb.ID,
		tenantID:   expectedTenant,
		permission: types.OrgRoleEditor,
		task:       true,
	}
	// A worker gets exactly one scope, without inheriting another task's KBs.
	return context.WithValue(ctx, types.KBGrantsContextKey, []kbGrant{grant}), nil
}
