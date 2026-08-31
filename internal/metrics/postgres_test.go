package metrics

import (
	"context"
	"testing"
	"time"

	"rest-api/internal/testutil"
)

func TestPostgresRepositoryInsertAndAggregate(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Truncate(t, pool)

	repo := NewPostgresRepository(pool)
	ctx := context.Background()

	base := time.Now().UTC().Truncate(time.Minute)
	events := []Event{
		{RequestID: "int-1", StatusCode: 200, ValidAPIKey: true, ClientID: "hash-a", AccountID: 1, Endpoint: "POST /v1/downloads", Platform: "facebook", Duration: 5 * time.Millisecond, Timestamp: base},
		{RequestID: "int-2", StatusCode: 500, ValidAPIKey: true, ClientID: "hash-a", AccountID: 1, Endpoint: "POST /v1/downloads", Platform: "facebook", Duration: 9 * time.Millisecond, Timestamp: base.Add(time.Second)},
		{RequestID: "int-3", StatusCode: 401, InvalidAPIKey: true, Endpoint: "POST /v1/downloads", Duration: time.Millisecond, Timestamp: base.Add(2 * time.Second)},
		{RequestID: "int-4", StatusCode: 429, RateLimited: true, ValidAPIKey: true, ClientID: "hash-a", AccountID: 1, Endpoint: "POST /v1/downloads", Timestamp: base.Add(3 * time.Second)},
	}

	for _, e := range events {
		if err := repo.Insert(ctx, e); err != nil {
			t.Fatalf("Insert(%s): %v", e.RequestID, err)
		}
	}

	stats, err := repo.Aggregate(ctx, base.Add(-time.Hour), base.Add(time.Hour))
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	if stats.TotalRequests != 4 {
		t.Errorf("TotalRequests = %d, want 4", stats.TotalRequests)
	}
	if stats.SuccessCount != 1 {
		t.Errorf("SuccessCount = %d, want 1 (only 200 counts as success)", stats.SuccessCount)
	}
	if stats.ErrorCount != 3 {
		t.Errorf("ErrorCount = %d, want 3", stats.ErrorCount)
	}
	if stats.ValidAPIKeyCount != 3 {
		t.Errorf("ValidAPIKeyCount = %d, want 3", stats.ValidAPIKeyCount)
	}
	if stats.InvalidAPIKeyCount != 1 {
		t.Errorf("InvalidAPIKeyCount = %d, want 1", stats.InvalidAPIKeyCount)
	}
	if stats.RateLimitedCount != 1 {
		t.Errorf("RateLimitedCount = %d, want 1", stats.RateLimitedCount)
	}
	if stats.SuccessRate != 0.25 {
		t.Errorf("SuccessRate = %v, want 0.25", stats.SuccessRate)
	}

	if stats.AvgDurationMs != 3.75 {
		t.Errorf("AvgDurationMs = %v, want 3.75", stats.AvgDurationMs)
	}
}

func TestPostgresRepositoryNullableColumns(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Truncate(t, pool)

	repo := NewPostgresRepository(pool)
	ctx := context.Background()

	if err := repo.Insert(ctx, Event{
		RequestID:  "int-null",
		StatusCode: 401,
		Endpoint:   "POST /v1/downloads",
		Timestamp:  time.Now().UTC(),
	}); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	var accountID *int64
	var clientID *string
	err := pool.QueryRow(ctx,
		`SELECT account_id, client_id FROM api_metrics WHERE request_id = 'int-null'`,
	).Scan(&accountID, &clientID)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if accountID != nil {
		t.Errorf("account_id = %v, want NULL", *accountID)
	}
	if clientID != nil {
		t.Errorf("client_id = %q, want NULL", *clientID)
	}
}
