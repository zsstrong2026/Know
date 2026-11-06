package workbench

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// memLifecycleRepo 覆盖生命周期与清理所需的最小仓储面。
// 其余方法由内嵌接口的 nil 兜底；越界调用会 panic，正好暴露遗漏。
type memLifecycleRepo struct {
	interfaces.WorkbenchRepository

	items    map[string]int64 // "tenant:owner" → 条目数
	parts    map[string]int64 // "tenant:owner" → live 归档分片数
	orphans  []*types.WorkbenchOrphan
	notices  []*types.WorkbenchNotice
	archives []*types.WorkbenchArchive
	trashed  int64
	// versionsPruned 模拟版本保留删掉的行数。
	versionsPruned int64
	purgedFor      []string // 记录 PurgeOwnerData 的 owner
	deleted        []string // 记录被标记 deleted 的 knowledge id
	activated      []string // 记录被激活的 archive id
	now            time.Time
}

func newMemLifecycleRepo() *memLifecycleRepo {
	return &memLifecycleRepo{
		items:   map[string]int64{},
		parts:   map[string]int64{},
		trashed: 0,
		now:     time.Now().UTC(),
	}
}

func scopeKey(tenantID uint64, ownerID string) string {
	return string(rune(tenantID)) + ":" + ownerID
}

func (m *memLifecycleRepo) CountItems(_ context.Context, tenantID uint64, ownerID string) (int64, error) {
	return m.items[scopeKey(tenantID, ownerID)], nil
}

func (m *memLifecycleRepo) CountArchiveParts(
	_ context.Context, tenantID uint64, ownerID string,
) (int64, error) {
	return m.parts[scopeKey(tenantID, ownerID)], nil
}

func (m *memLifecycleRepo) UpsertOrphan(
	_ context.Context, orphan *types.WorkbenchOrphan,
) error {
	for _, row := range m.orphans {
		if row.TenantID == orphan.TenantID && row.UserID == orphan.UserID {
			row.Reason = orphan.Reason
			row.SuspendedAt = orphan.SuspendedAt
			row.PurgeAfter = orphan.PurgeAfter
			row.UpdatedAt = orphan.UpdatedAt
			return nil
		}
	}
	m.orphans = append(m.orphans, orphan)
	return nil
}

func (m *memLifecycleRepo) GetOrphan(
	_ context.Context, tenantID uint64, userID string,
) (*types.WorkbenchOrphan, error) {
	for _, row := range m.orphans {
		if row.TenantID == tenantID && row.UserID == userID {
			return row, nil
		}
	}
	return nil, nil
}

func (m *memLifecycleRepo) DeleteOrphan(
	_ context.Context, tenantID uint64, userID string,
) (int64, error) {
	kept := make([]*types.WorkbenchOrphan, 0, len(m.orphans))
	var removed int64
	for _, row := range m.orphans {
		if row.TenantID == tenantID && row.UserID == userID {
			removed++
			continue
		}
		kept = append(kept, row)
	}
	m.orphans = kept
	return removed, nil
}

func (m *memLifecycleRepo) ListOrphansDue(
	_ context.Context, at time.Time, limit int,
) ([]*types.WorkbenchOrphan, error) {
	out := make([]*types.WorkbenchOrphan, 0)
	for _, row := range m.orphans {
		if !row.PurgeAfter.After(at) {
			out = append(out, row)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PurgeAfter.Before(out[j].PurgeAfter) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *memLifecycleRepo) ListOrphansExpiring(
	_ context.Context, from, to time.Time, limit int,
) ([]*types.WorkbenchOrphan, error) {
	out := make([]*types.WorkbenchOrphan, 0)
	for _, row := range m.orphans {
		if row.PurgeAfter.After(from) && !row.PurgeAfter.After(to) {
			out = append(out, row)
		}
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *memLifecycleRepo) PurgeOwnerData(
	_ context.Context, tenantID uint64, ownerID string,
) error {
	m.purgedFor = append(m.purgedFor, ownerID)
	return nil
}

func (m *memLifecycleRepo) PurgeTrashedBefore(
	_ context.Context, _ time.Time, _ int,
) (int64, error) {
	return m.trashed, nil
}

func (m *memLifecycleRepo) InsertNotice(
	_ context.Context, notice *types.WorkbenchNotice,
) (bool, error) {
	if notice.DedupeKey != "" {
		for _, existing := range m.notices {
			if existing.UserID == notice.UserID && existing.DedupeKey == notice.DedupeKey {
				return false, nil
			}
		}
	}
	m.notices = append(m.notices, notice)
	return true, nil
}

func (m *memLifecycleRepo) ListLiveArchives(
	_ context.Context, limit int,
) ([]*types.WorkbenchArchive, error) {
	out := make([]*types.WorkbenchArchive, 0, len(m.archives))
	for _, row := range m.archives {
		if row.Status == types.WorkbenchArchiveDeleted {
			continue
		}
		out = append(out, row)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (m *memLifecycleRepo) ActivatePending(_ context.Context, id string) (int64, error) {
	for _, row := range m.archives {
		if row.ID == id && row.Status == types.WorkbenchArchivePending {
			row.Status = types.WorkbenchArchiveActive
			m.activated = append(m.activated, id)
			return 1, nil
		}
	}
	return 0, nil
}

func (m *memLifecycleRepo) MarkDeletedBySourcePart(
	_ context.Context, _ uint64, _, _, _ string, _ int,
) (int64, error) {
	return 1, nil
}

func (m *memLifecycleRepo) PruneExpiredVersions(
	_ context.Context, _ time.Time, _, _ int,
) (int64, error) {
	return m.versionsPruned, nil
}

// ListStaleAssets / SoftDeleteAsset 是清理任务第 6 步（资产回收）用到的两个方法。
// 生命周期测试不关心资产，返回空集合即可，避免内嵌接口的 nil 兜底被调用而 panic。
func (m *memLifecycleRepo) ListStaleAssets(
	_ context.Context, _ time.Time, _ int,
) ([]*types.WorkbenchAsset, error) {
	return nil, nil
}

func (m *memLifecycleRepo) SoftDeleteAsset(
	_ context.Context, _ uint64, _, _ string,
) (int64, error) {
	return 0, nil
}

// stubLifecycleKnowledge 只提供清理对账需要的「知识是否存在」判断。
type stubLifecycleKnowledge struct {
	archiveKnowledgeService
	exists map[string]bool
}

func (s *stubLifecycleKnowledge) GetKnowledgeBatch(
	_ context.Context, _ uint64, ids []string,
) ([]*types.Knowledge, error) {
	out := make([]*types.Knowledge, 0, len(ids))
	for _, id := range ids {
		if s.exists[id] {
			out = append(out, &types.Knowledge{ID: id})
		}
	}
	return out, nil
}

// 有工作台数据时才落保留标记，并写一条 membership_removed 通知。
func TestOnMemberRemovedWritesOrphanAndNotice(t *testing.T) {
	repo := newMemLifecycleRepo()
	repo.items[scopeKey(1, "user-a")] = 3
	svc := NewService(repo)

	err := svc.OnMemberRemoved(newTestContext(), 1, "user-a", types.WorkbenchOrphanReasonRemoved)
	require.NoError(t, err)

	require.Len(t, repo.orphans, 1)
	orphan := repo.orphans[0]
	assert.Equal(t, "user-a", orphan.UserID)
	assert.WithinDuration(t, time.Now().UTC().AddDate(0, 0, 30), orphan.PurgeAfter, time.Minute)

	require.Len(t, repo.notices, 1)
	assert.Equal(t, types.WorkbenchNoticeRemoved, repo.notices[0].Kind)
	assert.Contains(t, string(repo.notices[0].Payload), "\"item_count\":3")
}

// 没有任何工作台数据时不写标记，避免孤儿表被访客污染。
func TestOnMemberRemovedSkipsUserWithoutData(t *testing.T) {
	repo := newMemLifecycleRepo()
	svc := NewService(repo)

	require.NoError(t, svc.OnMemberRemoved(
		newTestContext(), 1, "user-empty", types.WorkbenchOrphanReasonLeft))
	assert.Empty(t, repo.orphans)
	assert.Empty(t, repo.notices)
}

// 重新加入时删除标记并写恢复通知；没有标记时不写通知。
func TestOnMemberAddedClearsOrphanAndNotifies(t *testing.T) {
	repo := newMemLifecycleRepo()
	repo.items[scopeKey(1, "user-a")] = 2
	svc := NewService(repo)
	require.NoError(t, svc.OnMemberRemoved(
		newTestContext(), 1, "user-a", types.WorkbenchOrphanReasonRemoved))

	require.NoError(t, svc.OnMemberAdded(newTestContext(), 1, "user-a"))
	assert.Empty(t, repo.orphans)
	assert.Equal(t, types.WorkbenchNoticeRestored, repo.notices[len(repo.notices)-1].Kind)

	// 第二次调用时已无标记：不应再产生通知。
	before := len(repo.notices)
	require.NoError(t, svc.OnMemberAdded(newTestContext(), 1, "user-a"))
	assert.Equal(t, before, len(repo.notices))
}

// 到期孤儿：销毁数据 + 删标记；7 天预警窗口内的孤儿只提醒一次。
func TestRunCleanupPurgesDueOrphansAndWarnsExpiring(t *testing.T) {
	repo := newMemLifecycleRepo()
	now := time.Now().UTC()
	repo.orphans = []*types.WorkbenchOrphan{
		{
			ID: "due", TenantID: 1, UserID: "user-due",
			PurgeAfter: now.Add(-time.Hour), SuspendedAt: now.AddDate(0, 0, -31),
		},
		{
			ID: "soon", TenantID: 1, UserID: "user-soon",
			PurgeAfter: now.AddDate(0, 0, 3), SuspendedAt: now.AddDate(0, 0, -27),
		},
	}
	repo.trashed = 2
	svc := NewService(repo)

	stats, err := svc.RunCleanup(newTestContext())
	require.NoError(t, err)
	assert.Equal(t, 1, stats.OrphansPurged)
	assert.Equal(t, 2, stats.TrashPurged)
	assert.Equal(t, 1, stats.NoticesCreated)
	assert.Equal(t, []string{"user-due"}, repo.purgedFor)

	// 只留下未到期的那条；预警通知只写一次。
	require.Len(t, repo.orphans, 1)
	assert.Equal(t, "user-soon", repo.orphans[0].UserID)
	require.Len(t, repo.notices, 1)
	assert.Equal(t, types.WorkbenchNoticeCleanup, repo.notices[0].Kind)

	// 再跑一轮：dedupe 命中，不重复提醒。
	stats, err = svc.RunCleanup(newTestContext())
	require.NoError(t, err)
	assert.Equal(t, 0, stats.NoticesCreated)
	assert.Len(t, repo.notices, 1)
}

// 归档对账：知识不存在 → 该 part 收敛；pending 超时且知识存在 → active。
func TestRunCleanupReconcilesArchives(t *testing.T) {
	repo := newMemLifecycleRepo()
	now := time.Now().UTC()
	repo.archives = []*types.WorkbenchArchive{
		{
			ID: "a-missing", TenantID: 1, OwnerID: "user-a", SourceType: types.WorkbenchSourceItem,
			SourceID: "item-1", PartNo: 1, KnowledgeID: "k-gone",
			Status: types.WorkbenchArchiveActive, UpdatedAt: now,
		},
		{
			ID: "a-pending", TenantID: 1, OwnerID: "user-a", SourceType: types.WorkbenchSourceItem,
			SourceID: "item-1", PartNo: 2, KnowledgeID: "k-ok",
			Status: types.WorkbenchArchivePending, UpdatedAt: now.Add(-2 * time.Hour),
		},
	}
	svc := NewService(repo)
	svc.knowledge = &stubLifecycleKnowledge{exists: map[string]bool{"k-ok": true}}

	_, err := svc.RunCleanup(newTestContext())
	require.NoError(t, err)
	assert.Equal(t, []string{"a-pending"}, repo.activated)
}

// 归档分片失败要写 archive_failed 通知；同一版本同一失败集只提醒一次。
func TestArchiveFailureWritesNoticeOnce(t *testing.T) {
	repo := newMemArchiveRepo()
	repo.rows["row-1"] = &types.WorkbenchArchive{
		ID: "row-1", TenantID: 1, OwnerID: "user-a", KBID: "kb-work",
		KnowledgeID: "k-1", SourceType: types.WorkbenchSourceItem, SourceID: "item-1",
		SourceVersion: 5, PartNo: 1, PartCount: 2, Title: "甲", Status: types.WorkbenchArchiveActive,
	}
	kn := newStubArchiveKnowledge()
	kn.docs["k-1"] = &types.Knowledge{ID: "k-1"}
	// 第二个分片建知识失败 → 部分失败。
	kn.failCreate = true

	svc := NewService(repo).WithArchiveServices(stubArchiveKB{}, kn)

	req := &types.WorkbenchArchiveRequest{
		SourceType:  types.WorkbenchSourceItem,
		SourceID:    "item-1",
		BaseVersion: 5,
		Markdown:    "## 甲\n\nA\n\n## 乙\n\nB",
	}
	_, err := svc.Archive(newTestContext(), req)
	require.Error(t, err)

	require.Len(t, repo.notices, 1)
	assert.Equal(t, types.WorkbenchNoticeArchive, repo.notices[0].Kind)

	// 同版本再失败一次：dedupe 命中，不再新增。
	_, err = svc.Archive(newTestContext(), req)
	require.Error(t, err)
	assert.Len(t, repo.notices, 1)
}
