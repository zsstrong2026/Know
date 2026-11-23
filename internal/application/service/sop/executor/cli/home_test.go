package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/application/service/sop/engine"
	"github.com/Tencent/WeKnora/internal/types"
)

func newTestWS(t *testing.T) *engine.Workspace {
	t.Helper()
	ws, err := engine.PrepareWorkspace(t.TempDir(), "r1")
	require.NoError(t, err)
	return ws
}

// 符号链接指向私有目录之外时，写入必须被拒（os.Root 的语义）。
func TestPrivateHomeRejectsSymlinkEscape(t *testing.T) {
	ws := newTestWS(t)
	home, err := PrivateHome(ws, "zcode")
	require.NoError(t, err)
	defer home.Close()

	require.NoError(t, home.WriteFile("config.json", []byte("{}")))
	body, err := home.ReadFile("config.json")
	require.NoError(t, err)
	require.JSONEq(t, "{}", string(body))

	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(home.Dir, "escape")); err != nil {
		t.Skipf("当前环境不支持创建符号链接：%v", err)
	}
	err = home.WriteFile("escape/pwned", []byte("x"))
	require.Error(t, err, "os.Root 必须拒绝越界写入")
	_, statErr := os.Stat(filepath.Join(outside, "pwned"))
	require.True(t, os.IsNotExist(statErr), "私密目录之外的落盘必须没有发生")
}

// 路径逃逸（..）与绝对路径在进 os.Root 之前就被拦下。
func TestPrivateHomeRejectsTraversal(t *testing.T) {
	ws := newTestWS(t)
	home, err := PrivateHome(ws, "zcode")
	require.NoError(t, err)
	defer home.Close()

	require.Error(t, home.WriteFile("../evil", []byte("x")))
	require.Error(t, home.WriteFile("/abs/evil", []byte("x")))
	require.Error(t, home.WriteFile("  ", []byte("x")))
}

// kind 来自用户配置：必须收敛成安全目录段（不能带路径分隔符）。
func TestPrivateHomeSanitizesKind(t *testing.T) {
	ws := newTestWS(t)
	home, err := PrivateHome(ws, "../evil/kind")
	require.NoError(t, err)
	defer home.Close()

	require.NotContains(t, filepath.ToSlash(home.Dir), "..")
	require.True(t, strings.HasPrefix(home.Dir, filepath.Join(ws.Root, ".sop", "private-home")),
		"私有目录必须落在工作区内：%s", home.Dir)
}

// 子目录自动创建 + 系统提示写入。
func TestPrivateHomeMkdirAndAgentSystem(t *testing.T) {
	ws := newTestWS(t)
	home, err := PrivateHome(ws, "zcode")
	require.NoError(t, err)
	defer home.Close()

	require.NoError(t, home.WriteFile("nested/deep/file.txt", []byte("deep")))
	body, err := home.ReadFile("nested/deep/file.txt")
	require.NoError(t, err)
	require.Equal(t, "deep", string(body))

	require.NoError(t, home.WriteAgentSystem("你是工程师"))
	body, err = home.ReadFile("agent-system.md")
	require.NoError(t, err)
	require.Equal(t, "你是工程师", string(body))

	// 空系统提示不写文件
	require.NoError(t, home.WriteAgentSystem("  "))
}

// 未初始化的工作区拒绝建私有目录。
func TestPrivateHomeRequiresWorkspace(t *testing.T) {
	_, err := PrivateHome(nil, "zcode")
	require.Error(t, err)
	_, err = PrivateHome(&engine.Workspace{}, "zcode")
	require.Error(t, err)
}

// 凭据只进环境变量；配置文件里只有变量引用。
func TestCredentialEnvOnlyNoPlaintextInConfig(t *testing.T) {
	c := &types.SopCLICredential{
		CLIKind: "zcode", APIKey: "sk-secret",
		BaseURL: "https://api.example.com", DefaultModel: "z-model",
	}
	env := CredentialEnv(c)
	require.Equal(t, "sk-secret", env["SOP_CLI_API_KEY"])
	require.Equal(t, "https://api.example.com", env["SOP_CLI_BASE_URL"])
	require.Equal(t, "z-model", env["SOP_CLI_MODEL"])

	cfg := RenderZcodeConfig(c)
	require.NotContains(t, cfg, "sk-secret", "配置里绝不能出现明文密钥")
	require.Contains(t, cfg, "SOP_CLI_API_KEY", "只留变量引用")
	require.Contains(t, cfg, "z-model")

	// 空凭据：不产生任何变量
	require.Empty(t, CredentialEnv(nil))
	require.Empty(t, CredentialEnv(&types.SopCLICredential{}))
}

// prompt 投递三档：小→argv；大且支持 stdin→stdin；大且不支持→落文件传路径。
func TestPromptDeliveryThreshold(t *testing.T) {
	ws := newTestWS(t)
	small := strings.Repeat("a", 1000)
	res, err := DeliverPrompt(small, false, ws, "n1", 1)
	require.NoError(t, err)
	require.Equal(t, small, res.Argv)
	require.Empty(t, res.Stdin)
	require.Equal(t, PromptDeliveryArgv, res.Mode)

	big := strings.Repeat("字", 10000) // 30000 字节 > 24576
	res, err = DeliverPrompt(big, false, ws, "n1", 1)
	require.NoError(t, err)
	require.Empty(t, res.Stdin)
	require.Equal(t, PromptDeliveryFile, res.Mode)
	require.FileExists(t, res.Argv, "argv 是文件绝对路径")
	body, err := os.ReadFile(res.Argv)
	require.NoError(t, err)
	require.Equal(t, big, string(body))
	require.True(t, strings.HasPrefix(res.FilePath, filepath.Join(ws.Root, ".sop")))

	res, err = DeliverPrompt(big, true, ws, "n1", 1)
	require.NoError(t, err)
	require.Empty(t, res.Argv)
	require.Equal(t, big, res.Stdin)
	require.Equal(t, PromptDeliveryStdin, res.Mode)
}

// 阈值边界：正好 24KiB 走 argv，多 1 字节就换通道。
func TestPromptDeliveryBoundary(t *testing.T) {
	ws := newTestWS(t)
	exact := strings.Repeat("b", PromptArgMaxBytes)
	res, err := DeliverPrompt(exact, true, ws, "n1", 1)
	require.NoError(t, err)
	require.Equal(t, PromptDeliveryArgv, res.Mode)

	over := exact + "c"
	res, err = DeliverPrompt(over, true, ws, "n1", 1)
	require.NoError(t, err)
	require.Equal(t, PromptDeliveryStdin, res.Mode)
}

// 落文件时节点 id 要收敛（文件名里不能出现路径分隔符）。
func TestPromptFileSanitizesNodeID(t *testing.T) {
	ws := newTestWS(t)
	big := strings.Repeat("x", PromptArgMaxBytes+1)
	res, err := DeliverPrompt(big, false, ws, "../evil/节点", 2)
	require.NoError(t, err)
	// 文件名口径：prompt-<节点>-<尝试>.md；节点 id 里的分隔符要被收敛掉。
	base := filepath.Base(res.FilePath)
	require.True(t, strings.HasPrefix(base, "prompt-"), base)
	require.True(t, strings.HasSuffix(base, "-2.md"), base)
	// 字母本身可以保留（evil 不在文件名里构成路径），危险的是分隔符与 ..
	require.NotContains(t, base, "/")
	require.NotContains(t, base, "..")
}

// 没有工作区时大 prompt 不能静默丢：必须报错。
func TestPromptDeliveryRequiresWorkspaceForFile(t *testing.T) {
	big := strings.Repeat("x", PromptArgMaxBytes+1)
	_, err := DeliverPrompt(big, false, nil, "n1", 1)
	require.Error(t, err)
}
