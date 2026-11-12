package services

import (
	"bytes"
	"context"
	"errors"
	"log"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestPunchStagesLogsMarksInOrderAndTail(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)

	ctx, st := withPunchStages(context.Background(), "activity_start")
	stageMark(ctx, "identity")
	stageMark(ctx, "lock")
	st.finish(errors.New("boom"))

	line := buf.String()
	for _, want := range []string{"[punch] op=activity_start", "identity_ms=", "lock_ms=", "tail_ms=", "total_ms=", "failed=true"} {
		if !strings.Contains(line, want) {
			t.Fatalf("log line missing %q: %s", want, line)
		}
	}
	if strings.Index(line, "identity_ms=") > strings.Index(line, "lock_ms=") {
		t.Fatalf("stages out of order: %s", line)
	}
}

func TestStageMarkWithoutTimerIsNoop(t *testing.T) {
	stageMark(context.Background(), "identity") // must not panic
	var st *punchStages
	st.finish(nil) // nil timer must be tolerated
}

func TestTelegramRequestStorePrimesCacheOnce(t *testing.T) {
	s := &WorkforceTelegramService{}
	ctx := WithTelegramRequestCache(context.Background())
	telegramRequestStore(ctx, s, "k", "first")
	telegramRequestStore(ctx, s, "k", "second")
	got, err := telegramRequestRead(ctx, s, "k", func() (string, error) {
		t.Fatal("primed value must not be read again")
		return "", nil
	})
	if err != nil || got != "first" {
		t.Fatalf("got %q err=%v", got, err)
	}
	// Without a request cache nothing is stored and reads fall through.
	telegramRequestStore(context.Background(), s, "k", "ignored")
}

func TestSettingsCacheServesFreshEntryAndInvalidates(t *testing.T) {
	s := &WorkforceTelegramService{}
	want := &TelegramSettings{Timezone: "Asia/Singapore"}
	s.settingsCached, s.settingsCachedAt = want, time.Now()
	got, err := s.loadTelegramSettingsCached(context.Background()) // no db: must come from the cache
	if err != nil || got != want {
		t.Fatalf("fresh entry should be served from cache, got %v err=%v", got, err)
	}
	s.invalidateTelegramSettingsCache()
	if s.settingsCached != nil {
		t.Fatal("invalidate must drop the cached row")
	}
}

func TestSpeculativeLockAdoptedOnlyByMatchingEmployeeOnce(t *testing.T) {
	emp := uuid.New()
	released := 0
	sp := &speculativeLock{empID: emp, done: make(chan struct{}), release: func() { released++ }}
	close(sp.done)
	if sp.take(uuid.New()) != nil {
		t.Fatal("a different employee must not adopt the lock")
	}
	release := sp.take(emp)
	if release == nil {
		t.Fatal("the matching employee should adopt the lock")
	}
	if sp.take(emp) != nil {
		t.Fatal("the lock can be adopted only once")
	}
	sp.discard() // already adopted: must not release it again
	if released != 0 {
		t.Fatalf("adopted lock released early: %d", released)
	}
	release()
	if released != 1 {
		t.Fatalf("release should run once, got %d", released)
	}
	var none *speculativeLock
	none.discard() // nil receiver is a no-op
	if none.take(emp) != nil {
		t.Fatal("nil speculative lock must not hand out a lock")
	}
}

func TestNoteResolvedEmployeeDropsMismatchedSpeculativeLock(t *testing.T) {
	s := &WorkforceTelegramService{}
	const tg = int64(987654321)
	defer lastEmployeeByTelegramUser.Delete(tg)
	guess, actual := uuid.New(), uuid.New()
	released := 0
	sp := &speculativeLock{empID: guess, done: make(chan struct{}), release: func() { released++ }}
	close(sp.done)
	ctx := context.WithValue(context.Background(), speculativeLockKey{}, sp)

	s.noteResolvedEmployee(ctx, tg, actual, nil, false)
	if released != 1 {
		t.Fatalf("mismatched speculative lock must be released, got %d", released)
	}
	if v, _ := lastEmployeeByTelegramUser.Load(tg); v != actual {
		t.Fatalf("resolved employee should be remembered, got %v", v)
	}
	s.noteResolvedEmployee(ctx, tg, actual, errors.New("not bound"), false)
	if _, ok := lastEmployeeByTelegramUser.Load(tg); ok {
		t.Fatal("a failed lookup must forget the guess")
	}
	// Device-pinned requests neither remember nor release anything.
	s.noteResolvedEmployee(ctx, tg, actual, nil, true)
	if _, ok := lastEmployeeByTelegramUser.Load(tg); ok {
		t.Fatal("pinned requests must not remember an employee")
	}
}

func TestWaitForPendingReleaseBlocksUntilClosed(t *testing.T) {
	const key = "pending-release-test"
	done := make(chan struct{})
	attendanceLockReleasing.Store(key, done)
	defer attendanceLockReleasing.Delete(key)
	finished := make(chan struct{})
	go func() {
		waitForPendingRelease(context.Background(), key)
		close(finished)
	}()
	select {
	case <-finished:
		t.Fatal("must wait while the previous release is pending")
	case <-time.After(50 * time.Millisecond):
	}
	close(done)
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("must continue once the release finished")
	}
}
