package userbot

import (
	"context"
	"errors"
	"strings"
	"sync"
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
		// Sessiya bekor qilingan: kutish foydasiz — "revoked" (Mini App qayta ulashni so'raydi).
		if acc.Status == store.StatusRevoked && !running {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("worker to'xtamadi yoki holat revoked emas: %s running=%v", acc.Status, running)
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

func waitStatus(t *testing.T, st *store.Memory, owner int64, want store.AccountStatus) store.Account {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		acc, _ := st.GetAccount(context.Background(), owner)
		if acc.Status == want {
			return acc
		}
		select {
		case <-deadline:
			t.Fatalf("holat %s emas: %s", want, acc.Status)
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func TestFatalReasons(t *testing.T) {
	// AUTH_KEY_DUPLICATED — sessiya o'lik: qayta ulash kerak.
	m, st := newManager(&fakeConn{runErr: &FatalError{Reason: "AUTH_KEY_DUPLICATED"}})
	var notes []string
	m.Notify = func(_ context.Context, _ int64, text string, _ bool) { notes = append(notes, text) }
	_ = st.UpsertAccount(context.Background(), store.Account{OwnerID: 7, Status: store.StatusActive})
	m.startWorker(7)
	acc := waitStatus(t, st, 7, store.StatusRevoked)
	if !strings.Contains(acc.StatusNote, "AUTH_KEY_DUPLICATED") {
		t.Fatalf("izoh: %q", acc.StatusNote)
	}
	// PEER_FLOOD — cheklov: kutish mumkin, "broken".
	m2, st2 := newManager(&fakeConn{runErr: &FatalError{Reason: "PEER_FLOOD"}})
	_ = st2.UpsertAccount(context.Background(), store.Account{OwnerID: 7, Status: store.StatusActive})
	m2.startWorker(7)
	waitStatus(t, st2, 7, store.StatusBroken)
}

// countConn — Run chaqiruvlarini sanaydi va ctx tugaguncha ishlaydi.
type countConn struct {
	fakeConn
	runs chan struct{}
}

func (c *countConn) Run(ctx context.Context, _ int64, _ func(Telegram), _ func(Incoming)) error {
	c.runs <- struct{}{}
	<-ctx.Done()
	return ctx.Err()
}

func TestGateDelaysTelegramConnect(t *testing.T) {
	c := &countConn{runs: make(chan struct{}, 4)}
	m, st := newManager(c)
	gate := make(chan struct{})
	m.Gate = gate
	_ = st.UpsertAccount(context.Background(), store.Account{OwnerID: 7, Status: store.StatusActive})
	m.startWorker(7)
	select {
	case <-c.runs:
		t.Fatal("gate ochilmasdan Telegram'ga ulanmasligi kerak")
	case <-time.After(100 * time.Millisecond):
	}
	close(gate)
	select {
	case <-c.runs:
	case <-time.After(2 * time.Second):
		t.Fatal("gate ochilgach ulanishi kerak")
	}
	m.Shutdown()
}

func waitLogin(t *testing.T, m *Manager, owner int64, step string) miniapp.LoginState {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		st := m.LoginState(owner)
		if st.Step == step {
			return st
		}
		select {
		case <-deadline:
			t.Fatalf("login holati %q emas: %+v", step, st)
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func TestPhoneLoginFlow(t *testing.T) {
	conn := &fakeConn{loginFn: func(ctx context.Context, io LoginIO) error {
		code, err := io.Code(ctx, "sms", "")
		if err != nil {
			return err
		}
		if code != "12345" {
			return errors.New("kod noto'g'ri")
		}
		_, err = io.Password(ctx)
		return err
	}}
	m, st := newManager(conn)
	connected := make(chan int64, 1)
	m.OnConnected = func(_ context.Context, owner int64) { connected <- owner }
	ctx := context.Background()
	now := time.Now()
	_ = st.UpsertAccount(ctx, store.Account{OwnerID: 7, Status: store.StatusRevoked, ConsentAt: &now})
	if err := m.BeginLogin(ctx, 7, "+998901234567"); err != nil {
		t.Fatal(err)
	}
	if cs := waitLogin(t, m, 7, "wait_code"); cs.Via != "sms" {
		t.Fatalf("kod holati: %+v", cs)
	}
	if err := m.SubmitPassword(7, "x"); err == nil {
		t.Fatal("kod kutilayotganda parol qabul qilinmasligi kerak")
	}
	if err := m.SubmitCode(7, "12345"); err != nil {
		t.Fatal(err)
	}
	waitLogin(t, m, 7, "wait_password")
	if err := m.SubmitPassword(7, "secret"); err != nil {
		t.Fatal(err)
	}
	waitLogin(t, m, 7, "done")
	if acc, _ := st.GetAccount(ctx, 7); acc.Status != store.StatusActive {
		t.Fatalf("akkaunt faol bo'lishi kerak: %s", acc.Status)
	}
	select {
	case o := <-connected:
		if o != 7 {
			t.Fatalf("OnConnected egasi: %d", o)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("OnConnected chaqirilmadi (referal mukofoti)")
	}
	m.Shutdown()
}

func TestLoginWrongAccount(t *testing.T) {
	m, st := newManager(&fakeConn{loginFn: func(context.Context, LoginIO) error { return ErrWrongAccount }})
	ctx := context.Background()
	_ = st.UpsertAccount(ctx, store.Account{OwnerID: 7, Status: store.StatusRevoked})
	if err := m.BeginLogin(ctx, 7, "+998901234567"); err != nil {
		t.Fatal(err)
	}
	es := waitLogin(t, m, 7, "error")
	if !strings.Contains(es.Error, "boshqa Telegram akkaunt") {
		t.Fatalf("xato matni: %q", es.Error)
	}
	if acc, _ := st.GetAccount(ctx, 7); acc.Status == store.StatusActive {
		t.Fatal("boshqa akkaunt bilan faollashmasligi kerak")
	}
}

func TestNewLoginReplacesStuckOne(t *testing.T) {
	var mu sync.Mutex
	started := 0
	conn := &fakeConn{loginFn: func(ctx context.Context, io LoginIO) error {
		mu.Lock()
		started++
		mu.Unlock()
		_, err := io.Code(ctx, "app", "")
		return err
	}}
	m, st := newManager(conn)
	ctx := context.Background()
	_ = st.UpsertAccount(ctx, store.Account{OwnerID: 7, Status: store.StatusRevoked})
	if err := m.BeginLogin(ctx, 7, "+998901234567"); err != nil {
		t.Fatal(err)
	}
	waitLogin(t, m, 7, "wait_code")
	// Ekran yangilandi va tugma qayta bosildi: eski urinish bekor bo'lib, yangisi boshlanadi.
	if err := m.BeginLogin(ctx, 7, "+998901234568"); err != nil {
		t.Fatalf("qayta urinish rad etilmasligi kerak: %v", err)
	}
	waitLogin(t, m, 7, "wait_code")
	mu.Lock()
	n := started
	mu.Unlock()
	if n != 2 {
		t.Fatalf("ikkita urinish boshlanishi kerak: %d", n)
	}
	m.CancelLogin(7)
}

// «Orqaga»: kod kutilayotgan ulanish bekor qilinadi, holat idle'ga qaytadi va telefon
// raqamini qaytadan kiritish mumkin (Mini App qotib qolmaydi).
func TestCancelLoginReturnsToIdle(t *testing.T) {
	var mu sync.Mutex
	exited := 0
	conn := &fakeConn{loginFn: func(ctx context.Context, io LoginIO) error {
		defer func() { mu.Lock(); exited++; mu.Unlock() }()
		_, err := io.Code(ctx, "sms", "")
		return err
	}}
	m, st := newManager(conn)
	ctx := context.Background()
	_ = st.UpsertAccount(ctx, store.Account{OwnerID: 7, Status: store.StatusPending})
	if err := m.BeginLogin(ctx, 7, "+998901234567"); err != nil {
		t.Fatal(err)
	}
	waitLogin(t, m, 7, "wait_code")
	m.CancelLogin(7)
	mu.Lock()
	n := exited
	mu.Unlock()
	if n != 1 {
		t.Fatal("CancelLogin Telegram ulanishi yopilishini kutishi kerak")
	}
	if st := m.LoginState(7); st.Step != "idle" {
		t.Fatalf("bekor qilingach idle bo'lishi kerak: %+v", st)
	}
	if err := m.SubmitCode(7, "12345"); err == nil {
		t.Fatal("bekor qilingan ulanishga kod qabul qilinmasligi kerak")
	}
	if err := m.BeginLogin(ctx, 7, "+998901234567"); err != nil {
		t.Fatalf("qayta boshlash: %v", err)
	}
	waitLogin(t, m, 7, "wait_code")
	m.CancelLogin(7)
	m.CancelLogin(7) // ikki marta — xavfsiz
}
