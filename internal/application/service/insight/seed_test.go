package insight

import (
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

// 存量内置主题仍是旧口径时要升级到最新提示词，否则老库永远不会拿到新规则。
func TestShouldUpgradeInstructions(t *testing.T) {
	seed := findBuiltinSeed(t, "insight-builtin-mbti")

	cases := []struct {
		name  string
		theme *types.InsightTheme
		want  bool
	}{
		{"旧口径需要升级", &types.InsightTheme{ID: seed.ID, Builtin: true, Instructions: seed.LegacyInstructions[0]}, true},
		{"已是新口径", &types.InsightTheme{ID: seed.ID, Builtin: true, Instructions: seed.Instructions}, false},
		{"用户自定义口径", &types.InsightTheme{ID: seed.ID, Builtin: true, Instructions: "我自己的口径"}, false},
		{"非内置主题", &types.InsightTheme{ID: seed.ID, Instructions: seed.LegacyInstructions[0]}, false},
		{"空主题", nil, false},
	}
	for _, tc := range cases {
		if got := shouldUpgradeInstructions(tc.theme, seed); got != tc.want {
			t.Fatalf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

// MBTI 口径必须要求结论给出四字母倾向组合，同时保留“只作参考”的约束。
func TestMBTISeedRequiresTypeInConclusion(t *testing.T) {
	seed := findBuiltinSeed(t, "insight-builtin-mbti")
	for _, want := range []string{"conclusion", "四字母", "倾向类型"} {
		if !strings.Contains(seed.Instructions, want) {
			t.Fatalf("MBTI instructions should mention %q, got %q", want, seed.Instructions)
		}
	}
	if strings.Contains(seed.Instructions, "不要输出四字母类型标签") {
		t.Fatal("MBTI instructions should no longer forbid the type label")
	}
}

func findBuiltinSeed(t *testing.T, id string) builtinThemeSeed {
	t.Helper()
	for _, seed := range builtinThemes {
		if seed.ID == id {
			return seed
		}
	}
	t.Fatalf("builtin seed %q not found", id)
	return builtinThemeSeed{}
}
