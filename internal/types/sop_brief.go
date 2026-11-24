package types

// DeriveParentExecState 父事项（大项）执行情况：由子项五态汇总，不落库。
//
// 口径与事项五态一致（spec §3.10）：任一子项 intervene→intervene；否则任一
// running→running；全部 closed→closed；否则任一 pr→pr；否则 none。
// 空容器（还没有子项）= none：大项可以为空，不能因为「没有子项」就当作已闭环。
func DeriveParentExecState(states []string) string {
	if len(states) == 0 {
		return SopExecStateNone
	}
	seen := make(map[string]bool, len(states))
	for _, s := range states {
		seen[s] = true
	}
	switch {
	case seen[SopExecStateIntervene]:
		return SopExecStateIntervene
	case seen[SopExecStateRunning]:
		return SopExecStateRunning
	case seen[SopExecStatePR]:
		return SopExecStatePR
	case seen[SopExecStateClosed] && len(seen) == 1:
		return SopExecStateClosed
	}
	return SopExecStateNone
}
