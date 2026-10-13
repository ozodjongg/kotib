package userbot

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ozodjongg/secretar_ai_agent/internal/miniapp"
	"github.com/ozodjongg/secretar_ai_agent/internal/store"
)

// fakeConn — Connector: Run darhol runErr qaytaradi; Login ctx tugaguncha (yoki kod kelguncha) kutadi.
type fakeConn struct {
	runErr  error
	loginFn func(ctx context.Context, io LoginIO) error
}

func (f *fakeConn) Login(ctx context.Context, _ int64, _ string, io LoginIO) error {
	if f.loginFn != nil {
		return f.loginFn(ctx, io)
	}
	<-ctx.Done()
	return ctx.Err()
}

func (f *fakeConn) Run(ctx context.Context, _ int64, _ func(Telegram), _ func(Incoming)) error {
	return f.runErr
}

func newManager(conn Connector) (*Manager, *store.Memory) {
	st := store.NewMemory()
	m := &Manager{Store: st, Conn: conn}
	m.init(context.Background())
	return m, st
}

// Regressiya: runLoop ichidagi FatalError ilgari breaker → stopWorker → o'z done kanalini
// kutib, abadiy qotib qolardi.
func TestFatalRunErrorDoesNotDeadlock(t *testing.T) {
	m, st := newManager(&fakeConn{runErr: &FatalError{Reason: "SESSION_REVOKED"}})
	ctx := context.Background()
	_ = st.UpsertAccount(ctx, store.Account{OwnerID: 7, Status: store.StatusActive})
	m.startWorker(7)

	deadline := time.After(3 * time.Second)
	for {
		acc, _ := st.GetAccount(ctx, 7)
		m.mu.Lock()
		_, running := m.workers[7]
		m.mu.Unlock()
		if acc.Status == store.StatusBroken && !running {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("worker to'xtamadi yoki holat broken emas: %s running=%v", acc.Status, running)
		case <-time.After(10 * time.Millisecond):
		}
	}
	done := make(chan struct{})
	go func() { m.Shutdown(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Shutdown qotib qoldi")
	}
}

func TestLoginAttemptsCapped(t *testing.T) {
	m, _ := newManager(&fakeConn{loginFn: func(context.Context, LoginIO) error { return errors.New("bad code") }})
	ctx := context.Background()
	for i := 0; i < loginMaxPerHour; i++ {
		if err := m.BeginLogin(ctx, 3, "+998901234567"); err != nil {
			t.Fatalf("urinish %d: %v", i, err)
		}
		waitStep(t, m, 3, "error")
	}
	if err := m.BeginLogin(ctx, 3, "+998901234567"); !errors.Is(err, miniapp.ErrTooManyAttempts) {
		t.Fatalf("limit kutilgan: %v", err)
	}
}

func TestRevokeCancelsPendingLogin(t *testing.T) {
	m, _ := newManager(&fakeConn{}) // Login ctx tugaguncha kutadi
	ctx := context.Background()
	if err := m.BeginLogin(ctx, 4, "+998901234567"); err != nil {
		t.Fatal(err)
	}
	_ = m.Revoke(ctx, 4)
	if st := m.LoginState(4).Step; st != "idle" {
		t.Fatalf("login bekor qilinishi kerak: %s", st)
	}
}

func waitStep(t *testing.T, m *Manager, owner int64, step string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for m.LoginState(owner).Step != step {
		if time.Now().After(deadline) {
			t.Fatalf("step %q kutilgan, bor: %q", step, m.LoginState(owner).Step)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
