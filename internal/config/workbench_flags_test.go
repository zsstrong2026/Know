package config

import "testing"

// TestApplyWorkbenchDefaults 钉住工作台发布门禁的默认值与 env 覆盖语义（§9.1）。
//
// 三个约束不能松动：
//   - 缺省必须是关闭：生产上必须先按里程碑验证再打开；
//   - YAML 写 true 时 env 不设也不能被重置成 false；
//   - env 只认显式的 true/false，非法值必须原样保留 YAML 的值。
func TestApplyWorkbenchDefaults(t *testing.T) {
	cases := []struct {
		name       string
		yaml       *WorkbenchConfig
		env        map[string]string
		wantTpl    bool
		wantAI     bool
		wantEnable bool
	}{
		{
			name: "omitted section defaults to all off",
			yaml: nil,
		},
		{
			name:    "yaml true survives when env unset",
			yaml:    &WorkbenchConfig{Enabled: true, TemplatesEnabled: true, AIEnabled: true},
			wantTpl: true, wantAI: true, wantEnable: true,
		},
		{
			name:    "env true turns templates on",
			yaml:    &WorkbenchConfig{},
			env:     map[string]string{"WEKNORA_WORKBENCH_TEMPLATES_ENABLED": "true"},
			wantTpl: true,
		},
		{
			name:    "env false turns yaml true off",
			yaml:    &WorkbenchConfig{TemplatesEnabled: true, AIEnabled: true},
			env:     map[string]string{"WEKNORA_WORKBENCH_TEMPLATES_ENABLED": "0"},
			wantAI:  true,
			wantTpl: false,
		},
		{
			name:    "illegal env value keeps yaml",
			yaml:    &WorkbenchConfig{TemplatesEnabled: true},
			env:     map[string]string{"WEKNORA_WORKBENCH_TEMPLATES_ENABLED": "maybe"},
			wantTpl: true,
		},
		{
			name: "case insensitive yes/on",
			yaml: &WorkbenchConfig{},
			env: map[string]string{
				"WEKNORA_WORKBENCH_TEMPLATES_ENABLED": "ON",
				"WEKNORA_WORKBENCH_AI_ENABLED":        "Yes",
			},
			wantTpl: true, wantAI: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for key, value := range tc.env {
				t.Setenv(key, value)
			}
			cfg := &Config{Workbench: tc.yaml}
			applyWorkbenchDefaults(cfg)

			if cfg.Workbench == nil {
				t.Fatal("applyWorkbenchDefaults must not leave Workbench nil")
			}
			if got := cfg.Workbench.TemplatesEnabled; got != tc.wantTpl {
				t.Errorf("TemplatesEnabled = %v, want %v", got, tc.wantTpl)
			}
			if got := cfg.Workbench.AIEnabled; got != tc.wantAI {
				t.Errorf("AIEnabled = %v, want %v", got, tc.wantAI)
			}
			if got := cfg.Workbench.Enabled; got != tc.wantEnable {
				t.Errorf("Enabled = %v, want %v", got, tc.wantEnable)
			}
		})
	}
}

// IsTemplateEnabled 对 nil 接收者必须安全返回 false，避免未配置时 panic。
func TestWorkbenchConfigIsTemplateEnabled(t *testing.T) {
	var nilCfg *WorkbenchConfig
	if nilCfg.IsTemplateEnabled() {
		t.Error("nil config must report templates disabled")
	}
	if (&WorkbenchConfig{}).IsTemplateEnabled() {
		t.Error("zero config must report templates disabled")
	}
	if !(&WorkbenchConfig{TemplatesEnabled: true}).IsTemplateEnabled() {
		t.Error("enabled config must report templates enabled")
	}
}
