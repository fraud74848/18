// Consolidated source; original file boundaries are marked below.
package config

import (
	log "log"
	urlPackage2 "net/url"
	os "os"
	strings "strings"
	sync "sync"
	time "time"
)

// ---- Source: backend/config/config.go ----
var (
	corsProvider   func() []string
	corsProviderMu sync.RWMutex
)

// AllowedOrigins 返回 CORS / WebSocket 允许的 Origin 列表
func AllowedOrigins() []string {
	corsProviderMu.RLock()
	provider := corsProvider
	corsProviderMu.RUnlock()
	if provider != nil {
		return provider()
	}

	raw := strings.TrimSpace(os.Getenv("CORS_ALLOWED_ORIGINS"))
	if raw == "" {
		if os.Getenv("ENV") != "production" {
			return []string{
				"http://localhost:5173",
				"http://localhost:3000",
				"http://127.0.0.1:5173",
				"http://127.0.0.1:3000",
			}
		}
		return nil
	}

	parts := strings.Split(raw, ",")
	origins := make([]string, 0, len(parts))
	for _, part := range parts {
		if o := strings.TrimSpace(part); o != "" {
			origins = append(origins, o)
		}
	}
	return origins
}

// normalizeOrigin 规范化 Origin，便于 http:80 / https:443 与省略端口的写法互通
func normalizeOrigin(origin string) string {
	origin = strings.TrimSpace(origin)
	if origin == "" {
		return ""
	}

	u, err := urlPackage2.Parse(origin)
	if err != nil {
		return strings.TrimRight(strings.ToLower(origin), "/")
	}

	scheme := strings.ToLower(u.Scheme)
	host := strings.ToLower(u.Hostname())
	port := u.Port()

	switch {
	case port == "" && scheme == "https":
		port = "443"
	case port == "" && scheme == "http":
		port = "80"
	}

	if (scheme == "http" && port == "80") || (scheme == "https" && port == "443") {
		return scheme + "://" + host
	}
	return scheme + "://" + host + ":" + port
}

// IsOriginAllowed 校验 Origin（空 Origin 允许，兼容非浏览器客户端）
func IsOriginAllowed(origin string) bool {
	origin = strings.TrimSpace(origin)
	if origin == "" {
		return true
	}

	normalized := normalizeOrigin(origin)
	for _, allowed := range AllowedOrigins() {
		if strings.EqualFold(normalizeOrigin(allowed), normalized) {
			return true
		}
	}
	return false
}

// LogOriginRejected 记录被拒绝的 WebSocket/CORS Origin（便于生产排错）
func LogOriginRejected(origin string) {
	allowed := AllowedOrigins()
	if len(allowed) == 0 {
		log.Printf("[CORS] Origin rejected: %q (CORS_ALLOWED_ORIGINS is empty in production)", origin)
		return
	}
	log.Printf("[CORS] Origin rejected: %q (allowed: %v)", origin, allowed)
}

// LiveKitConfig holds LiveKit connection and token settings.
type LiveKitConfig struct {
	URL                  string
	APIHost              string
	RTMPBaseURL          string
	WHIPBaseURL          string
	PublishProtocol      string // whip | rtmp | auto
	APIKey               string
	APISecret            string
	TokenTTL             time.Duration
	MaxViewersPerDev     int
	StartingTimeout      time.Duration
	PublisherGrace       time.Duration
	LifecycleDiagEnabled bool
}

func (c LiveKitConfig) Enabled() bool {
	return c.URL != "" && c.APIKey != "" && c.APISecret != "" && c.APIHost != ""
}
