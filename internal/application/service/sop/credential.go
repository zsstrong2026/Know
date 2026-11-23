// credential.go 是 CLI 凭据的业务层：写入（加密由模型负责）、脱敏列表、删除，
// 以及「本机装了哪些 CLI」的探测与安装指引。
//
// 两条口径：
//   - 明文永不回显：列表只给尾 4 位；解密后的明文只在执行器注入 env 时使用；
//   - 探测是只读且带超时：`<binary> --version` 卡住不能挂住整个设置页（3s 上限），
//     未安装时给一条可照着做的安装指引，而不是只返回「未安装」。
package sop

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/application/service/sop/executor/cli"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// CLIStatus 一个本地 CLI 的安装状态（设置页「本地 CLI」区块的数据源）。
type CLIStatus struct {
	Kind      string `json:"kind"`
	Binary    string `json:"binary"`
	Installed bool   `json:"installed"`
	Version   string `json:"version,omitempty"`
	Guidance  string `json:"guidance,omitempty"`
}

// cliAdapters 是全部 10 个适配器（顺序即设置页展示顺序）。
func cliAdapters() []cli.Adapter {
	return []cli.Adapter{
		cli.ClaudeAdapter{}, cli.OpencodeAdapter{}, cli.CursorAdapter{}, cli.AgyAdapter{},
		cli.KimiAdapter{}, cli.PiAdapter{}, cli.DshAdapter{}, cli.ZcodeAdapter{},
		cli.CodebuddyAdapter{}, cli.CodexAdapter{},
	}
}

// cliGuidance 是各 CLI 的安装指引（白话，给出包名与官方方式）。
var cliGuidance = map[string]string{
	"claude":       "安装 Claude Code：npm install -g @anthropic-ai/claude-code，安装后运行 claude 完成登录",
	"opencode":     "安装 opencode：npm install -g opencode-ai，详见 opencode 官方文档",
	"cursor-agent": "安装 Cursor CLI：见 Cursor 官网 Agent 文档，安装后运行 cursor-agent login",
	"agy":          "安装 Antigravity CLI：见官方文档，安装后运行 agy 完成登录",
	"kimi":         "安装 Kimi Code CLI：见 Moonshot 官方文档，安装后运行 kimi 完成登录",
	"pi":           "安装 pi-coding-agent：npm install -g @mariozechner/pi-coding-agent",
	"dsh":          "安装 DeepSeek CLI：见 DeepSeek 官方文档，安装后配置 API Key",
	"zcode":        "安装 zcode CLI：见官方文档，安装后在设置里填写 API Key",
	"codebuddy":    "安装 CodeBuddy CLI：见官方文档，安装后在设置里填写 API Key",
	"codex":        "安装 OpenAI Codex CLI：npm install -g @openai/codex，安装后运行 codex login",
}

// CredentialService CLI 凭据与安装状态。
type CredentialService struct {
	repo interfaces.SopCredentialRepository
}

// NewCredentialService 构造凭据服务；repo 可 nil（只做 PATH 探测时）。
func NewCredentialService(repo interfaces.SopCredentialRepository) *CredentialService {
	return &CredentialService{repo: repo}
}

// CredentialView 脱敏后的凭据（列表与写入响应共用）。
type CredentialView struct {
	CLIKind      string `json:"cli_kind"`
	Name         string `json:"name"`
	BaseURL      string `json:"base_url,omitempty"`
	DefaultModel string `json:"default_model,omitempty"`
	// APIKeyTail 只保留末 4 位，用于「是不是同一把密钥」的肉眼确认。
	APIKeyTail string `json:"api_key_tail"`
	UpdatedAt  string `json:"updated_at,omitempty"`
}

// Upsert 写入/更新凭据（apiKey 为空表示沿用旧密钥）。
func (s *CredentialService) Upsert(
	ctx context.Context, tenantID uint64, userID, kind, name, apiKey, baseURL, model string,
) (*CredentialView, error) {
	if s.repo == nil {
		return nil, errors.New("凭据服务未就绪：仓储未注入")
	}
	kind = strings.TrimSpace(kind)
	if kind == "" {
		return nil, errors.New("请选择本地 CLI 类型")
	}
	c := &types.SopCLICredential{
		TenantID: tenantID, OwnerUserID: userID, CLIKind: kind,
		APIKey: strings.TrimSpace(apiKey), BaseURL: strings.TrimSpace(baseURL),
		DefaultModel: strings.TrimSpace(model), Extra: map[string]any{},
	}
	if n := strings.TrimSpace(name); n != "" {
		c.Extra["name"] = n
	}
	if err := s.repo.Upsert(ctx, c); err != nil {
		return nil, err
	}
	// 回读再出视图：api_key 留空时库里仍是旧密钥，用入参拼尾号会误导。
	if saved, err := s.repo.Get(ctx, tenantID, userID, kind); err == nil && saved != nil {
		c = saved
	}
	return credentialView(c), nil
}

// ListMasked 列当前用户的凭据（脱敏）。
func (s *CredentialService) ListMasked(
	ctx context.Context, tenantID uint64, userID string,
) ([]CredentialView, error) {
	if s.repo == nil {
		return nil, errors.New("凭据服务未就绪：仓储未注入")
	}
	list, err := s.repo.ListByOwner(ctx, tenantID, userID)
	if err != nil {
		return nil, err
	}
	out := make([]CredentialView, 0, len(list))
	for _, c := range list {
		out = append(out, *credentialView(&c))
	}
	return out, nil
}

// Delete 删除某 CLI 种类的凭据（软删）。
func (s *CredentialService) Delete(ctx context.Context, tenantID uint64, userID, kind string) error {
	if s.repo == nil {
		return errors.New("凭据服务未就绪：仓储未注入")
	}
	cur, err := s.repo.Get(ctx, tenantID, userID, strings.TrimSpace(kind))
	if err != nil {
		return err
	}
	return s.repo.Delete(ctx, tenantID, cur.ID)
}

// credentialView 把凭据转成脱敏视图。
func credentialView(c *types.SopCLICredential) *CredentialView {
	name, _ := c.Extra["name"].(string)
	view := &CredentialView{
		CLIKind: c.CLIKind, Name: name, BaseURL: c.BaseURL,
		DefaultModel: c.DefaultModel, APIKeyTail: tail4(c.APIKey),
	}
	if !c.UpdatedAt.IsZero() {
		view.UpdatedAt = c.UpdatedAt.Format("2006-01-02 15:04:05")
	}
	return view
}

// tail4 取字符串末 4 位（不足 4 位就全给）。
func tail4(s string) string {
	rs := []rune(strings.TrimSpace(s))
	if len(rs) <= 4 {
		return string(rs)
	}
	return string(rs[len(rs)-4:])
}

// cliVersionTimeout 是 `<binary> --version` 的上限：探测不该挂住设置页。
const cliVersionTimeout = 3 * time.Second

// Status 逐个 CLI 探测安装状态。
func (s *CredentialService) Status(ctx context.Context) []CLIStatus {
	out := make([]CLIStatus, 0, len(cliAdapters()))
	for _, a := range cliAdapters() {
		binary := a.BinaryName()
		status := CLIStatus{Kind: a.Kind(), Binary: binary}
		if path, err := exec.LookPath(binary); err == nil && path != "" {
			status.Installed = true
			status.Version = cliVersion(ctx, binary)
		} else {
			status.Guidance = cliGuidance[a.Kind()]
		}
		out = append(out, status)
	}
	return out
}

// cliVersion 调 `<binary> --version` 取首行（失败返回空，不影响「已安装」判定）。
func cliVersion(ctx context.Context, binary string) string {
	runCtx, cancel := context.WithTimeout(ctx, cliVersionTimeout)
	defer cancel()
	cmd := exec.CommandContext(runCtx, binary, "--version")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	line := strings.SplitN(strings.TrimSpace(string(out)), "\n", 2)[0]
	return strings.TrimSpace(line)
}
