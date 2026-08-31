package health

import (
	"context"
	"testing"
	"time"
)

func TestCheckReportsOK(t *testing.T) {
	c := New("1.2.3")

	status := c.Check(context.Background())

	if status.Status != "ok" {
		t.Errorf("Status = %q, want ok", status.Status)
	}
	if status.Version != "1.2.3" {
		t.Errorf("Version = %q, want 1.2.3", status.Version)
	}
	if status.Uptime == "" {
		t.Error("Uptime should not be empty")
	}
	if _, err := time.Parse(time.RFC3339, status.Time); err != nil {
		t.Errorf("Time = %q is not RFC3339: %v", status.Time, err)
	}
}

func TestCheckUptimeAdvances(t *testing.T) {
	c := New("test")
	first := c.Check(context.Background()).Uptime

	time.Sleep(1100 * time.Millisecond)

	second := c.Check(context.Background()).Uptime
	if first == second {
		t.Errorf("uptime did not advance: before=%q after=%q", first, second)
	}
}
