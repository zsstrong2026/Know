package insight

import (
	"archive/zip"
	"bytes"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestImportThemePackageMarkdown(t *testing.T) {
	raw := []byte(`---
name: 决策复盘
description: 检查每个决策节点
sensitive: true
dimensions:
  - name: 决策质量
    criteria: 是否记录备选方案
  - name: 事后偏差
    criteria: 是否出现“早就知道”
---

# 分析方法
先定位决策节点。
`)
	draft, err := (&Service{}).ImportThemePackage("decision.md", raw)
	if err != nil {
		t.Fatalf("import markdown failed: %v", err)
	}
	if draft.Name != "决策复盘" || !draft.Sensitive {
		t.Fatalf("unexpected draft: %+v", draft)
	}
	if len(draft.Dimensions) != 2 || draft.Dimensions[0].Name != "决策质量" {
		t.Fatalf("unexpected dimensions: %+v", draft.Dimensions)
	}
	if draft.Dimensions[0].Scale == "" {
		t.Fatal("scale should default to 0-100")
	}
	if draft.Instructions == "" {
		t.Fatal("instructions should come from the markdown body")
	}
}

func TestImportThemePackageZip(t *testing.T) {
	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)
	entry, err := writer.Create("skill/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := entry.Write([]byte("---\nname: 主要矛盾\n---\n\n正文")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	draft, err := (&Service{}).ImportThemePackage("skill.zip", buf.Bytes())
	if err != nil {
		t.Fatalf("import zip failed: %v", err)
	}
	if draft.Name != "主要矛盾" {
		t.Fatalf("unexpected name: %s", draft.Name)
	}
	// 没有声明维度时给一个可编辑的默认维度，避免创建出无法出报告的主题。
	if len(draft.Dimensions) != 1 {
		t.Fatalf("want 1 fallback dimension, got %d", len(draft.Dimensions))
	}
}

func TestImportThemePackageRejectsZipWithoutSkill(t *testing.T) {
	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)
	entry, _ := writer.Create("readme.txt")
	_, _ = entry.Write([]byte("no skill here"))
	_ = writer.Close()

	if _, err := (&Service{}).ImportThemePackage("empty.zip", buf.Bytes()); err == nil {
		t.Fatal("zip without SKILL.md should be rejected")
	}
}

func TestFilterDocsByFolderAndDays(t *testing.T) {
	now := time.Now()
	docs := []*types.Knowledge{
		{ID: "1", FolderPath: "产品思考", UpdatedAt: now.AddDate(0, 0, -5)},
		{ID: "2", FolderPath: "产品思考/复盘", UpdatedAt: now.AddDate(0, 0, -120)},
		{ID: "3", FolderPath: "个人笔记", UpdatedAt: now.AddDate(0, 0, -1)},
		{ID: "4", FolderPath: "", UpdatedAt: now.AddDate(0, 0, -1)},
	}

	byFolder := filterDocs(docs, types.InsightScopeFilter{FolderPath: "/产品思考/"})
	if len(byFolder) != 2 {
		t.Fatalf("folder filter should include subfolders, got %d", len(byFolder))
	}

	recent := filterDocs(docs, types.InsightScopeFilter{RecentDays: 30})
	if len(recent) != 3 {
		t.Fatalf("recent filter mismatch, got %d", len(recent))
	}

	both := filterDocs(docs, types.InsightScopeFilter{FolderPath: "产品思考", RecentDays: 30})
	if len(both) != 1 || both[0].ID != "1" {
		t.Fatalf("combined filter mismatch: %+v", both)
	}

	folders := collectFolders(docs)
	if len(folders) != 3 || folders[0] != "个人笔记" || folders[2] != "产品思考/复盘" {
		t.Fatalf("unexpected folders: %+v", folders)
	}
}
