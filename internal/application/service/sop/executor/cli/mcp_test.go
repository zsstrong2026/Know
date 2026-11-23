package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/application/service/sop/executor"
	"github.com/Tencent/WeKnora/internal/types"
)

// 只渲染节点勾选的 server；Know 端点恒定追加（Bearer + kb_scope）。
func TestRenderMCPOnlyNodeSubset(t *testing.T) {
	servers := []types.MCPService{{
		Name: "team-wiki", TransportType: types.MCPTransportHTTPStreamable,
		URL: ptr("http://127.0.0.1:9000/mcp"), Headers: types.MCPHeaders{"X-Trace": "1"},
	}}
	cfg := RenderMCPConfig("claude", servers, KnowMCPEndpoint{
		URL: "http://127.0.0.1:8080/mcp/sop", Token: "tok", KBScope: "project",
	})
	body, ok := cfg[".sop/mcp.json"]
	require.True(t, ok, "claude 走 --mcp-config 指向的单独文件")
	require.Contains(t, body, "team-wiki")
	require.Contains(t, body, "X-Trace")
	require.Contains(t, body, "Bearer tok", "Know 端点必须带 Bearer")
	require.Contains(t, body, "kb_scope=project", "范围随配置下发（服务端强制校验）")
	require.NotContains(t, body, "未勾选的server")
}

// 空 servers + 空 Know 端点：不渲染任何东西（结构性零）。
func TestRenderMCPEmpty(t *testing.T) {
	cfg := RenderMCPConfig("claude", nil, KnowMCPEndpoint{})
	body := cfg[".sop/mcp.json"]
	require.JSONEq(t, `{"mcpServers":{}}`, strings.TrimSpace(body))
}

// 各 CLI 的 MCP 配置路径表。
func TestRenderMCPPerCLIPaths(t *testing.T) {
	servers := []types.MCPService{{
		Name: "know-kb", TransportType: types.MCPTransportHTTPStreamable, URL: ptr("http://x/mcp"),
	}}
	ep := KnowMCPEndpoint{URL: "http://x/mcp/sop", Token: "t", KBScope: "all"}
	cases := map[string]string{
		"cursor-agent": ".cursor/mcp.json",
		"agy":          ".gemini/antigravity-cli/settings.json",
		"kimi":         ".kimi-code/config.toml",
		"pi":           ".pi/agent/mcp.json",
		"dsh":          ".dsh/config.json",
		"zcode":        ".zcode/cli/config.json",
		"claude":       ".sop/mcp.json",
		"codebuddy":    ".codebuddy/mcp.json",
		"codex":        ".codex/config.toml",
	}
	for kind, wantPath := range cases {
		cfg := RenderMCPConfig(kind, servers, ep)
		require.Contains(t, cfg, wantPath, "kind=%s", kind)
		require.NotEmpty(t, cfg[wantPath], "kind=%s 的内容不能为空", kind)
	}
	// opencode 走环境变量整段合并
	oc := RenderMCPConfig("opencode", servers, ep)
	require.Contains(t, oc, MCPEnvKeyPrefix+"OPENCODE_CONFIG_CONTENT")
	require.Contains(t, oc[MCPEnvKeyPrefix+"OPENCODE_CONFIG_CONTENT"], `"mcp"`)
}

// stdio：只透传本机已安装命令，不做安装托管。
func TestRenderMCPStdioLocalOnly(t *testing.T) {
	servers := []types.MCPService{{
		Name: "local-tool", TransportType: types.MCPTransportStdio,
		StdioConfig: &types.MCPStdioConfig{Command: "npx", Args: []string{"-y", "some-mcp"}},
		EnvVars:     types.MCPEnvVars{"TOKEN": "env-token"},
	}}
	cfg := RenderMCPConfig("claude", servers, KnowMCPEndpoint{})
	body := cfg[".sop/mcp.json"]
	require.Contains(t, body, "npx")
	require.Contains(t, body, "some-mcp")
	require.Contains(t, body, "env-token")
}

// 凭据形态：AuthConfig.Token → Bearer；APIKey → 默认 X-API-Key 头。
func TestRenderMCPCredentialHeaders(t *testing.T) {
	servers := []types.MCPService{
		{
			Name: "with-token", TransportType: types.MCPTransportHTTPStreamable, URL: ptr("http://a/mcp"),
			AuthConfig: &types.MCPAuthConfig{Token: "tk-1"},
		},
		{
			Name: "with-api-key", TransportType: types.MCPTransportHTTPStreamable, URL: ptr("http://b/mcp"),
			AuthConfig: &types.MCPAuthConfig{APIKey: "ak-2"},
		},
	}
	body := RenderMCPConfig("claude", servers, KnowMCPEndpoint{})[".sop/mcp.json"]
	require.Contains(t, body, "Bearer tk-1")
	require.Contains(t, body, `"X-API-Key":"ak-2"`)
}

// 节点没勾 MCP（[]）就不该出现 Know 端点以外的条目。
func TestRenderMCPExplicitEmptySlice(t *testing.T) {
	body := RenderMCPConfig("claude", []types.MCPService{}, KnowMCPEndpoint{
		URL: "http://know/mcp", Token: "t", KBScope: "project",
	})[".sop/mcp.json"]
	require.Contains(t, body, "know-kb", "平台端点恒定追加")
	// 条目名只能是 know-kb：数一下 mcpServers 里的键（"name":{ 形态）。
	require.Equal(t, 1, strings.Count(body, `"know-kb":`), "只应有 know-kb 一条：%s", body)
	require.NotContains(t, body, "team-wiki")
}

// MergeMCPIntoConfig：文件合并 + ENV 类分流。
func TestMergeMCPIntoConfig(t *testing.T) {
	files := map[string]string{".zcode/cli/config.json": "{\"apiKey\":\"${SOP_CLI_API_KEY}\"}"}
	rendered := map[string]string{
		".zcode/cli/config.json":                    `{"mcpServers":{"know-kb":{"url":"http://x"}}}`,
		MCPEnvKeyPrefix + "OPENCODE_CONFIG_CONTENT": `{"mcp":{}}`,
	}
	merged, env := MergeMCPIntoConfig(files, rendered)
	require.Contains(t, merged[".zcode/cli/config.json"], "mcpServers", "同路径 JSON 要合并而不是覆盖")
	require.Contains(t, merged[".zcode/cli/config.json"], "SOP_CLI_API_KEY", "原配置字段保留")
	require.Equal(t, `{"mcp":{}}`, env["OPENCODE_CONFIG_CONTENT"])
}

// ptr 取字符串指针。
func ptr(s string) *string { return &s }

// kb_ids 也进 query。
func TestWithKBScopeIncludesIDs(t *testing.T) {
	url := withKBScope("http://x/mcp", "custom", []string{"kb1", "kb2"})
	require.Contains(t, url, "kb_scope=custom")
	require.Contains(t, url, "kb_ids=kb1,kb2")
	// 已有 query 时用 &
	url2 := withKBScope("http://x/mcp?a=1", "all", nil)
	require.Contains(t, url2, "?a=1&kb_scope=all")
}

// RunStream 端到端：MCP 配置落进私有 HOME，know 端点带 Bearer 与 kb_scope。
func TestRunStreamWritesMCPConfig(t *testing.T) {
	a := fakeBinary(t, `ls -R "$HOME" > "$HOME/../listing.txt"`)
	root := t.TempDir()
	tb := executor.TaskBook{
		NodeID: "n1", WorkspaceRoot: root, Attempt: 1, Timeout: 20 * time.Second,
		MCPServers: []types.MCPService{{
			Name: "team-wiki", TransportType: types.MCPTransportHTTPStreamable, URL: ptr("http://a/mcp"),
		}},
		KnowMCP: &executor.KnowMCPEndpoint{
			URL: "http://know/mcp", Token: "tk", KBScope: "project",
		},
	}
	_, err := RunStream(context.Background(), a, tb, nil)
	require.NoError(t, err)

	body, err := os.ReadFile(filepath.Join(root, ".sop", "private-home", "claude", ".sop", "mcp.json"))
	require.NoError(t, err, "MCP 配置必须落在私有 HOME 内")
	require.Contains(t, string(body), "team-wiki")
	require.Contains(t, string(body), "Bearer tk")
	require.Contains(t, string(body), "kb_scope=project")
}
