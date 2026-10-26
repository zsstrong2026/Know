package skills

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPMEmbeddedSkills 断言 5 个 PM 技能随 CLI 二进制内嵌，
// 否则 `weknora skills install` 无法写出它们。
func TestPMEmbeddedSkills(t *testing.T) {
	list, err := List()
	require.NoError(t, err)

	names := make(map[string]bool, len(list))
	for _, s := range list {
		names[s.Name] = true
	}

	for _, want := range []string{
		"pm-roadmap-planner", "pm-requirement-analysis",
		"pm-prd-generator", "pm-prototype", "pm-material",
	} {
		assert.True(t, names[want], "missing skill %s", want)
	}
}
