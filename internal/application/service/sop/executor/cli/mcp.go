// mcp.go 把「节点勾选的 MCP 服务 + 平台签发的 Know MCP 端点」渲染成各 CLI 能读的
// 配置片段。
//
// 三条口径：
//   - 只渲染勾选的 server：节点 MCP 为 nil 表示用角色的全部（已由 Resolver 展开），
//     显式 [] 表示一个都不要——配置里绝不能悄悄多出没勾选的服务；
//   - 凭据进头不进日志：HTTP server 的 token 渲染成 `Authorization: Bearer <token>`，
//     够 CLI 用即可，不落明文到别处；
//   - Know 端点恒定追加：URL 带 kb_scope/kb_ids，服务端按 token 强制校验范围
//     （客户端传参只是提示，真正的边界在服务端）。
package cli

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/Tencent/WeKnora/internal/application/service/sop/executor"
	"github.com/Tencent/WeKnora/internal/types"
)

// sortStrings 让渲染顺序稳定（同一份配置每次生成的字节一致，便于对账）。
func sortStrings(keys []string) { sort.Strings(keys) }

// KnowMCPEndpoint 是平台签发的受限检索端点（类型别名，实体在 executor 包）。
type KnowMCPEndpoint = executor.KnowMCPEndpoint

// MCPEnvKeyPrefix 是「该 CLI 的 MCP 配置走环境变量」的标记前缀。
//
// opencode 的整段配置就在 OPENCODE_CONFIG_CONTENT 里，没法拆成文件；渲染结果用
// 这个前缀表达「写进哪个 env」，调用方据此合并进 Env()。
const MCPEnvKeyPrefix = "ENV:"

// mcpServerEntry 是标准 mcpServers 条目（claude / cursor / codebuddy / pi 等同形）。
type mcpServerEntry struct {
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	Type    string            `json:"type,omitempty"`
	URL     string            `json:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

// RenderMCPConfig 按 CLI 种类渲染 MCP 配置：返回「私有 HOME 相对路径 → 内容」
// （opencode 返回 `ENV:OPENCODE_CONFIG_CONTENT` 形式的合并建议，见 MCPEnvKeyPrefix）。
//
// servers 是节点解析后的真实服务（nil=不渲染任何自定义 MCP）；know 是平台端点
// （URL 为空表示本次不注入 Know MCP）。
func RenderMCPConfig(kind string, servers []types.MCPService, know KnowMCPEndpoint) map[string]string {
	entries := map[string]mcpServerEntry{}
	for i := range servers {
		if name, entry, ok := mcpServerConfig(&servers[i]); ok {
			entries[name] = entry
		}
	}
	if know.URL != "" {
		entries["know-kb"] = mcpServerEntry{
			Type: "http",
			URL:  withKBScope(know.URL, know.KBScope, know.KBIDs),
			Headers: map[string]string{
				"Authorization": "Bearer " + know.Token,
			},
		}
	}
	body := renderMCPServersJSON(entries)

	switch kind {
	case "opencode":
		// opencode 的配置整段在 env 里：给出一个可直接合并的 JSON 片段。
		return map[string]string{MCPEnvKeyPrefix + "OPENCODE_CONFIG_CONTENT": `{"mcp":` + body + "}"}
	case "cursor-agent":
		return map[string]string{".cursor/mcp.json": wrapMCP(body)}
	case "agy":
		return map[string]string{".gemini/antigravity-cli/settings.json": wrapMCP(body)}
	case "kimi":
		return map[string]string{".kimi-code/config.toml": renderMCPToml(entries)}
	case "pi":
		return map[string]string{".pi/agent/mcp.json": wrapMCP(body)}
	case "dsh":
		return map[string]string{".dsh/config.json": wrapMCP(body)}
	case "zcode":
		return map[string]string{".zcode/cli/config.json": wrapMCP(body)}
	case "claude":
		// claude 用 --mcp-config 指向单独文件（BuildArgs 追加该参数）。
		return map[string]string{".sop/mcp.json": wrapMCP(body)}
	case "codebuddy":
		return map[string]string{".codebuddy/mcp.json": wrapMCP(body)}
	case "codex":
		return map[string]string{".codex/config.toml": renderMCPToml(entries)}
	default:
		return map[string]string{".sop/mcp.json": wrapMCP(body)}
	}
}

// wrapMCP 包成标准 mcpServers 文档。
func wrapMCP(body string) string { return `{"mcpServers":` + body + "}\n" }

// renderMCPServersJSON 把条目渲染成稳定的 JSON（键升序，便于对账与测试）。
func renderMCPServersJSON(entries map[string]mcpServerEntry) string {
	if len(entries) == 0 {
		return "{}"
	}
	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	sortStrings(names)
	parts := make([]string, 0, len(names))
	for _, name := range names {
		raw, err := json.Marshal(entries[name])
		if err != nil {
			continue
		}
		parts = append(parts, fmt.Sprintf("%q:%s", name, raw))
	}
	return "{" + strings.Join(parts, ",") + "}"
}

// renderMCPToml 把条目渲染成 TOML 的 [mcp_servers.<name>] 段（kimi/codex 形态）。
func renderMCPToml(entries map[string]mcpServerEntry) string {
	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	sortStrings(names)
	var b strings.Builder
	for _, name := range names {
		entry := entries[name]
		fmt.Fprintf(&b, "[mcp_servers.%s]\n", tomlKey(name))
		if entry.Command != "" {
			fmt.Fprintf(&b, "command = %q\n", entry.Command)
		}
		if len(entry.Args) > 0 {
			quoted := make([]string, 0, len(entry.Args))
			for _, a := range entry.Args {
				quoted = append(quoted, fmt.Sprintf("%q", a))
			}
			fmt.Fprintf(&b, "args = [%s]\n", strings.Join(quoted, ", "))
		}
		if entry.URL != "" {
			fmt.Fprintf(&b, "url = %q\n", entry.URL)
		}
		if len(entry.Headers) > 0 {
			keys := make([]string, 0, len(entry.Headers))
			for k := range entry.Headers {
				keys = append(keys, k)
			}
			sortStrings(keys)
			fmt.Fprintf(&b, "[mcp_servers.%s.headers]\n", tomlKey(name))
			for _, k := range keys {
				fmt.Fprintf(&b, "%s = %q\n", tomlKey(k), entry.Headers[k])
			}
		}
		b.WriteString("\n")
	}
	return b.String()
}

// tomlKey 把可能含特殊字符的键名包成引号形式（TOML 允许 "a b" = 1）。
func tomlKey(k string) string {
	safe := k != ""
	for _, r := range k {
		ok := r < 128 && (r == '_' || r == '-' ||
			(r >= '0' && r <= '9') || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z'))
		if !ok {
			safe = false
			break
		}
	}
	if safe {
		return k
	}
	return fmt.Sprintf("%q", k)
}

// mcpServerConfig 把一个 MCP 服务翻译成标准条目（stdio 只透传本机命令）。
func mcpServerConfig(svc *types.MCPService) (string, mcpServerEntry, bool) {
	name := strings.TrimSpace(svc.Name)
	if name == "" {
		return "", mcpServerEntry{}, false
	}
	switch svc.TransportType {
	case types.MCPTransportStdio:
		if svc.StdioConfig == nil || strings.TrimSpace(svc.StdioConfig.Command) == "" {
			return "", mcpServerEntry{}, false
		}
		entry := mcpServerEntry{
			Command: svc.StdioConfig.Command,
			Args:    append([]string{}, svc.StdioConfig.Args...),
		}
		if len(svc.EnvVars) > 0 {
			entry.Env = map[string]string{}
			for k, v := range svc.EnvVars {
				entry.Env[k] = v
			}
		}
		return name, entry, true
	default:
		// HTTP / SSE：URL 必填；凭据合并成 Bearer / 自定义头。
		if svc.URL == nil || strings.TrimSpace(*svc.URL) == "" {
			return "", mcpServerEntry{}, false
		}
		entry := mcpServerEntry{Type: "http", URL: strings.TrimSpace(*svc.URL)}
		headers := map[string]string{}
		for k, v := range svc.Headers {
			headers[k] = v
		}
		if svc.AuthConfig != nil {
			if token := strings.TrimSpace(svc.AuthConfig.Token); token != "" {
				headers["Authorization"] = "Bearer " + token
			}
			if apiKey := strings.TrimSpace(svc.AuthConfig.APIKey); apiKey != "" {
				headerName := strings.TrimSpace(svc.AuthConfig.APIKeyHeader)
				if headerName == "" {
					headerName = "X-API-Key"
				}
				headers[headerName] = apiKey
			}
			for k, v := range svc.AuthConfig.CustomHeaders {
				headers[k] = v
			}
		}
		if len(headers) > 0 {
			entry.Headers = headers
		}
		return name, entry, true
	}
}

// withKBScope 在 Know 端点 URL 上追加范围参数（服务端按 token 强制校验）。
func withKBScope(url, scope string, kbIDs []string) string {
	sep := "?"
	if strings.Contains(url, "?") {
		sep = "&"
	}
	out := url
	if s := strings.TrimSpace(scope); s != "" {
		out += sep + "kb_scope=" + s
		sep = "&"
	}
	if len(kbIDs) > 0 {
		out += sep + "kb_ids=" + strings.Join(kbIDs, ",")
	}
	return out
}

// MergeMCPIntoConfig 把 RenderMCPConfig 的结果合并进基准配置：
// 文件类并入 files；`ENV:` 前缀的条目交给调用方的 Env() 合并（返回 env 增量）。
func MergeMCPIntoConfig(
	files map[string]string, rendered map[string]string,
) (merged map[string]string, env map[string]string) {
	if merged == nil {
		merged = map[string]string{}
	}
	for k, v := range files {
		merged[k] = v
	}
	for k, v := range rendered {
		if strings.HasPrefix(k, MCPEnvKeyPrefix) {
			if env == nil {
				env = map[string]string{}
			}
			env[strings.TrimPrefix(k, MCPEnvKeyPrefix)] = v
			continue
		}
		// 同路径已存在（如 settings.json）：保留原内容并把 mcpServers 合并进去。
		if prev, ok := merged[k]; ok && strings.HasSuffix(k, ".json") &&
			strings.Contains(prev, "{") && strings.HasPrefix(strings.TrimSpace(v), "{") {
			if combined, ok := mergeJSONObjects(prev, v); ok {
				merged[k] = combined
				continue
			}
		}
		merged[k] = v
	}
	return merged, env
}

// mergeJSONObjects 尽力把两个 JSON 对象合并成一个（失败返回 false，调用方保留后者）。
func mergeJSONObjects(a, b string) (string, bool) {
	var left, right map[string]json.RawMessage
	if json.Unmarshal([]byte(a), &left) != nil || json.Unmarshal([]byte(b), &right) != nil {
		return "", false
	}
	for k, v := range right {
		left[k] = v
	}
	raw, err := json.Marshal(left)
	if err != nil {
		return "", false
	}
	return string(raw) + "\n", true
}
