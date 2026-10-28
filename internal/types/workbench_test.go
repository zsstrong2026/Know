package types

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

// wbStrPtr 避免与包内既有测试 helper 重名。
func wbStrPtr(s string) *string { return &s }

// TestWorkbenchConstants 锁定协议值，防止后续误改导致前后端不一致。
func TestWorkbenchConstants(t *testing.T) {
	cases := []struct {
		name string
		got  string
		want string
	}{
		{"KindCanvas", WorkbenchKindCanvas, "canvas"},
		{"KindDatabase", WorkbenchKindDatabase, "database"},
		{"SourceDoc", WorkbenchSourceDoc, "doc"},
		{"SourceItem", WorkbenchSourceItem, "item"},
		{"ArchivePending", WorkbenchArchivePending, "pending"},
		{"ArchiveActive", WorkbenchArchiveActive, "active"},
		{"ArchiveDeleted", WorkbenchArchiveDeleted, "deleted"},
		{"NoticeRemoved", WorkbenchNoticeRemoved, "membership_removed"},
		{"NoticeRestored", WorkbenchNoticeRestored, "membership_restored"},
		{"NoticeCleanup", WorkbenchNoticeCleanup, "cleanup_warning"},
		{"NoticeArchive", WorkbenchNoticeArchive, "archive_failed"},
		{"ModePage", WorkbenchModePage, "page"},
		{"ModeEdgeless", WorkbenchModeEdgeless, "edgeless"},
		{"VisibilityPrivate", WorkbenchVisibilityPrivate, "private"},
		{"VisibilityWorkspace", WorkbenchVisibilityWorkspace, "workspace"},
		{"TargetItem", WorkbenchTargetItem, "item"},
		{"TargetDoc", WorkbenchTargetDoc, "doc"},
		{"TargetKnowledge", WorkbenchTargetKnowledge, "knowledge"},
		{"OrphanLeft", WorkbenchOrphanReasonLeft, "left"},
		{"OrphanRemoved", WorkbenchOrphanReasonRemoved, "removed"},
		{"OrphanAccountDeleted", WorkbenchOrphanReasonAccountDeleted, "account_deleted"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Fatalf("got %q, want %q", tc.got, tc.want)
			}
		})
	}

	intCases := []struct {
		name string
		got  int
		want int
	}{
		{"SupportedEditorSchemaVersion", SupportedEditorSchemaVersion, 1},
		{"SnapshotVersionInitial", WorkbenchSnapshotVersionInitial, 0},
		{"ItemsDefaultLimit", WorkbenchItemsDefaultLimit, 30},
		{"ItemsMaxLimit", WorkbenchItemsMaxLimit, 100},
		{"VersionsDefaultLimit", WorkbenchVersionsDefaultLimit, 50},
		{"NoticesDefaultLimit", WorkbenchNoticesDefaultLimit, 20},
		{"SearchMaxLimit", WorkbenchSearchMaxLimit, 50},
		{"JournalMonthMaxEntries", WorkbenchJournalMonthMaxEntries, 500},
		{"MaxYDocBytes", WorkbenchMaxYDocBytes, 8 * 1024 * 1024},
		{"MaxTextBytes", WorkbenchMaxTextBytes, 256 * 1024},
		{"MaxArchiveBytes", WorkbenchMaxArchiveBytes, 6 * 1024 * 1024},
		{"ItemTitleMaxRunes", WorkbenchItemTitleMaxRunes, 200},
	}
	for _, tc := range intCases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Fatalf("got %d, want %d", tc.got, tc.want)
			}
		})
	}

	if got := DefaultModeForKind(WorkbenchKindCanvas); got != WorkbenchModeEdgeless {
		t.Fatalf("DefaultModeForKind(canvas) = %q, want edgeless", got)
	}
	if got := DefaultModeForKind(WorkbenchKindDatabase); got != WorkbenchModeEdgeless {
		t.Fatalf("DefaultModeForKind(database) = %q, want edgeless", got)
	}
}

// TestWorkbenchEnsureDefaults 覆盖默认值兜底与幂等性。
func TestWorkbenchEnsureDefaults(t *testing.T) {
	cases := []struct {
		name        string
		item        *WorkbenchItem
		wantSnap    int
		wantSchema  int
		wantVisible string
	}{
		{
			name:        "零值条目补齐默认值",
			item:        &WorkbenchItem{},
			wantSnap:    0,
			wantSchema:  1,
			wantVisible: "private",
		},
		{
			name: "显式值保持不变",
			item: &WorkbenchItem{
				SnapshotVersion:     7,
				EditorSchemaVersion: 1,
				Visibility:          WorkbenchVisibilityWorkspace,
			},
			wantSnap:    7,
			wantSchema:  1,
			wantVisible: "workspace",
		},
		{
			name:        "负快照版本归零",
			item:        &WorkbenchItem{SnapshotVersion: -3},
			wantSnap:    0,
			wantSchema:  1,
			wantVisible: "private",
		},
		{
			name:        "非法 schema 回落为受支持版本",
			item:        &WorkbenchItem{EditorSchemaVersion: -1},
			wantSnap:    0,
			wantSchema:  SupportedEditorSchemaVersion,
			wantVisible: "private",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.item.EnsureDefaults()
			if tc.item.SnapshotVersion != tc.wantSnap {
				t.Errorf("SnapshotVersion = %d, want %d", tc.item.SnapshotVersion, tc.wantSnap)
			}
			if tc.item.EditorSchemaVersion != tc.wantSchema {
				t.Errorf("EditorSchemaVersion = %d, want %d", tc.item.EditorSchemaVersion, tc.wantSchema)
			}
			if tc.item.Visibility != tc.wantVisible {
				t.Errorf("Visibility = %q, want %q", tc.item.Visibility, tc.wantVisible)
			}
			// kind 不做兜底，由 Validate 校验。
			if tc.item.Kind != "" {
				t.Errorf("EnsureDefaults 不应改动 Kind，got %q", tc.item.Kind)
			}
		})
	}

	var nilItem *WorkbenchItem
	nilItem.EnsureDefaults() // 不应 panic
}

// newValidItem 返回一条可通过 Validate 的画布条目。
func newValidItem() *WorkbenchItem {
	return &WorkbenchItem{
		Kind:                WorkbenchKindCanvas,
		Title:               "未命名画布",
		SnapshotVersion:     0,
		EditorSchemaVersion: SupportedEditorSchemaVersion,
		Visibility:          WorkbenchVisibilityPrivate,
	}
}

// TestWorkbenchValidate 覆盖条目不变量边界。
func TestWorkbenchValidate(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*WorkbenchItem)
		wantErr error
	}{
		{name: "合法画布", mutate: func(it *WorkbenchItem) {}},
		{name: "合法数据库", mutate: func(it *WorkbenchItem) { it.Kind = WorkbenchKindDatabase }},
		{name: "kind 为空", mutate: func(it *WorkbenchItem) { it.Kind = "" }, wantErr: ErrWorkbenchInvalidKind},
		{name: "kind 非法", mutate: func(it *WorkbenchItem) { it.Kind = "doc" }, wantErr: ErrWorkbenchInvalidKind},
		{name: "标题为空", mutate: func(it *WorkbenchItem) { it.Title = "" }, wantErr: ErrWorkbenchInvalidTitle},
		{name: "标题仅空白", mutate: func(it *WorkbenchItem) { it.Title = "   " }, wantErr: ErrWorkbenchInvalidTitle},
		{
			name:   "标题 200 rune 合法",
			mutate: func(it *WorkbenchItem) { it.Title = strings.Repeat("题", 200) },
		},
		{
			name:    "标题 201 rune 超限",
			mutate:  func(it *WorkbenchItem) { it.Title = strings.Repeat("题", 201) },
			wantErr: ErrWorkbenchInvalidTitle,
		},
		{name: "快照版本为负", mutate: func(it *WorkbenchItem) { it.SnapshotVersion = -1 }, wantErr: ErrWorkbenchInvalidSnapshotVersion},
		{name: "schema 为 0", mutate: func(it *WorkbenchItem) { it.EditorSchemaVersion = 0 }, wantErr: ErrWorkbenchInvalidEditorSchemaVersion},
		{name: "schema 超前", mutate: func(it *WorkbenchItem) { it.EditorSchemaVersion = SupportedEditorSchemaVersion + 1 }, wantErr: ErrWorkbenchInvalidEditorSchemaVersion},
		{name: "可见性非法", mutate: func(it *WorkbenchItem) { it.Visibility = "public" }, wantErr: ErrWorkbenchInvalidVisibility},
		{name: "形态非法", mutate: func(it *WorkbenchItem) { it.CurrentMode = wbStrPtr("grid") }, wantErr: ErrWorkbenchInvalidMode},
		{name: "形态为 page", mutate: func(it *WorkbenchItem) { it.CurrentMode = wbStrPtr(WorkbenchModePage) }},
		{
			name: "画布带合法日历日",
			mutate: func(it *WorkbenchItem) {
				it.JournalDate = NewDateOnly("2026-10-21")
			},
		},
		{
			name: "数据库条目不允许日历日",
			mutate: func(it *WorkbenchItem) {
				it.Kind = WorkbenchKindDatabase
				it.JournalDate = NewDateOnly("2026-10-21")
			},
			wantErr: ErrWorkbenchInvalidJournalDate,
		},
		{
			name: "非法日历日",
			mutate: func(it *WorkbenchItem) {
				it.JournalDate = NewDateOnly("2026-02-30")
			},
			wantErr: ErrInvalidDateOnly,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			it := newValidItem()
			tc.mutate(it)
			err := it.Validate()
			if tc.wantErr == nil {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("error = %v, want errors.Is(%v)", err, tc.wantErr)
			}
		})
	}

	if err := (*WorkbenchItem)(nil).Validate(); err == nil {
		t.Fatal("nil item should be invalid")
	}
}

// TestWorkbenchSaveContentRequestValidate 覆盖保存请求的服务端校验。
func TestWorkbenchSaveContentRequestValidate(t *testing.T) {
	cases := []struct {
		name    string
		req     *SaveWorkbenchContentRequest
		wantErr error
	}{
		{
			name: "合法请求",
			req:  &SaveWorkbenchContentRequest{BaseVersion: 0, EditorSchemaVersion: 1, ClientID: "c1"},
		},
		{
			name:    "base_version 为负",
			req:     &SaveWorkbenchContentRequest{BaseVersion: -1, EditorSchemaVersion: 1, ClientID: "c1"},
			wantErr: ErrWorkbenchInvalidBaseVersion,
		},
		{
			name:    "client_id 为空",
			req:     &SaveWorkbenchContentRequest{BaseVersion: 0, EditorSchemaVersion: 1, ClientID: "  "},
			wantErr: ErrWorkbenchInvalidClientID,
		},
		{
			name:    "schema 为 0",
			req:     &SaveWorkbenchContentRequest{BaseVersion: 0, EditorSchemaVersion: 0, ClientID: "c1"},
			wantErr: ErrWorkbenchInvalidEditorSchemaVersion,
		},
		{
			name:    "schema 超前",
			req:     &SaveWorkbenchContentRequest{BaseVersion: 0, EditorSchemaVersion: 2, ClientID: "c1"},
			wantErr: ErrWorkbenchInvalidEditorSchemaVersion,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.req.Validate()
			if tc.wantErr == nil {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("error = %v, want errors.Is(%v)", err, tc.wantErr)
			}
		})
	}
}

// TestWorkbenchJSONOmitsYDoc 确保 Y.Doc 与文本投影绝不外泄。
func TestWorkbenchJSONOmitsYDoc(t *testing.T) {
	item := newValidItem()
	item.ID = "item-1"
	item.YDoc = []byte("binary-ydoc-payload")
	item.TextContent = "plain-text-projection"

	raw, err := json.Marshal(item)
	if err != nil {
		t.Fatalf("marshal item: %v", err)
	}
	out := string(raw)
	for _, banned := range []string{"ydoc", "text_content", "binary-ydoc-payload", "plain-text-projection"} {
		if strings.Contains(out, banned) {
			t.Fatalf("serialized item leaked %q: %s", banned, out)
		}
	}
	if !strings.Contains(out, `"snapshot_version":0`) {
		t.Fatalf("expected snapshot_version in JSON: %s", out)
	}

	version := &WorkbenchItemVersion{Version: 3, YDoc: []byte("snap"), TextContent: "secret"}
	vraw, err := json.Marshal(version)
	if err != nil {
		t.Fatalf("marshal version: %v", err)
	}
	if vout := string(vraw); strings.Contains(vout, "ydoc") || strings.Contains(vout, "secret") {
		t.Fatalf("serialized version leaked content: %s", vout)
	}

	asset := &WorkbenchItemAsset{ID: "a1", StoragePath: "/var/secret", ContentHash: "hash"}
	araw, err := json.Marshal(asset)
	if err != nil {
		t.Fatalf("marshal asset: %v", err)
	}
	if aout := string(araw); strings.Contains(aout, "storage_path") || strings.Contains(aout, "/var/secret") {
		t.Fatalf("serialized asset leaked storage path: %s", aout)
	}
}

// TestWorkbenchToSummary 验证投影只含元数据。
func TestWorkbenchToSummary(t *testing.T) {
	item := newValidItem()
	item.ID = "item-1"
	item.JournalDate = NewDateOnly("2026-10-21")
	item.CurrentMode = wbStrPtr(WorkbenchModePage)
	item.YDoc = []byte("binary-ydoc-payload")
	item.TextContent = "plain-text-projection"

	summary := item.ToSummary()
	if summary == nil {
		t.Fatal("ToSummary returned nil")
	}
	if summary.ID != item.ID || summary.JournalDate.Date != "2026-10-21" {
		t.Fatalf("unexpected summary: %+v", summary)
	}
	if summary.CurrentMode == nil || *summary.CurrentMode != WorkbenchModePage {
		t.Fatalf("CurrentMode not copied: %+v", summary.CurrentMode)
	}
	// 修改投影不应影响源条目的指针字段。
	*summary.CurrentMode = WorkbenchModeEdgeless
	if *item.CurrentMode != WorkbenchModePage {
		t.Fatal("ToSummary aliased CurrentMode pointer")
	}

	raw, err := json.Marshal(summary)
	if err != nil {
		t.Fatalf("marshal summary: %v", err)
	}
	if out := string(raw); strings.Contains(out, "ydoc") || strings.Contains(out, "plain-text") {
		t.Fatalf("summary leaked content: %s", out)
	}

	if (*WorkbenchItem)(nil).ToSummary() != nil {
		t.Fatal("nil item summary should be nil")
	}
}

// TestWorkbenchDateOnly 覆盖 DateOnly 的解析、Scan 与 JSON 形状。
func TestWorkbenchDateOnly(t *testing.T) {
	t.Run("ParseDateOnly", func(t *testing.T) {
		valid := []string{"2026-02-28", "2024-02-29", "2026-10-21"}
		for _, s := range valid {
			d, err := ParseDateOnly(s)
			if err != nil {
				t.Fatalf("ParseDateOnly(%q) unexpected error: %v", s, err)
			}
			if !d.Valid || d.Date != s || d.String() != s || d.IsZero() {
				t.Fatalf("ParseDateOnly(%q) = %+v", s, d)
			}
		}
		invalid := []string{"2026-02-30", "2026-13-01", "2026-2-1", "", "2026/10/21"}
		for _, s := range invalid {
			if _, err := ParseDateOnly(s); !errors.Is(err, ErrInvalidDateOnly) {
				t.Fatalf("ParseDateOnly(%q) error = %v, want ErrInvalidDateOnly", s, err)
			}
		}
		if got := (DateOnly{}).String(); got != "" {
			t.Fatalf("zero DateOnly String() = %q, want empty", got)
		}
	})

	t.Run("ParseMonth", func(t *testing.T) {
		cases := []struct {
			month     string
			wantStart string
			wantEnd   string
			wantErr   bool
		}{
			{"2026-02", "2026-02-01", "2026-02-28", false},
			{"2024-02", "2024-02-01", "2024-02-29", false},
			{"2026-12", "2026-12-01", "2026-12-31", false},
			{"2026-13", "", "", true},
			{"2026-1", "", "", true},
			{"", "", "", true},
		}
		for _, tc := range cases {
			start, end, err := ParseMonth(tc.month)
			if tc.wantErr {
				if !errors.Is(err, ErrWorkbenchInvalidCalendarField) {
					t.Fatalf("ParseMonth(%q) error = %v, want ErrWorkbenchInvalidCalendarField", tc.month, err)
				}
				continue
			}
			if err != nil {
				t.Fatalf("ParseMonth(%q) unexpected error: %v", tc.month, err)
			}
			if start != tc.wantStart || end != tc.wantEnd {
				t.Fatalf("ParseMonth(%q) = (%q, %q), want (%q, %q)", tc.month, start, end, tc.wantStart, tc.wantEnd)
			}
		}
	})

	t.Run("Scan", func(t *testing.T) {
		cases := []struct {
			name     string
			input    interface{}
			wantDate string
			wantNil  bool
			wantErr  bool
		}{
			{"nil", nil, "", true, false},
			{"string", "2026-10-21", "2026-10-21", false, false},
			{"bytes", []byte("2026-10-21"), "2026-10-21", false, false},
			{"time", time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC), "2026-10-21", false, false},
			{"unsupported", 42, "", false, true},
			{"bad string", "2026-02-30", "", false, true},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				var d DateOnly
				err := d.Scan(tc.input)
				if tc.wantErr {
					if err == nil {
						t.Fatalf("Scan(%v) expected error", tc.input)
					}
					return
				}
				if err != nil {
					t.Fatalf("Scan(%v) unexpected error: %v", tc.input, err)
				}
				if d.IsZero() != tc.wantNil || d.Date != tc.wantDate {
					t.Fatalf("Scan(%v) = %+v", tc.input, d)
				}
			})
		}
	})

	t.Run("Value", func(t *testing.T) {
		v, err := NewDateOnly("2026-10-21").Value()
		if err != nil || v != "2026-10-21" {
			t.Fatalf("Value() = (%v, %v)", v, err)
		}
		v, err = (DateOnly{}).Value()
		if err != nil || v != nil {
			t.Fatalf("zero Value() = (%v, %v)", v, err)
		}
	})

	t.Run("JSON", func(t *testing.T) {
		raw, err := json.Marshal(NewDateOnly("2026-10-21"))
		if err != nil || string(raw) != `"2026-10-21"` {
			t.Fatalf("marshal = (%s, %v)", raw, err)
		}
		raw, err = json.Marshal(DateOnly{})
		if err != nil || string(raw) != "null" {
			t.Fatalf("marshal zero = (%s, %v)", raw, err)
		}

		var d DateOnly
		if err := json.Unmarshal([]byte(`"2026-10-21"`), &d); err != nil || d.Date != "2026-10-21" {
			t.Fatalf("unmarshal string = (%+v, %v)", d, err)
		}
		if err := json.Unmarshal([]byte("null"), &d); err != nil || !d.IsZero() {
			t.Fatalf("unmarshal null = (%+v, %v)", d, err)
		}
		if err := json.Unmarshal([]byte(`"2026-02-30"`), &d); !errors.Is(err, ErrInvalidDateOnly) {
			t.Fatalf("unmarshal invalid error = %v", err)
		}
	})
}

// TestWorkbenchVersionConflictError 校验错误链与数据字段。
func TestWorkbenchVersionConflictError(t *testing.T) {
	at := time.Date(2026, 10, 27, 8, 0, 0, 0, time.UTC)
	err := NewVersionConflictError(7, at)
	if !errors.Is(err, ErrWorkbenchVersionConflict) {
		t.Fatalf("error is not ErrWorkbenchVersionConflict: %v", err)
	}
	var conflict *WorkbenchVersionConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("errors.As failed: %v", err)
	}
	if conflict.CurrentVersion != 7 || !conflict.UpdatedAt.Equal(at) {
		t.Fatalf("unexpected conflict payload: %+v", conflict)
	}
}
