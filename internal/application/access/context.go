package access

import (
	"context"
	"sync"

	"github.com/Tencent/WeKnora/internal/types"
)

// Context grants contain values, not mutable KB objects. Deriving a context
// copies the grant slice so parallel search branches cannot widen each other.
type kbGrant struct {
	caller     types.Caller
	kbID       string
	tenantID   uint64
	permission types.OrgMemberRole
	task       bool
}

type agentGrant struct {
	caller types.Caller
	scope  types.SharedAgentKBScope
}

// WithSharedAgent scopes execution after the caller has authorized this exact
// agent. Only its configured KBs receive read access; other source-tenant KBs
// and the source tenant's organization memberships are not inherited.
func WithSharedAgent(ctx context.Context, agent *types.CustomAgent) context.Context {
	grant := agentGrant{caller: types.CallerFromContext(ctx), scope: types.NewSharedAgentKBScope(agent)}
	ctx = context.WithValue(ctx, types.SharedAgentGrantContextKey, grant)
	if agent == nil {
		return ctx
	}
	return types.WithExecutionTenant(ctx, agent.TenantID)
}

// HasKBGrant checks only previously resolved resource access. It never infers
// ownership from the execution tenant, and always reapplies API-key scope.
func HasKBGrant(ctx context.Context, kbID string, tenantID uint64, required types.OrgMemberRole) bool {
	if !required.IsValid() || types.AuthorizeTenantAPIKeyKnowledgeBases(ctx, kbID) != nil {
		return false
	}
	caller := types.CallerFromContext(ctx)
	if kbID == "" || tenantID == 0 {
		return false
	}
	grants, _ := ctx.Value(types.KBGrantsContextKey).([]kbGrant)
	for _, grant := range grants {
		if (caller.TenantID != 0 || grant.task) && grant.caller == caller && grant.kbID == kbID &&
			grant.tenantID == tenantID &&
			grant.permission.HasPermission(required) {
			return true
		}
	}
	grant, ok := ctx.Value(types.SharedAgentGrantContextKey).(agentGrant)
	return ok && caller.TenantID != 0 && required == types.OrgRoleViewer && grant.caller == caller &&
		grant.scope.Allows(kbID, tenantID)
}

// KBVisibilityLookup 是可见性判定所需的最小依赖面：
// 只按 id 加载 KB（不过滤 tenant），因为跨租户共享的 KB 也要能判断可见性。
// knowledgeBaseService 天然满足；测试用一个小 fake。
type KBVisibilityLookup interface {
	GetKnowledgeBaseByID(ctx context.Context, id string) (*types.KnowledgeBase, error)
}

// KBPermissions combines caller ownership, exact context grants and organization
// shares for one service operation. Pass nil shares when that entry point does
// not permit organization expansion (for example, a caller without a user).
type KBPermissions struct {
	ctx    context.Context
	caller types.Caller
	shares *KBSharePermissions
	kbs    KBVisibilityLookup

	mu    sync.Mutex
	cache map[string]kbVisibilityResult
}

// kbVisibilityResult 缓存一次请求内加载过的 KB，避免 knowledge/chunk 循环里重复查库。
type kbVisibilityResult struct {
	kb  *types.KnowledgeBase
	err error
}

// NewKBPermissions resolves reads for one caller and operation.
//
// kbs 必须传入真实的 KB 查询能力：私有知识库的可见性判定依赖它，
// 传 nil 会让所有 Check 一律拒绝（宁可拒绝也不放过）。
func NewKBPermissions(
	ctx context.Context, shares KBShareLookup, kbs KBVisibilityLookup,
) *KBPermissions {
	caller := types.CallerFromContext(ctx)
	return &KBPermissions{
		ctx:    ctx,
		caller: caller,
		shares: NewKBSharePermissions(ctx, shares, caller.TenantID, caller.Role),
		kbs:    kbs,
		cache:  make(map[string]kbVisibilityResult),
	}
}

// CheckKB 用于调用方已经持有 KB 对象的路径，避免额外查库。
func (p *KBPermissions) CheckKB(kb *types.KnowledgeBase, required types.OrgMemberRole) (bool, error) {
	if kb == nil || kb.ID == "" || !required.IsValid() {
		return false, nil
	}
	if !CanSeeKB(p.ctx, kb) {
		return false, nil
	}
	return p.checkResolved(kb.ID, kb.TenantID, required)
}

// Check 先加载 KB 做可见性判定，再走既有的归属/授权/共享逻辑。
//
// 这一步不能省：原来的快捷路径 `caller.TenantID == ownerTenantID` 只看
// kbID + tenantID，不知道 visibility/creator_id，同租户成员对私有 KB 会直接放行。
func (p *KBPermissions) Check(kbID string, ownerTenantID uint64, required types.OrgMemberRole) (bool, error) {
	if kbID == "" || ownerTenantID == 0 || !required.IsValid() {
		return false, nil
	}
	kb, err := p.lookup(kbID)
	if err != nil {
		return false, err
	}
	// 加载不到、或调用方声明/伪造了不一致的 tenant，都按无权处理。
	if kb == nil || kb.ID != kbID || kb.TenantID != ownerTenantID {
		return false, nil
	}
	return p.CheckKB(kb, required)
}

func (p *KBPermissions) checkResolved(kbID string, ownerTenantID uint64, required types.OrgMemberRole) (bool, error) {
	if err := types.AuthorizeTenantAPIKeyKnowledgeBases(p.ctx, kbID); err != nil {
		return false, err
	}
	if (p.caller.TenantID == ownerTenantID && required == types.OrgRoleViewer) ||
		HasKBGrant(p.ctx, kbID, ownerTenantID, required) {
		return true, nil
	}
	if p.caller.TenantID == 0 || p.caller.TenantID == ownerTenantID {
		return false, nil
	}
	return p.shares.Check(kbID, required)
}

// Lookup 暴露一次带缓存的 KB 加载，供“先判类型再决定后续规则”的调用方使用
// （例如 kbWritableIDs 需要知道目标是否是私有 KB）。查不到返回 nil。
func (p *KBPermissions) Lookup(kbID string) *types.KnowledgeBase {
	if p == nil || kbID == "" {
		return nil
	}
	kb, err := p.lookup(kbID)
	if err != nil {
		return nil
	}
	return kb
}

// lookup 按 id 加载 KB，并在本次 KBPermissions 生命周期内缓存。
func (p *KBPermissions) lookup(kbID string) (*types.KnowledgeBase, error) {
	p.mu.Lock()
	if cached, ok := p.cache[kbID]; ok {
		p.mu.Unlock()
		return cached.kb, cached.err
	}
	p.mu.Unlock()

	if p.kbs == nil {
		// 没有查询能力就无法判定私有可见性：拒绝，而不是默认放行。
		return nil, nil
	}
	kb, err := p.kbs.GetKnowledgeBaseByID(p.ctx, kbID)
	p.mu.Lock()
	p.cache[kbID] = kbVisibilityResult{kb: kb, err: err}
	p.mu.Unlock()
	return kb, err
}
