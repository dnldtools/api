package health

import (
	"context"
	"time"
)

type Checker struct {
	version   string
	startedAt time.Time
}

func New(version string) *Checker {
	return &Checker{
		version:   version,
		startedAt: time.Now(),
	}
}

type Status struct {
	Status  string `json:"status"`
	Version string `json:"version"`
	Uptime  string `json:"uptime"`
	Time    string `json:"time"`
}

func (c *Checker) Check(_ context.Context) Status {
	return Status{
		Status:  "ok",
		Version: c.version,
		Uptime:  time.Since(c.startedAt).Round(time.Second).String(),
		Time:    time.Now().UTC().Format(time.RFC3339),
	}
}
