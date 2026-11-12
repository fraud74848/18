package services

import (
	"context"
	"database/sql/driver"
	"github.com/google/uuid"
	"sync"
	"time"
)

var attendanceOperationSlots sync.Map // *sql.DB -> chan struct{}

// employee id -> channel closed once that employee's database lock is released.
// Releases run in the background (the reply does not wait for the unlock round
// trip), so a new operation on the same employee waits here first.
var attendanceLockReleasing sync.Map

func waitForPendingRelease(ctx context.Context, key string) {
	if v, ok := attendanceLockReleasing.Load(key); ok {
		select {
		case <-v.(chan struct{}):
		case <-ctx.Done():
		case <-time.After(3 * time.Second):
		}
	}
}

// Keep the entire rule check and settlement exclusive across web and Telegram,
// including during overlapping deployments. The lock is a session advisory lock
// on a pinned connection (the inbox already relies on session locks, so the
// database is a direct connection): one round trip to take it, and the release
// happens after the caller has its result. Existing business transactions remain
// responsible for their own writes.
func (s *WorkforceTelegramService) acquireAttendanceOperation(ctx context.Context, employeeID uuid.UUID) (func(), error) {
	if employeeID == uuid.Nil {
		return nil, ErrEmployeeNotFound
	}
	if sp, _ := ctx.Value(speculativeLockKey{}).(*speculativeLock); sp != nil {
		if release := sp.take(employeeID); release != nil {
			return release, nil
		}
	}
	return s.lockEmployeeOperation(ctx, employeeID)
}

func (s *WorkforceTelegramService) lockEmployeeOperation(ctx context.Context, employeeID uuid.UUID) (func(), error) {
	key := employeeID.String()
	waitForPendingRelease(ctx, key)
	// Each lock keeps a connection while the business code uses others. Reserve
	// two connections so concurrent employees cannot exhaust the small standalone
	// pool (one connection may also belong to the inbox owner).
	capacity := s.db.Stats().MaxOpenConnections - 2
	if capacity < 1 {
		capacity = 1
	}
	value, _ := attendanceOperationSlots.LoadOrStore(s.db, make(chan struct{}, capacity))
	slots := value.(chan struct{})
	select {
	case slots <- struct{}{}:
	default:
		return nil, ErrOperationBusy
	}
	releaseSlot := func() { <-slots }
	conn, err := s.db.Conn(ctx)
	if err != nil {
		releaseSlot()
		return nil, err
	}
	// A connection whose lock state is unknown is dropped, not returned to the
	// pool: the server releases every session lock when the connection closes.
	discard := func() { _ = conn.Raw(func(any) error { return driver.ErrBadConn }) }
	var acquired bool
	err = conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock(hashtextextended($1, 84392703))`, key).Scan(&acquired)
	if err != nil || !acquired {
		if err != nil {
			discard()
		}
		_ = conn.Close()
		releaseSlot()
		if err != nil {
			return nil, err
		}
		return nil, ErrOperationBusy
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			done := make(chan struct{})
			attendanceLockReleasing.Store(key, done)
			go func() {
				defer func() {
					close(done)
					attendanceLockReleasing.CompareAndDelete(key, done)
					releaseSlot()
				}()
				unlockCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				var released bool
				if err := conn.QueryRowContext(unlockCtx, `SELECT pg_advisory_unlock(hashtextextended($1, 84392703))`, key).Scan(&released); err != nil || !released {
					discard()
				}
				_ = conn.Close()
			}()
		})
	}, nil
}

// ---- Speculative lock: take the lock while the identity lookup runs ----
//
// The Telegram worker knows which employee a Telegram user was resolved to last
// time. It starts taking that employee's lock while the identity query runs, so
// the two round trips overlap. Identity is still read from the database on every
// request; the lock is only adopted when the lookup returns the same employee,
// otherwise it is released and the normal path runs.

type speculativeLockKey struct{}

var lastEmployeeByTelegramUser sync.Map // int64 telegram user id -> uuid.UUID

type speculativeLock struct {
	empID   uuid.UUID
	done    chan struct{}
	release func() // set once the lock is held
	mu      sync.Mutex
	used    bool
}

func (s *WorkforceTelegramService) beginSpeculativeLock(ctx context.Context, telegramUserID int64) (context.Context, *speculativeLock) {
	v, ok := lastEmployeeByTelegramUser.Load(telegramUserID)
	if !ok {
		return ctx, nil
	}
	emp, _ := v.(uuid.UUID)
	if emp == uuid.Nil {
		return ctx, nil
	}
	sp := &speculativeLock{empID: emp, done: make(chan struct{})}
	go func() {
		defer close(sp.done)
		if release, err := s.lockEmployeeOperation(ctx, emp); err == nil {
			sp.release = release
		}
	}()
	return context.WithValue(ctx, speculativeLockKey{}, sp), sp
}

// take hands the held lock to the operation for the same employee, once.
func (sp *speculativeLock) take(emp uuid.UUID) func() {
	if sp == nil || sp.empID != emp {
		return nil
	}
	<-sp.done
	sp.mu.Lock()
	defer sp.mu.Unlock()
	if sp.used || sp.release == nil {
		return nil
	}
	sp.used = true
	return sp.release
}

// discard releases the lock when no operation adopted it. Safe to call repeatedly.
func (sp *speculativeLock) discard() {
	if sp == nil {
		return
	}
	<-sp.done
	sp.mu.Lock()
	defer sp.mu.Unlock()
	if !sp.used && sp.release != nil {
		sp.used = true
		sp.release()
	}
}

// noteResolvedEmployee records the identity result for the next request and
// drops a speculative lock that turned out to be for a different employee.
func (s *WorkforceTelegramService) noteResolvedEmployee(ctx context.Context, telegramUserID int64, empID uuid.UUID, err error, pinned bool) {
	if pinned {
		return
	}
	if err != nil {
		lastEmployeeByTelegramUser.Delete(telegramUserID)
	} else {
		lastEmployeeByTelegramUser.Store(telegramUserID, empID)
	}
	if sp, _ := ctx.Value(speculativeLockKey{}).(*speculativeLock); sp != nil && (err != nil || sp.empID != empID) {
		sp.discard()
	}
}
