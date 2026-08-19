package download

import (
	"net/http"
	"net/url"
	"testing"
	"time"
)

func mustRequest(t *testing.T, rawURL string) *http.Request {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	return req
}

func TestNewClient_NoProxy(t *testing.T) {
	c, err := NewClient(10*time.Second, ProxyConfig{}, ClientOptions{})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	tr, ok := c.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("Transport type = %T", c.Transport)
	}
	if tr.Proxy != nil {
		t.Errorf("expected nil Proxy when ProxyConfig.URL empty")
	}
	if c.Timeout != 10*time.Second {
		t.Errorf("Timeout = %v", c.Timeout)
	}
}

func TestNewClient_ProxyAppliedToMatchingHost(t *testing.T) {
	c, err := NewClient(10*time.Second, ProxyConfig{
		URL: "http://proxy.internal:8080",
	}, ClientOptions{})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	tr := c.Transport.(*http.Transport)
	if tr.Proxy == nil {
		t.Fatal("expected Proxy func to be set")
	}
	got, err := tr.Proxy(mustRequest(t, "https://example.com/x"))
	if err != nil {
		t.Fatalf("Proxy(): %v", err)
	}
	if got == nil {
		t.Fatal("Proxy() returned nil URL")
	}
	if got.Host != "proxy.internal:8080" {
		t.Errorf("proxy host = %q", got.Host)
	}
}

func TestNewClient_ProxyWithBasicAuth(t *testing.T) {
	c, err := NewClient(10*time.Second, ProxyConfig{
		URL:      "http://proxy.internal:8080",
		User:     "alice",
		Password: "s3cret",
	}, ClientOptions{})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	tr := c.Transport.(*http.Transport)
	got, err := tr.Proxy(mustRequest(t, "https://example.com/x"))
	if err != nil {
		t.Fatalf("Proxy(): %v", err)
	}
	if got.User == nil {
		t.Fatal("expected userinfo on proxy URL")
	}
	if got.User.Username() != "alice" {
		t.Errorf("user = %q", got.User.Username())
	}
	pw, _ := got.User.Password()
	if pw != "s3cret" {
		t.Errorf("password = %q", pw)
	}
}

func TestNewClient_NoProxyBypassesProxy(t *testing.T) {
	c, err := NewClient(10*time.Second, ProxyConfig{
		URL:     "http://proxy.internal:8080",
		NoProxy: "localhost, 127.0.0.1, internal.example.com",
	}, ClientOptions{})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	tr := c.Transport.(*http.Transport)

	cases := []struct {
		url        string
		wantBypass bool
	}{
		{"http://localhost/x", true},
		{"http://127.0.0.1:9000/x", true},
		{"https://internal.example.com/x", true},
		{"https://sub.internal.example.com/x", true},
		{"https://example.com/x", false},
		{"https://other.com/x", false},
	}
	for _, c := range cases {
		req := mustRequest(t, c.url)
		got, err := tr.Proxy(req)
		if err != nil {
			t.Fatalf("Proxy(%s): %v", c.url, err)
		}
		bypassed := got == nil
		if bypassed != c.wantBypass {
			t.Errorf("%s: bypass = %v, want %v", c.url, bypassed, c.wantBypass)
		}
	}
}

func TestNewClient_InvalidProxyURL(t *testing.T) {
	_, err := NewClient(10*time.Second, ProxyConfig{URL: "not-a-url"}, ClientOptions{})
	if err == nil {
		t.Fatal("expected error for proxy URL without scheme/host")
	}
}

func TestMatchesNoProxy_LeadingDot(t *testing.T) {
	entries := parseNoProxy(".example.com")
	if !matchesNoProxy("x.example.com", entries) {
		t.Error("x.example.com should match .example.com")
	}
	if matchesNoProxy("example.com", entries) {
		t.Error("example.com should NOT match .example.com (suffix-only)")
	}
	_ = url.Parse // keep import used in other tests below; harmless
}
