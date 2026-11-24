package sop

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
)

// fakeBriefKnowledge 记录知识库写调用（简报同步只经这两个入口）。
type fakeBriefKnowledge struct {
	createCalls     int
	updateCalls     int
	lastKnowledgeID string
	lastTitle       string
	lastContent     string
	failNext        error
}

func (f *fakeBriefKnowledge) CreateKnowledgeFromManual(
	_ context.Context, _ string, p *types.ManualKnowledgePayload, _ string,
) (*types.Knowledge, error) {
	if f.failNext != nil {
		err := f.failNext
		f.failNext = nil
		return nil, err
	}
	f.createCalls++
	f.lastKnowledgeID = fmt.Sprintf("k-%d", f.createCalls)
	f.lastTitle, f.lastContent = p.Title, p.Content
	return &types.Knowledge{ID: f.lastKnowledgeID, Title: p.Title}, nil
}

func (f *fakeBriefKnowledge) UpdateManualKnowledge(
	_ context.Context, knowledgeID string, p *types.ManualKnowledgePayload,
) (*types.Knowledge, error) {
	if f.failNext != nil {
		err := f.failNext
		f.failNext = nil
		return nil, err
	}
	f.updateCalls++
	f.lastKnowledgeID = knowledgeID
	f.lastTitle, f.lastContent = p.Title, p.Content
	return &types.Knowledge{ID: knowledgeID, Title: p.Title}, nil
}

func newBriefTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, db.AutoMigrate(
		&types.SopProject{}, &types.SopItem{}, &types.SopRun{}, &types.SopNodeRun{},
		&types.SopArtifact{}, &types.SopProjectNote{},
	))
	require.NoError(t, db.Exec(
		"CREATE UNIQUE INDEX IF NOT EXISTS ux_sop_node_runs_run_node ON sop_node_runs(run_id, node_id)",
	).Error)
	require.NoError(t, db.Exec(
		"CREATE UNIQUE INDEX IF NOT EXISTS ux_sop_notes_project_seq ON sop_project_notes(project_id, seq) WHERE deleted_at IS NULL",
	).Error)
	return db
}

// newTestBriefService 用真实仓储 + 真实 ItemService（派生执行情况）+ 假知识库。
func newTestBriefService(t *testing.T, db *gorm.DB, fk *fakeBriefKnowledge) *BriefService {
	t.Helper()
	projects := repository.NewSopProjectRepo(db)
	notes := repository.NewSopProjectNoteRepo(db)
	itemSvc := NewItemService(
		projects, repository.NewSopItemRepo(db), repository.NewSopItemExtraRepo(db),
		repository.NewSopRunRepo(db),
	)
	return NewBriefService(BriefDeps{
		Projects: projects, Notes: notes, Items: itemSvc, Knowledge: fk,
	})
}

func seedNotesProject(t *testing.T, db *gorm.DB, tenantID uint64, kbID string) *types.SopProject {
	t.Helper()
	p := &types.SopProject{TenantID: tenantID, Name: "知识库改造", Code: "KNOW", KBID: kbID}
	require.NoError(t, repository.NewSopProjectRepo(db).Create(context.Background(), p))
	return p
}

func seedBriefNote(t *testing.T, db *gorm.DB, tenantID uint64, projectID, body string) *types.SopProjectNote {
	t.Helper()
	n := &types.SopProjectNote{
		TenantID: tenantID, ProjectID: projectID, Kind: types.SopNoteKindDecision, Body: body,
	}
	require.NoError(t, repository.NewSopProjectNoteRepo(db).Create(context.Background(), n))
	return n
}

func mustBriefProject(t *testing.T, db *gorm.DB, tenantID uint64, id string) *types.SopProject {
	t.Helper()
	p, err := repository.NewSopProjectRepo(db).GetByID(context.Background(), tenantID, id)
	require.NoError(t, err)
	return p
}

// seq 未变不重写知识库；新增一条后覆盖同一文档并回填水位。
func TestBriefRenderSkipsWhenSeqUnchanged(t *testing.T) {
	db := newBriefTestDB(t)
	fk := &fakeBriefKnowledge{}
	svc := newTestBriefService(t, db, fk)
	ctx := context.Background()

	p := seedNotesProject(t, db, 1, "kb-1")
	seedBriefNote(t, db, 1, p.ID, "决策一")

	require.NoError(t, svc.EnsureInitial(ctx, 1, p.ID))
	require.Equal(t, 1, fk.createCalls)
	require.Equal(t, "知识库改造 项目简报", fk.lastTitle)
	require.Contains(t, fk.lastContent, "决策一")
	require.Contains(t, fk.lastContent, "自动生成")

	require.NoError(t, svc.EnsureInitial(ctx, 1, p.ID))
	require.Equal(t, 1, fk.createCalls+fk.updateCalls, "seq 未变不得重复写 KB")

	seedBriefNote(t, db, 1, p.ID, "决策二")
	require.NoError(t, svc.EnsureInitial(ctx, 1, p.ID))
	require.Equal(t, 1, fk.updateCalls, "新增后覆盖同一文档")

	got := mustBriefProject(t, db, 1, p.ID)
	require.Equal(t, fk.lastKnowledgeID, got.BriefKnowledgeID, "回填 brief_knowledge_id")
	require.Equal(t, int64(2), got.BriefRenderedSeq)
}

// 未绑知识库：渲染照常可用，同步降级为 no-op（不报错、不写库）。
func TestBriefDegradesWithoutKB(t *testing.T) {
	db := newBriefTestDB(t)
	fk := &fakeBriefKnowledge{}
	svc := newTestBriefService(t, db, fk)
	ctx := context.Background()

	p := seedNotesProject(t, db, 1, "")
	seedBriefNote(t, db, 1, p.ID, "决策一")

	md, err := svc.Render(ctx, 1, p.ID)
	require.NoError(t, err)
	require.Contains(t, md, "决策一")
	require.Contains(t, md, "未绑定")

	require.NoError(t, svc.EnsureInitial(ctx, 1, p.ID))
	require.Zero(t, fk.createCalls+fk.updateCalls)
	require.Empty(t, mustBriefProject(t, db, 1, p.ID).BriefKnowledgeID)
}

// 简报正文按就绪口径组入：事项（含父项子项计数）与黑板都在。
func TestBriefRenderIncludesItemsAndParentCounts(t *testing.T) {
	db := newBriefTestDB(t)
	svc := newTestBriefService(t, db, &fakeBriefKnowledge{})
	ctx := context.Background()

	p := seedNotesProject(t, db, 1, "")
	items := repository.NewSopItemRepo(db)
	parent := &types.SopItem{TenantID: 1, ProjectID: p.ID, No: "KNOW-1", Title: "大需求", IsBig: true}
	require.NoError(t, items.Create(ctx, parent))
	for i, title := range []string{"子项甲", "子项乙"} {
		child := &types.SopItem{
			TenantID: 1, ProjectID: p.ID, No: fmt.Sprintf("KNOW-%d", i+2), Title: title,
			ParentID: parent.ID, SplitSource: types.SopSplitSourceManual,
		}
		require.NoError(t, items.Create(ctx, child))
		if i == 0 {
			require.NoError(t, items.UpdateStatus(ctx, 1, child.ID, types.SopItemStatusTodo))
			require.NoError(t, items.UpdateStatus(ctx, 1, child.ID, types.SopItemStatusInProgress))
			require.NoError(t, items.UpdateStatus(ctx, 1, child.ID, types.SopItemStatusDone))
			require.NoError(t, items.UpdateStatus(ctx, 1, child.ID, types.SopItemStatusClosed))
		}
	}

	md, err := svc.Render(ctx, 1, p.ID)
	require.NoError(t, err)
	require.Contains(t, md, "大需求")
	require.Contains(t, md, "（子项 1/2）")
	require.NotContains(t, md, "子项甲", "子项汇总进父项行，不单独占行")
}

// 知识库写入失败：EnsureInitial 返回错误且水位不前移（下次仍会重试）。
func TestBriefEnsureInitialKeepsWatermarkOnFailure(t *testing.T) {
	db := newBriefTestDB(t)
	fk := &fakeBriefKnowledge{failNext: fmt.Errorf("kb 写入失败")}
	svc := newTestBriefService(t, db, fk)
	ctx := context.Background()

	p := seedNotesProject(t, db, 1, "kb-1")
	seedBriefNote(t, db, 1, p.ID, "决策一")

	require.ErrorContains(t, svc.EnsureInitial(ctx, 1, p.ID), "kb 写入失败")
	require.Zero(t, mustBriefProject(t, db, 1, p.ID).BriefRenderedSeq, "失败不得推进水位")
	require.Empty(t, mustBriefProject(t, db, 1, p.ID).BriefKnowledgeID)

	require.NoError(t, svc.EnsureInitial(ctx, 1, p.ID))
	require.Equal(t, int64(1), mustBriefProject(t, db, 1, p.ID).BriefRenderedSeq)
}

// Trigger 是 2s 合并的 debounce：同项目连续触发只跑一次。
func TestBriefTriggerDebounces(t *testing.T) {
	db := newBriefTestDB(t)
	fk := &fakeBriefKnowledge{}
	svc := NewBriefService(BriefDeps{
		Projects:  repository.NewSopProjectRepo(db),
		Notes:     repository.NewSopProjectNoteRepo(db),
		Items:     itemListerOf(t, db),
		Knowledge: fk,
	})
	svc.debounce = time.Millisecond

	p := seedNotesProject(t, db, 1, "kb-1")
	seedBriefNote(t, db, 1, p.ID, "决策一")

	svc.Trigger(1, p.ID)
	svc.Trigger(1, p.ID)
	svc.Trigger(1, p.ID)
	require.Eventually(t, func() bool {
		return fk.createCalls == 1
	}, 2*time.Second, 10*time.Millisecond)
	time.Sleep(20 * time.Millisecond)
	require.Equal(t, 1, fk.createCalls, "同项目窗口内的多次触发合并成一次")
}

func itemListerOf(t *testing.T, db *gorm.DB) *ItemService {
	t.Helper()
	return NewItemService(
		repository.NewSopProjectRepo(db), repository.NewSopItemRepo(db),
		repository.NewSopItemExtraRepo(db), repository.NewSopRunRepo(db),
	)
}
