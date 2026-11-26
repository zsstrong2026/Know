package hitl

import (
	"cmp"
	"context"
	"errors"
	"slices"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// Store persists prompts. Production wiring passes the GORM repository; the
// in-memory store keeps single-process callers and tests on the same code path.
type Store = interfaces.AgentPromptRepository

// memoryStore is a process-local Store. It cannot coordinate replicas.
type memoryStore struct {
	mu   sync.Mutex
	rows map[string]types.AgentPrompt
	// seq records insertion order. created_at alone is not a total order:
	// Windows' clock granularity is 1–15 ms, so rows created back to back can
	// share a timestamp and sorting on it alone would shuffle them between
	// reads (the GORM store breaks ties on the primary key, which is random).
	seq map[string]uint64
	n   uint64
}

// NewMemoryStore returns a process-local Store.
func NewMemoryStore() Store {
	return &memoryStore{rows: make(map[string]types.AgentPrompt), seq: make(map[string]uint64)}
}

func (s *memoryStore) Create(_ context.Context, row *types.AgentPrompt) error {
	if row == nil || row.ID == "" {
		return errors.New("agent prompt row requires an id")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.rows[row.ID]; exists {
		return errors.New("agent prompt already exists")
	}
	now := time.Now()
	stored := *row
	// 与 GORM 同语义：只有零值才补当前时间，调用方显式给的时间戳原样保留
	// （否则两条存储链路的排序、分页与「最近一条」判定会不一致）。
	if stored.CreatedAt.IsZero() {
		stored.CreatedAt = now
	}
	stored.UpdatedAt = now
	s.rows[row.ID] = stored
	s.n++
	s.seq[row.ID] = s.n
	return nil
}

func (s *memoryStore) Get(_ context.Context, id string) (*types.AgentPrompt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.rows[id]
	if !ok {
		return nil, nil
	}
	return &row, nil
}

func (s *memoryStore) Transition(_ context.Context, id string, t types.AgentPromptTransition) (bool, error) {
	if len(t.From) == 0 || t.To == "" {
		return false, errors.New("transition requires from and to states")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.rows[id]
	if !ok || !slices.Contains(t.From, row.Status) {
		return false, nil
	}
	if t.UnexpiredAt != nil && row.ExpiresAt != nil && !row.ExpiresAt.After(*t.UnexpiredAt) {
		return false, nil
	}
	if t.ExpectedOwner != nil && row.OwnerInstance != *t.ExpectedOwner {
		return false, nil
	}
	if t.LeaseBefore != nil && (row.LeaseUntil == nil || !row.LeaseUntil.Before(*t.LeaseBefore)) {
		return false, nil
	}
	if t.Owner != nil {
		row.OwnerInstance = *t.Owner
	}
	if t.LeaseUntil != nil {
		at := *t.LeaseUntil
		row.LeaseUntil = &at
	}
	row.Status = t.To
	row.UpdatedAt = time.Now()
	if len(t.Response) > 0 {
		row.Response = t.Response
	}
	if t.CloseReason != nil {
		row.CloseReason = *t.CloseReason
	}
	if t.ResolvedBy != nil {
		row.ResolvedBy = *t.ResolvedBy
	}
	if t.ResolvedAt != nil {
		at := *t.ResolvedAt
		row.ResolvedAt = &at
	}
	if t.ExpiresAt != nil {
		at := *t.ExpiresAt
		row.ExpiresAt = &at
	}
	s.rows[id] = row
	return true, nil
}

func (s *memoryStore) RenewLease(_ context.Context, id, owner string, until time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.rows[id]
	if !ok || !slices.Contains(leasedStatuses, row.Status) || row.OwnerInstance != owner {
		return false, nil
	}
	row.LeaseUntil = &until
	row.UpdatedAt = time.Now()
	s.rows[id] = row
	return true, nil
}

// leasedStatuses are the states whose row is held by an owner replica.
var leasedStatuses = []string{types.AgentPromptPending, types.AgentPromptAnswered, types.AgentPromptExecuting}

func (s *memoryStore) ListStaleLeases(
	_ context.Context, statuses []string, before time.Time, limit int,
) ([]*types.AgentPrompt, error) {
	return s.list(limit, func(row types.AgentPrompt) bool {
		return slices.Contains(statuses, row.Status) && row.LeaseUntil != nil && row.LeaseUntil.Before(before)
	}, func(row types.AgentPrompt) time.Time { return *row.LeaseUntil }), nil
}

func (s *memoryStore) ListExpired(_ context.Context, before time.Time, limit int) ([]*types.AgentPrompt, error) {
	return s.list(limit, func(row types.AgentPrompt) bool {
		return row.Status == types.AgentPromptSuspended && row.ExpiresAt != nil && row.ExpiresAt.Before(before)
	}, func(row types.AgentPrompt) time.Time { return *row.ExpiresAt }), nil
}

func (s *memoryStore) ListBySession(
	_ context.Context, sessionID string, statuses []string,
) ([]*types.AgentPrompt, error) {
	return s.list(0, func(row types.AgentPrompt) bool {
		return row.SessionID == sessionID && slices.Contains(statuses, row.Status)
	}, func(row types.AgentPrompt) time.Time { return row.CreatedAt }), nil
}

// list returns matching rows ordered by key; limit 0 is unlimited.
func (s *memoryStore) list(
	limit int, match func(types.AgentPrompt) bool, key func(types.AgentPrompt) time.Time,
) []*types.AgentPrompt {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*types.AgentPrompt
	for _, row := range s.rows {
		if match(row) {
			r := row
			out = append(out, &r)
		}
	}
	// 时间戳相同时按插入顺序兜底：Windows 的时钟精度约 1–15ms，同一批写入常
	// 命中同一个 created_at，只按时间排序会让顺序在两次读之间漂移。
	slices.SortFunc(out, func(a, b *types.AgentPrompt) int {
		if c := key(*a).Compare(key(*b)); c != 0 {
			return c
		}
		return cmp.Compare(s.seq[a.ID], s.seq[b.ID])
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// SuspensionStore persists the state of runs blocked on a prompt.
type SuspensionStore = interfaces.AgentRunSuspensionRepository

// memorySuspensionStore is a process-local SuspensionStore.
type memorySuspensionStore struct {
	mu   sync.Mutex
	rows map[string]types.AgentRunSuspension
}

// NewMemorySuspensionStore returns a process-local SuspensionStore.
func NewMemorySuspensionStore() SuspensionStore {
	return &memorySuspensionStore{rows: make(map[string]types.AgentRunSuspension)}
}

func (s *memorySuspensionStore) Save(_ context.Context, row *types.AgentRunSuspension) error {
	if row == nil || row.AssistantMessageID == "" {
		return errors.New("agent run suspension requires an assistant message id")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	stored := *row
	stored.UpdatedAt = time.Now()
	if prev, ok := s.rows[row.AssistantMessageID]; ok {
		stored.CreatedAt = prev.CreatedAt
	} else {
		stored.CreatedAt = stored.UpdatedAt
	}
	s.rows[row.AssistantMessageID] = stored
	return nil
}

func (s *memorySuspensionStore) Get(_ context.Context, assistantMessageID string) (*types.AgentRunSuspension, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.rows[assistantMessageID]
	if !ok {
		return nil, nil
	}
	return &row, nil
}

func (s *memorySuspensionStore) Delete(_ context.Context, assistantMessageID, promptID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if row, ok := s.rows[assistantMessageID]; ok && row.PromptID == promptID {
		delete(s.rows, assistantMessageID)
	}
	return nil
}
