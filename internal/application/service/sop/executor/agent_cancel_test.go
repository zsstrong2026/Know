package executor

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/types"
)

// multiBlockingQA 让 N 个并发执行各自阻塞到 ctx 取消。
type multiBlockingQA struct {
	mu      sync.Mutex
	started []chan struct{}
}

func (m *multiBlockingQA) AgentQA(ctx context.Context, _ *types.QARequest, _ *event.EventBus) error {
	ch := make(chan struct{})
	m.mu.Lock()
	m.started = append(m.started, ch)
	m.mu.Unlock()
	close(ch)
	<-ctx.Done()
	return ctx.Err()
}

// 两个节点并发执行时，Cancel 必须中断**全部**在途执行。
// 单槽实现里后一个会覆盖前一个的取消函数，Cancel 只能停掉最后一个。
func TestAgentProviderCancelStopsAllInFlight(t *testing.T) {
	qa := &multiBlockingQA{}
	p := NewAgentProvider(AgentDeps{QA: qa, Sessions: &stubSessions{}, Messages: &stubMessages{}})

	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			tb := TaskBook{
				RunID: "r1", NodeRunID: "nr" + string(rune('1'+i)), NodeID: "n" + string(rune('1'+i)),
				NodeName: "实现", Prompt: "p", Role: &types.CustomAgent{}, Attempt: 1,
			}
			_, errs[i] = p.Execute(context.Background(), tb, ExecOptions{})
		}(i)
	}

	require.Eventually(t, func() bool {
		qa.mu.Lock()
		defer qa.mu.Unlock()
		return len(qa.started) == 2
	}, 5*time.Second, 20*time.Millisecond, "两路执行都要真的跑起来")

	require.NoError(t, p.Cancel(context.Background()))

	// 有界等待：单槽实现只能停掉一路，另一路会永久阻塞，没有超时用例只会挂在
	// wg.Wait() 上（跑满 10min 才报超时，看不出真正原因）。
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Cancel 没能中断全部在途执行（仍有执行未退出）")
	}

	for i, err := range errs {
		require.ErrorIs(t, err, context.Canceled, "第 %d 路执行没被取消", i+1)
	}
}
