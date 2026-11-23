// tools_sop.go 是 MCP 端点上的 SOP 工具组：让外部 MCP 客户端（CLI 执行器、
// 外部 Agent）能查模板/运行、发起运行、审批节点、读写运行工作区。
//
// 三条口径：
//   - 白名单由既有机制把住：filterTools 只暴露端点 Tools 里的名字，guardTool
//     拒绝白名单外调用——本文件不重复实现一遍权限；
//   - 工作区写入只允许 out/ 与 work/：inputs/ 是上游产物（只读事实），.sop/ 是
//     引擎的内部状态，模型随手改写会把整条流程的追溯打乱；
//   - 运行归属以端点的租户为准：所有查询都带 TenantID，跨租户 id 一律按「不存在」。
package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/Tencent/WeKnora/internal/application/service/sop"
	"github.com/Tencent/WeKnora/internal/application/service/sop/engine"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// SOPToolDeps SOP 工具组的依赖（RunSvc/Engine 可 nil：只做读的端点也能用）。
type SOPToolDeps struct {
	Templates interfaces.SopTemplateRepository
	Runs      interfaces.SopRunRepository
	RunSvc    *sop.RunService
	Engine    *engine.Engine
	// Workspaces 提供运行工作区根（测试可注入；nil 时用 run.WorkspaceDir）。
}

// sopTools 构造 SOP 工具组的注册项。
//
// 工具面由 catalog 决定（settings 页列出的就是服务端暴露的），所以这组工具在
// NewServer 里就注册；依赖（模板/运行服务）由 container 稍后用 RegisterSOPTools
// 填入，未填前处理器统一回「not available on this server」。
func (s *Server) sopTools() []server.ServerTool {
	return []server.ServerTool{
		{Tool: sopListTemplatesTool(), Handler: s.guardTool(s.handleSOPListTemplates)},
		{Tool: sopListRunsTool(), Handler: s.guardTool(s.handleSOPListRuns)},
		{Tool: sopGetRunTool(), Handler: s.guardTool(s.handleSOPGetRun)},
		{Tool: sopCreateRunTool(), Handler: s.guardTool(s.handleSOPCreateRun)},
		{Tool: sopApproveNodeTool(), Handler: s.guardTool(s.handleSOPApproveNode)},
		{Tool: sopRejectNodeTool(), Handler: s.guardTool(s.handleSOPRejectNode)},
		{Tool: sopWorkspaceListTool(), Handler: s.guardTool(s.handleSOPWorkspaceList)},
		{Tool: sopWorkspaceReadTool(), Handler: s.guardTool(s.handleSOPWorkspaceRead)},
		{Tool: sopWorkspaceWriteTool(), Handler: s.guardTool(s.handleSOPWorkspaceWrite)},
	}
}

// RegisterSOPTools 注入 SOP 工具组的依赖（构造后由 container 调用）。
func (s *Server) RegisterSOPTools(d SOPToolDeps) {
	if s == nil {
		return
	}
	s.sopDeps = d
}

// ---- 工具定义 ----

func sopListTemplatesTool() mcp.Tool {
	return mcp.NewTool(types.MCPEndpointToolSOPListTemplates,
		mcp.WithDescription("List SOP workflow templates available to this workspace, including the ones bound "+
			"to a project. Published templates can start runs."),
		mcp.WithString("project_id", mcp.Description("Restrict to one project; omit for the whole workspace")),
		readOnlyEndpointAnnotations(),
	)
}

func sopListRunsTool() mcp.Tool {
	return mcp.NewTool(types.MCPEndpointToolSOPListRuns,
		mcp.WithDescription("List SOP runs, newest first. Filter by project, item or status to find the run "+
			"a request refers to."),
		mcp.WithString("project_id", mcp.Description("Only runs of this project")),
		mcp.WithString("item_id", mcp.Description("Only runs of this work item")),
		mcp.WithString("status", mcp.Description("queued|running|waiting|succeeded|failed|cancelled|paused")),
		mcp.WithNumber("limit", mcp.Description("Maximum runs to return, default 20")),
		readOnlyEndpointAnnotations(),
	)
}

func sopGetRunTool() mcp.Tool {
	return mcp.NewTool(types.MCPEndpointToolSOPGetRun,
		mcp.WithDescription("Read one SOP run: overall status, per-node progress and artifacts."),
		mcp.WithString("run_id", mcp.Required(), mcp.Description("Run id")),
		readOnlyEndpointAnnotations(),
	)
}

func sopCreateRunTool() mcp.Tool {
	return mcp.NewTool(types.MCPEndpointToolSOPCreateRun,
		mcp.WithDescription("Start a SOP run from a published template, optionally attached to a work item. "+
			"The run executes asynchronously; poll sop_get_run for progress."),
		mcp.WithString("template_id", mcp.Required(), mcp.Description("Published template id from "+
			"sop_list_templates")),
		mcp.WithString("item_id", mcp.Description("Optional work item id the run belongs to")),
		destructiveEndpointAnnotations(),
	)
}

func sopApproveNodeTool() mcp.Tool {
	return mcp.NewTool(types.MCPEndpointToolSOPApproveNode,
		mcp.WithDescription("Approve a node that is waiting for human approval, with an optional comment."),
		mcp.WithString("run_id", mcp.Required(), mcp.Description("Run id")),
		mcp.WithString("node_id", mcp.Required(), mcp.Description("Waiting node id")),
		mcp.WithString("comment", mcp.Description("Why it is approved; shown in the decision record")),
		destructiveEndpointAnnotations(),
	)
}

func sopRejectNodeTool() mcp.Tool {
	return mcp.NewTool(types.MCPEndpointToolSOPRejectNode,
		mcp.WithDescription("Send a waiting node back for rework. Name the upstream node to return to when the "+
			"template allows it."),
		mcp.WithString("run_id", mcp.Required(), mcp.Description("Run id")),
		mcp.WithString("node_id", mcp.Required(), mcp.Description("Waiting node id")),
		mcp.WithString("comment", mcp.Description("What needs to change")),
		mcp.WithString("reject_to", mcp.Description("Upstream node id to return to; defaults to the template's "+
			"setting")),
		destructiveEndpointAnnotations(),
	)
}

func sopWorkspaceListTool() mcp.Tool {
	return mcp.NewTool(types.MCPEndpointToolSOPWorkspaceList,
		mcp.WithDescription("List files in a run's workspace: out/ holds this run's artifacts, work/ holds "+
			"cloned repositories, inputs/ holds upstream artifacts (read-only)."),
		mcp.WithString("run_id", mcp.Required(), mcp.Description("Run id")),
		mcp.WithString("path", mcp.Description("Directory relative to the workspace root; defaults to the root")),
		readOnlyEndpointAnnotations(),
	)
}

func sopWorkspaceReadTool() mcp.Tool {
	return mcp.NewTool(types.MCPEndpointToolSOPWorkspaceRead,
		mcp.WithDescription("Read one text file from a run's workspace by relative path."),
		mcp.WithString("run_id", mcp.Required(), mcp.Description("Run id")),
		mcp.WithString("path", mcp.Required(), mcp.Description("File path relative to the workspace root")),
		readOnlyEndpointAnnotations(),
	)
}

func sopWorkspaceWriteTool() mcp.Tool {
	return mcp.NewTool(types.MCPEndpointToolSOPWorkspaceWrite,
		mcp.WithDescription("Write a text file under out/ or work/ in a run's workspace. inputs/ and .sop/ are "+
			"not writable through this tool."),
		mcp.WithString("run_id", mcp.Required(), mcp.Description("Run id")),
		mcp.WithString("path", mcp.Required(), mcp.Description("File path under out/ or work/")),
		mcp.WithString("content", mcp.Required(), mcp.Description("Full file content")),
		destructiveEndpointAnnotations(),
	)
}

// destructiveEndpointAnnotations 标记会改数据的工具（客户端据此提示）。
func destructiveEndpointAnnotations() mcp.ToolOption {
	return func(t *mcp.Tool) {
		t.Annotations.DestructiveHint = boolPtr(true)
	}
}

// boolPtr 取 bool 指针（mcp.Tool 的注解字段是指针）。
func boolPtr(v bool) *bool { return &v }

// ---- 处理器 ----

// handleSOPListTemplates 列模板（含内置与本项目）。
func (s *Server) handleSOPListTemplates(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ep, err := endpointFromContext(ctx)
	if err != nil {
		return mcp.NewToolResultError("unauthorized"), nil
	}
	if s.sopDeps.Templates == nil {
		return mcp.NewToolResultError("SOP templates are not available on this server"), nil
	}
	projectID := strings.TrimSpace(req.GetString("project_id", ""))
	list, err := s.sopDeps.Templates.ListByTenant(ctx, ep.TenantID, projectID)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("list templates: %v", err)), nil
	}
	rows := make([]map[string]any, 0, len(list))
	for i := range list {
		tpl := &list[i]
		rows = append(rows, map[string]any{
			"id": tpl.ID, "name": tpl.Name, "project_id": tpl.ProjectID,
			"status": tpl.Status, "is_builtin": tpl.IsBuiltin,
			"published_revision_id": tpl.PublishedRevisionID,
		})
	}
	return jsonToolResult(map[string]any{"templates": rows})
}

// handleSOPListRuns 列运行。
func (s *Server) handleSOPListRuns(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ep, err := endpointFromContext(ctx)
	if err != nil {
		return mcp.NewToolResultError("unauthorized"), nil
	}
	if s.sopDeps.Runs == nil {
		return mcp.NewToolResultError("SOP runs are not available on this server"), nil
	}
	limit := int(req.GetFloat("limit", 20))
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	list, err := s.sopDeps.Runs.ListRuns(ctx, ep.TenantID, types.SopRunFilter{
		ProjectID: strings.TrimSpace(req.GetString("project_id", "")),
		ItemID:    strings.TrimSpace(req.GetString("item_id", "")),
		Status:    strings.TrimSpace(req.GetString("status", "")),
	}, 1, limit)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("list runs: %v", err)), nil
	}
	rows := make([]map[string]any, 0, len(list))
	for i := range list {
		run := &list[i]
		rows = append(rows, map[string]any{
			"id": run.ID, "template_id": run.TemplateID, "item_id": run.ItemID,
			"project_id": run.ProjectID, "status": run.Status, "error": run.Error,
			"created_at": run.CreatedAt,
		})
	}
	return jsonToolResult(map[string]any{"runs": rows})
}

// handleSOPGetRun 运行详情（节点进度 + 产物）。
func (s *Server) handleSOPGetRun(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ep, err := endpointFromContext(ctx)
	if err != nil {
		return mcp.NewToolResultError("unauthorized"), nil
	}
	runID := strings.TrimSpace(req.GetString("run_id", ""))
	run, err := s.sopRun(ctx, ep.TenantID, runID)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	nodes, _ := s.sopDeps.Runs.ListNodeRuns(ctx, ep.TenantID, run.ID)
	arts, _ := s.sopDeps.Runs.ListArtifacts(ctx, ep.TenantID, run.ID)
	nodeRows := make([]map[string]any, 0, len(nodes))
	for i := range nodes {
		n := &nodes[i]
		nodeRows = append(nodeRows, map[string]any{
			"node_id": n.NodeID, "status": n.Status, "attempt": n.Attempt,
			"gate_result": n.GateResult, "gate_reason": n.GateReason, "error": n.Error,
		})
	}
	return jsonToolResult(map[string]any{
		"run": map[string]any{
			"id": run.ID, "status": run.Status, "template_id": run.TemplateID,
			"item_id": run.ItemID, "error": run.Error,
		},
		"nodes":     nodeRows,
		"artifacts": artifactsJSON(arts),
	})
}

// handleSOPCreateRun 从已发布模板发起运行。
func (s *Server) handleSOPCreateRun(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ep, err := endpointFromContext(ctx)
	if err != nil {
		return mcp.NewToolResultError("unauthorized"), nil
	}
	if s.sopDeps.RunSvc == nil {
		return mcp.NewToolResultError("starting SOP runs is not available on this server"), nil
	}
	run, err := s.sopDeps.RunSvc.Create(ctx, ep.TenantID, "mcp", sop.CreateRunInput{
		TemplateID: strings.TrimSpace(req.GetString("template_id", "")),
		ItemID:     strings.TrimSpace(req.GetString("item_id", "")),
	})
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return jsonToolResult(map[string]any{"run_id": run.ID, "status": run.Status})
}

// handleSOPApproveNode 审批通过。
func (s *Server) handleSOPApproveNode(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ep, err := endpointFromContext(ctx)
	if err != nil {
		return mcp.NewToolResultError("unauthorized"), nil
	}
	if s.sopDeps.Engine == nil {
		return mcp.NewToolResultError("approving SOP nodes is not available on this server"), nil
	}
	runID := strings.TrimSpace(req.GetString("run_id", ""))
	if err := s.sopDeps.Engine.Approve(ctx, ep.TenantID, runID,
		strings.TrimSpace(req.GetString("node_id", "")),
		strings.TrimSpace(req.GetString("comment", "")), "mcp"); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return jsonToolResult(map[string]any{"approved": true})
}

// handleSOPRejectNode 打回。
func (s *Server) handleSOPRejectNode(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ep, err := endpointFromContext(ctx)
	if err != nil {
		return mcp.NewToolResultError("unauthorized"), nil
	}
	if s.sopDeps.Engine == nil {
		return mcp.NewToolResultError("rejecting SOP nodes is not available on this server"), nil
	}
	runID := strings.TrimSpace(req.GetString("run_id", ""))
	if err := s.sopDeps.Engine.Reject(ctx, ep.TenantID, runID,
		strings.TrimSpace(req.GetString("node_id", "")),
		strings.TrimSpace(req.GetString("comment", "")), "mcp",
		strings.TrimSpace(req.GetString("reject_to", ""))); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return jsonToolResult(map[string]any{"rejected": true})
}

// ---- 工作区三件套 ----

// sopWorkspaceRoot 取运行工作区根（归属由 GetRun 的租户过滤保证）。
func (s *Server) sopWorkspaceRoot(ctx context.Context, tenantID uint64, runID string) (string, error) {
	run, err := s.sopRun(ctx, tenantID, runID)
	if err != nil {
		return "", err
	}
	root := strings.TrimSpace(run.WorkspaceDir)
	if root == "" {
		return "", errors.New("该运行还没有工作区")
	}
	return root, nil
}

// handleSOPWorkspaceList 列目录（默认根）。
func (s *Server) handleSOPWorkspaceList(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ep, err := endpointFromContext(ctx)
	if err != nil {
		return mcp.NewToolResultError("unauthorized"), nil
	}
	root, err := s.sopWorkspaceRoot(ctx, ep.TenantID, strings.TrimSpace(req.GetString("run_id", "")))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	rel := strings.TrimSpace(req.GetString("path", ""))
	target, err := engine.SafeWorkspacePath(&engine.Workspace{Root: root}, rel)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	entries, err := os.ReadDir(target)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("read directory: %v", err)), nil
	}
	rows := make([]map[string]any, 0, len(entries))
	for _, entry := range entries {
		info, statErr := entry.Info()
		row := map[string]any{"name": entry.Name(), "dir": entry.IsDir()}
		if statErr == nil {
			row["size"] = info.Size()
		}
		rows = append(rows, row)
	}
	return jsonToolResult(map[string]any{"entries": rows})
}

// handleSOPWorkspaceRead 读文本文件。
func (s *Server) handleSOPWorkspaceRead(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ep, err := endpointFromContext(ctx)
	if err != nil {
		return mcp.NewToolResultError("unauthorized"), nil
	}
	root, err := s.sopWorkspaceRoot(ctx, ep.TenantID, strings.TrimSpace(req.GetString("run_id", "")))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	full, err := engine.SafeWorkspacePath(&engine.Workspace{Root: root},
		strings.TrimSpace(req.GetString("path", "")))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	body, err := os.ReadFile(full)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("read file: %v", err)), nil
	}
	return mcp.NewToolResultText(string(body)), nil
}

// 可写目录：out/ 是本节点产物，work/ 是克隆仓库等作业目录。
var sopWritableDirs = []string{"out", "work"}

// handleSOPWorkspaceWrite 写文本文件（仅 out/ 与 work/）。
func (s *Server) handleSOPWorkspaceWrite(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ep, err := endpointFromContext(ctx)
	if err != nil {
		return mcp.NewToolResultError("unauthorized"), nil
	}
	root, err := s.sopWorkspaceRoot(ctx, ep.TenantID, strings.TrimSpace(req.GetString("run_id", "")))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	rel := filepath.ToSlash(strings.TrimSpace(req.GetString("path", "")))
	if !writableSOPPath(rel) {
		return mcp.NewToolResultError("only out/ and work/ are writable through this tool; " +
			"inputs/ and .sop/ are managed by the engine"), nil
	}
	full, err := engine.SafeWorkspacePath(&engine.Workspace{Root: root}, rel)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("create directory: %v", err)), nil
	}
	content := req.GetString("content", "")
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("write file: %v", err)), nil
	}
	return jsonToolResult(map[string]any{"written": len(content), "path": rel})
}

// writableSOPPath 判断相对路径是否落在可写目录里（拒绝 ../ 与绝对路径）。
func writableSOPPath(rel string) bool {
	clean := strings.TrimSpace(rel)
	if clean == "" || strings.HasPrefix(clean, "/") || strings.Contains(clean, "..") {
		return false
	}
	for _, dir := range sopWritableDirs {
		if clean == dir || strings.HasPrefix(clean, dir+"/") {
			return true
		}
	}
	return false
}

// sopRun 按租户取运行（不存在统一报「运行不存在」）。
func (s *Server) sopRun(ctx context.Context, tenantID uint64, runID string) (*types.SopRun, error) {
	if s.sopDeps.Runs == nil {
		return nil, errors.New("SOP runs are not available on this server")
	}
	if runID == "" {
		return nil, errors.New("run_id is required")
	}
	run, err := s.sopDeps.Runs.GetRun(ctx, tenantID, runID)
	if err != nil || run == nil || run.ID == "" {
		return nil, errors.New("运行不存在")
	}
	return run, nil
}

// artifactsJSON 把产物摊平成 MCP 响应形状。
func artifactsJSON(arts []types.SopArtifact) []map[string]any {
	rows := make([]map[string]any, 0, len(arts))
	for i := range arts {
		a := &arts[i]
		rows = append(rows, map[string]any{
			"id": a.ID, "node_id": a.NodeID, "name": a.Name, "path": a.Path,
			"size": a.Size, "sha256": a.SHA256, "knowledge_id": a.KnowledgeID,
		})
	}
	return rows
}

// jsonToolResult 把结构体编码成 MCP 文本结果（客户端直接可读的 JSON）。
func jsonToolResult(v any) (*mcp.CallToolResult, error) {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("encode result: %v", err)), nil
	}
	return mcp.NewToolResultText(string(raw)), nil
}
