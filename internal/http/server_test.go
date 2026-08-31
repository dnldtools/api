package http

import (
	"net/http"
	"testing"
	"time"
)

func TestNewServerConfiguresTimeouts(t *testing.T) {
	cfg := ServerConfig{
		Addr:            "127.0.0.1:0",
		ReadTimeout:     11 * time.Second,
		WriteTimeout:    22 * time.Second,
		IdleTimeout:     33 * time.Second,
		ShutdownTimeout: 44 * time.Second,
	}

	s := NewServer(cfg, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}), nil)

	if s.srv.Addr != cfg.Addr {
		t.Errorf("Addr = %q, want %q", s.srv.Addr, cfg.Addr)
	}
	if s.srv.ReadTimeout != cfg.ReadTimeout {
		t.Errorf("ReadTimeout = %v, want %v", s.srv.ReadTimeout, cfg.ReadTimeout)
	}
	if s.srv.WriteTimeout != cfg.WriteTimeout {
		t.Errorf("WriteTimeout = %v, want %v", s.srv.WriteTimeout, cfg.WriteTimeout)
	}
	if s.srv.IdleTimeout != cfg.IdleTimeout {
		t.Errorf("IdleTimeout = %v, want %v", s.srv.IdleTimeout, cfg.IdleTimeout)
	}
	if s.srv.ReadHeaderTimeout != 5*time.Second {
		t.Errorf("ReadHeaderTimeout = %v, want %v", s.srv.ReadHeaderTimeout, 5*time.Second)
	}
	if s.cfg.ShutdownTimeout != cfg.ShutdownTimeout {
		t.Errorf("ShutdownTimeout = %v, want %v", s.cfg.ShutdownTimeout, cfg.ShutdownTimeout)
	}
}

func TestNewServerAppliesDefaultsForZeroTimeouts(t *testing.T) {
	s := NewServer(ServerConfig{Addr: "127.0.0.1:0"}, http.NotFoundHandler(), nil)

	if s.srv.ReadTimeout != 30*time.Second {
		t.Errorf("default ReadTimeout = %v, want 30s", s.srv.ReadTimeout)
	}
	if s.srv.WriteTimeout != 60*time.Second {
		t.Errorf("default WriteTimeout = %v, want 60s", s.srv.WriteTimeout)
	}
	if s.srv.IdleTimeout != 60*time.Second {
		t.Errorf("default IdleTimeout = %v, want 60s", s.srv.IdleTimeout)
	}
	if s.cfg.ShutdownTimeout != 10*time.Second {
		t.Errorf("default ShutdownTimeout = %v, want 10s", s.cfg.ShutdownTimeout)
	}
}
