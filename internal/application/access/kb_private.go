package access

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// CanSeeKB 是私有知识库的唯一可见性闸门。
//
// 返回 false 时调用方必须按「知识库不存在」处理（返回 NotFound/404），
// 不得返回 Forbidden/403，也不得使用差异化错误信息，避免向调用者暴露
// 私有知识库的存在性。
func CanSeeKB(ctx context.Context, kb *types.KnowledgeBase) bool {
	if kb == nil || kb.ID == "" {
		return false
	}
	// 工作区知识库沿用既有 ACL 判定，本函数只收紧私有知识库。
	if !kb.IsPrivate() {
		return true
	}
	// 任务授权来自已通过 ACL 校验的上游请求，允许后台 worker 继续处理。
	if HasTaskKBGrant(ctx, kb.ID, kb.TenantID) {
		return true
	}
	// 租户 API Key 主体（包括全权 Key）一律不可见私有知识库。
	if _, ok := types.TenantAPIKeyScopeFromContext(ctx); ok {
		return false
	}
	caller := types.CallerFromContext(ctx)
	if caller.TenantID == 0 || caller.TenantID != kb.TenantID {
		return false
	}
	// 缺失用户或合成用户（X-API-Key 的 system-<tenantID>）没有「本人」概念。
	if caller.UserID == "" || types.IsSyntheticUserID(caller.UserID) {
		return false
	}
	return caller.UserID == kb.CreatorID
}

// HasTaskKBGrant reports whether ctx carries a task-scoped grant for this KB.
func HasTaskKBGrant(ctx context.Context, kbID string, tenantID uint64) bool {
	if kbID == "" || tenantID == 0 {
		return false
	}
	grants, _ := ctx.Value(types.KBGrantsContextKey).([]kbGrant)
	for _, grant := range grants {
		if grant.task && grant.kbID == kbID && grant.tenantID == tenantID {
			return true
		}
	}
	return false
}
