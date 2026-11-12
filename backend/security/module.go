// Consolidated source; original file boundaries are marked below.
package security

import (
	context "context"
	ed25519 "crypto/ed25519"
	subtle "crypto/subtle"
	errors "errors"
	redisPackage2 "github.com/redis/go-redis/v9"
	strings "strings"
	sync "sync"
	time "time"
)

// ---- Source: backend/security/nonce.go ----
var (
	ErrNonceReused      = errors.New("nonce already used")
	ErrNonceTooLarge    = errors.New("nonce too large")
	ErrCacheFull        = errors.New("nonce cache full")
	ErrNonceUnavailable = errors.New("nonce store unavailable")
)

// NonceCache 内存版 nonce 防重放缓存
//
// 适用于：
// - 单机
// - 中低并发
//
// 多实例请使用 Redis SETNX
type NonceCache struct {
	mu sync.RWMutex

	// nonce -> 创建时间
	items map[string]time.Time

	// nonce 有效期
	ttl time.Duration

	// 最大缓存数
	maxEntries int

	// cleanup 周期
	cleanupInterval time.Duration

	// lifecycle
	ctx    context.Context
	cancel context.CancelFunc

	wg sync.WaitGroup
}

// Count 当前数量
func (c *NonceCache) Count() int {

	c.mu.RLock()
	defer c.mu.RUnlock()

	return len(c.items)
}

// cleanupLoop
func (c *NonceCache) cleanupLoop() {
	defer c.wg.Done()

	ticker := time.NewTicker(
		c.cleanupInterval,
	)

	defer ticker.Stop()

	for {
		select {

		case <-c.ctx.Done():
			return

		case <-ticker.C:

			now := time.Now()

			c.mu.Lock()

			c.cleanupLocked(now)

			c.mu.Unlock()
		}
	}
}

// cleanupLocked
//
// 调用方必须持有锁
func (c *NonceCache) cleanupLocked(
	now time.Time,
) {

	for nonce, createdAt := range c.items {

		if now.Sub(createdAt) > c.ttl {
			delete(c.items, nonce)
		}
	}
}

// Close 关闭 cache
func (c *NonceCache) Close() error {

	c.cancel()

	c.wg.Wait()

	return nil
}

// DistributedNonceStore 签名 nonce 防重放（Redis 优先，内存兜底）
type DistributedNonceStore struct {
	redis *redisPackage2.Client
	mem   *NonceCache
	ttl   time.Duration
}

func (s *DistributedNonceStore) Close() error {
	if s.mem != nil {
		return s.mem.Close()
	}
	return nil
}

// ---- Source: backend/security/policy_sign_payload.go ----
const (
	PolicySignatureVersion = 1
	PolicySignatureAlg     = "Ed25519"
)

var excludedPolicySignFields = map[string]struct{}{
	"signature":        {},
	"signatureAlg":     {},
	"signatureVersion": {},
	"keyId":            {},
}

var (
	policySignerOnce sync.Once
	policySignerErr  error
	policyPrivateKey ed25519.PrivateKey
	policyKeyID      string
)

// ---- Source: backend/security/secure_compare.go ----
// SecureCompare performs constant-time comparison of two secret strings.
// Returns false when either operand is empty or lengths differ.
func SecureCompare(a, b string) bool {
	a = strings.TrimSpace(a)
	b = strings.TrimSpace(b)
	if a == "" || b == "" {
		return false
	}
	if len(a) != len(b) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// ---- Source: backend/security/signature.go ----
var (
	ErrSecretNotConfigured = errors.New("signature secret not configured")
	ErrMissingSignature    = errors.New("missing signature")
	ErrMissingTimestamp    = errors.New("missing timestamp")
	ErrInvalidTimestamp    = errors.New("invalid timestamp format")
	ErrTimestampExpired    = errors.New("timestamp expired")
	ErrInvalidNonce        = errors.New("invalid nonce")
	ErrInvalidSignature    = errors.New("invalid signature")

	ErrEmptyPath     = errors.New("empty path")
	ErrPathTraversal = errors.New("path traversal detected")
	ErrAccessDenied  = errors.New("access denied")
)

// ---- Source: backend/security/update_package_sign.go ----
const (
	UpdatePackageSignatureAlg = "Ed25519"
	// UpdatePackageSignPrefix domain-separates update signatures from policy JCS signatures.
	UpdatePackageSignPrefix = "MonitorAgent-update-v1\n"
)

