package userbot

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/ozodjongg/secretar_ai_agent/internal/miniapp"
	"github.com/ozodjongg/secretar_ai_agent/internal/store"
)

type loginSession struct {
	mu     sync.Mutex
	state  miniapp.LoginState
	codeCh chan string
	pwCh   chan string
	cancel context.CancelFunc
	done   chan struct{} // login goroutine tugaganda yopiladi
}

func (l *loginSession) set(step, errMsg string) {
	l.mu.Lock()
	l.state = miniapp.LoginState{Step: step, Error: errMsg}
	l.mu.Unlock()
}

func (l *loginSession) get() miniapp.LoginState {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.state
}

// loginIO Connector.Login ga beriladi: kod/parol Mini App'dan kelguncha bloklanadi.
type loginIO struct{ l *loginSession }

func (io loginIO) Code(ctx context.Context, via, hint string) (string, error) {
	io.l.mu.Lock()
	io.l.state = miniapp.LoginState{Step: "wait_code", Via: via, Hint: hint}
	io.l.mu.Unlock()
	return recv(ctx, io.l.codeCh)
}

func (io loginIO) QR(url, image string, expires time.Time) {
	io.l.mu.Lock()
	io.l.state = miniapp.LoginState{Step: "wait_qr", QRURL: url, QRImage: image, QRExpires: &expires}
	io.l.mu.Unlock()
}

func (io loginIO) Password(ctx context.Context) (string, error) {
	io.l.set("wait_password", "")
	return recv(ctx, io.l.pwCh)
}

func recv(ctx context.Context, ch <-chan string) (string, error) {
	select {
	case v := <-ch:
		return v, nil
	case <-ctx.Done():
		return "", errors.New("kutish vaqti tugadi")
	}
}

const (
	loginTimeout = 5 * time.Minute
	// Bir soatda ko'pi bilan shuncha login urinishi: kod/parolni tanlab topish (brute-force)
	// va Telegram'ning PHONE_CODE/FLOOD cheklovlariga tushib qolishdan himoya.
	loginMaxPerHour = 5
)

var ErrTooManyLogins = miniapp.ErrTooManyAttempts

// ErrWrongAccount — tasdiqlangan Telegram akkaunt Mini App egasiniki emas (boshqa akkauntni
// birovning kotibiga ulab bo'lmaydi). Bunday sessiya darhol uziladi.
var ErrWrongAccount = errors.New("boshqa Telegram akkaunt tasdiqladi")

func (m *Manager) BeginLogin(ctx context.Context, owner int64, phone string) error {
	return m.beginLogin(ctx, owner, func(lctx context.Context, io LoginIO) error {
		return m.Conn.Login(lctx, owner, phone, io)
	})
}

// BeginQRLogin kodsiz kirish: Telegram ilovasida tasdiqlanadigan token (SMS/kod kelmaganda ham ishlaydi).
func (m *Manager) BeginQRLogin(ctx context.Context, owner int64) error {
	return m.beginLogin(ctx, owner, func(lctx context.Context, io LoginIO) error {
		return m.Conn.LoginQR(lctx, owner, io)
	})
}

func (m *Manager) beginLogin(ctx context.Context, owner int64, run func(context.Context, LoginIO) error) error {
	m.init(ctx)
	m.mu.Lock()
	if old := m.logins[owner]; old != nil && old.done != nil {
		if st := old.get().Step; st != "done" && st != "error" {
			// Tugallanmagan eski urinish (ekran yangilangan, tugma qayta bosilgan): uni bekor qilamiz.
			// Eski Telegram ulanishi to'liq yopilishini kutamiz, toki bitta sessiyaga ikki ulanish
			// bo'lmasin (AUTH_KEY_DUPLICATED).
			m.mu.Unlock()
			old.cancel()
			select {
			case <-old.done:
			case <-time.After(15 * time.Second):
				return errors.New("oldingi ulanish hali yopilmadi, birozdan keyin urinib ko'ring")
			}
			m.mu.Lock()
			if cur := m.logins[owner]; cur != nil && cur != old {
				if st := cur.get().Step; st != "done" && st != "error" {
					m.mu.Unlock()
					return errors.New("ulanish allaqachon jarayonda")
				}
			}
		}
	}
	if _, running := m.workers[owner]; running {
		m.mu.Unlock()
		return errors.New("akkaunt allaqachon ulangan")
	}
	now := time.Now()
	recent := m.loginTimes[owner][:0]
	for _, t := range m.loginTimes[owner] {
		if now.Sub(t) < time.Hour {
			recent = append(recent, t)
		}
	}
	if len(recent) >= loginMaxPerHour {
		m.loginTimes[owner] = recent
		m.mu.Unlock()
		return ErrTooManyLogins
	}
	m.loginTimes[owner] = append(recent, now)
	lctx, cancel := context.WithTimeout(context.WithoutCancel(m.BaseCtx), loginTimeout)
	ls := &loginSession{codeCh: make(chan string, 1), pwCh: make(chan string, 1), cancel: cancel, done: make(chan struct{})}
	ls.set("connecting", "")
	m.logins[owner] = ls
	m.mu.Unlock()

	go func() {
		defer close(ls.done)
		defer cancel()
		err := run(lctx, loginIO{ls})
		if err == nil && lctx.Err() != nil {
			err = lctx.Err() // bekor qilingan (akkaunt o'chirildi) yoki muddati o'tdi
		}
		if err != nil {
			// Xato tafsilotini mijozga ko'rsatmaymiz (telefon/kod bo'lishi mumkin) — umumiy matn.
			m.Log.Warn("login", "owner", owner, "err", err)
			msg := "Ulanib bo'lmadi. Kod/parolni tekshirib, qayta urinib ko'ring."
			if errors.Is(err, ErrWrongAccount) {
				msg = "Boshqa Telegram akkaunt tasdiqladi. Kotibni ochgan akkauntingizning o'zida tasdiqlang."
			}
			ls.set("error", msg)
			m.Store.Audit(context.Background(), owner, "login_failed", "")
			return
		}
		acc, _ := m.Store.GetAccount(context.Background(), owner)
		acc.OwnerID, acc.Status, acc.StatusNote = owner, store.StatusActive, ""
		if err := m.Store.UpsertAccount(context.Background(), acc); err != nil {
			ls.set("error", "Saqlashda xatolik.")
			return
		}
		m.Store.Audit(context.Background(), owner, "login_ok", "")
		ls.set("done", "")
		m.startWorker(owner)
	}()
	return nil
}

func (m *Manager) SubmitCode(owner int64, code string) error {
	return m.submit(owner, "wait_code", code, func(l *loginSession) chan string { return l.codeCh })
}

func (m *Manager) SubmitPassword(owner int64, pw string) error {
	return m.submit(owner, "wait_password", pw, func(l *loginSession) chan string { return l.pwCh })
}

func (m *Manager) submit(owner int64, wantStep, v string, ch func(*loginSession) chan string) error {
	m.mu.Lock()
	l := m.logins[owner]
	m.mu.Unlock()
	if l == nil || l.get().Step != wantStep {
		return errors.New("kutilmayapti")
	}
	select {
	case ch(l) <- v:
		l.set("connecting", "")
		return nil
	default:
		return errors.New("band")
	}
}

// cancelLogin jarayondagi loginni bekor qiladi (masalan, akkaunt o'chirilganda).
func (m *Manager) cancelLogin(owner int64) {
	m.mu.Lock()
	l := m.logins[owner]
	delete(m.logins, owner)
	m.mu.Unlock()
	if l != nil && l.cancel != nil {
		l.cancel()
	}
}

func (m *Manager) LoginState(owner int64) miniapp.LoginState {
	m.mu.Lock()
	l := m.logins[owner]
	m.mu.Unlock()
	if l == nil {
		return miniapp.LoginState{Step: "idle"}
	}
	return l.get()
}
