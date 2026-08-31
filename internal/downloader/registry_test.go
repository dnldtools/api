package downloader

import (
	stderrors "errors"
	"testing"
)

func TestRegistryRegisterAndGet(t *testing.T) {
	r := NewRegistry()

	p := &stubProvider{name: "facebook", platform: PlatformFacebook, ptype: ProviderNative}
	if err := r.Register(p); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	got, err := r.Get(PlatformFacebook)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got != p {
		t.Error("Get() returned a different provider than the one registered")
	}

	if got := r.Len(); got != 1 {
		t.Errorf("Len() = %d, want 1", got)
	}
	if got := r.Platforms(); len(got) != 1 || got[0] != PlatformFacebook {
		t.Errorf("Platforms() = %v, want [%s]", got, PlatformFacebook)
	}
	if got := r.All(); len(got) != 1 || got[0] != p {
		t.Errorf("All() = %v, want the registered provider", got)
	}
}

func TestRegistryPreservesRegistrationOrder(t *testing.T) {
	r := NewRegistry()
	want := []Platform{PlatformFacebook, Platform("second"), Platform("third")}
	for i, platform := range want {
		if err := r.Register(&stubProvider{name: string(platform), platform: platform}); err != nil {
			t.Fatalf("Register(%s) error = %v", platform, err)
		}
		_ = i
	}

	got := r.Platforms()
	if len(got) != len(want) {
		t.Fatalf("Platforms() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Platforms()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestRegistryRejectsDuplicatePlatform(t *testing.T) {
	r := NewRegistry()

	if err := r.Register(&stubProvider{name: "a", platform: PlatformFacebook}); err != nil {
		t.Fatalf("first Register() error = %v", err)
	}
	if err := r.Register(&stubProvider{name: "b", platform: PlatformFacebook}); err == nil {
		t.Fatal("expected duplicate platform registration to fail")
	}
}

func TestRegistryRejectsNilProvider(t *testing.T) {
	if err := NewRegistry().Register(nil); err == nil {
		t.Fatal("expected nil provider registration to fail")
	}
}

func TestRegistryRejectsEmptyPlatform(t *testing.T) {
	if err := NewRegistry().Register(&stubProvider{name: "empty", platform: ""}); err == nil {
		t.Fatal("expected empty-platform provider registration to fail")
	}
}

func TestRegistryGetUnknownPlatform(t *testing.T) {
	if _, err := NewRegistry().Get(Platform("unknown")); err == nil {
		t.Fatal("expected error for unknown platform")
	} else if !stderrors.Is(err, ErrPlatformUnsupported) {
		t.Errorf("expected ErrPlatformUnsupported, got %v", err)
	}
}
