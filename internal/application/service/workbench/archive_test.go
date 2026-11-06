package workbench

import (
	"context"
	"errors"
	"sort"
	"testing"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// memArchiveRepo 是归档链路的内存仓储替身，复刻部分唯一索引的语义：
// (source_type, source_id, part_no) 上同时最多一条 status<>'deleted' 的行。
type memArchiveRepo struct {
	interfaces.WorkbenchRepository

	item    *types.WorkbenchItem
	trashed *types.WorkbenchItem
	rows    map[string]*types.WorkbenchArchive // 按 id 存
	items   map[string]*types.WorkbenchItem    // 额外条目，供列表批量填充用例
	order   int
	// notices 记录归档失败通知，用于断言 archive_failed 只写一次。
	notices []*types.WorkbenchNotice
}

// InsertNotice 复刻 dedupe_key 唯一索引：同 (user, dedupe) 只留一条。
func (m *memArchiveRepo) InsertNotice(
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

func newMemArchiveRepo() *memArchiveRepo {
	return &memArchiveRepo{
		item: &types.WorkbenchItem{
			ID:                  "item-1",
			TenantID:            1,
			OwnerID:             "user-a",
			Kind:                types.WorkbenchKindCanvas,
			Title:               "项目看板",
			SnapshotVersion:     5,
			EditorSchemaVersion: 1,
			Visibility:          types.WorkbenchVisibilityPrivate,
		},
		rows:  map[string]*types.WorkbenchArchive{},
		items: map[string]*types.WorkbenchItem{},
	}
}

func (m *memArchiveRepo) GetItemMeta(
	context.Context, uint64, string, string,
) (*types.WorkbenchItem, error) {
	if m.item == nil {
		return nil, gorm.ErrRecordNotFound
	}
	return m.item, nil
}

func (m *memArchiveRepo) GetTrashedItem(
	context.Context, uint64, string, string,
) (*types.WorkbenchItem, error) {
	if m.trashed == nil {
		return nil, gorm.ErrRecordNotFound
	}
	return m.trashed, nil
}

func (m *memArchiveRepo) GetLiveByPart(
	_ context.Context, tenantID uint64, ownerID, sourceType, sourceID string, partNo int,
) (*types.WorkbenchArchive, error) {
	for _, row := range m.rows {
		if row.TenantID == tenantID && row.OwnerID == ownerID &&
			row.SourceType == sourceType && row.SourceID == sourceID &&
			row.PartNo == partNo && row.Status != types.WorkbenchArchiveDeleted {
			return row, nil
		}
	}
	return nil, nil
}

func (m *memArchiveRepo) CreatePending(
	_ context.Context, row *types.WorkbenchArchive,
) (*types.WorkbenchArchive, error) {
	// 复刻唯一索引：已有 live 行 → 冲突 → ErrArchiveInProgress。
	existing, err := m.GetLiveByPart(
		context.Background(), row.TenantID, row.OwnerID, row.SourceType, row.SourceID, row.PartNo)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return existing, types.ErrArchiveInProgress
	}
	row.Status = types.WorkbenchArchivePending
	m.rows[row.ID] = row
	m.order++
	return row, nil
}

func (m *memArchiveRepo) ActivatePart(
	_ context.Context, id, knowledgeID string, sourceVersion int, title string,
) error {
	row, ok := m.rows[id]
	if !ok {
		return gorm.ErrRecordNotFound
	}
	row.KnowledgeID = knowledgeID
	row.SourceVersion = sourceVersion
	row.Title = title
	row.Status = types.WorkbenchArchiveActive
	return nil
}

func (m *memArchiveRepo) RebindKnowledge(
	ctx context.Context, id, knowledgeID string, sourceVersion int, title string,
) error {
	return m.ActivatePart(ctx, id, knowledgeID, sourceVersion, title)
}

func (m *memArchiveRepo) ListLiveBySource(
	_ context.Context, tenantID uint64, ownerID, sourceType, sourceID string,
) ([]*types.WorkbenchArchive, error) {
	out := make([]*types.WorkbenchArchive, 0, len(m.rows))
	for _, row := range m.rows {
		if row.TenantID == tenantID && row.OwnerID == ownerID &&
			row.SourceType == sourceType && row.SourceID == sourceID &&
			row.Status != types.WorkbenchArchiveDeleted {
			out = append(out, row)
		}
	}
	// 真实仓储按 part_no ASC 返回；这里同样排序，测试才有确定顺序。
	sort.Slice(out, func(i, j int) bool { return out[i].PartNo < out[j].PartNo })
	return out, nil
}

func (m *memArchiveRepo) ListLiveBySources(
	_ context.Context, tenantID uint64, ownerID, sourceType string, ids []string,
) (map[string][]*types.WorkbenchArchive, error) {
	want := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		want[id] = struct{}{}
	}
	out := make(map[string][]*types.WorkbenchArchive, len(ids))
	for _, row := range m.rows {
		if row.TenantID != tenantID || row.OwnerID != ownerID || row.SourceType != sourceType {
			continue
		}
		if row.Status == types.WorkbenchArchiveDeleted {
			continue
		}
		if _, ok := want[row.SourceID]; !ok {
			continue
		}
		out[row.SourceID] = append(out[row.SourceID], row)
	}
	return out, nil
}

func (m *memArchiveRepo) MarkDeletedBySource(
	_ context.Context, tenantID uint64, ownerID, sourceType, sourceID string,
) (int64, error) {
	var count int64
	for _, row := range m.rows {
		if row.TenantID == tenantID && row.OwnerID == ownerID &&
			row.SourceType == sourceType && row.SourceID == sourceID &&
			row.Status != types.WorkbenchArchiveDeleted {
			row.Status = types.WorkbenchArchiveDeleted
			count++
		}
	}
	return count, nil
}

func (m *memArchiveRepo) MarkDeletedBySourcePart(
	_ context.Context, tenantID uint64, ownerID, sourceType, sourceID string, partNo int,
) (int64, error) {
	var count int64
	for _, row := range m.rows {
		if row.TenantID == tenantID && row.OwnerID == ownerID &&
			row.SourceType == sourceType && row.SourceID == sourceID &&
			row.PartNo == partNo && row.Status != types.WorkbenchArchiveDeleted {
			row.Status = types.WorkbenchArchiveDeleted
			count++
		}
	}
	return count, nil
}

func (m *memArchiveRepo) MarkDeletedByKB(
	_ context.Context, tenantID uint64, ownerID, kbID string,
) (int64, error) {
	var count int64
	for _, row := range m.rows {
		if row.TenantID == tenantID && row.OwnerID == ownerID && row.KBID == kbID &&
			row.Status != types.WorkbenchArchiveDeleted {
			row.Status = types.WorkbenchArchiveDeleted
			count++
		}
	}
	return count, nil
}

func (m *memArchiveRepo) CountAnyBySource(
	_ context.Context, tenantID uint64, ownerID, sourceType, sourceID string,
) (int64, error) {
	var count int64
	for _, row := range m.rows {
		if row.TenantID == tenantID && row.OwnerID == ownerID &&
			row.SourceType == sourceType && row.SourceID == sourceID {
			count++
		}
	}
	return count, nil
}

// stubArchiveKB 只提供工作知识库；其余方法保持 nil（越界调用会 panic）。
type stubArchiveKB struct{}

func (stubArchiveKB) EnsureWorkKnowledgeBase(
	context.Context, uint64, string,
) (*types.KnowledgeBase, error) {
	return &types.KnowledgeBase{
		ID: "kb-work", TenantID: 1, CreatorID: "user-a",
		IsSystem: true, SystemKey: types.KBSystemKeyWork, Visibility: types.KBVisibilityPrivate,
	}, nil
}

// stubArchiveKnowledge 用内存 knowledge 表模拟创建/更新/删除，便于断言幂等。
type stubArchiveKnowledge struct {
	docs map[string]*types.Knowledge
	// failDelete 里的知识 id 删除时报错，用于验证“部分失败保持映射”。
	failDelete map[string]bool
	// failCreate 为真时创建知识失败。
	failCreate bool
	created    int
	updated    int
}

func newStubArchiveKnowledge() *stubArchiveKnowledge {
	return &stubArchiveKnowledge{
		docs:       map[string]*types.Knowledge{},
		failDelete: map[string]bool{},
	}
}

func (s *stubArchiveKnowledge) CreateKnowledgeFromManualWithID(
	_ context.Context, kbID, knowledgeID string, payload *types.ManualKnowledgePayload, _ string,
) (*types.Knowledge, error) {
	if s.failCreate {
		return nil, errors.New("create failed")
	}
	s.created++
	doc := &types.Knowledge{
		ID: knowledgeID, TenantID: 1, KnowledgeBaseID: kbID, Type: types.KnowledgeTypeManual,
		Title: payload.Title, ParseStatus: types.ParseStatusCompleted,
	}
	s.docs[knowledgeID] = doc
	return doc, nil
}

func (s *stubArchiveKnowledge) UpdateManualKnowledge(
	_ context.Context, knowledgeID string, payload *types.ManualKnowledgePayload,
) (*types.Knowledge, error) {
	doc, ok := s.docs[knowledgeID]
	if !ok {
		return nil, errors.New("knowledge not found")
	}
	s.updated++
	doc.Title = payload.Title
	doc.ParseStatus = types.ParseStatusPending
	return doc, nil
}

func (s *stubArchiveKnowledge) DeleteKnowledge(_ context.Context, id string) error {
	if s.failDelete[id] {
		return errors.New("delete failed")
	}
	delete(s.docs, id)
	return nil
}

func (s *stubArchiveKnowledge) GetKnowledgeBatch(
	_ context.Context, tenantID uint64, ids []string,
) ([]*types.Knowledge, error) {
	out := make([]*types.Knowledge, 0, len(ids))
	for _, id := range ids {
		if doc, ok := s.docs[id]; ok && doc.TenantID == tenantID {
			out = append(out, doc)
		}
	}
	return out, nil
}

func newArchiveService() (*Service, *memArchiveRepo, *stubArchiveKnowledge) {
	repo := newMemArchiveRepo()
	knowledge := newStubArchiveKnowledge()
	svc := NewService(repo).WithArchiveServices(stubArchiveKB{}, knowledge)
	return svc, repo, knowledge
}

func archiveRequest(markdown string) *types.WorkbenchArchiveRequest {
	return &types.WorkbenchArchiveRequest{
		SourceType:  types.WorkbenchSourceItem,
		SourceID:    "item-1",
		BaseVersion: 5,
		Markdown:    markdown,
	}
}

// 同一 source 同一版本点 10 次归档，只应产生一组 part，不重复建知识。
func TestArchiveIsIdempotent(t *testing.T) {
	svc, repo, knowledge := newArchiveService()
	ctx := newTestContext()
	req := archiveRequest("# 看板\n\n## 本周\n\n内容 A\n\n## 下周\n\n内容 B")

	for i := 0; i < 10; i++ {
		state, err := svc.Archive(ctx, req)
		require.NoError(t, err, "第 %d 次归档应成功", i+1)
		assert.Equal(t, archiveStatusActive, state.Status)
	}

	assert.Len(t, repo.rows, 2, "两个 H2 只应产生两条映射")
	assert.Equal(t, 2, knowledge.created, "重复归档不得重复创建知识")
	assert.Equal(t, 0, knowledge.updated, "版本与标题都没变时不应更新知识")
}

// 内容版本前进后归档：应更新既有知识而不是新建，archived_version 追上最新。
func TestArchiveUpdatesAfterVersionBump(t *testing.T) {
	svc, repo, knowledge := newArchiveService()
	ctx := newTestContext()
	req := archiveRequest("## 本周\n\n旧内容")

	_, err := svc.Archive(ctx, req)
	require.NoError(t, err)

	repo.item.SnapshotVersion = 6
	req.BaseVersion = 6
	req.Markdown = "## 本周\n\n新内容"
	state, err := svc.Archive(ctx, req)
	require.NoError(t, err)

	assert.Equal(t, 1, knowledge.created, "同一 part 不应新建知识")
	assert.Equal(t, 1, knowledge.updated, "内容变化应更新既有知识")
	require.NotNil(t, state.ArchivedVersion)
	assert.Equal(t, 6, *state.ArchivedVersion)
}

// 并发归档同一 part：后到者拿到 2404，而不是写坏同一份知识。
func TestArchiveConcurrentPartConflict(t *testing.T) {
	svc, repo, _ := newArchiveService()
	ctx := newTestContext()
	req := archiveRequest("## 本周\n\n内容")

	// 预置一条 pending 行，模拟另一个标签页正在建知识。
	repo.rows["row-busy"] = &types.WorkbenchArchive{
		ID: "row-busy", TenantID: 1, OwnerID: "user-a",
		KBID: "kb-work", KnowledgeID: "k-busy",
		SourceType: types.WorkbenchSourceItem, SourceID: "item-1",
		SourceVersion: 5, PartNo: 1, PartCount: 1, Title: "项目看板",
		Status: types.WorkbenchArchivePending,
	}

	_, err := svc.Archive(ctx, req)
	appErr, ok := apperrors.IsAppError(err)
	require.True(t, ok, "并发归档必须返回 AppError，got %v", err)
	assert.Equal(t, int(apperrors.ErrWorkbenchArchiveBusy), int(appErr.Code))
	assert.Equal(t, 409, appErr.HTTPCode)
}

// base_version 落后于源内容 → 2405，且不写任何映射。
func TestArchiveRejectsStaleBaseVersion(t *testing.T) {
	svc, repo, _ := newArchiveService()
	req := archiveRequest("## 本周\n\n内容")
	req.BaseVersion = 4

	_, err := svc.Archive(newTestContext(), req)
	appErr, ok := apperrors.IsAppError(err)
	require.True(t, ok)
	assert.Equal(t, int(apperrors.ErrWorkbenchArchiveStale), int(appErr.Code))
	assert.Empty(t, repo.rows)
}

// 源版本前进但未刷新归档 → state 为 stale。
func TestArchiveStateStaleAfterEdit(t *testing.T) {
	svc, repo, _ := newArchiveService()
	ctx := newTestContext()
	_, err := svc.Archive(ctx, archiveRequest("## 本周\n\n内容"))
	require.NoError(t, err)

	repo.item.SnapshotVersion = 9
	state, err := svc.GetArchiveState(ctx, types.WorkbenchSourceItem, "item-1")
	require.NoError(t, err)
	assert.Equal(t, archiveStatusStale, state.Status)
	assert.Equal(t, "source_newer", state.StaleReason)
	assert.Equal(t, 9, *state.CurrentVersion)
	assert.Equal(t, 5, *state.ArchivedVersion)
}

// 单条归档知识被用户删除 → state 为 partial，且不自动重建。
func TestArchiveStatePartialWhenKnowledgeDeleted(t *testing.T) {
	svc, repo, knowledge := newArchiveService()
	ctx := newTestContext()
	_, err := svc.Archive(ctx, archiveRequest("## 本周\n\n内容"))
	require.NoError(t, err)

	for id := range knowledge.docs {
		delete(knowledge.docs, id)
	}
	state, err := svc.GetArchiveState(ctx, types.WorkbenchSourceItem, "item-1")
	require.NoError(t, err)
	assert.Equal(t, archiveStatusPartial, state.Status)
	require.Len(t, state.Parts, 1)
	assert.False(t, state.Parts[0].KnowledgeExists, "缺失的知识必须标记 knowledge_exists=false")
	assert.Len(t, repo.rows, 1, "state 查询不得重建或删除映射")
}

// 删除归档：没有 live 映射时返回 status=none 的成功结果，连点两次结果一致。
func TestDeleteArchiveIsIdempotent(t *testing.T) {
	svc, _, knowledge := newArchiveService()
	ctx := newTestContext()

	first, err := svc.DeleteArchive(ctx, &types.WorkbenchArchiveDeleteRequest{
		SourceType: types.WorkbenchSourceItem, SourceID: "item-1",
	})
	require.NoError(t, err)
	assert.Equal(t, archiveStatusNone, first.Status, "从未归档时是 none")

	_, err = svc.Archive(ctx, archiveRequest("## 本周\n\n内容"))
	require.NoError(t, err)

	// 第一次删除返回 deleted；第二次已经没有任何 live 映射，按 §7.6 返回 none。
	deleted, err := svc.DeleteArchive(ctx, &types.WorkbenchArchiveDeleteRequest{
		SourceType: types.WorkbenchSourceItem, SourceID: "item-1",
	})
	require.NoError(t, err)
	assert.Equal(t, archiveStatusDeleted, deleted.Status)

	second, err := svc.DeleteArchive(ctx, &types.WorkbenchArchiveDeleteRequest{
		SourceType: types.WorkbenchSourceItem, SourceID: "item-1",
	})
	require.NoError(t, err, "重复删除不得报 404")
	assert.Equal(t, archiveStatusNone, second.Status, "没有 live 映射时返回 none")

	assert.Equal(t, 1, len(knowledge.docs), "delete_knowledge 默认 false 时知识应保留")

	// 删除后查 state：仍表达 deleted，而不是退回 none。
	state, err := svc.GetArchiveState(ctx, types.WorkbenchSourceItem, "item-1")
	require.NoError(t, err)
	assert.Equal(t, archiveStatusDeleted, state.Status)
}

// delete_knowledge=true 时先删知识；部分失败返回 failed_part_nos 且映射保持 active。
func TestDeleteArchivePartialFailureKeepsMapping(t *testing.T) {
	svc, repo, knowledge := newArchiveService()
	ctx := newTestContext()
	req := archiveRequest("## 第一节\n\nA\n\n## 第二节\n\nB")
	_, err := svc.Archive(ctx, req)
	require.NoError(t, err)
	require.Len(t, knowledge.docs, 2)

	// 让其中一个 part 的知识删除失败。
	failedID := ""
	for id := range knowledge.docs {
		failedID = id
		break
	}
	knowledge.failDelete[failedID] = true

	_, err = svc.DeleteArchive(ctx, &types.WorkbenchArchiveDeleteRequest{
		SourceType: types.WorkbenchSourceItem, SourceID: "item-1", DeleteKnowledge: true,
	})
	appErr, ok := apperrors.IsAppError(err)
	require.True(t, ok)
	assert.Equal(t, int(apperrors.ErrInternalServer), int(appErr.Code))
	details, _ := appErr.Details.(map[string]any)
	require.NotNil(t, details)
	assert.NotEmpty(t, details["failed_part_nos"])

	// 映射必须保持原状，用户可重试。
	live, err := repo.ListLiveBySource(ctx, 1, "user-a", types.WorkbenchSourceItem, "item-1")
	require.NoError(t, err)
	assert.Len(t, live, 2, "部分失败时映射不得被标 deleted")
}

// 清空工作知识库后，映射全部标 deleted；那之后再查 state 是 deleted 而不是 active。
func TestMarkAllDeletedForKB(t *testing.T) {
	svc, repo, _ := newArchiveService()
	ctx := newTestContext()
	_, err := svc.Archive(ctx, archiveRequest("## 本周\n\n内容"))
	require.NoError(t, err)

	count, err := svc.MarkAllDeletedForKB(ctx, "kb-work")
	require.NoError(t, err)
	assert.Equal(t, int64(1), count)

	for _, row := range repo.rows {
		assert.Equal(t, types.WorkbenchArchiveDeleted, row.Status)
	}
}

// 删除源条目并把归档标 deleted 后，state 返回 source_deleted（快照与知识都还在）。
func TestGetArchiveStateSourceDeleted(t *testing.T) {
	svc, repo, knowledge := newArchiveService()
	ctx := newTestContext()
	_, err := svc.Archive(ctx, archiveRequest("## 本周\n\n内容"))
	require.NoError(t, err)

	trashed := *repo.item
	repo.trashed = &trashed
	repo.item = nil // 条目已进回收站
	state, err := svc.GetArchiveState(ctx, types.WorkbenchSourceItem, "item-1")
	require.NoError(t, err)
	assert.Equal(t, archiveStatusSourceDeleted, state.Status)
	assert.Len(t, state.Parts, 1, "快照必须保留以便展示")
	assert.Len(t, knowledge.docs, 1, "知识不得被删除")
}

// 列表批量填充：一次 ListLiveBySources 覆盖 none / stale / partial 三种形态。
func TestListItemsFillsArchiveStatus(t *testing.T) {
	svc, repo, knowledge := newArchiveService()
	ctx := newTestContext()

	// 三条条目：a 无归档、b 归档落后一版、c 归档知识已被删。
	repo.item = &types.WorkbenchItem{
		ID: "item-a", TenantID: 1, OwnerID: "user-a", Kind: types.WorkbenchKindCanvas,
		Title: "A", SnapshotVersion: 3, EditorSchemaVersion: 1,
	}
	repo.items["item-b"] = &types.WorkbenchItem{
		ID: "item-b", TenantID: 1, OwnerID: "user-a", Kind: types.WorkbenchKindCanvas,
		Title: "B", SnapshotVersion: 9, EditorSchemaVersion: 1,
	}
	repo.items["item-c"] = &types.WorkbenchItem{
		ID: "item-c", TenantID: 1, OwnerID: "user-a", Kind: types.WorkbenchKindCanvas,
		Title: "C", SnapshotVersion: 4, EditorSchemaVersion: 1,
	}

	// b：知识仍存在但 live 映射停在 v5（落后于 9）→ stale；
	// c：live 映射存在但知识已被删 → partial。
	knowledge.docs["k-b"] = &types.Knowledge{
		ID: "k-b", TenantID: 1, ParseStatus: types.ParseStatusCompleted,
	}
	repo.rows["row-b"] = &types.WorkbenchArchive{
		ID: "row-b", TenantID: 1, OwnerID: "user-a", KBID: "kb-work", KnowledgeID: "k-b",
		SourceType: types.WorkbenchSourceItem, SourceID: "item-b",
		SourceVersion: 5, PartNo: 1, PartCount: 1, Title: "B",
		Status: types.WorkbenchArchiveActive,
	}
	repo.rows["row-c"] = &types.WorkbenchArchive{
		ID: "row-c", TenantID: 1, OwnerID: "user-a", KBID: "kb-work", KnowledgeID: "k-c",
		SourceType: types.WorkbenchSourceItem, SourceID: "item-c",
		SourceVersion: 4, PartNo: 1, PartCount: 1, Title: "C",
		Status: types.WorkbenchArchiveActive,
	}
	// k-c 故意不放进 knowledge.docs：模拟“用户删掉了这条归档知识”。

	items := []*types.WorkbenchItem{repo.item, repo.items["item-b"], repo.items["item-c"]}
	require.NoError(t, svc.fillArchiveStatus(ctx, 1, "user-a", items))

	assert.Equal(t, archiveStatusNone, items[0].ArchiveStatus)
	assert.Equal(t, 0, items[0].ArchivedVersion)

	assert.Equal(t, archiveStatusStale, items[1].ArchiveStatus)
	assert.Equal(t, 5, items[1].ArchivedVersion)

	assert.Equal(t, archiveStatusPartial, items[2].ArchiveStatus)
	assert.Equal(t, 4, items[2].ArchivedVersion)
}

// 空列表不发查询，直接返回 nil。
func TestFillArchiveStatusSkipsEmptyList(t *testing.T) {
	svc, _, _ := newArchiveService()
	require.NoError(t, svc.fillArchiveStatus(newTestContext(), 1, "user-a", nil))
}

// 章节文档列表的归档填充（P2-5）：source_type='doc'，source_id 是任务 id，
// 当前版本取 doc_documents.version；三种形态与条目列表同口径。
func TestFillDocArchiveStatus(t *testing.T) {
	svc, repo, knowledge := newArchiveService()
	ctx := newTestContext()

	docs := []*types.DocDocumentSummary{
		{ID: "doc-a", TaskID: "task-a", Title: "A", Version: 2},
		{ID: "doc-b", TaskID: "task-b", Title: "B", Version: 4},
		{ID: "doc-c", TaskID: "task-c", Title: "C", Version: 3},
	}

	// b：归档停在 v2，落后当前 v4 → stale。
	knowledge.docs["k-b"] = &types.Knowledge{
		ID: "k-b", TenantID: 1, ParseStatus: types.ParseStatusCompleted,
	}
	repo.rows["row-b"] = &types.WorkbenchArchive{
		ID: "row-b", TenantID: 1, OwnerID: "user-a", KBID: "kb-work", KnowledgeID: "k-b",
		SourceType: types.WorkbenchSourceDoc, SourceID: "task-b",
		SourceVersion: 2, PartNo: 1, PartCount: 1, Title: "B",
		Status: types.WorkbenchArchiveActive,
	}
	// c：映射存在但知识已被删 → partial。
	repo.rows["row-c"] = &types.WorkbenchArchive{
		ID: "row-c", TenantID: 1, OwnerID: "user-a", KBID: "kb-work", KnowledgeID: "k-c",
		SourceType: types.WorkbenchSourceDoc, SourceID: "task-c",
		SourceVersion: 3, PartNo: 1, PartCount: 1, Title: "C",
		Status: types.WorkbenchArchiveActive,
	}

	require.NoError(t, svc.FillDocArchiveStatus(ctx, docs))

	assert.Equal(t, archiveStatusNone, docs[0].ArchiveStatus)
	assert.Equal(t, 0, docs[0].ArchivedVersion)
	assert.Equal(t, archiveStatusStale, docs[1].ArchiveStatus)
	assert.Equal(t, 2, docs[1].ArchivedVersion)
	assert.Equal(t, archiveStatusPartial, docs[2].ArchiveStatus)
	assert.Equal(t, 3, docs[2].ArchivedVersion)
}

// 多 part 归档标题必须带 (n/m) 后缀，且 part_no 从 1 连续编号。
func TestArchiveMultiPartTitles(t *testing.T) {
	svc, repo, _ := newArchiveService()
	state, err := svc.Archive(newTestContext(), archiveRequest("## 甲\n\nA\n\n## 乙\n\nB\n\n## 丙\n\nC"))
	require.NoError(t, err)
	require.Len(t, state.Parts, 3)

	titles := make([]string, 0, 3)
	for i, part := range state.Parts {
		titles = append(titles, part.Title)
		assert.Equal(t, 3, part.PartCount)
		assert.Equal(t, i+1, part.PartNo, "part_no 必须从 1 连续编号")
	}
	assert.Equal(t, []string{"项目看板（1/3）", "项目看板（2/3）", "项目看板（3/3）"}, titles)
	assert.Len(t, repo.rows, 3, "三个 H2 应产生三条映射")
}
