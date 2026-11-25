// mcp.go 为每次「运行 × 节点」签发一个受限的 Know MCP 端点，供 CLI 执行器
// 拉取知识库与读写工作区。
//
// 三条口径：
//   - 确定性 token：HMAC(SYSTEM_AES_KEY, "sop-mcp:<run>:<node>")，不落明文。
//     重启/续跑/重试拿到的都是同一个 token，于是不必存储、也不必轮换；
//   - 服务端强制范围：端点行的 KnowledgeBaseIDs 就是解析后的 KB 白名单（空=全部
//     自有 KB，沿用 MCPEndpoint 既有语义），下游知识工具每次调用都会按它校验；
//   - 工具最小集：CLI 节点只给「检索 + 工作区读写」，不给 sop_create_run 这类
//     驱动流程的工具——执行器不该反过来编排自己。
package sop

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
)

// MCPEndpointStore 是端点仓储中本文件需要的方法（真实实现 = MCPEndpointRepository）。
type MCPEndpointStore interface {
	GetByName(ctx context.Context, tenantID uint64, name string) (*types.MCPEndpoint, error)
	Create(ctx context.Context, ep *types.MCPEndpoint) error
	Update(ctx context.Context, ep *types.MCPEndpoint) error
	Delete(ctx context.Context, tenantID uint64, id string) error
	ListByTenant(ctx context.Context, tenantID uint64) ([]*types.MCPEndpoint, error)
}

// MCPProvisioner 为运行节点签发受限 MCP 端点。
type MCPProvisioner struct {
	endpoints MCPEndpointStore
	baseURL   string
}

// NewMCPProvisioner 构造签发器；baseURL 是 Know MCP 服务的 HTTP 地址（如
// http://127.0.0.1:8080/mcp）。
func NewMCPProvisioner(endpoints MCPEndpointStore, baseURL string) *MCPProvisioner {
	return &MCPProvisioner{endpoints: endpoints, baseURL: baseURL}
}

// cliEndpointTools 是 CLI 节点可用的工具集：知识检索 + 工作区三件套。
func cliEndpointTools() []string {
	return types.NormalizeMCPEndpointTools([]string{
		types.MCPEndpointToolListKnowledgeBases, types.MCPEndpointToolSearchKnowledge,
		types.MCPEndpointToolReadDocument,
		types.MCPEndpointToolSOPWorkspaceList, types.MCPEndpointToolSOPWorkspaceRead,
		types.MCPEndpointToolSOPWorkspaceWrite,
	})
}

// EnsureNodeEndpoint 幂等地保证「该运行该节点」的端点存在，返回 MCP URL 与 token。
//
// kbIDs 是解析后的知识库白名单（nil/空 = 不限，沿用 MCPEndpoint 的语义）。
func (p *MCPProvisioner) EnsureNodeEndpoint(
	ctx context.Context, tenantID uint64, runID, nodeID string, kbIDs []string,
) (string, string, error) {
	if p == nil || p.endpoints == nil {
		return "", "", errors.New("Know MCP 签发器未就绪")
	}
	key := utils.GetAESKey()
	if key == nil {
		// 不静默降级：没有密钥就签不出可验证的 token，CLI 拿到空凭据只会在运行期
		// 报 401，更难排查。
		return "", "", errors.New("SYSTEM_AES_KEY 未配置，无法为 CLI 生成 Know MCP 凭据")
	}
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte("sop-mcp:" + runID + ":" + nodeID))
	sum := hex.EncodeToString(mac.Sum(nil))
	token := types.MCPEndpointTokenPrefix + sum[:40]

	name := "sop:" + runID + ":" + nodeID
	toolList := cliEndpointTools()
	ep, err := p.endpoints.GetByName(ctx, tenantID, name)
	if err != nil {
		return "", "", fmt.Errorf("读取已有 MCP 端点失败：%w", err)
	}
	wanted := normalizeKBIDs(kbIDs)
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(token)))
	if ep == nil {
		created := &types.MCPEndpoint{
			TenantID: tenantID, Name: name, Enabled: true,
			TokenHash: hash, TokenHint: token[:8],
			KnowledgeBaseIDs: types.StringArray(wanted), Tools: types.StringArray(toolList),
			RateLimitPerMinute: 120,
		}
		if err := p.endpoints.Create(ctx, created); err != nil {
			return "", "", fmt.Errorf("创建 MCP 端点失败：%w", err)
		}
		return p.endpointURL(), token, nil
	}
	// 已存在：范围或工具集变了就更新（配置改范围 → 下一次执行即生效）。
	changed := !sameStringSet(ep.KnowledgeBaseIDs, wanted) || !sameStringSet(ep.Tools, toolList)
	if changed {
		ep.KnowledgeBaseIDs = types.StringArray(wanted)
		ep.Tools = types.StringArray(toolList)
		if err := p.endpoints.Update(ctx, ep); err != nil {
			return "", "", fmt.Errorf("更新 MCP 端点失败：%w", err)
		}
	}
	return p.endpointURL(), token, nil
}

// runEndpointPrefix 是本运行全部端点行的名称前缀（包含结尾冒号，避免
// run-1 误伤 run-10）。
func runEndpointPrefix(runID string) string { return "sop:" + runID + ":" }

// ReleaseRun 清理某次运行签发的全部端点：运行收口（成功/失败/取消）后调用。
//
// 端点行只增不减会让设置页越积越多，且过期 token 永远可用——那是可被外部
// 持有者重放的凭据。只删本运行前缀的行，用户自建端点与其它运行不受影响；
// 失败只返回错误由调用方记日志（清理失败不该影响运行结论）。
func (p *MCPProvisioner) ReleaseRun(ctx context.Context, tenantID uint64, runID string) error {
	if p == nil || p.endpoints == nil || runID == "" {
		return nil
	}
	list, err := p.endpoints.ListByTenant(ctx, tenantID)
	if err != nil {
		return fmt.Errorf("列出 MCP 端点失败：%w", err)
	}
	prefix := runEndpointPrefix(runID)
	for _, ep := range list {
		if ep == nil || !strings.HasPrefix(ep.Name, prefix) {
			continue
		}
		if err := p.endpoints.Delete(ctx, tenantID, ep.ID); err != nil {
			return fmt.Errorf("删除 MCP 端点 %s 失败：%w", ep.Name, err)
		}
	}
	return nil
}

// endpointURL 返回 MCP 服务地址（空配置时给本地默认）。
func (p *MCPProvisioner) endpointURL() string {
	if p.baseURL != "" {
		return p.baseURL
	}
	return "http://127.0.0.1:8080/mcp"
}

// normalizeKBIDs 去空去重并排序（端点行的白名单要稳定，便于对账）。
func normalizeKBIDs(ids []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// sameStringSet 判断两个字符串集合是否一致（顺序无关）。
func sameStringSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	set := make(map[string]int, len(a))
	for _, v := range a {
		set[v]++
	}
	for _, v := range b {
		set[v]--
		if set[v] < 0 {
			return false
		}
	}
	return true
}
