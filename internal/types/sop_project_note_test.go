package types

import "testing"

func TestValidateSopNote(t *testing.T) {
	for _, ok := range []string{"", SopNoteKindDecision, SopNoteKindRunSummary, SopNoteKindRisk, SopNoteKindRelease, SopNoteKindNote} {
		if err := ValidateSopNote(ok); err != nil {
			t.Fatalf("kind %q should be valid: %v", ok, err)
		}
	}
	for _, bad := range []string{"bogus", "DECISION", "run-summary"} {
		if err := ValidateSopNote(bad); err == nil {
			t.Fatalf("kind %q should be invalid", bad)
		}
	}
}

func TestValidateSopNoteSource(t *testing.T) {
	for _, ok := range []string{"", SopNoteSourceHuman, SopNoteSourceEngine, SopNoteSourceAI} {
		if err := ValidateSopNoteSource(ok); err != nil {
			t.Fatalf("source %q should be valid: %v", ok, err)
		}
	}
	for _, bad := range []string{"system", "Human", "robot"} {
		if err := ValidateSopNoteSource(bad); err == nil {
			t.Fatalf("source %q should be invalid", bad)
		}
	}
}

func TestDeriveParentExecState(t *testing.T) {
	cases := []struct {
		in   []string
		want string
	}{
		{[]string{"none", "intervene"}, "intervene"},
		{[]string{"intervene", "running"}, "intervene"}, // intervene 优先级最高
		{[]string{"closed", "running"}, "running"},
		{[]string{"closed", "pr"}, "pr"},
		{[]string{"closed", "closed"}, "closed"},
		{[]string{"none", "none"}, "none"},
		{[]string{"closed", "none"}, "none"}, // 未全闭环不算关闭
		{nil, "none"},                        // 空容器视为尚未执行
	}
	for _, c := range cases {
		if got := DeriveParentExecState(c.in); got != c.want {
			t.Fatalf("DeriveParentExecState(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}
