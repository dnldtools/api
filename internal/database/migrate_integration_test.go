package database_test

import (
	"context"
	"testing"
	"time"

	"rest-api/internal/database"
	"rest-api/internal/testutil"
)

func TestMigrateIdempotentAndPreservesData(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Truncate(t, pool)

	ctx := context.Background()

	if _, err := pool.Exec(ctx, `
		INSERT INTO api_metrics
			(request_id, status_code, success, failed, endpoint, duration_ms, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		"preserve-me", 200, true, false, "POST /v1/downloads", 12, time.Now().UTC(),
	); err != nil {
		t.Fatalf("seed metric: %v", err)
	}

	if err := database.Migrate(ctx, pool); err != nil {
		t.Fatalf("re-run Migrate: %v", err)
	}

	var count int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM api_metrics WHERE request_id = 'preserve-me'`,
	).Scan(&count); err != nil {
		t.Fatalf("count preserved row: %v", err)
	}
	if count != 1 {
		t.Errorf("preserved row count = %d, want 1 (re-migration must not wipe data)", count)
	}
}
