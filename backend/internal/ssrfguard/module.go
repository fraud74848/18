// Consolidated source; original file boundaries are marked below.
package ssrfguard

import (
	fmt "fmt"
	net "net"
	url "net/url"
	strings "strings"
)

// ---- Source: backend/internal/ssrfguard/ssrfguard.go ----
var blockedHosts = map[string]struct{}{
	"localhost":                {},
	"localhost.localdomain":    {},
	"metadata.google.internal": {},
}

// ValidateOutboundURL 校验服务端出站 HTTP(S) 请求目标，阻断内网/元数据等 SSRF 目标。
func ValidateOutboundURL(rawURL string) error {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return fmt.Errorf("url is empty")
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid url: %w", err)
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return fmt.Errorf("unsupported url scheme: %s", scheme)
	}
	host := strings.ToLower(strings.TrimSpace(u.Hostname()))
	if host == "" {
		return fmt.Errorf("url host is empty")
	}
	if _, blocked := blockedHosts[host]; blocked {
		return fmt.Errorf("url host is not allowed: %s", host)
	}
	if strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") {
		return fmt.Errorf("url host is not allowed: %s", host)
	}

	if ip := net.ParseIP(host); ip != nil {
		if isBlockedIP(ip) {
			return fmt.Errorf("url host IP is not allowed")
		}
		return nil
	}

	ips, err := net.LookupIP(host)
	if err != nil {
		return fmt.Errorf("url host lookup failed: %w", err)
	}
	if len(ips) == 0 {
		return fmt.Errorf("url host has no IP addresses")
	}
	for _, ip := range ips {
		if isBlockedIP(ip) {
			return fmt.Errorf("url host resolves to blocked IP: %s", ip.String())
		}
	}
	return nil
}

// IsBlockedIP reports whether an IP is loopback/private/link-local/metadata (SSRF-unsafe).
func IsBlockedIP(ip net.IP) bool {
	return isBlockedIP(ip)
}

// ValidateDialHost validates a dial hostname (no scheme) against SSRF rules.
// Literal IPs and DNS results that land on blocked ranges are rejected.
func ValidateDialHost(host string) error {
	host = strings.ToLower(strings.TrimSpace(host))
	host = strings.TrimPrefix(host, "[")
	host = strings.TrimSuffix(host, "]")
	if host == "" {
		return fmt.Errorf("dial host is empty")
	}
	if _, blocked := blockedHosts[host]; blocked {
		return fmt.Errorf("dial host is not allowed: %s", host)
	}
	if strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") {
		return fmt.Errorf("dial host is not allowed: %s", host)
	}
	if ip := net.ParseIP(host); ip != nil {
		if isBlockedIP(ip) {
			return fmt.Errorf("dial host IP is not allowed")
		}
		return nil
	}
	ips, err := net.LookupIP(host)
	if err != nil {
		return fmt.Errorf("dial host lookup failed: %w", err)
	}
	if len(ips) == 0 {
		return fmt.Errorf("dial host has no IP addresses")
	}
	for _, ip := range ips {
		if isBlockedIP(ip) {
			return fmt.Errorf("dial host resolves to blocked IP: %s", ip.String())
		}
	}
	return nil
}
func isBlockedIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return true
	}
	if ip.IsMulticast() || ip.IsUnspecified() {
		return true
	}
	// AWS/GCP/Azure 等常见 metadata 地址
	if v4 := ip.To4(); v4 != nil {
		if v4[0] == 169 && v4[1] == 254 {
			return true
		}
	}
	return false
}
