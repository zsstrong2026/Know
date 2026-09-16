package chat

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	ollamautil "github.com/Tencent/WeKnora/internal/models/utils/ollama"
	"github.com/Tencent/WeKnora/internal/types"
	secutils "github.com/Tencent/WeKnora/internal/utils"
)

func newHungOllamaChat(t *testing.T) *OllamaChat {
	t.Helper()
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"models":[{"name":"test-model:latest"}]}`)
		case "/api/chat":
			select {
			case <-r.Context().Done():
			case <-release:
			}
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	t.Cleanup(server.Close)
	t.Cleanup(func() { close(release) })

	t.Setenv("OLLAMA_BASE_URL", server.URL)
	t.Setenv("OLLAMA_OPTIONAL", "")
	svc, err := ollamautil.GetOllamaService()
	if err != nil {
		t.Fatalf("GetOllamaService: %v", err)
	}
	return &OllamaChat{modelName: "test-model", ollamaService: svc}
}

func TestOllamaChatChatAppliesDeadlineWhenParentHasNone(t *testing.T) {
	old := defaultChatTimeout
	defaultChatTimeout = time.Second
	defer func() { defaultChatTimeout = old }()

	c := newHungOllamaChat(t)

	start := time.Now()
	_, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected fallback timeout error from hung ollama, got nil")
	}
	if elapsed > 5*time.Second {
		t.Fatalf("chat ignored fallback timeout, blocked for %v", elapsed)
	}
}

func TestOllamaChatChatStreamAppliesDeadlineWhenParentHasNone(t *testing.T) {
	old := defaultStreamTimeout
	defaultStreamTimeout = time.Second
	defer func() { defaultStreamTimeout = old }()

	c := newHungOllamaChat(t)

	start := time.Now()
	stream, err := c.ChatStream(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil)
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	sawError := false
	for resp := range stream {
		if resp.ResponseType == types.ResponseTypeError {
			sawError = true
		}
	}
	elapsed := time.Since(start)

	if !sawError {
		t.Fatal("expected error response from hung ollama stream, got none")
	}
	if elapsed > 5*time.Second {
		t.Fatalf("stream ignored fallback timeout, blocked for %v", elapsed)
	}
}

func TestResolveImageForOllamaRejectsInternalURL(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "")
	secutils.ResetSSRFWhitelistForTest()
	t.Cleanup(secutils.ResetSSRFWhitelistForTest)

	if data := resolveImageForOllama("http://169.254.169.254/latest/meta-data/"); data != nil {
		t.Fatalf("resolveImageForOllama returned data for blocked internal URL")
	}
}

func TestResolveImageForOllamaBlocksRedirectToInternalURL(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	secutils.ResetSSRFWhitelistForTest()
	t.Cleanup(secutils.ResetSSRFWhitelistForTest)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://169.254.169.254/latest/meta-data/", http.StatusFound)
	}))
	defer server.Close()

	if data := resolveImageForOllama(server.URL); data != nil {
		t.Fatalf("resolveImageForOllama returned data after redirect to blocked internal URL")
	}
}
