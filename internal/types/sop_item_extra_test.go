package types

import "testing"

// 澄清回答只允许 open → answered 单向：终态不可再答。
func TestCanAnswerSopClarification(t *testing.T) {
	if !CanAnswerSopClarification(SopClarificationStatusOpen) {
		t.Fatal("open should be answerable")
	}
	if CanAnswerSopClarification(SopClarificationStatusAnswered) {
		t.Fatal("answered must not be answerable again")
	}
}
