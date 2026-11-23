package sop

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/application/service/sop/executor"
)

func TestEventHubPubSubAndUnsubscribe(t *testing.T) {
	h := NewEventHub()
	ch, unsub := h.Subscribe("r1")
	h.Publish("r1", executor.StreamEvent{Type: executor.StreamEventTextDelta, Text: "hi"})
	select {
	case ev := <-ch:
		require.Equal(t, "hi", ev.Text)
	case <-time.After(time.Second):
		t.Fatal("没收到事件")
	}
	unsub()
	// 退订后广播既不阻塞也不 panic
	h.Publish("r1", executor.StreamEvent{Type: executor.StreamEventTextDelta, Text: "after"})
	require.Equal(t, 0, h.SubscriberCount("r1"))

	// 退订后通道已关闭（range 立刻结束）
	_, open := <-ch
	require.False(t, open)
	unsub() // 重复退订安全
}

// 多订阅者都能收到同一条事件；只退订自己。
func TestEventHubFansOutAndIsolates(t *testing.T) {
	h := NewEventHub()
	a, unsubA := h.Subscribe("r1")
	b, _ := h.Subscribe("r1")
	require.Equal(t, 2, h.SubscriberCount("r1"))

	h.Publish("r1", executor.StreamEvent{Type: executor.StreamEventToolCall, ToolName: "search_knowledge"})
	for _, ch := range []<-chan executor.StreamEvent{a, b} {
		select {
		case ev := <-ch:
			require.Equal(t, "search_knowledge", ev.ToolName)
		case <-time.After(time.Second):
			t.Fatal("订阅者没有收到事件")
		}
	}
	unsubA()
	require.Equal(t, 1, h.SubscriberCount("r1"))
}

// 慢订阅者缓冲满时丢事件，不阻塞 Publish（一条慢连接不能拖住整条流程）。
func TestEventHubDropsWhenBufferFull(t *testing.T) {
	h := NewEventHub()
	_, _ = h.Subscribe("r1") // 不消费
	done := make(chan struct{})
	go func() {
		for i := 0; i < eventHubSubscriberBuffer*4; i++ {
			h.Publish("r1", executor.StreamEvent{Type: executor.StreamEventTextDelta, Text: "x"})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Publish 被慢订阅者阻塞了")
	}
}

// 订阅上限：超出返回已关闭的通道（调用方立刻读到 EOF），不会无限堆积连接。
func TestEventHubSubscriptionLimit(t *testing.T) {
	h := NewEventHub()
	for i := 0; i < eventHubMaxSubscribers; i++ {
		_, unsub := h.Subscribe("r1")
		defer unsub()
	}
	ch, unsub := h.Subscribe("r1")
	require.Equal(t, eventHubMaxSubscribers, h.SubscriberCount("r1"))
	_, open := <-ch
	require.False(t, open, "超限订阅立刻结束")
	unsub()
}
