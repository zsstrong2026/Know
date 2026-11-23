package sop

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
)

// stubKnowledge 捕获入库调用。
type stubKnowledge struct {
	payloads []*types.ManualKnowledgePayload
	kbIDs    []string
	channels []string
	err      error
}

func (s *stubKnowledge) CreateKnowledgeFromManual(
	_ context.Context, kbID string, p *types.ManualKnowledgePayload, channel string,
) (*types.Knowledge, error) {
	if s.err != nil {
		return nil, s.err
	}
	s.payloads = append(s.payloads, p)
	s.kbIDs = append(s.kbIDs, kbID)
	s.channels = append(s.channels, channel)
	return &types.Knowledge{ID: fmt.Sprintf("kn-%d", len(s.payloads))}, nil
}

func TestBuildArchiveMarkdownFrontmatter(t *testing.T) {
	art := &types.SopArtifact{Name: "分析报告", Path: "out/report.md", SHA256: "abc123"}
	md, err := BuildArchiveMarkdown(art, "需求分析", "run-1", []byte("# 正文\n内容"))
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(md, "---\n"), "必须以 frontmatter 开头")
	require.Contains(t, md, `title: "分析报告"`)
	require.Contains(t, md, `source_node: "需求分析"`)
	require.Contains(t, md, "run: run-1")
	require.Contains(t, md, "sha256: abc123")
	require.Contains(t, md, "# 正文")

	// 已有 frontmatter 原样保留（不重复加）
	original := "---\ntitle: 原有\n---\n\n正文"
	md2, err := BuildArchiveMarkdown(art, "n", "r", []byte(original))
	require.NoError(t, err)
	require.Equal(t, original, md2)

	// 纯文本包裹为代码块
	logArt := &types.SopArtifact{Name: "日志", Path: "out/test.log", SHA256: "s"}
	md3, err := BuildArchiveMarkdown(logArt, "测试", "r", []byte("line1\nline2"))
	require.NoError(t, err)
	require.Contains(t, md3, "```log\nline1\nline2\n```")

	// 二进制拒绝
	_, err = BuildArchiveMarkdown(&types.SopArtifact{Path: "out/a.zip"}, "n", "r", []byte{0, 1})
	require.Error(t, err)
	require.Contains(t, err.Error(), "暂不支持")
}

// newArchiveTestService 建 sqlite 库 + 项目（KBID=kb-p）+ 运行 + 产物文件。
func newArchiveIngestTestService(t *testing.T) (*ArchiveService, *gorm.DB, *stubKnowledge, *types.SopRun, *types.SopArtifact) {
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
		&types.SopArtifact{},
	))
	require.NoError(t, db.Exec(
		"CREATE UNIQUE INDEX IF NOT EXISTS ux_sop_node_runs_run_node ON sop_node_runs(run_id, node_id)",
	).Error)

	ctx := context.Background()
	proj := &types.SopProject{TenantID: 1, Name: "P", Code: "TP", KBID: "kb-p"}
	require.NoError(t, repository.NewSopProjectRepo(db).Create(ctx, proj))

	ws := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(ws, "out"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(ws, "out", "report.md"), []byte("# 报告"), 0o644))

	run := &types.SopRun{
		TenantID: 1, ProjectID: proj.ID, Status: types.SopRunSucceeded, WorkspaceDir: ws,
		WorkflowSnapshot: types.SopDefinition{Nodes: []types.SopNode{{ID: "n1", Name: "分析"}}},
	}
	require.NoError(t, repository.NewSopRunRepo(db).CreateRun(ctx, run))
	art := &types.SopArtifact{
		TenantID: 1, RunID: run.ID, NodeID: "n1", Name: "分析报告",
		Path: "out/report.md", MediaType: "text/markdown", SHA256: "abc",
	}
	require.NoError(t, repository.NewSopRunRepo(db).CreateArtifact(ctx, art))

	stub := &stubKnowledge{}
	svc := NewArchiveService(ArchiveDeps{
		Runs:      repository.NewSopRunRepo(db),
		Items:     repository.NewSopItemRepo(db),
		Projects:  repository.NewSopProjectRepo(db),
		Knowledge: stub,
	})
	return svc, db, stub, run, art
}

func TestIngestArtifactBackfillsAndIdempotent(t *testing.T) {
	svc, db, stub, run, art := newArchiveIngestTestService(t)
	ctx := context.Background()

	got, err := svc.IngestArtifact(ctx, 1, "u1", run.ID, art.ID, "")
	require.NoError(t, err)
	require.Equal(t, "kn-1", got.KnowledgeID)
	require.Equal(t, "kb-p", got.KBID, "项目绑定兜底")
	require.NotNil(t, got.IngestedAt)
	require.Len(t, stub.payloads, 1)
	require.Equal(t, "sop", stub.channels[0])
	require.Contains(t, stub.payloads[0].Content, "sha256:")
	require.Equal(t, "publish", stub.payloads[0].Status)

	// 幂等：二次调用不再入库
	_, err = svc.IngestArtifact(ctx, 1, "u1", run.ID, art.ID, "")
	require.NoError(t, err)
	require.Len(t, stub.payloads, 1, "已归档的产物不得重复入库")

	// 库内状态持久（重开弹窗状态保持）
	reloaded, err := repository.NewSopRunRepo(db).GetArtifact(ctx, 1, art.ID)
	require.NoError(t, err)
	require.Equal(t, "kn-1", reloaded.KnowledgeID)
	require.Equal(t, "kb-p", reloaded.KBID)
}

// 显式指定 kb_id 优先于绑定链。
func TestIngestArtifactExplicitKB(t *testing.T) {
	svc, _, stub, run, art := newArchiveIngestTestService(t)
	_, err := svc.IngestArtifact(context.Background(), 1, "u1", run.ID, art.ID, "kb-explicit")
	require.NoError(t, err)
	require.Equal(t, []string{"kb-explicit"}, stub.kbIDs)
}

// 没有绑定知识库：白话错误。
func TestIngestRequiresKBBinding(t *testing.T) {
	svc, db, _, run, art := newArchiveIngestTestService(t)
	ctx := context.Background()
	proj, err := repository.NewSopProjectRepo(db).GetByID(ctx, 1, run.ProjectID)
	require.NoError(t, err)
	proj.KBID = ""
	require.NoError(t, repository.NewSopProjectRepo(db).Update(ctx, proj))

	_, err = svc.IngestArtifact(ctx, 1, "u1", run.ID, art.ID, "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "绑定知识库")
}

// 跨运行取产物：按「不存在」处理（不泄露别的运行有没有这个 id）。
func TestIngestArtifactRejectsForeignRun(t *testing.T) {
	svc, db, _, run, art := newArchiveIngestTestService(t)
	ctx := context.Background()
	other := &types.SopRun{TenantID: 1, ProjectID: run.ProjectID, Status: types.SopRunSucceeded}
	require.NoError(t, repository.NewSopRunRepo(db).CreateRun(ctx, other))
	_, err := svc.IngestArtifact(ctx, 1, "u1", other.ID, art.ID, "")
	require.Error(t, err)
}

// 自动归档钩子：逐个归档，单个失败只记日志（返回不 panic）。
func TestIngestNodeArtifactsContinuesOnFailure(t *testing.T) {
	svc, _, stub, run, art := newArchiveIngestTestService(t)
	ctx := context.Background()
	stub.err = fmt.Errorf("kb down")
	svc.IngestNodeArtifacts(ctx, run, &types.SopNode{ID: "n1", AutoIngest: true}, []*types.SopArtifact{art})
	require.Len(t, stub.payloads, 0, "失败时没有入库调用落地")
}
