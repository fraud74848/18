// Consolidated source; original file boundaries are marked below.
package proxytransport

import (
	context "context"
	tls "crypto/tls"
	fmt "fmt"
	proxy "golang.org/x/net/proxy"
	net "net"
	http "net/http"
	url "net/url"
	regexp "regexp"
	strings "strings"
	ssrfguard "tg-attendance/backend/internal/ssrfguard"
	time "time"
)

// ---- Source: backend/internal/proxytransport/client.go ----
var nonStandardSOCKS = regexp.MustCompile(`(?i)^(socks5?|sock5)://([^:/@]+):(\d+):([^:@/]+):([^:@/]+)$`)

// NormalizeProxyURL 规范代理 URL，支持标准格式与 sock5://host:port:user:pass 写法。
func NormalizeProxyURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	lower := strings.ToLower(raw)
	if strings.HasPrefix(lower, "sock5://") {
		raw = "socks5://" + raw[len("sock5://"):]
	}
	if m := nonStandardSOCKS.FindStringSubmatch(raw); len(m) == 6 {
		user := url.QueryEscape(m[4])
		pass := url.QueryEscape(m[5])
		return fmt.Sprintf("socks5://%s:%s@%s:%s", user, pass, m[2], m[3]), nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid proxy URL: %w", err)
	}
	scheme := strings.ToLower(strings.TrimSpace(u.Scheme))
	switch scheme {
	case "sock5":
		u.Scheme = "socks5"
	case "http", "https", "socks5", "socks5h", "socks4":
	default:
		return "", fmt.Errorf("unsupported proxy scheme: %s", u.Scheme)
	}
	if u.Host == "" {
		return "", fmt.Errorf("proxy URL missing host")
	}
	return u.String(), nil
}
// SafeDialContext wraps a net.Dialer so TCP dials never connect to SSRF-blocked IPs.
// Resolves hostnames, rejects blocked ranges, then dials a validated address.
func SafeDialContext(base *net.Dialer) func(ctx context.Context, network, address string) (net.Conn, error) {
	if base == nil {
		base = &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}
	}
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, fmt.Errorf("ssrf: invalid dial address: %w", err)
		}
		host = strings.TrimPrefix(host, "[")
		host = strings.TrimSuffix(host, "]")

		if ip := net.ParseIP(host); ip != nil {
			if ssrfguard.IsBlockedIP(ip) {
				return nil, fmt.Errorf("ssrf: dial to blocked IP not allowed")
			}
			return base.DialContext(ctx, network, address)
		}
		if err := ssrfguard.ValidateDialHost(host); err != nil {
			return nil, fmt.Errorf("ssrf: %w", err)
		}
		ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, err
		}
		var lastErr error
		for _, ipa := range ips {
			if ssrfguard.IsBlockedIP(ipa.IP) {
				lastErr = fmt.Errorf("ssrf: dial host resolves to blocked IP: %s", ipa.IP)
				continue
			}
			target := net.JoinHostPort(ipa.IP.String(), port)
			conn, dialErr := base.DialContext(ctx, network, target)
			if dialErr == nil {
				return conn, nil
			}
			lastErr = dialErr
		}
		if lastErr != nil {
			return nil, lastErr
		}
		return nil, fmt.Errorf("ssrf: no allowed IP for host %s", host)
	}
}

// wrapDialContextValidatesTarget validates the destination host before calling inner
// (used for SOCKS where DialContext address is the ultimate target).
func wrapDialContextValidatesTarget(inner func(ctx context.Context, network, address string) (net.Conn, error)) func(ctx context.Context, network, address string) (net.Conn, error) {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			host = address
		}
		host = strings.TrimPrefix(host, "[")
		host = strings.TrimSuffix(host, "]")
		if err := ssrfguard.ValidateDialHost(host); err != nil {
			return nil, fmt.Errorf("ssrf: %w", err)
		}
		return inner(ctx, network, address)
	}
}

// SafeCheckRedirect rejects redirect targets that fail SSRF validation.
func SafeCheckRedirect(maxHops int) func(req *http.Request, via []*http.Request) error {
	if maxHops <= 0 {
		maxHops = 10
	}
	return func(req *http.Request, via []*http.Request) error {
		if len(via) >= maxHops {
			return fmt.Errorf("too many redirects")
		}
		if req != nil && req.URL != nil {
			if err := ssrfguard.ValidateOutboundURL(req.URL.String()); err != nil {
				return fmt.Errorf("ssrf redirect blocked: %w", err)
			}
		}
		return nil
	}
}

// ConfigureTransport 为 Transport 配置 HTTP 或 SOCKS 代理。
func ConfigureTransport(tr *http.Transport, proxyURL string, dialTimeout time.Duration) error {
	if tr == nil {
		return fmt.Errorf("transport is nil")
	}
	proxyURL, err := NormalizeProxyURL(proxyURL)
	if err != nil {
		return err
	}
	if proxyURL == "" {
		return nil
	}
	u, err := url.Parse(proxyURL)
	if err != nil {
		return err
	}
	baseDialer := &net.Dialer{
		Timeout:   dialTimeout,
		KeepAlive: 30 * time.Second,
	}
	scheme := strings.ToLower(u.Scheme)
	switch scheme {
	case "http", "https":
		// Dial connects to the proxy itself (may be private); SSRF for targets is via
		// ValidateOutboundURL + SafeCheckRedirect on the request URL.
		tr.Proxy = http.ProxyURL(u)
		if tr.DialContext == nil {
			tr.DialContext = baseDialer.DialContext
		}
		return nil
	case "socks5", "socks5h", "socks4":
		dialer, err := proxy.FromURL(u, baseDialer)
		if err != nil {
			return err
		}
		contextDialer, ok := dialer.(proxy.ContextDialer)
		if !ok {
			return fmt.Errorf("SOCKS proxy dialer does not support context")
		}
		tr.Proxy = nil
		tr.DialContext = wrapDialContextValidatesTarget(contextDialer.DialContext)
		return nil
	default:
		return fmt.Errorf("unsupported proxy scheme: %s", u.Scheme)
	}
}

// NewHTTPClient 创建带 HTTP/SOCKS 代理的客户端（与域名监控检测逻辑一致）。
// 直连与 SOCKS 在 Dial 层拦截内网/元数据目标；重定向目标同样校验。
func NewHTTPClient(timeout time.Duration, proxyURL string) *http.Client {
	if timeout <= 0 {
		timeout = 8 * time.Second
	}
	dialTimeout := timeout
	if dialTimeout > 5*time.Second {
		dialTimeout = 5 * time.Second
	}
	useProxy := strings.TrimSpace(proxyURL) != ""
	tr := &http.Transport{
		MaxIdleConns:          1000,
		MaxIdleConnsPerHost:   100,
		MaxConnsPerHost:       200,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		ForceAttemptHTTP2:     !useProxy,
	}
	if useProxy {
		tr.ForceAttemptHTTP2 = false
		tr.TLSNextProto = make(map[string]func(string, *tls.Conn) http.RoundTripper)
		_ = ConfigureTransport(tr, proxyURL, dialTimeout)
	} else {
		tr.DialContext = SafeDialContext(&net.Dialer{
			Timeout:   dialTimeout,
			KeepAlive: 30 * time.Second,
		})
	}
	return &http.Client{
		Timeout:       timeout,
		Transport:     tr,
		CheckRedirect: SafeCheckRedirect(10),
	}
}

