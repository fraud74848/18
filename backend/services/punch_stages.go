package services

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"
)

// Per-operation stage timing for activity punches. It only measures and logs;
// it never changes what a request reads or writes. Marks are taken from the
// request goroutine, in order, so each value is the time since the previous mark:
//
//	[punch] op=activity_start settings_ms=.. identity_ms=.. lock_ms=.. pre_ms=..
//	        state_ms=.. rules_ms=.. insert_ms=.. reply_ms=.. tail_ms=.. total_ms=.. failed=false
//
// tail_ms is everything after the last mark (releasing the advisory-lock
// transaction and returning), so the stages always add up to total_ms.
type punchStageKey struct{}

type punchStages struct {
	mu      sync.Mutex
	op      string
	started time.Time
	last    time.Time
	parts   []string
}

func withPunchStages(ctx context.Context, op string) (context.Context, *punchStages) {
	now := time.Now()
	st := &punchStages{op: op, started: now, last: now}
	return context.WithValue(ctx, punchStageKey{}, st), st
}

// stageMark is a no-op when the context carries no timer.
func stageMark(ctx context.Context, name string) {
	st, _ := ctx.Value(punchStageKey{}).(*punchStages)
	if st == nil {
		return
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	now := time.Now()
	st.parts = append(st.parts, fmt.Sprintf("%s_ms=%d", name, now.Sub(st.last).Milliseconds()))
	st.last = now
}

func (st *punchStages) finish(err error) {
	if st == nil {
		return
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	now := time.Now()
	st.parts = append(st.parts, fmt.Sprintf("tail_ms=%d", now.Sub(st.last).Milliseconds()))
	log.Printf("[punch] op=%s %s total_ms=%d failed=%t",
		st.op, strings.Join(st.parts, " "), now.Sub(st.started).Milliseconds(), err != nil)
}
