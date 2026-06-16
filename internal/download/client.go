package download

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ProxyConfig describes proxy settings parsed from updater.properties.
// All fields may be empty; an empty URL means "no proxy".
type ProxyConfig struct {
	URL      string
	User     string
	Password string
	NoProxy  string
}

// NewClient builds an *http.Client honouring the given timeout and proxy config.
func NewClient(timeout time.Duration, p ProxyConfig) (*http.Client, error) {
	transport := &http.Transport{
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          10,
		IdleConnTimeout:       30 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}

	if strings.TrimSpace(p.URL) != "" {
		proxyFn, err := buildProxyFunc(p)
		if err != nil {
			return nil, err
		}
		transport.Proxy = proxyFn
	}

	return &http.Client{
		Transport: transport,
		Timeout:   timeout,
	}, nil
}

// buildProxyFunc returns an http.Transport.Proxy function that:
//   - injects User/Password into the proxy URL,
//   - returns nil (direct) for requests whose host matches an entry in NoProxy.
func buildProxyFunc(p ProxyConfig) (func(*http.Request) (*url.URL, error), error) {
	proxyURL, err := url.Parse(strings.TrimSpace(p.URL))
	if err != nil {
		return nil, fmt.Errorf("download: parse proxy URL %q: %w", p.URL, err)
	}
	if proxyURL.Scheme == "" || proxyURL.Host == "" {
		return nil, fmt.Errorf("download: proxy URL %q must include scheme and host", p.URL)
	}
	if p.User != "" {
		if p.Password != "" {
			proxyURL.User = url.UserPassword(p.User, p.Password)
		} else {
			proxyURL.User = url.User(p.User)
		}
	}

	noProxy := parseNoProxy(p.NoProxy)

	return func(req *http.Request) (*url.URL, error) {
		host := req.URL.Hostname()
		if matchesNoProxy(host, noProxy) {
			return nil, nil
		}
		return proxyURL, nil
	}, nil
}

func parseNoProxy(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(strings.ToLower(p))
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// matchesNoProxy reports whether host should bypass the proxy.
// A host matches an entry if it equals the entry or is a subdomain ("."+entry suffix).
// A leading dot on the entry is treated as suffix-only ("foo.bar" does NOT match ".bar"
// but "x.bar" does).
func matchesNoProxy(host string, entries []string) bool {
	host = strings.ToLower(host)
	for _, entry := range entries {
		if entry == "" {
			continue
		}
		if strings.HasPrefix(entry, ".") {
			if strings.HasSuffix(host, entry) {
				return true
			}
			continue
		}
		if host == entry {
			return true
		}
		if strings.HasSuffix(host, "."+entry) {
			return true
		}
	}
	return false
}
