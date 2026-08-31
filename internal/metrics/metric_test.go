package metrics

import (
	"testing"
	"time"
)

func TestIsSuccess(t *testing.T) {
	cases := []struct {
		code int
		want bool
	}{
		{200, true},
		{201, false},
		{204, false},
		{301, false},
		{400, false},
		{404, false},
		{429, false},
		{500, false},
		{502, false},
		{504, false},
	}
	for _, c := range cases {
		if got := IsSuccess(c.code); got != c.want {
			t.Errorf("IsSuccess(%d) = %v, want %v", c.code, got, c.want)
		}
	}
}

func TestEventNormalize(t *testing.T) {
	e := Event{StatusCode: 200, Timestamp: time.Time{}}
	e.Normalize()

	if !e.Success {
		t.Error("Success should be true for status 200")
	}
	if e.Failed {
		t.Error("Failed should be false for status 200")
	}
	if e.Timestamp.IsZero() || e.Timestamp.Location() != time.UTC {
		t.Errorf("Timestamp should be set to UTC, got %v", e.Timestamp)
	}

	e2 := Event{StatusCode: 500, Timestamp: time.Date(2024, 1, 2, 3, 4, 5, 0, time.FixedZone("x", 3600))}
	e2.Normalize()
	if e2.Success || !e2.Failed {
		t.Errorf("status 500 should be failed, got success=%v failed=%v", e2.Success, e2.Failed)
	}
	if e2.Timestamp.Location() != time.UTC {
		t.Errorf("Timestamp should be normalized to UTC, got %v", e2.Timestamp)
	}
}

func TestSuccessRate(t *testing.T) {
	cases := []struct {
		success, total int64
		want           float64
	}{
		{0, 0, 0},
		{0, 10, 0},
		{5, 10, 0.5},
		{10, 10, 1},
		{1, 3, 1.0 / 3.0},
	}
	for _, c := range cases {
		if got := SuccessRate(c.success, c.total); got != c.want {
			t.Errorf("SuccessRate(%d, %d) = %v, want %v", c.success, c.total, got, c.want)
		}
	}
}
