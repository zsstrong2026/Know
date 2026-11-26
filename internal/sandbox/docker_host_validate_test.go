package sandbox

import (
	"strings"
	"testing"
)

// Windows 上 Docker Desktop 的端点就是命名管道，DetectLocalDockerHost 会从
// docker context 原样读出来。校验必须接受它，否则「留空 host 自动探测」在
// Windows 上必然失败——填进去的值立刻被自己拒掉。
func TestValidateDockerHostAcceptsLocalNamedPipe(t *testing.T) {
	for _, host := range []string{
		"npipe:////./pipe/dockerDesktopLinuxEngine",
		"npipe:////./pipe/docker_engine",
		`npipe:\\.\pipe\docker_engine`,
	} {
		if err := ValidateDockerHost(host, false); err != nil {
			t.Errorf("host %q should be accepted: %v", host, err)
		}
	}
}

// 命名管道可以是远程的（\\host\pipe\name），那等于把一个远端 daemon 当本地用，
// 必须拒绝：只有 //./pipe/（点=本机）才算本地端点。
func TestValidateDockerHostRejectsRemoteNamedPipe(t *testing.T) {
	for _, host := range []string{
		"npipe:////remotehost/pipe/docker_engine",
		"npipe:////192.168.1.10/pipe/docker_engine",
	} {
		err := ValidateDockerHost(host, false)
		if err == nil {
			t.Errorf("host %q should be rejected as remote", host)
			continue
		}
		if !strings.Contains(err.Error(), "local") {
			t.Errorf("host %q: error should say the pipe must be local, got %v", host, err)
		}
	}
}

// 空的管道名没有可用端点，同样拒绝。
func TestValidateDockerHostRejectsEmptyNamedPipe(t *testing.T) {
	for _, host := range []string{"npipe://", "npipe:////./pipe/"} {
		if err := ValidateDockerHost(host, false); err == nil {
			t.Errorf("host %q should be rejected", host)
		}
	}
}

// 存量 scheme 的行为不能变：unix 要求绝对路径，tcp 走外发策略，未知 scheme 拒绝。
func TestValidateDockerHostKeepsExistingSchemes(t *testing.T) {
	if err := ValidateDockerHost("unix:///var/run/docker.sock", false); err != nil {
		t.Errorf("absolute unix socket should pass: %v", err)
	}
	if err := ValidateDockerHost("unix://relative.sock", false); err == nil {
		t.Error("relative unix socket should be rejected")
	}
	if err := ValidateDockerHost("tcp://10.0.0.5:2376", true); err != nil {
		t.Errorf("tcp with private allowed should pass: %v", err)
	}
	if err := ValidateDockerHost("ssh://host", false); err == nil {
		t.Error("unknown scheme should still be rejected")
	}
	if err := ValidateDockerHost("no-scheme-at-all", false); err == nil {
		t.Error("host without scheme should still be rejected")
	}
}

// 命名管道是本机端点：不需要外发拨号守卫，也不需要 TLS 证书目录。
func TestNamedPipeSkipsDialGuardAndTLS(t *testing.T) {
	host := "npipe:////./pipe/dockerDesktopLinuxEngine"
	if dockerHostNeedsDialGuard(host) {
		t.Error("named pipe is local, must not get the outbound dial guard")
	}
	if err := ValidateDockerRemoteTLS(host, ""); err != nil {
		t.Errorf("named pipe is local, TLS should not be required: %v", err)
	}
}
