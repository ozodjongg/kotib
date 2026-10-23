// Package control: egasi bot chatiga oddiy so'z bilan yozadi — kotib Mini App'da qilinadigan
// amallarni (sozlamalar, qoralamalar, vazifalar, plaginlar, obuna...) function calling orqali
// bajaradi. Har bir amal Mini App API'si orqali o'tadi (miniapp.Server.Invoke), shuning uchun
// tekshiruvlar, tarif cheklovlari va audit brauzerdagi bilan bir xil.
package control

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/ozodjongg/secretar_ai_agent/internal/llm"
	"github.com/ozodjongg/secretar_ai_agent/internal/ratelimit"
	"github.com/ozodjongg/secretar_ai_agent/internal/store"
)

// API — Mini App API'si (miniapp.Server).
type API interface {
	Invoke(ctx context.Context, user, account int64, method, path string, body any) (int, []byte)
}

const (
	// historyTTL — shuncha vaqt yozilmasa suhbat konteksti unutiladi.
	historyTTL = 30 * time.Minute
	maxHistory = 12
	maxMessage = 2000
	// runTimeout — bitta so'rov (bir necha vosita chaqiruvi + model).
	runTimeout = 2 * time.Minute
	maxUsers   = 5000
)

type session struct {
	mu    sync.Mutex // navbat: bitta foydalanuvchining so'rovlari ketma-ket
	turns []llm.ChatTurn
	last  time.Time // oxirgi so'rov (mu ostida)

	account int64     // tanlangan qo'shimcha akkaunt (0 — asosiy); Agent.mu ostida
	seen    time.Time // Agent.mu ostida
	bucket  *ratelimit.Bucket
}

// Agent — bot chatidagi erkin matnli so'rovlarni bajaradi.
type Agent struct {
	API   API
	LLM   llm.Controller
	Store store.Store
	// Ask — yozishma haqida savol (botcmd: asosiy va qo'shimcha akkauntlarda qidiradi); nil → o'chiq.
	Ask func(ctx context.Context, user int64, question string) string
	Log *slog.Logger

	mu       sync.Mutex
	sessions map[int64]*session
}

// ErrBusy — foydalanuvchi juda tez-tez yozmoqda.
var ErrBusy = errors.New("juda tez-tez so'rov")

func (a *Agent) log() *slog.Logger {
	if a.Log != nil {
		return a.Log
	}
	return slog.Default()
}

func (a *Agent) session(user int64) *session {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := time.Now()
	if a.sessions == nil {
		a.sessions = map[int64]*session{}
	}
	if len(a.sessions) > maxUsers {
		for id, s := range a.sessions {
			if now.Sub(s.seen) > historyTTL {
				delete(a.sessions, id)
			}
		}
	}
	s := a.sessions[user]
	if s == nil {
		// Model chaqiruvlari pullik: 10 ta ketma-ket, keyin har 20 soniyada bitta.
		s = &session{bucket: ratelimit.NewBucket(10, 1.0/20)}
		a.sessions[user] = s
	}
	if now.Sub(s.seen) > historyTTL {
		s.account = 0
	}
	s.seen = now
	return s
}

func (a *Agent) setAccount(user, account int64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if s := a.sessions[user]; s != nil {
		s.account = account
	}
}

func (a *Agent) account(user int64) int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	if s := a.sessions[user]; s != nil {
		return s.account
	}
	return 0
}

// Allow — foydalanuvchi hozir so'rov yubora oladimi (tezlik cheklovi).
func (a *Agent) Allow(user int64) bool {
	return a.session(user).bucket.Allow()
}

// Handle egasining xabarini bajaradi va bot chatiga yuboriladigan javobni qaytaradi.
// Bitta foydalanuvchining xabarlari navbat bilan: keyingisi oldingisining natijasini ko'radi.
func (a *Agent) Handle(ctx context.Context, user int64, text string) (string, error) {
	text = strings.TrimSpace(text)
	if r := []rune(text); len(r) > maxMessage {
		text = string(r[:maxMessage])
	}
	s := a.session(user)
	s.mu.Lock()
	defer s.mu.Unlock()
	if time.Since(s.last) > historyTTL {
		s.turns = nil
	}
	s.last = time.Now()

	ctx, cancel := context.WithTimeout(ctx, runTimeout)
	defer cancel()
	req := llm.ControlRequest{History: append([]llm.ChatTurn(nil), s.turns...), Message: text, Now: time.Now()}
	if a.Store != nil {
		if st, err := a.Store.GetSettings(ctx, user); err == nil {
			req.OwnerName, req.Now = st.OwnerName, time.Now().In(st.Location())
		}
	}
	tools := catalog()
	byName := map[string]tool{}
	for _, t := range tools {
		req.Tools = append(req.Tools, t.decl)
		byName[t.decl.Name] = t
	}
	req.Call = func(ctx context.Context, name string, args map[string]any) (string, error) {
		t, ok := byName[name]
		if !ok {
			return "", errors.New("bunday vosita yo'q")
		}
		if args == nil {
			args = map[string]any{}
		}
		return t.run(ctx, &call{a: a, user: user, account: a.account(user), args: args})
	}
	res, err := a.LLM.Control(ctx, req)
	if err != nil {
		a.log().Warn("control", "owner", user, "err", err)
		return "", err
	}
	if len(res.Tools) > 0 && a.Store != nil {
		a.Store.Audit(ctx, user, "bot_control", strings.Join(res.Tools, ","))
	}
	s.turns = append(s.turns, llm.ChatTurn{FromOwner: true, Text: text}, llm.ChatTurn{Text: res.Text})
	if len(s.turns) > maxHistory {
		s.turns = s.turns[len(s.turns)-maxHistory:]
	}
	return res.Text, nil
}
