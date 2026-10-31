package service

import (
	"context"
	"sync"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/access"
	"github.com/Tencent/WeKnora/internal/application/repository"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// workKBsTestDDL 是 knowledge_bases 表在 SQLite 下的最小可用建表语句，
// 含 000136 迁移新增的 is_system / system_key / visibility 与部分唯一索引。
// 不使用 AutoMigrate，因为 KnowledgeBase 的 `type:jsonb` 字段无法映射到 SQLite。
const workKBsTestDDL = `
CREATE TABLE IF NOT EXISTS knowledge_bases (
    id VARCHAR(36) PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    type VARCHAR(32) NOT NULL DEFAULT 'document',
    is_temporary BOOLEAN NOT NULL DEFAULT 0,
    description TEXT,
    tenant_id INTEGER NOT NULL,
    creator_id VARCHAR(36),
    is_system BOOLEAN NOT NULL DEFAULT 0,
    system_key VARCHAR(64),
    visibility VARCHAR(16) NOT NULL DEFAULT 'workspace',
    chunking_config TEXT,
    image_processing_config TEXT,
    embedding_model_id VARCHAR(64) NOT NULL DEFAULT '',
    summary_model_id VARCHAR(64) NOT NULL DEFAULT '',
    vlm_config TEXT,
    asr_config TEXT,
    storage_provider_config TEXT,
    storage_backend_id VARCHAR(36) DEFAULT NULL,
    cos_config TEXT,
    vector_store_id VARCHAR(36),
    extract_config TEXT,
    faq_config TEXT,
    question_generation_config TEXT,
    auto_tag_config TEXT,
    profile_config TEXT,
    generated_profile TEXT,
    wiki_config TEXT,
    indexing_strategy TEXT,
    created_at DATETIME,
    updated_at DATETIME,
    deleted_at DATETIME
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_kb_user_system
    ON knowledge_bases (tenant_id, creator_id, system_key)
    WHERE system_key = 'work' AND deleted_at IS NULL;
`

// openWorkKBTestDB 打开内存 SQLite 并限制为单连接：
// `:memory:` 按连接隔离，单连接才能让并发用例看到同一个库并串行化写入。
func openWorkKBTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.Exec(workKBsTestDDL).Error)
	return db
}

// stubMembership 用于注入成员状态，避免测试装配整个成员仓储。
type stubMembership struct {
	status types.TenantMemberStatus
}

func (s stubMembership) Get(context.Context, string, uint64) (*types.TenantMember, error) {
	if s.status == "" {
		return nil, nil
	}
	return &types.TenantMember{Status: s.status}, nil
}

func countWorkKBs(t *testing.T, db *gorm.DB, tenantID uint64, userID string) int64 {
	t.Helper()
	var count int64
	require.NoError(t, db.Model(&types.KnowledgeBase{}).
		Where("tenant_id = ? AND creator_id = ? AND system_key = ?", tenantID, userID, types.KBSystemKeyWork).
		Count(&count).Error)
	return count
}

// TestEnsureWorkKnowledgeBaseConcurrentCreate 验证并发首次调用只落一行，
// 且所有调用方拿到同一个知识库。
func TestEnsureWorkKnowledgeBaseConcurrentCreate(t *testing.T) {
	db := openWorkKBTestDB(t)
	svc := &knowledgeBaseService{repo: repository.NewKnowledgeBaseRepository(db)}
	ctx := t.Context()

	const callers = 20
	ids := make(chan string, callers)
	errs := make(chan error, callers)
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			kb, err := svc.EnsureWorkKnowledgeBase(ctx, 1, "user-a")
			if err != nil {
				errs <- err
				return
			}
			ids <- kb.ID
		}()
	}
	wg.Wait()
	close(ids)
	close(errs)

	for err := range errs {
		require.NoError(t, err, "all concurrent callers must succeed")
	}
	var first string
	for id := range ids {
		if first == "" {
			first = id
			continue
		}
		require.Equal(t, first, id, "every caller must observe the same work KB")
	}
	require.NotEmpty(t, first)
	require.Equal(t, int64(1), countWorkKBs(t, db, 1, "user-a"))
}

func TestEnsureWorkKnowledgeBaseIsIdempotent(t *testing.T) {
	db := openWorkKBTestDB(t)
	svc := &knowledgeBaseService{repo: repository.NewKnowledgeBaseRepository(db)}
	ctx := t.Context()

	first, err := svc.EnsureWorkKnowledgeBase(ctx, 1, "user-a")
	require.NoError(t, err)
	require.True(t, first.IsSystem)
	require.Equal(t, types.KBSystemKeyWork, first.SystemKey)
	require.Equal(t, types.KBVisibilityPrivate, first.Visibility)
	require.Equal(t, "user-a", first.CreatorID)
	require.Equal(t, uint64(1), first.TenantID)

	second, err := svc.EnsureWorkKnowledgeBase(ctx, 1, "user-a")
	require.NoError(t, err)
	require.Equal(t, first.ID, second.ID)
	require.Equal(t, int64(1), countWorkKBs(t, db, 1, "user-a"))

	// 不同用户在同租户下各自一份，互不复用。
	other, err := svc.EnsureWorkKnowledgeBase(ctx, 1, "user-b")
	require.NoError(t, err)
	require.NotEqual(t, first.ID, other.ID)
	require.Equal(t, int64(1), countWorkKBs(t, db, 1, "user-b"))
}

// TestEnsureWorkKnowledgeBaseRejectsUnrepresentableCallers 覆盖两种不能拥有
// 个人工作库的调用方：租户 API Key 与成员状态非 active 的用户。
func TestEnsureWorkKnowledgeBaseRejectsUnrepresentableCallers(t *testing.T) {
	db := openWorkKBTestDB(t)
	repo := repository.NewKnowledgeBaseRepository(db)
	ctx := t.Context()

	t.Run("api key", func(t *testing.T) {
		svc := &knowledgeBaseService{repo: repo}
		apiKeyCtx := types.WithTenantAPIKeyScope(ctx, types.TenantAPIKeyScope{KeyID: 1, FullAccess: true})
		_, err := svc.EnsureWorkKnowledgeBase(apiKeyCtx, 1, "user-a")
		appErr, ok := apperrors.IsAppError(err)
		require.True(t, ok)
		require.Equal(t, apperrors.ErrForbidden, appErr.Code)
		require.Equal(t, int64(0), countWorkKBs(t, db, 1, "user-a"))
	})

	for _, tt := range []struct {
		name    string
		members stubMembership
	}{
		{name: "invited member", members: stubMembership{status: types.TenantMemberStatusInvited}},
		{name: "suspended member", members: stubMembership{status: types.TenantMemberStatusSuspended}},
		{name: "unknown member", members: stubMembership{}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			svc := &knowledgeBaseService{repo: repo, members: tt.members}
			_, err := svc.EnsureWorkKnowledgeBase(ctx, 1, "user-a")
			appErr, ok := apperrors.IsAppError(err)
			require.True(t, ok)
			require.Equal(t, apperrors.ErrForbidden, appErr.Code)
			require.Equal(t, int64(0), countWorkKBs(t, db, 1, "user-a"))
		})
	}

	t.Run("active member", func(t *testing.T) {
		svc := &knowledgeBaseService{
			repo:    repo,
			members: stubMembership{status: types.TenantMemberStatusActive},
		}
		kb, err := svc.EnsureWorkKnowledgeBase(ctx, 1, "user-a")
		require.NoError(t, err)
		require.Equal(t, "user-a", kb.CreatorID)
	})
}

// TestWorkKnowledgeBaseNotVisibleOutsideOwner 用真实仓储 + CanSeeKB/ResolveKB
// 组合验证：同租户他人、Admin、API Key 拿到的都是 NotFound，绝不 403。
// 取舍：不装配 dig 全套依赖，直接用 knowledgeBaseService{repo} 驱动懒创建，
// 可见性判定复用生产路径 access.ResolveKB，避免测试复制 ACL 逻辑。
func TestWorkKnowledgeBaseNotVisibleOutsideOwner(t *testing.T) {
	db := openWorkKBTestDB(t)
	repo := repository.NewKnowledgeBaseRepository(db)
	ctx := t.Context()

	svc := &knowledgeBaseService{repo: repo}
	_, err := svc.EnsureWorkKnowledgeBase(ctx, 1, "user-a")
	require.NoError(t, err)

	stored, err := repo.GetWorkKnowledgeBase(ctx, 1, "user-a")
	require.NoError(t, err)
	require.NotNil(t, stored)

	resolve := func(caller types.Caller, extra func(context.Context) context.Context) (*access.KBAccess, error) {
		callerCtx := types.WithCaller(context.Background(), caller)
		if extra != nil {
			callerCtx = extra(callerCtx)
		}
		return access.ResolveKB(callerCtx, access.KBRequest{Caller: caller}, stored, types.OrgRoleViewer, nil, nil)
	}

	owner := types.Caller{TenantID: 1, UserID: "user-a", Role: types.TenantRoleContributor}
	got, err := resolve(owner, nil)
	require.NoError(t, err)
	require.Equal(t, stored.ID, got.KnowledgeBase.ID)

	// 同样的判定也必须经过 KBPermissions 这条生产主路径：
	// 列表/检索/附件校验都走它，不能只在 ResolveKB 上正确。
	visibility := access.NewKBPermissions(
		types.WithCaller(context.Background(), owner), nil, repo)
	allowed, err := visibility.Check(stored.ID, stored.TenantID, types.OrgRoleViewer)
	require.NoError(t, err)
	require.True(t, allowed, "创建者必须能通过 KBPermissions 读到自己的工作 KB")

	for _, tt := range []struct {
		name   string
		caller types.Caller
		extra  func(context.Context) context.Context
	}{
		{
			name:   "other member of same tenant",
			caller: types.Caller{TenantID: 1, UserID: "user-b", Role: types.TenantRoleContributor},
		},
		{
			name:   "tenant admin",
			caller: types.Caller{TenantID: 1, UserID: "admin", Role: types.TenantRoleAdmin},
		},
		{
			name:   "tenant api key",
			caller: types.Caller{TenantID: 1, UserID: "system-1", Role: types.TenantRoleAdmin},
			extra: func(c context.Context) context.Context {
				return types.WithTenantAPIKeyScope(c, types.TenantAPIKeyScope{KeyID: 1, FullAccess: true})
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolve(tt.caller, tt.extra)
			require.ErrorIs(t, err, access.ErrNotFound, "must hide existence with 404 semantics")
			require.NotErrorIs(t, err, access.ErrForbidden)
			require.Nil(t, got)

			// 主路径同样要拒绝：否则列表/检索会绕过 ResolveKB 泄漏。
			callerCtx := types.WithCaller(context.Background(), tt.caller)
			if tt.extra != nil {
				callerCtx = tt.extra(callerCtx)
			}
			allowed, permErr := access.NewKBPermissions(callerCtx, nil, repo).
				Check(stored.ID, stored.TenantID, types.OrgRoleViewer)
			require.NoError(t, permErr)
			require.False(t, allowed, "KBPermissions 也必须隐藏私有 KB")
		})
	}
}
