package session

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// 用例统一使用平台原生绝对路径（t.TempDir），不要硬编码 Unix 风格路径：
// Windows 上 filepath.IsAbs("/Users/...") 为 false，会把用例绑死到特定平台。
func TestMatchApprovedDirAcceptsOnlyApprovedAbsolutePaths(t *testing.T) {
	base := t.TempDir()
	other := t.TempDir()
	approved := []string{base, " " + other + " ", ""}

	got, ok := MatchApprovedDir(base, approved)
	require.True(t, ok)
	require.Equal(t, filepath.Clean(base), got)

	got, ok = MatchApprovedDir("  "+base+string(filepath.Separator)+"  ", approved)
	require.True(t, ok, "trailing separator and padding must still match")
	require.Equal(t, filepath.Clean(base), got)

	got, ok = MatchApprovedDir(other, approved)
	require.True(t, ok, "an approved entry is cleaned before comparison")
	require.Equal(t, filepath.Clean(other), got)

	for _, bad := range []string{
		"",
		"relative/dir",
		filepath.Join(base, "nested"),
		filepath.Join(other, "nested"),
	} {
		_, ok := MatchApprovedDir(bad, approved)
		require.False(t, ok, bad)
	}
}

func TestMatchApprovedDirRejectsEverythingWithoutApprovals(t *testing.T) {
	_, ok := MatchApprovedDir(t.TempDir(), nil)
	require.False(t, ok)
}
