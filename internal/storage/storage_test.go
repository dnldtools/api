package storage

import (
	"net"
	"strings"
	"testing"

	"rest-api/internal/downloader"
	"rest-api/internal/r2"
)

func TestSkipPlatforms(t *testing.T) {
	for _, p := range []downloader.Platform{
		downloader.PlatformYouTube,
		downloader.PlatformUCShare,
		downloader.PlatformSavefrom,
		downloader.Platform9xbuddy,
		downloader.PlatformDoodstream,
	} {
		if !SkipPlatforms[p] {
			t.Errorf("SkipPlatforms[%s] = false, want true", p)
		}
	}
}

func TestShouldMirror(t *testing.T) {
	u := &Uploader{r2: nil}
	if u.Enabled() {
		t.Fatal("Enabled() = true with nil manager")
	}
	if u.ShouldMirror(downloader.PlatformFacebook) {
		t.Fatal("ShouldMirror = true when disabled")
	}

	mgr, err := r2.NewManager(r2.Config{Accounts: []r2.Account{
		{Name: "r2-01", AccountID: "a", AccessKey: "k", SecretKey: "s", Bucket: "b"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	enabled := &Uploader{r2: mgr}
	if !enabled.Enabled() {
		t.Fatal("Enabled() = false with manager")
	}
	for _, p := range []downloader.Platform{
		downloader.PlatformFacebook,
		downloader.PlatformInstagram,
		downloader.PlatformTikTok,
		downloader.PlatformShopee,
		downloader.PlatformApple,
		downloader.PlatformPinterest,
		downloader.PlatformX,
		downloader.PlatformThreads,
	} {
		if !enabled.ShouldMirror(p) {
			t.Errorf("ShouldMirror(%s) = false, want true", p)
		}
	}
	for _, p := range []downloader.Platform{
		downloader.PlatformYouTube,
		downloader.PlatformUCShare,
		downloader.PlatformSavefrom,
		downloader.Platform9xbuddy,
		downloader.PlatformDoodstream,
	} {
		if enabled.ShouldMirror(p) {
			t.Errorf("ShouldMirror(%s) = true, want false", p)
		}
	}
}

func TestObjectKey(t *testing.T) {
	k1 := objectKey(downloader.PlatformTikTok, "https://cdn.example.com/a.mp4", 0, "mp4")
	k2 := objectKey(downloader.PlatformTikTok, "https://cdn.example.com/a.mp4", 0, "mp4")
	if k1 != k2 {
		t.Fatalf("objectKey not deterministic: %s vs %s", k1, k2)
	}
	if !strings.HasPrefix(k1, "tiktok/") {
		t.Fatalf("objectKey missing platform prefix: %s", k1)
	}
	if !strings.HasSuffix(k1, ".mp4") {
		t.Fatalf("objectKey missing extension: %s", k1)
	}
	if strings.Contains(k1, "//") {
		t.Fatalf("objectKey has empty hash segment: %s", k1)
	}
}

func TestObjectKeyDefaultExt(t *testing.T) {
	k := objectKey(downloader.PlatformFacebook, "https://x.example.com/v", 1, "")
	if !strings.HasSuffix(k, ".bin") {
		t.Fatalf("objectKey default ext = %s, want .bin", k)
	}
}

func TestGuardURLRejectsLoopback(t *testing.T) {
	if err := guardURL("http://127.0.0.1:8080/x"); err == nil {
		t.Fatal("guardURL accepted loopback")
	}
	if err := guardURL("http://localhost/x"); err == nil {
		t.Fatal("guardURL accepted localhost")
	}
}

func TestGuardURLRejectsPrivate(t *testing.T) {
	for _, u := range []string{
		"http://10.0.0.1/x",
		"http://192.168.1.1/x",
		"http://169.254.1.1/x",
		"http://[::1]/x",
	} {
		if err := guardURL(u); err == nil {
			t.Fatalf("guardURL accepted %s", u)
		}
	}
}

func TestGuardURLRejectsScheme(t *testing.T) {
	if err := guardURL("file:///etc/passwd"); err == nil {
		t.Fatal("guardURL accepted file scheme")
	}
}

func TestGuardURLAcceptsPublicIP(t *testing.T) {
	if err := guardURL("https://8.8.8.8/x"); err != nil {
		t.Fatalf("guardURL rejected public IP: %v", err)
	}
}

func TestForbiddenIP(t *testing.T) {
	cases := []struct {
		ip        string
		forbidden bool
	}{
		{"127.0.0.1", true},
		{"10.0.0.1", true},
		{"192.168.1.1", true},
		{"172.16.0.1", true},
		{"169.254.0.1", true},
		{"0.0.0.0", true},
		{"8.8.8.8", false},
		{"104.16.0.1", false},
	}
	for _, c := range cases {
		ip := net.ParseIP(c.ip)
		if got := forbiddenIP(ip); got != c.forbidden {
			t.Errorf("forbiddenIP(%s) = %v, want %v", c.ip, got, c.forbidden)
		}
	}
}
