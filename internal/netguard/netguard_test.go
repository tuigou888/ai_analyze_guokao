package netguard

import (
	"errors"
	"net/netip"
	"testing"
)

func TestIsForbidden(t *testing.T) {
	for _, s := range []string{
		"127.0.0.1", "127.1.2.3", "10.1.2.3", "172.16.5.5", "192.168.1.1",
		"169.254.169.254", "0.0.0.0", "100.64.0.1", "::1", "fe80::1",
		"fd00::1", "ff02::1", "198.18.0.1",
	} {
		if !IsForbidden(netip.MustParseAddr(s)) {
			t.Fatalf("%s should be forbidden", s)
		}
	}
	for _, s := range []string{"8.8.8.8", "1.1.1.1", "203.0.113.10", "2001:4860:4860::8888"} {
		if IsForbidden(netip.MustParseAddr(s)) {
			t.Fatalf("%s should be allowed", s)
		}
	}
}

func TestCheckHost(t *testing.T) {
	for _, h := range []string{"", "localhost", "api.localhost", "127.0.0.1", "10.0.0.1", "::1", "[::1]"} {
		if err := CheckHost(h); err == nil {
			t.Fatalf("%q accepted", h)
		}
	}
	// 域名无法在保存时判定解析结果，交给 DialControl。
	for _, h := range []string{"api.example.com", "8.8.8.8"} {
		if err := CheckHost(h); err != nil {
			t.Fatalf("%q rejected: %v", h, err)
		}
	}
}

func TestCheckURL(t *testing.T) {
	for _, u := range []string{
		"ftp://example.com", "http://user:pw@example.com", "not a url",
		"http://127.0.0.1:8080/v1", "http://[::1]:8080", "http://169.254.169.254/latest",
	} {
		if err := CheckURL(u); err == nil {
			t.Fatalf("%q accepted", u)
		}
	}
	if err := CheckURL("https://api.example.com/v1"); err != nil {
		t.Fatal(err)
	}
}

func TestDialControlRejectsResolvedPrivateIP(t *testing.T) {
	ctl := DialControl(false)
	if ctl == nil {
		t.Fatal("guard disabled")
	}
	if err := ctl("tcp", "127.0.0.1:443", nil); !errors.Is(err, ErrForbiddenTarget) {
		t.Fatalf("loopback dial allowed: %v", err)
	}
	if err := ctl("tcp", "8.8.8.8:443", nil); err != nil {
		t.Fatalf("public dial rejected: %v", err)
	}
	if DialControl(true) != nil {
		t.Fatal("allowPrivate should disable the guard")
	}
}
