// Consolidated source; original file boundaries are marked below.
package logx

import (
	log "log"
	os "os"
	strings "strings"
	sync "sync"
)

// ---- Source: backend/services/logx/logx.go ----
var (
	debugOnce    sync.Once
	debugEnabled bool
)

// DebugEnabled is true when LOG_LEVEL=debug, or ENV is not production.
func DebugEnabled() bool {
	debugOnce.Do(func() {
		level := strings.ToLower(strings.TrimSpace(os.Getenv("LOG_LEVEL")))
		if level == "debug" {
			debugEnabled = true
			return
		}
		env := strings.ToLower(strings.TrimSpace(os.Getenv("ENV")))
		debugEnabled = env != "production"
	})
	return debugEnabled
}

// Debug writes a debug line when DebugEnabled() is true.
func Debug(format string, v ...any) {
	if DebugEnabled() {
		log.Printf("[DEBUG] "+format, v...)
	}
}

// quietSuccessPaths — production 下 2xx 成功时不打 access log，避免刷屏。
var quietSuccessPaths = []string{
	"/api/v1/upload/chunk",
	"/api/v1/upload/complete",
	"/api/v1/activities/report",
}

