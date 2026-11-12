package workbench

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubDocArchive 是 DocArchiveSource 的内存替身，复刻章节文档档案的最小行为：
// 任务、倒序文档版本、渲染结果与归档状态回写。
type stubDocArchive struct {
	task      *types.DocGenerationTask
	docs      []*types.DocDocument
	markdown  string
	renderErr error
	// marked 记录每次回写，供断言 pending/archived/failed/skipped 的序列。
	marked []string
}

func (s *stubDocArchive) GetTask(
	context.Context, string,
) (*types.DocGenerationTask, error) {
	if s.task == nil {
		return nil, errors.New("task not found")
	}
	return s.task, nil
}

func (s *stubDocArchive) ListDocuments(
	context.Context, string,
) ([]*types.DocDocument, error) {
	return s.docs, nil
}

func (s *stubDocArchive) RenderMarkdown(
	context.Context, *types.DocDocument,
) (string, error) {
	if s.renderErr != nil {
		return "", s.renderErr
	}
	return s.markdown, nil
}

func (s *stubDocArchive) MarkTaskArchiveState(
	_ context.Context, _ string, status, archiveErr string,
) error {
	s.marked = append(s.marked, status)
	if s.task != nil {
		s.task.ArchiveStatus = status
		s.task.ArchiveError = archiveErr
	}
	return nil
}

// newDocArchiveFixture 构造一个可归档的章节文档：任务 + v3 文档 + Markdown。
func newDocArchiveFixture() (*Service, *memArchiveRepo, *stubDocArchive) {
	repo := newMemArchiveRepo()
	docSrc := &stubDocArchive{
		task: &types.DocGenerationTask{
			ID: "task-1", TenantID: 1, OwnerID: "user-a",
			Theme: "季度复盘", AutoArchive: true,
		},
		docs:     []*types.DocDocument{{ID: "doc-3", TaskID: "task-1", Version: 3}},
		markdown: "# 季度复盘\n\n## 一、本周\n\n内容 A\n\n## 二、下周\n\n内容 B",
	}
	svc := NewService(repo).
		WithArchiveServices(stubArchiveKB{}, newStubArchiveKnowledge()).
		WithDocArchive(docSrc)
	return svc, repo, docSrc
}

// 章节文档归档：服务端渲染 Markdown 后按 H2 拆 part，source_id 用任务 id。
func TestArchiveDocWritesParts(t *testing.T) {
	svc, repo, docSrc := newDocArchiveFixture()
	ctx := newTestContext()

	state, err := svc.Archive(ctx, &types.WorkbenchArchiveRequest{
		SourceType: types.WorkbenchSourceDoc,
		SourceID:   "task-1",
	})
	require.NoError(t, err)
	assert.Equal(t, archiveStatusActive, state.Status)
	require.Len(t, state.Parts, 2, "两个 H2 应产生两个分片")
	assert.Equal(t, 3, *state.ArchivedVersion, "source_version 取最新文档版本")

	for _, row := range repo.rows {
		assert.Equal(t, types.WorkbenchSourceDoc, row.SourceType)
		assert.Equal(t, "task-1", row.SourceID)
	}
	assert.Contains(t, docSrc.marked, types.DocArchiveStatusArchived, "归档成功要回写任务状态")
}

// 章节文档状态：归档版本落后最新文档版本时判 stale。
func TestGetArchiveStateForDoc(t *testing.T) {
	svc, repo, docSrc := newDocArchiveFixture()
	ctx := newTestContext()

	_, err := svc.Archive(ctx, &types.WorkbenchArchiveRequest{
		SourceType: types.WorkbenchSourceDoc,
		SourceID:   "task-1",
	})
	require.NoError(t, err)

	// 编辑产生 v4：归档仍停在 v3 → stale。
	docSrc.docs = []*types.DocDocument{{ID: "doc-4", TaskID: "task-1", Version: 4}}
	state, err := svc.GetArchiveState(ctx, types.WorkbenchSourceDoc, "task-1")
	require.NoError(t, err)
	assert.Equal(t, archiveStatusStale, state.Status)
	assert.Equal(t, "source_newer", state.StaleReason)
	assert.Len(t, repo.rows, 2)
}

// 自动归档 worker：已有任何映射（含 deleted）时写 skipped，不重建知识。
func TestProcessDocArchiveSkipsWhenMappingExists(t *testing.T) {
	svc, repo, docSrc := newDocArchiveFixture()
	ctx := newTestContext()

	// 先人工归档一次，再标 deleted：模拟“用户删过归档”。
	_, err := svc.Archive(ctx, &types.WorkbenchArchiveRequest{
		SourceType: types.WorkbenchSourceDoc,
		SourceID:   "task-1",
	})
	require.NoError(t, err)
	for _, row := range repo.rows {
		row.Status = types.WorkbenchArchiveDeleted
	}
	docSrc.task.ArchiveStatus = types.DocArchiveStatusNone
	docSrc.marked = nil

	task, err := DocArchiveTask("task-1", 1, "user-a")
	require.NoError(t, err)
	require.NoError(t, svc.ProcessDocArchive(context.Background(), task))
	assert.Equal(t, []string{types.DocArchiveStatusSkipped}, docSrc.marked,
		"已有映射时必须只写 skipped，不得重建")
}

// 自动归档 worker：关闭自动归档的任务不处理。
func TestProcessDocArchiveSkipsWhenDisabled(t *testing.T) {
	svc, _, docSrc := newDocArchiveFixture()
	docSrc.task.AutoArchive = false

	task, err := DocArchiveTask("task-1", 1, "user-a")
	require.NoError(t, err)
	require.NoError(t, svc.ProcessDocArchive(context.Background(), task))
	assert.Empty(t, docSrc.marked, "未开启自动归档时不得回写任何状态")
}

// 自动归档 worker：成功路径先 pending 再 archived，并写入分片。
func TestProcessDocArchiveSuccess(t *testing.T) {
	svc, repo, docSrc := newDocArchiveFixture()

	task, err := DocArchiveTask("task-1", 1, "user-a")
	require.NoError(t, err)
	require.NoError(t, svc.ProcessDocArchive(context.Background(), task))

	assert.Equal(t, []string{
		types.DocArchiveStatusPending,
		types.DocArchiveStatusArchived,
	}, docSrc.marked)
	assert.Len(t, repo.rows, 2)
}

// 自动归档 worker：失败时写 failed 并落 archive_error，同时写 archive_failed 通知；
// worker 本身不返回错误（生成任务仍算成功）。
func TestProcessDocArchiveFailureRecordsNotice(t *testing.T) {
	svc, repo, docSrc := newDocArchiveFixture()
	docSrc.renderErr = errors.New("render failed")

	task, err := DocArchiveTask("task-1", 1, "user-a")
	require.NoError(t, err)
	require.NoError(t, svc.ProcessDocArchive(context.Background(), task))

	assert.Contains(t, docSrc.marked, types.DocArchiveStatusFailed)
	assert.NotEmpty(t, docSrc.task.ArchiveError)
	assert.Empty(t, repo.rows, "渲染失败不应写入分片")
	require.Len(t, repo.notices, 1, "失败要写一条归档失败通知")
	assert.Equal(t, types.WorkbenchNoticeArchive, repo.notices[0].Kind)
}

// worker payload 非法时直接返回，不做任何写操作。
func TestProcessDocArchiveIgnoresBadPayload(t *testing.T) {
	svc, _, docSrc := newDocArchiveFixture()
	task := asynq.NewTask(types.TypeWorkbenchArchiveDoc, []byte("not-json"))
	require.NoError(t, svc.ProcessDocArchive(context.Background(), task))
	assert.Empty(t, docSrc.marked)
}
