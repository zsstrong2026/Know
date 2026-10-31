package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type fakeKBShareService struct {
	allowedKBs map[string]bool
}

func (f *fakeKBShareService) ShareKnowledgeBase(context.Context, string, string, string, uint64, types.OrgMemberRole) (*types.KnowledgeBaseShare, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeKBShareService) UpdateSharePermission(context.Context, string, types.OrgMemberRole, string, uint64) error {
	return errors.New("not implemented")
}

func (f *fakeKBShareService) RemoveShare(context.Context, string, string, uint64) error {
	return errors.New("not implemented")
}

func (f *fakeKBShareService) ListSharesByKnowledgeBase(context.Context, string, uint64) ([]*types.KnowledgeBaseShare, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeKBShareService) ListSharesByOrganization(context.Context, string) ([]*types.KnowledgeBaseShare, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeKBShareService) ListSharedKnowledgeBases(context.Context, uint64, types.TenantRole) ([]*types.SharedKnowledgeBaseInfo, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeKBShareService) ListSharedKnowledgeBasesInOrganization(context.Context, string, uint64, types.TenantRole) ([]*types.OrganizationSharedKnowledgeBaseItem, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeKBShareService) ListSharedKnowledgeBaseIDsByOrganizations(context.Context, []string, uint64) (map[string][]string, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeKBShareService) GetShare(context.Context, string) (*types.KnowledgeBaseShare, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeKBShareService) GetShareByKBAndOrg(context.Context, string, string) (*types.KnowledgeBaseShare, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeKBShareService) CheckTenantKBPermission(
	_ context.Context,
	kbID string,
	_ uint64,
	_ types.TenantRole,
) (types.OrgMemberRole, bool, error) {
	return types.OrgRoleViewer, f.allowedKBs[kbID], nil
}

func (f *fakeKBShareService) HasTenantKBPermission(ctx context.Context, kbID string, callerTenantID uint64, callerTenantRole types.TenantRole, requiredRole types.OrgMemberRole) (bool, error) {
	return f.allowedKBs[kbID], nil
}

func (f *fakeKBShareService) GetKBSourceTenant(context.Context, string) (uint64, error) {
	return 0, errors.New("not implemented")
}

func (f *fakeKBShareService) CountSharesByKnowledgeBaseIDs(context.Context, []string) (map[string]int64, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeKBShareService) CountByOrganizations(context.Context, []string) (map[string]int64, error) {
	return nil, errors.New("not implemented")
}

func setupKnowledgeSharedAccessDB(t *testing.T) *gorm.DB {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.Knowledge{}))
	return db
}

func newKnowledgeSharedAccessService(t *testing.T, kbShare interfaces.KBShareService) (*knowledgeService, *gorm.DB) {
	t.Helper()

	db := setupKnowledgeSharedAccessDB(t)
	repo := repository.NewKnowledgeRepository(db)
	return &knowledgeService{
		repo:           repo,
		kbShareService: kbShare,
		// 可见性判定改为按 id 加载 KB；测试里全部当作 workspace KB，
		// 否则 Check 会因为 lookup 为 nil 而一律拒绝。
		kbService: staticKBService{lookup: staticKBLookup(nil)},
	}, db
}

func newSharedAccessContext() context.Context {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
	ctx = context.WithValue(ctx, types.UserIDContextKey, "user-1")
	return ctx
}

func seedKnowledge(t *testing.T, db *gorm.DB, knowledge *types.Knowledge) {
	t.Helper()
	require.NoError(t, db.Create(knowledge).Error)
}

func TestGetKnowledgeBatchWithSharedAccess_IncludesSharedKnowledgeWithPermission(t *testing.T) {
	service, db := newKnowledgeSharedAccessService(t, &fakeKBShareService{
		allowedKBs: map[string]bool{"kb-shared": true},
	})

	now := time.Now()
	sharedKnowledge := &types.Knowledge{
		ID:              "k-shared",
		TenantID:        2,
		KnowledgeBaseID: "kb-shared",
		Type:            "file",
		Title:           "shared doc",
		FileName:        "shared.txt",
		FileType:        "txt",
		ParseStatus:     types.ParseStatusCompleted,
		EnableStatus:    "enabled",
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	seedKnowledge(t, db, sharedKnowledge)

	got, err := service.GetKnowledgeBatchWithSharedAccess(newSharedAccessContext(), 1, []string{"k-shared"})

	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "k-shared", got[0].ID)
	require.Equal(t, uint64(2), got[0].TenantID)
	require.Equal(t, "kb-shared", got[0].KnowledgeBaseID)
}

func TestGetKnowledgeBatchWithSharedAccess_ExcludesSharedKnowledgeWithoutPermission(t *testing.T) {
	service, db := newKnowledgeSharedAccessService(t, &fakeKBShareService{
		allowedKBs: map[string]bool{},
	})

	now := time.Now()
	sharedKnowledge := &types.Knowledge{
		ID:              "k-shared",
		TenantID:        2,
		KnowledgeBaseID: "kb-shared",
		Type:            "file",
		Title:           "shared doc",
		FileName:        "shared.txt",
		FileType:        "txt",
		ParseStatus:     types.ParseStatusCompleted,
		EnableStatus:    "enabled",
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	seedKnowledge(t, db, sharedKnowledge)

	got, err := service.GetKnowledgeBatchWithSharedAccess(newSharedAccessContext(), 1, []string{"k-shared"})

	require.NoError(t, err)
	require.Empty(t, got)
}

var _ interfaces.KBShareService = (*fakeKBShareService)(nil)

type countingKBShareService struct {
	fakeKBShareService
	calls int
}

func (s *countingKBShareService) CheckTenantKBPermission(
	ctx context.Context,
	id string,
	tenantID uint64,
	role types.TenantRole,
) (types.OrgMemberRole, bool, error) {
	s.calls++
	return s.fakeKBShareService.CheckTenantKBPermission(ctx, id, tenantID, role)
}

func TestGetKnowledgeBatchWithSharedAccessChecksEachKBOnceAndRechecksNextRequest(t *testing.T) {
	shares := &countingKBShareService{
		fakeKBShareService: fakeKBShareService{allowedKBs: map[string]bool{"kb-shared": true}},
	}
	svc, db := newKnowledgeSharedAccessService(t, shares)
	for _, id := range []string{"first", "second"} {
		seedKnowledge(t, db, &types.Knowledge{ID: id, TenantID: 2, KnowledgeBaseID: "kb-shared", Type: "file"})
	}
	got, err := svc.GetKnowledgeBatchWithSharedAccess(newSharedAccessContext(), 1, []string{"first", "second"})
	require.NoError(t, err)
	require.Len(t, got, 2)
	require.Equal(t, 1, shares.calls)
	shares.allowedKBs["kb-shared"] = false
	got, err = svc.GetKnowledgeBatchWithSharedAccess(newSharedAccessContext(), 1, []string{"first", "second"})
	require.NoError(t, err)
	require.Empty(t, got)
	require.Equal(t, 2, shares.calls)
}

// staticKBLookup 是 KBVisibilityLookup 的测试替身：按 id 返回预置的 KB，
// 未预置的 id 视为 tenant 2 的 workspace KB（保持既有断言语义）。
// 内嵌 interfaces.KnowledgeBaseService 以便直接赋给 knowledgeService.kbService。
type staticKBLookup map[string]*types.KnowledgeBase

func (l staticKBLookup) GetKnowledgeBaseByID(
	_ context.Context, id string,
) (*types.KnowledgeBase, error) {
	if kb, ok := l[id]; ok {
		return kb, nil
	}
	return &types.KnowledgeBase{ID: id, TenantID: 2, Visibility: types.KBVisibilityWorkspace}, nil
}

// staticKBService 同时满足 KnowledgeBaseService（内嵌空接口）与 KBVisibilityLookup。
type staticKBService struct {
	interfaces.KnowledgeBaseService
	lookup staticKBLookup
}

func (s staticKBService) GetKnowledgeBaseByID(
	ctx context.Context, id string,
) (*types.KnowledgeBase, error) {
	return s.lookup.GetKnowledgeBaseByID(ctx, id)
}

// kbLookupOf 把一批 KB 转成 staticKBLookup，避免测试里手写 map。
func kbLookupOf(kbs []*types.KnowledgeBase) staticKBLookup {
	lookup := make(staticKBLookup, len(kbs))
	for _, kb := range kbs {
		if kb != nil {
			lookup[kb.ID] = kb
		}
	}
	return lookup
}

func TestResolveKBReadTenantPreservesServiceBoundary(t *testing.T) {
	kb := &types.KnowledgeBase{ID: "kb", TenantID: 2}
	shares := &fakeKBShareService{allowedKBs: map[string]bool{"kb": true}}
	// 可见性判定用最小替身：把 "kb" 视为 tenant 2 的 workspace KB。
	kbs := staticKBLookup{"kb": kb}
	tenant, err := resolveKBReadTenant(newSharedAccessContext(), kb, shares, kbs)
	require.NoError(t, err)
	require.Equal(t, uint64(2), tenant)
	_, err = resolveKBReadTenant(
		context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1)),
		kb,
		shares,
		kbs,
	)
	require.Error(t, err, "direct cross-tenant calls still require a user identity")
	_, err = resolveKBReadTenant(newSharedAccessContext(), kb, nil, kbs)
	require.Error(t, err, "a missing share service cannot grant access")
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(2))
	tenant, err = resolveKBReadTenant(ctx, kb, nil, kbs)
	require.NoError(t, err, "a service execution context already scoped to the KB remains usable")
	require.Equal(t, uint64(2), tenant)
}
