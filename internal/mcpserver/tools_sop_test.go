package mcpserver

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
)

// newSOPTestServer 装配带 SOP 工具组的服务端（真实仓储 + sqlite 内存库）。
func newSOPTestServer(t *testing.T) (*Server, *gorm.DB) {
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
		&types.SopProject{}, &types.SopItem{}, &types.SopTemplate{},
		&types.SopTemplateRevision{}, &types.SopRun{}, &types.SopNodeRun{},
		&types.SopArtifact{}, &types.MCPEndpoint{},
	))
	require.NoError(t, db.Exec(
		"CREATE UNIQUE INDEX IF NOT EXISTS ux_sop_node_runs_run_node ON sop_node_runs(run_id, node_id)",
	).Error)

	srv := NewServer(nil, nil, nil, nil, nil, nil, nil, nil, nil,
		&recordingEndpointRepo{touched: make(chan string, 1)}, nil, nil, nil, nil, nil, nil)
	srv.RegisterSOPTools(SOPToolDeps{
		Templates: repository.NewSopTemplateRepo(db),
		Runs:      repository.NewSopRunRepo(db),
		RunSvc:    nil, // 只有读路径的用例不需要
		Engine:    nil,
	})
	return srv, db
}

// seedSOPEndpoint 建端点行（白名单由用例决定）。
func seedSOPEndpoint(t *testing.T, db *gorm.DB, name string, tools ...string) *types.MCPEndpoint {
	t.Helper()
	ep := &types.MCPEndpoint{
		TenantID: 1, Name: name, Enabled: true,
		Tools: types.StringArray(tools),
	}
	require.NoError(t, db.Create(ep).Error)
	return ep
}

// ctxWithEndpoint 把端点放进 ctx（照 server_test.go 的注入方式）。
func ctxWithEndpoint(ep *types.MCPEndpoint) context.Context {
	ctx := context.WithValue(context.Background(), types.MCPEndpointContextKey, ep)
	return context.WithValue(ctx, types.TenantIDContextKey, ep.TenantID)
}

// toolReq 构造一次工具调用请求。
func toolReq(args map[string]any) mcp.CallToolRequest {
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args
	return req
}

// textOf 取文本结果。
func textOf(res *mcp.CallToolResult) string {
	if res == nil || len(res.Content) == 0 {
		return ""
	}
	if text, ok := res.Content[0].(mcp.TextContent); ok {
		return text.Text
	}
	return ""
}

func TestSOPToolCatalogAndCapabilities(t *testing.T) {
	def, ok := types.LookupMCPEndpointTool(types.MCPEndpointToolSOPCreateRun)
	require.True(t, ok)
	require.True(t, def.Destructive, "发起运行会改数据")

	caps := types.MCPEndpointCapabilitiesForTools([]string{
		types.MCPEndpointToolSOPCreateRun, types.MCPEndpointToolSOPGetRun,
	})
	require.Contains(t, caps, string(types.APIKeyCapabilityRunSOP))
	require.Contains(t, caps, string(types.APIKeyCapabilityReadSOP))

	require.Equal(t, []string{types.MCPEndpointToolSOPGetRun, types.MCPEndpointToolSOPCreateRun},
		types.NormalizeMCPEndpointTools([]string{
			types.MCPEndpointToolSOPCreateRun, types.MCPEndpointToolSOPGetRun,
			types.MCPEndpointToolSOPCreateRun, "bogus",
		}), "catalog 序 + 去重 + 丢未知")

	// sop 组出现在组清单里
	require.Contains(t, types.MCPEndpointToolGroups(), types.MCPEndpointToolGroupSOP)
}

// 工作区写入只允许 out/ 与 work/；其余目录一律拒绝。
func TestWorkspaceWriteRestrictedToOutAndWork(t *testing.T) {
	s, db := newSOPTestServer(t)
	ws := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(ws, "out"), 0o755))
	require.NoError(t, repository.NewSopRunRepo(db).CreateRun(context.Background(), &types.SopRun{
		TenantID: 1, Status: types.SopRunSucceeded, WorkspaceDir: ws,
	}))
	runs, err := repository.NewSopRunRepo(db).ListRuns(context.Background(), 1, types.SopRunFilter{}, 0, 0)
	require.NoError(t, err)
	require.Len(t, runs, 1)
	runID := runs[0].ID

	ep := seedSOPEndpoint(t, db, "sop:run:n1",
		types.MCPEndpointToolSOPWorkspaceList, types.MCPEndpointToolSOPWorkspaceRead,
		types.MCPEndpointToolSOPWorkspaceWrite)
	ctx := ctxWithEndpoint(ep)

	// 越界目录：inputs 拒绝
	res, err := s.handleSOPWorkspaceWrite(ctx, toolReq(map[string]any{
		"run_id": runID, "path": "inputs/evil.md", "content": "x",
	}))
	require.NoError(t, err)
	require.True(t, res.IsError)

	// .sop 拒绝
	res, err = s.handleSOPWorkspaceWrite(ctx, toolReq(map[string]any{
		"run_id": runID, "path": ".sop/evil.md", "content": "x",
	}))
	require.NoError(t, err)
	require.True(t, res.IsError)

	// out/ 允许
	res, err = s.handleSOPWorkspaceWrite(ctx, toolReq(map[string]any{
		"run_id": runID, "path": "out/report.md", "content": "# ok",
	}))
	require.NoError(t, err)
	require.False(t, res.IsError, textOf(res))

	// 读回来一致
	res, err = s.handleSOPWorkspaceRead(ctx, toolReq(map[string]any{
		"run_id": runID, "path": "out/report.md",
	}))
	require.NoError(t, err)
	require.Contains(t, textOf(res), "# ok")

	// 路径穿越拒绝
	res, _ = s.handleSOPWorkspaceRead(ctx, toolReq(map[string]any{
		"run_id": runID, "path": "../../etc/passwd",
	}))
	require.True(t, res.IsError)

	// 列目录
	res, err = s.handleSOPWorkspaceList(ctx, toolReq(map[string]any{"run_id": runID, "path": "out"}))
	require.NoError(t, err)
	require.False(t, res.IsError, textOf(res))
	require.Contains(t, textOf(res), "report.md")
}

// 白名单外的工具调用被既有 guardTool 拒绝。
func TestSOPToolsGuardedByAllowlist(t *testing.T) {
	s, db := newSOPTestServer(t)
	ep := seedSOPEndpoint(t, db, "plain-endpoint") // 不含任何 sop_* 工具
	ctx := ctxWithEndpoint(ep)

	res, err := s.guardTool(s.handleSOPListRuns)(ctx, toolReq(map[string]any{}))
	require.NoError(t, err)
	require.True(t, res.IsError, "白名单外调用必须被拒")
	require.Contains(t, textOf(res), "not enabled")
}

// 跨租户的 run_id 一律按「不存在」。
func TestSOPGetRunTenantIsolation(t *testing.T) {
	s, db := newSOPTestServer(t)
	other := &types.SopRun{TenantID: 2, Status: types.SopRunSucceeded}
	require.NoError(t, repository.NewSopRunRepo(db).CreateRun(context.Background(), other))

	ep := seedSOPEndpoint(t, db, "sop:iso:n1", types.MCPEndpointToolSOPGetRun)
	ctx := ctxWithEndpoint(ep)
	res, err := s.handleSOPGetRun(ctx, toolReq(map[string]any{"run_id": other.ID}))
	require.NoError(t, err)
	require.True(t, res.IsError)
	require.Contains(t, textOf(res), "运行不存在")
}

// 运行详情包含节点与产物。
func TestSOPGetRunIncludesNodes(t *testing.T) {
	s, db := newSOPTestServer(t)
	ctx := context.Background()
	run := &types.SopRun{TenantID: 1, Status: types.SopRunSucceeded}
	require.NoError(t, repository.NewSopRunRepo(db).CreateRun(ctx, run))
	require.NoError(t, repository.NewSopRunRepo(db).CreateNodeRun(ctx, &types.SopNodeRun{
		TenantID: 1, RunID: run.ID, NodeID: "n1", Status: types.SopNodeSucceeded,
	}))
	require.NoError(t, repository.NewSopRunRepo(db).CreateArtifact(ctx, &types.SopArtifact{
		TenantID: 1, RunID: run.ID, NodeID: "n1", Name: "报告", Path: "out/a.md",
	}))

	ep := seedSOPEndpoint(t, db, "sop:detail:n1", types.MCPEndpointToolSOPGetRun)
	res, err := s.handleSOPGetRun(ctxWithEndpoint(ep), toolReq(map[string]any{"run_id": run.ID}))
	require.NoError(t, err)
	require.False(t, res.IsError, textOf(res))
	var payload map[string]any
	require.NoError(t, json.Unmarshal([]byte(textOf(res)), &payload))
	require.NotEmpty(t, payload["nodes"])
	require.NotEmpty(t, payload["artifacts"])
	require.True(t, strings.Contains(textOf(res), "out/a.md"))
}
