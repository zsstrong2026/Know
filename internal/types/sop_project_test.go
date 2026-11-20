package types

import "testing"

func TestValidateSopProjectCode(t *testing.T) {
	for _, ok := range []string{"KNOW", "A1", "ab", "SOP2026"} {
		if err := ValidateSopProjectCode(ok); err != nil {
			t.Fatalf("code %q should be valid: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "A", "1A", "ABCDEFGHIJK", "KN OW", "知识", "KN-OW"} {
		if err := ValidateSopProjectCode(bad); err == nil {
			t.Fatalf("code %q should be invalid", bad)
		}
	}
}

func TestValidateSopRepoURL(t *testing.T) {
	for _, ok := range []string{"https://github.com/a/b.git", "git@github.com:a/b.git", "ssh://git@host/a/b"} {
		if err := ValidateSopRepoURL(ok); err != nil {
			t.Fatalf("url %q should be valid: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "http://github.com/a/b", "ftp://x/y", "/local/path", "https://"} {
		if err := ValidateSopRepoURL(bad); err == nil {
			t.Fatalf("url %q should be invalid", bad)
		}
	}
}

func TestNormalizeSopRepos(t *testing.T) {
	in := []SopProjectRepo{
		{URL: "https://github.com/a/know.git", Branch: ""},
		{URL: "https://github.com/a/know.git", Name: "dup"},
		{URL: "git@github.com:a/web.git", Name: "站点", Branch: "dev", Default: true},
	}
	out := NormalizeSopRepos(in)
	if len(out) != 2 {
		t.Fatalf("dedup failed: %d", len(out))
	}
	if out[0].Name != "know" || out[0].Branch != "main" || !out[0].Default {
		t.Fatalf("first repo auto-name/branch/default wrong: %+v", out[0])
	}
	if out[1].Default {
		t.Fatalf("only first repo may be default: %+v", out[1])
	}
}
