package r2

import (
	"context"
	"strings"
	"testing"
	"time"
)

func testAccounts() []Account {
	return []Account{
		{Name: "r2-01", AccountID: "acc1", AccessKey: "ak1", SecretKey: "sk1", Bucket: "media"},
		{Name: "r2-02", AccountID: "acc2", AccessKey: "ak2", SecretKey: "sk2", Bucket: "media"},
		{Name: "r2-03", AccountID: "acc3", AccessKey: "ak3", SecretKey: "sk3", Bucket: "media"},
	}
}

func TestNewManagerRejectsEmpty(t *testing.T) {
	if _, err := NewManager(Config{}); err == nil {
		t.Fatal("expected error for no accounts")
	}
}

func TestNewManagerRejectsIncomplete(t *testing.T) {
	accounts := testAccounts()
	accounts[1].SecretKey = ""
	if _, err := NewManager(Config{Accounts: accounts}); err == nil {
		t.Fatal("expected error for incomplete account")
	}
}

func TestRoundRobin(t *testing.T) {
	m, err := NewManager(Config{Accounts: testAccounts(), PresignTTL: 5 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"r2-01", "r2-02", "r2-03", "r2-01"}
	for i, name := range want {
		got, err := m.Presign(context.Background(), "k/obj.mp4")
		if err != nil {
			t.Fatal(err)
		}
		if got.Account != name {
			t.Fatalf("call %d: got %s, want %s", i, got.Account, name)
		}
	}
}

func TestPresignURL(t *testing.T) {
	m, err := NewManager(Config{Accounts: testAccounts(), PresignTTL: 5 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	got, err := m.PresignOn(context.Background(), "r2-01", "dir/file.mp4")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.URL, "https://acc1.r2.cloudflarestorage.com/media/dir/file.mp4") {
		t.Fatalf("unexpected URL: %s", got.URL)
	}
	if !strings.Contains(got.URL, "X-Amz-Signature=") {
		t.Fatalf("missing signature: %s", got.URL)
	}
	if got.TTLSeconds != 300 {
		t.Fatalf("TTLSeconds = %d, want 300", got.TTLSeconds)
	}
	if got.Bucket != "media" || got.ObjectKey != "dir/file.mp4" {
		t.Fatalf("unexpected bucket/key: %s %s", got.Bucket, got.ObjectKey)
	}
}

func TestPresignOnUnknown(t *testing.T) {
	m, err := NewManager(Config{Accounts: testAccounts()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.PresignOn(context.Background(), "nope", "x"); err == nil {
		t.Fatal("expected error for unknown account")
	}
}

func TestNames(t *testing.T) {
	m, err := NewManager(Config{Accounts: testAccounts()})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(m.Names(), ","); got != "r2-01,r2-02,r2-03" {
		t.Fatalf("Names() = %s", got)
	}
	if m.Count() != 3 {
		t.Fatalf("Count() = %d", m.Count())
	}
}
