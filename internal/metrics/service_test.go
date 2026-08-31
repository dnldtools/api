package metrics

import (
	"context"
	"sync"
	"testing"
	"time"
)

type fakeRepository struct {
	mu     sync.Mutex
	events []Event
}

func (f *fakeRepository) Insert(_ context.Context, e Event) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, e)
	return nil
}

func (f *fakeRepository) Aggregate(context.Context, time.Time, time.Time) (Stats, error) {
	return Stats{}, nil
}

func (f *fakeRepository) snapshot() []Event {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]Event, len(f.events))
	copy(out, f.events)
	return out
}

type fakeAggregator struct {
	mu    sync.Mutex
	count int
}

func (f *fakeAggregator) Increment(context.Context, Event) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.count++
	return nil
}

func (f *fakeAggregator) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.count
}

func TestServiceRecordAndCloseDrains(t *testing.T) {
	repo := &fakeRepository{}
	agg := &fakeAggregator{}
	svc := New(repo, agg, nil, 8)

	for i := 0; i < 5; i++ {
		svc.Record(Event{
			RequestID:  "req",
			StatusCode: 200,
			Endpoint:   "/v1/downloads",
			Duration:   12 * time.Millisecond,
			Timestamp:  time.Now(),
		})
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	svc.Close(ctx)

	events := repo.snapshot()
	if len(events) != 5 {
		t.Fatalf("persisted %d events, want 5", len(events))
	}
	for _, e := range events {
		if !e.Success || e.Failed {
			t.Errorf("event normalized incorrectly: success=%v failed=%v", e.Success, e.Failed)
		}
		if e.DurationMilliseconds() != 12 {
			t.Errorf("duration ms = %d, want 12", e.DurationMilliseconds())
		}
	}
	if got := agg.calls(); got != 5 {
		t.Errorf("aggregator called %d times, want 5", got)
	}
}

func TestServiceRecordAfterCloseIsNoop(t *testing.T) {
	repo := &fakeRepository{}
	svc := New(repo, nil, nil, 4)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	svc.Close(ctx)

	svc.Record(Event{StatusCode: 200})

	if got := len(repo.snapshot()); got != 0 {
		t.Errorf("expected no persisted events after close, got %d", got)
	}
}

func TestServiceDisabledIsNoop(t *testing.T) {
	svc := New(nil, nil, nil, 4)

	svc.Record(Event{StatusCode: 500})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	svc.Close(ctx)

	stats, err := svc.Aggregate(context.Background(), time.Time{}, time.Now())
	if err != nil {
		t.Fatalf("Aggregate error: %v", err)
	}
	if stats != (Stats{}) {
		t.Errorf("disabled service Aggregate should return zero Stats, got %+v", stats)
	}
}

func TestServiceAggregateDelegatesToRepo(t *testing.T) {

	repo := &fixedStatsRepo{stats: Stats{TotalRequests: 10, SuccessCount: 9, SuccessRate: 0.9}}
	svc := New(repo, nil, nil, 4)

	got, err := svc.Aggregate(context.Background(), time.Time{}, time.Now())
	if err != nil {
		t.Fatalf("Aggregate error: %v", err)
	}
	if got.SuccessRate != 0.9 {
		t.Errorf("SuccessRate = %v, want 0.9", got.SuccessRate)
	}
}

type fixedStatsRepo struct {
	stats Stats
}

func (f *fixedStatsRepo) Insert(context.Context, Event) error { return nil }
func (f *fixedStatsRepo) Aggregate(context.Context, time.Time, time.Time) (Stats, error) {
	return f.stats, nil
}
