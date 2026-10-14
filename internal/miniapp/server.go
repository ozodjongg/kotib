package miniapp

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/ozodjongg/secretar_ai_agent/internal/billing"
	"github.com/ozodjongg/secretar_ai_agent/internal/config"
	"github.com/ozodjongg/secretar_ai_agent/internal/google"
	"github.com/ozodjongg/secretar_ai_agent/internal/ics"
	"github.com/ozodjongg/secretar_ai_agent/internal/ratelimit"
	"github.com/ozodjongg/secretar_ai_agent/internal/store"
)

var googleTmpl = template.Must(template.New("g").Parse(`<!doctype html><html lang="uz"><head><meta charset="utf-8">` +
	`<meta name="viewport" content="width=device-width, initial-scale=1"><title>Kotib · Google</title></head>` +
	`<body><h2>Kotib</h2><p>{{.}}</p></body></html>`))

// ErrTooManyAttempts — Controller login urinishlari chegarasidan oshganda qaytaradi.
var ErrTooManyAttempts = errors.New("juda ko'p urinish: birozdan keyin qayta urinib ko'ring")

// LoginState — Telegram akkauntni ulash jarayonining holati.
type LoginState struct {
	Step  string `json:"step"` // idle | wait_code | wait_password | done | error
	Error string `json:"error,omitempty"`
	// Via — kod qayerga yuborilgani (wait_code'da): app | sms | call | email | fragment.
	Via string `json:"via,omitempty"`
	// Hint — qo'shimcha: email shabloni (a***@gmail.com) yoki fragment havolasi.
	Hint string `json:"hint,omitempty"`
	// QR orqali kirish (wait_qr): tg://login havolasi, QR rasm (data:image/png) va muddati.
	QRURL     string     `json:"qr_url,omitempty"`
	QRImage   string     `json:"qr_image,omitempty"`
	QRExpires *time.Time `json:"qr_expires,omitempty"`
}

// ContactInfo — kontakt tanlash ro'yxati uchun (faqat egasiga ko'rsatiladi).
type ContactInfo struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// Controller — Mini App userbot menejeriga murojaat qiladigan interfeys.
type Controller interface {
	BeginLogin(ctx context.Context, owner int64, phone string) error
	// BeginQRLogin kodsiz kirish: Telegram ilovasida tasdiqlanadigan token (QR / tg://login havola).
	BeginQRLogin(ctx context.Context, owner int64) error
	SubmitCode(owner int64, code string) error
	SubmitPassword(owner int64, password string) error
	LoginState(owner int64) LoginState

	Pause(owner int64)
	Resume(ctx context.Context, owner int64) error
	// Revoke Telegram'da sessiyani tugatadi (auth.logOut), keyin lokal ma'lumotni o'chiradi.
	Revoke(ctx context.Context, owner int64) error
	SendDraft(ctx context.Context, owner int64, d store.Draft) error
	// Contacts mijozning Telegram kontaktlari (qoidalarda kontakt tanlash uchun).
	Contacts(ctx context.Context, owner int64) ([]ContactInfo, error)
}

// Billing — Telegram Stars obunasi (billing.Service). nil → to'lov o'chiq.
type Billing interface {
	Status(ctx context.Context, owner int64) (billing.Status, error)
	StartTrial(ctx context.Context, owner int64) error
	InvoiceLink(ctx context.Context, owner int64) (string, error)
	Cancel(ctx context.Context, owner int64) error
	Renew(ctx context.Context, owner int64) error
}

// Google — Google akkauntini ulash (google.Client). nil → integratsiya o'chiq.
type Google interface {
	AuthURL(owner int64) (string, error)
	Callback(ctx context.Context, state, code string) (int64, error)
	Status(ctx context.Context, owner int64) (bool, string)
	Disconnect(ctx context.Context, owner int64) error
}

type Server struct {
	BotToken string
	Store    store.Store
	Ctl      Controller
	Billing  Billing
	Google   Google
	// ICS — Google'siz taqvim havolalari (imzo tekshiruvi); nil → o'chiq.
	ICS *ics.Signer
	// BotUsername — deep link'lar uchun (t.me/<bot>?start=pause); bo'sh bo'lishi mumkin.
	BotUsername string
	// PublicURL — tashqi manzil (https://domen), avtomatlashtirish havolasi uchun.
	PublicURL string
	// OnGoogle — ulanish natijasi haqida egasiga bot xabari (ixtiyoriy).
	OnGoogle func(ctx context.Context, owner int64, err error)
	Log      *slog.Logger
	// Static — Mini App fayllari (index.html...) joylashgan papka.
	StaticDir string

	mu        sync.Mutex
	limiters  map[int64]*limiterEntry
	lastSweep time.Time
	authFails *ratelimit.Bucket // noto'g'ri initData bilan so'rovlar oqimini cheklaydi
}

type limiterEntry struct {
	b    *ratelimit.Bucket
	seen time.Time
}

type ctxKey struct{}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/me", s.auth(s.handleMe))
	mux.HandleFunc("POST /api/consent", s.auth(s.handleConsent))
	mux.HandleFunc("GET /api/settings", s.auth(s.handleGetSettings))
	mux.HandleFunc("PUT /api/settings", s.auth(s.handlePutSettings))
	mux.HandleFunc("POST /api/login/phone", s.auth(s.handleLoginPhone))
	mux.HandleFunc("POST /api/login/qr", s.auth(s.handleLoginQR))
	mux.HandleFunc("GET /tglogin", handleTGLogin)
	mux.HandleFunc("POST /api/login/code", s.auth(s.handleLoginCode))
	mux.HandleFunc("POST /api/login/password", s.auth(s.handleLoginPassword))
	mux.HandleFunc("GET /api/login/state", s.auth(s.handleLoginState))
	mux.HandleFunc("GET /api/contacts", s.auth(s.handleContacts))
	mux.HandleFunc("POST /api/pause", s.auth(s.handlePause))
	mux.HandleFunc("POST /api/resume", s.auth(s.handleResume))
	mux.HandleFunc("GET /api/drafts", s.auth(s.handleListDrafts))
	mux.HandleFunc("POST /api/drafts/{id}/send", s.auth(s.handleSendDraft))
	mux.HandleFunc("POST /api/drafts/{id}/reject", s.auth(s.handleRejectDraft))
	mux.HandleFunc("DELETE /api/account", s.auth(s.handleDeleteAccount))
	mux.HandleFunc("GET /api/activity", s.auth(s.handleActivity))
	mux.HandleFunc("GET /api/billing", s.auth(s.handleBilling))
	mux.HandleFunc("POST /api/billing/invoice", s.auth(s.handleInvoice))
	mux.HandleFunc("POST /api/billing/cancel", s.auth(s.handleBillingCancel))
	mux.HandleFunc("POST /api/billing/renew", s.auth(s.handleBillingRenew))
	mux.HandleFunc("POST /api/google/connect", s.auth(s.handleGoogleConnect))
	mux.HandleFunc("POST /api/google/disconnect", s.auth(s.handleGoogleDisconnect))
	mux.HandleFunc("GET /oauth/google/callback", s.handleGoogleCallback)
	mux.HandleFunc("GET /ics/{owner}/{id}/{sig}", s.handleICS)
	mux.HandleFunc("POST /api/hook", s.auth(s.handleHookCreate))
	mux.HandleFunc("DELETE /api/hook", s.auth(s.handleHookDelete))
	mux.HandleFunc("GET /hook/{token}", s.handleHook)
	mux.HandleFunc("POST /hook/{token}", s.handleHook)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })
	if s.StaticDir != "" {
		mux.Handle("GET /", staticFiles(s.StaticDir))
	}
	return secureHeaders(mux)
}

// staticFiles Mini App fayllarini beradi: papka ro'yxati (directory listing) va
// yashirin (".env", ".git"...) fayllar berilmaydi.
func staticFiles(dir string) http.Handler {
	fsrv := http.FileServer(http.Dir(dir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if strings.HasPrefix(p, "/api/") {
			httpErr(w, http.StatusNotFound, "topilmadi")
			return
		}
		for _, seg := range strings.Split(p, "/") {
			if strings.HasPrefix(seg, ".") {
				http.NotFound(w, r)
				return
			}
		}
		if strings.HasSuffix(p, "/") && p != "/" {
			http.NotFound(w, r)
			return
		}
		fsrv.ServeHTTP(w, r)
	})
}

func secureHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cache-Control", "no-store")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		h.Set("Cross-Origin-Resource-Policy", "same-origin")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=(), usb=()")
		// TLS reverse proxy'da tugaydi; brauzer HSTS'ni faqat HTTPS orqali qabul qiladi.
		h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		h.Set("Content-Security-Policy",
			"default-src 'none'; script-src 'self' https://telegram.org; style-src 'self'; "+
				"connect-src 'self'; img-src 'self' data:; base-uri 'none'; form-action 'none'; "+
				"frame-ancestors https://web.telegram.org https://*.telegram.org")
		next.ServeHTTP(w, r)
	})
}

// auth: "Authorization: tma <initData>" sarlavhasini tekshiradi, egasini kontekstga qo'yadi.
func (s *Server) auth(next func(http.ResponseWriter, *http.Request, int64)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		const pfx = "tma "
		hdr := r.Header.Get("Authorization")
		if !strings.HasPrefix(hdr, pfx) {
			httpErr(w, http.StatusUnauthorized, "avtorizatsiya kerak")
			return
		}
		raw := strings.TrimPrefix(hdr, pfx)
		if len(raw) > 8<<10 {
			httpErr(w, http.StatusUnauthorized, "initData yaroqsiz")
			return
		}
		u, err := ValidateInitData(raw, s.BotToken, 24*time.Hour, time.Now())
		if err != nil {
			if !s.allowAuthFailure() {
				httpErr(w, http.StatusTooManyRequests, "juda tez-tez so'rov")
				return
			}
			httpErr(w, http.StatusUnauthorized, "initData yaroqsiz")
			return
		}
		if !s.allow(u.ID) {
			httpErr(w, http.StatusTooManyRequests, "juda tez-tez so'rov")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 256<<10)
		next(w, r, u.ID)
	}
}

// allowAuthFailure — yaroqsiz initData'li so'rovlar uchun umumiy chegara
// (taxmin qilish/yuklama hujumlarini sekinlashtiradi).
func (s *Server) allowAuthFailure() bool {
	s.mu.Lock()
	if s.authFails == nil {
		s.authFails = ratelimit.NewBucket(60, 10)
	}
	b := s.authFails
	s.mu.Unlock()
	return b.Allow()
}

// allow — har mijoz uchun API tezlik cheklovi (brute-force: login kod/parol).
func (s *Server) allow(owner int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	if s.limiters == nil {
		s.limiters = map[int64]*limiterEntry{}
		s.lastSweep = now
	}
	// Eski yozuvlarni vaqti-vaqti bilan tozalaymiz (xotira cheksiz o'smasin).
	if now.Sub(s.lastSweep) > 10*time.Minute {
		for id, e := range s.limiters {
			if now.Sub(e.seen) > 30*time.Minute {
				delete(s.limiters, id)
			}
		}
		s.lastSweep = now
	}
	e, ok := s.limiters[owner]
	if !ok {
		e = &limiterEntry{b: ratelimit.NewBucket(30, 2)}
		s.limiters[owner] = e
	}
	e.seen = now
	return e.b.Allow()
}

func (s *Server) logger() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

// ---- Handlers ----

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request, owner int64) {
	acc, err := s.Store.GetAccount(r.Context(), owner)
	if errors.Is(err, store.ErrNotFound) {
		acc = store.Account{OwnerID: owner, Status: store.StatusPending}
	} else if err != nil {
		httpErr(w, 500, "xatolik")
		return
	}
	pending := 0
	if ds, err := s.Store.ListDrafts(r.Context(), owner, store.DraftPending, 100); err == nil {
		pending = len(ds)
	}
	out := map[string]any{
		"owner_id":       owner,
		"status":         acc.Status,
		"note":           acc.StatusNote,
		"consented":      acc.ConsentAt != nil,
		"presets":        config.Presets,
		"pending_drafts": pending,
		"billing":        billing.Status{Active: true},
	}
	if s.Billing != nil {
		if bs, err := s.Billing.Status(r.Context(), owner); err == nil {
			out["billing"] = bs
		}
	}
	g := map[string]any{"enabled": s.Google != nil, "connected": false, "email": ""}
	if s.Google != nil {
		g["connected"], g["email"] = s.Google.Status(r.Context(), owner)
	}
	out["google"] = g
	hasHook, _ := s.Store.HasHook(r.Context(), owner)
	out["hook"] = hasHook
	out["bot_username"] = s.BotUsername
	writeJSON(w, 200, out)
}

func (s *Server) handleConsent(w http.ResponseWriter, r *http.Request, owner int64) {
	var in struct {
		Accepted bool `json:"accepted"`
	}
	if !readJSON(w, r, &in) || !in.Accepted {
		httpErr(w, 400, "rozilik kerak")
		return
	}
	now := time.Now()
	acc, err := s.Store.GetAccount(r.Context(), owner)
	if errors.Is(err, store.ErrNotFound) {
		acc = store.Account{OwnerID: owner, Status: store.StatusPending}
	} else if err != nil {
		httpErr(w, 500, "xatolik")
		return
	}
	acc.ConsentAt = &now
	if err := s.Store.UpsertAccount(r.Context(), acc); err != nil {
		httpErr(w, 500, "xatolik")
		return
	}
	s.Store.Audit(r.Context(), owner, "consent", "")
	if s.Billing != nil {
		if err := s.Billing.StartTrial(r.Context(), owner); err != nil {
			s.logger().Warn("start trial", "owner", owner, "err", err)
		}
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request, owner int64) {
	st, err := s.Store.GetSettings(r.Context(), owner)
	if err != nil {
		httpErr(w, 500, "xatolik")
		return
	}
	if st.Rules == nil {
		st.Rules = []config.Rule{}
	}
	writeJSON(w, 200, st)
}

func (s *Server) handlePutSettings(w http.ResponseWriter, r *http.Request, owner int64) {
	var st config.Settings
	if !readJSON(w, r, &st) {
		return
	}
	if err := st.Validate(); err != nil {
		httpErr(w, 400, err.Error())
		return
	}
	// Sozlama jadvali accounts'ga bog'liq: rozilikdan oldin ham saqlash mumkin bo'lsin.
	if _, err := s.Store.GetAccount(r.Context(), owner); errors.Is(err, store.ErrNotFound) {
		if err := s.Store.UpsertAccount(r.Context(), store.Account{OwnerID: owner, Status: store.StatusPending}); err != nil {
			httpErr(w, 500, "xatolik")
			return
		}
	} else if err != nil {
		httpErr(w, 500, "xatolik")
		return
	}
	if err := s.Store.SaveSettings(r.Context(), owner, st); err != nil {
		httpErr(w, 500, "xatolik")
		return
	}
	s.Store.Audit(r.Context(), owner, "settings_saved", "mode="+string(st.Mode))
	writeJSON(w, 200, map[string]bool{"ok": true})
}

var phoneRe = regexp.MustCompile(`^\+[1-9]\d{7,14}$`)
var codeRe = regexp.MustCompile(`^\d{4,8}$`)

func (s *Server) requireConsent(w http.ResponseWriter, r *http.Request, owner int64) bool {
	acc, err := s.Store.GetAccount(r.Context(), owner)
	if err != nil || acc.ConsentAt == nil {
		httpErr(w, http.StatusForbidden, "avval rozilik bering")
		return false
	}
	return true
}

func (s *Server) handleLoginPhone(w http.ResponseWriter, r *http.Request, owner int64) {
	if !s.requireConsent(w, r, owner) {
		return
	}
	var in struct {
		Phone string `json:"phone"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	phone := strings.ReplaceAll(strings.TrimSpace(in.Phone), " ", "")
	if !phoneRe.MatchString(phone) {
		httpErr(w, 400, "telefon +998901234567 formatida bo'lsin")
		return
	}
	// MUHIM: kontekst so'rovdan emas — login jarayoni so'rov tugagandan keyin ham davom etadi.
	if err := s.Ctl.BeginLogin(context.WithoutCancel(r.Context()), owner, phone); err != nil {
		if errors.Is(err, ErrTooManyAttempts) {
			httpErr(w, http.StatusTooManyRequests, err.Error())
			return
		}
		s.logger().Warn("begin login", "owner", owner, "err", err)
		httpErr(w, 400, "ulanishni boshlab bo'lmadi")
		return
	}
	s.Store.Audit(r.Context(), owner, "login_begin", "")
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) handleLoginCode(w http.ResponseWriter, r *http.Request, owner int64) {
	if !s.requireConsent(w, r, owner) {
		return
	}
	var in struct {
		Code string `json:"code"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	code := strings.NewReplacer(" ", "", "-", "").Replace(in.Code)
	if !codeRe.MatchString(code) {
		httpErr(w, 400, "kod 4–8 raqam")
		return
	}
	if err := s.Ctl.SubmitCode(owner, code); err != nil {
		httpErr(w, 409, "kod kutilmayapti")
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) handleLoginPassword(w http.ResponseWriter, r *http.Request, owner int64) {
	if !s.requireConsent(w, r, owner) {
		return
	}
	var in struct {
		Password string `json:"password"`
	}
	if !readJSON(w, r, &in) || in.Password == "" || len(in.Password) > 256 {
		httpErr(w, 400, "parol noto'g'ri")
		return
	}
	// Parol faqat shu yerdan o'tadi: saqlanmaydi, log qilinmaydi.
	if err := s.Ctl.SubmitPassword(owner, in.Password); err != nil {
		httpErr(w, 409, "parol kutilmayapti")
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) handleLoginQR(w http.ResponseWriter, r *http.Request, owner int64) {
	if !s.requireConsent(w, r, owner) {
		return
	}
	// MUHIM: kontekst so'rovdan emas — login jarayoni so'rov tugagandan keyin ham davom etadi.
	if err := s.Ctl.BeginQRLogin(context.WithoutCancel(r.Context()), owner); err != nil {
		if errors.Is(err, ErrTooManyAttempts) {
			httpErr(w, http.StatusTooManyRequests, err.Error())
			return
		}
		s.logger().Warn("begin qr login", "owner", owner, "err", err)
		httpErr(w, 400, "ulanishni boshlab bo'lmadi")
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) handleLoginState(w http.ResponseWriter, _ *http.Request, owner int64) {
	writeJSON(w, 200, s.Ctl.LoginState(owner))
}

func (s *Server) handleContacts(w http.ResponseWriter, r *http.Request, owner int64) {
	cs, err := s.Ctl.Contacts(r.Context(), owner)
	if err != nil {
		httpErr(w, 409, "kontaktlar hozir mavjud emas (sessiya ulanmagan)")
		return
	}
	if cs == nil {
		cs = []ContactInfo{}
	}
	writeJSON(w, 200, cs)
}

func (s *Server) handlePause(w http.ResponseWriter, r *http.Request, owner int64) {
	s.Ctl.Pause(owner)
	_ = s.Store.SetStatus(r.Context(), owner, store.StatusPaused, "mijoz to'xtatdi")
	s.Store.Audit(r.Context(), owner, "paused", "")
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) handleResume(w http.ResponseWriter, r *http.Request, owner int64) {
	if !s.requireConsent(w, r, owner) {
		return
	}
	if acc, err := s.Store.GetAccount(r.Context(), owner); err != nil || acc.Status == store.StatusPending || acc.Status == store.StatusRevoked {
		httpErr(w, 400, "avval Telegram akkauntni ulang")
		return
	}
	if err := s.Ctl.Resume(context.WithoutCancel(r.Context()), owner); err != nil {
		httpErr(w, 400, "davom ettirib bo'lmadi")
		return
	}
	_ = s.Store.SetStatus(r.Context(), owner, store.StatusActive, "")
	s.Store.Audit(r.Context(), owner, "resumed", "")
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) handleListDrafts(w http.ResponseWriter, r *http.Request, owner int64) {
	ds, err := s.Store.ListDrafts(r.Context(), owner, store.DraftPending, 50)
	if err != nil {
		httpErr(w, 500, "xatolik")
		return
	}
	type row struct {
		ID       int64     `json:"id"`
		ChatID   int64     `json:"chat_id"`
		Incoming string    `json:"incoming"`
		Reply    string    `json:"reply"`
		Reason   string    `json:"reason"`
		At       time.Time `json:"at"`
	}
	out := make([]row, 0, len(ds))
	for _, d := range ds {
		out = append(out, row{d.ID, d.ChatID, d.Incoming, d.Reply, d.Reason, d.CreatedAt})
	}
	writeJSON(w, 200, out)
}

func pathID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id, err == nil && id > 0
}

func (s *Server) handleSendDraft(w http.ResponseWriter, r *http.Request, owner int64) {
	id, ok := pathID(r)
	if !ok {
		httpErr(w, 400, "id noto'g'ri")
		return
	}
	// Mijoz yuborishdan oldin matnni tahrirlashi mumkin.
	var in struct {
		Reply string `json:"reply"`
	}
	if err := readJSONOptional(r, &in); err != nil {
		httpErr(w, 400, "JSON noto'g'ri")
		return
	}

	d, err := s.Store.GetDraft(r.Context(), owner, id)
	if err != nil || d.Status != store.DraftPending {
		httpErr(w, 404, "draft topilmadi")
		return
	}
	if t := strings.TrimSpace(in.Reply); t != "" {
		if utf8.RuneCountInString(t) > 4000 { // Telegram limiti 4096
			httpErr(w, 400, "matn juda uzun")
			return
		}
		d.Reply = t
	}
	// Avval holatni "sent"ga o'tkazamiz (ikki marta bosilsa — ikkinchisi 404 oladi), keyin yuboramiz.
	if err := s.Store.ResolveDraft(r.Context(), owner, id, store.DraftSent); err != nil {
		httpErr(w, 409, "draft allaqachon hal qilingan")
		return
	}
	if err := s.Ctl.SendDraft(context.WithoutCancel(r.Context()), owner, d); err != nil {
		s.logger().Warn("send draft", "owner", owner, "draft", id, "err", err)
		// Yuborilmadi — draft yo'qolmasin: qayta "pending"ga qaytaramiz, mijoz keyin urinadi.
		if rerr := s.Store.ReopenDraft(context.WithoutCancel(r.Context()), owner, id); rerr != nil {
			s.logger().Error("reopen draft", "owner", owner, "draft", id, "err", rerr)
		}
		httpErr(w, 502, "yuborib bo'lmadi, keyinroq qayta urinib ko'ring")
		return
	}
	s.Store.Audit(r.Context(), owner, "draft_sent", strconv.FormatInt(id, 10))
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) handleRejectDraft(w http.ResponseWriter, r *http.Request, owner int64) {
	id, ok := pathID(r)
	if !ok {
		httpErr(w, 400, "id noto'g'ri")
		return
	}
	if err := s.Store.ResolveDraft(r.Context(), owner, id, store.DraftRejected); err != nil {
		httpErr(w, 404, "draft topilmadi")
		return
	}
	s.Store.Audit(r.Context(), owner, "draft_rejected", strconv.FormatInt(id, 10))
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) handleDeleteAccount(w http.ResponseWriter, r *http.Request, owner int64) {
	// Tasdiq so'zi tasodifiy bosib yuborishdan himoya qiladi.
	var in struct {
		Confirm string `json:"confirm"`
	}
	if !readJSON(w, r, &in) || in.Confirm != "DELETE" {
		httpErr(w, 400, `tasdiqlash uchun "DELETE" yuboring`)
		return
	}
	if s.Billing != nil {
		// Akkaunt o'chirilsa, keyingi oy pul yechilmasin.
		if err := s.Billing.Cancel(context.WithoutCancel(r.Context()), owner); err != nil && !errors.Is(err, billing.ErrNoSubscription) {
			s.logger().Warn("cancel on delete", "owner", owner, "err", err)
		}
	}
	if err := s.Ctl.Revoke(context.WithoutCancel(r.Context()), owner); err != nil {
		// Telegram tomonda uzib bo'lmasa ham lokal ma'lumot o'chiriladi; mijozga ogohlantiramiz.
		s.logger().Warn("revoke", "owner", owner, "err", err)
	}
	if s.Google != nil {
		// Google'dagi ruxsat ham bekor qilinsin (bazadagi token CASCADE bilan o'chadi).
		if err := s.Google.Disconnect(context.WithoutCancel(r.Context()), owner); err != nil {
			s.logger().Warn("google disconnect on delete", "owner", owner, "err", err)
		}
	}
	if err := s.Store.DeleteAccount(r.Context(), owner); err != nil {
		httpErr(w, 500, "o'chirishda xatolik")
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) handleActivity(w http.ResponseWriter, r *http.Request, owner int64) {
	es, err := s.Store.ListAudit(r.Context(), owner, 40)
	if err != nil {
		httpErr(w, 500, "xatolik")
		return
	}
	type row struct {
		At     time.Time `json:"at"`
		Event  string    `json:"event"`
		Detail string    `json:"detail"`
	}
	out := make([]row, 0, len(es))
	for _, e := range es {
		out = append(out, row{e.At, e.Event, e.Detail})
	}
	writeJSON(w, 200, out)
}

func (s *Server) handleBilling(w http.ResponseWriter, r *http.Request, owner int64) {
	if s.Billing == nil {
		writeJSON(w, 200, billing.Status{Active: true})
		return
	}
	st, err := s.Billing.Status(r.Context(), owner)
	if err != nil {
		httpErr(w, 500, "xatolik")
		return
	}
	writeJSON(w, 200, st)
}

func (s *Server) handleInvoice(w http.ResponseWriter, r *http.Request, owner int64) {
	if s.Billing == nil {
		httpErr(w, 404, "to'lov yoqilmagan")
		return
	}
	if !s.requireConsent(w, r, owner) {
		return
	}
	link, err := s.Billing.InvoiceLink(r.Context(), owner)
	switch {
	case errors.Is(err, billing.ErrAlreadySubscribed):
		httpErr(w, 409, "obunangiz allaqachon faol")
		return
	case errors.Is(err, billing.ErrDisabled):
		httpErr(w, 404, "to'lov yoqilmagan")
		return
	case err != nil:
		s.logger().Warn("invoice", "owner", owner, "err", err)
		httpErr(w, 502, "to'lov havolasini yaratib bo'lmadi")
		return
	}
	writeJSON(w, 200, map[string]string{"link": link})
}

func (s *Server) handleBillingCancel(w http.ResponseWriter, r *http.Request, owner int64) {
	s.billingToggle(w, r, owner, true)
}

func (s *Server) handleBillingRenew(w http.ResponseWriter, r *http.Request, owner int64) {
	s.billingToggle(w, r, owner, false)
}

func (s *Server) billingToggle(w http.ResponseWriter, r *http.Request, owner int64, cancel bool) {
	if s.Billing == nil {
		httpErr(w, 404, "to'lov yoqilmagan")
		return
	}
	var err error
	if cancel {
		err = s.Billing.Cancel(r.Context(), owner)
	} else {
		err = s.Billing.Renew(r.Context(), owner)
	}
	if errors.Is(err, billing.ErrNoSubscription) {
		httpErr(w, 409, "faol obuna yo'q")
		return
	} else if err != nil {
		s.logger().Warn("billing toggle", "owner", owner, "err", err)
		httpErr(w, 502, "Telegram bilan bog'lanib bo'lmadi, keyinroq urinib ko'ring")
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// ---- yordamchilar ----

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func httpErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		httpErr(w, 400, "JSON noto'g'ri")
		return false
	}
	return true
}

func readJSONOptional(r *http.Request, v any) error {
	b, err := io.ReadAll(r.Body)
	if err != nil || len(bytes.TrimSpace(b)) == 0 {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

func (s *Server) handleGoogleConnect(w http.ResponseWriter, r *http.Request, owner int64) {
	if s.Google == nil {
		httpErr(w, 404, "Google integratsiyasi yoqilmagan")
		return
	}
	acc, err := s.Store.GetAccount(r.Context(), owner)
	if err != nil || acc.ConsentAt == nil {
		httpErr(w, 400, "avval rozilik bering")
		return
	}
	u, err := s.Google.AuthURL(owner)
	if err != nil {
		httpErr(w, 503, err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"url": u})
}

func (s *Server) handleGoogleDisconnect(w http.ResponseWriter, r *http.Request, owner int64) {
	if s.Google == nil {
		httpErr(w, 404, "Google integratsiyasi yoqilmagan")
		return
	}
	if err := s.Google.Disconnect(r.Context(), owner); err != nil {
		httpErr(w, 500, "uzishda xatolik")
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// handleGoogleCallback — Google rozilik sahifasidan qaytish (tashqi brauzerda ochiladi,
// initData yo'q). Egasini faqat server bergan bir martalik state aniqlaydi.
func (s *Server) handleGoogleCallback(w http.ResponseWriter, r *http.Request) {
	if s.Google == nil {
		http.NotFound(w, r)
		return
	}
	q := r.URL.Query()
	state, code := q.Get("state"), q.Get("code")
	if len(state) > 128 || len(code) > 2048 {
		googlePage(w, 400, "Havola yaroqsiz.")
		return
	}
	owner, err := s.Google.Callback(r.Context(), state, code)
	if owner == 0 {
		if !s.allowAuthFailure() {
			googlePage(w, 429, "Juda ko'p urinish. Birozdan keyin qayta urinib ko'ring.")
			return
		}
		googlePage(w, 400, "Havola eskirgan yoki yaroqsiz. Ilovada \"Google'ni ulash\" tugmasini qayta bosing.")
		return
	}
	if s.OnGoogle != nil {
		s.OnGoogle(context.WithoutCancel(r.Context()), owner, err)
	}
	if err != nil {
		s.logger().Warn("google callback", "owner", owner, "err", err)
		googlePage(w, 400, "Google ulanmadi. Telegram'ga qaytib, qayta urinib ko'ring (taqvim ruxsatini belgilang).")
		return
	}
	googlePage(w, 200, "Google ulandi ✅ Endi Telegram'ga qaytishingiz mumkin.")
}

// loginTokenRe — Telegram login tokeni (base64url, padding bilan).
var loginTokenRe = regexp.MustCompile(`^[A-Za-z0-9_=-]{10,256}$`)

var tgLoginTmpl = template.Must(template.New("l").Parse(`<!doctype html><html lang="uz"><head><meta charset="utf-8">` +
	`<meta name="viewport" content="width=device-width, initial-scale=1"><title>Kotib · Telegram'da tasdiqlash</title>` +
	`<meta http-equiv="refresh" content="0;url={{.}}"></head>` +
	`<body><h2>Kotib</h2><p>Telegram ochilib, «Kirishni tasdiqlaysizmi?» deb so'raydi — tasdiqlang, so'ng Telegram'ga qayting.</p>` +
	`<p><a href="{{.}}"><b>Telegram'ni ochish</b></a></p>` +
	`<p>Ochilmasa: boshqa qurilmadagi Telegram → Sozlamalar → Qurilmalar → «Kompyuterni ulash» orqali Mini App'dagi QR'ni skanerlang.</p>` +
	`</body></html>`))

// handleTGLogin — Mini App ichida tg:// havolalarni ochib bo'lmaydi (Telegram openLink faqat
// https qabul qiladi). Shuning uchun Mini App tashqi brauzerda shu sahifani ochadi, sahifa esa
// tg://login havolasini Telegram ilovasiga uzatadi. Token qisqa muddatli (~30 s) va uni boshqa
// akkaunt tasdiqlasa ham kotib ulanmaydi (ensureOwner), shuning uchun alohida auth shart emas.
func handleTGLogin(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if !loginTokenRe.MatchString(token) {
		http.Error(w, "havola yaroqsiz", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// tg:// sxemasi html/template'da "xavfsiz" deb belgilanishi kerak; token regex bilan tekshirilgan.
	_ = tgLoginTmpl.Execute(w, template.URL("tg://login?token="+token))
}

func googlePage(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(code)
	_ = googleTmpl.Execute(w, msg) // html/template — matn ekranlanadi
}

// ---- Avtomatlashtirish havolasi (iPhone Shortcuts / Android MacroDroid, Tasker) ----

func hookHash(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

// handleHookCreate yangi maxfiy havola yaratadi (eskisi bekor bo'ladi). Token faqat shu
// javobda ko'rinadi — bazada xeshi saqlanadi.
func (s *Server) handleHookCreate(w http.ResponseWriter, r *http.Request, owner int64) {
	if !s.requireConsent(w, r, owner) {
		return
	}
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		httpErr(w, 500, "xatolik")
		return
	}
	token := base64.RawURLEncoding.EncodeToString(b)
	if err := s.Store.SetHook(r.Context(), owner, hookHash(token)); err != nil {
		httpErr(w, 500, "xatolik")
		return
	}
	s.Store.Audit(r.Context(), owner, "hook_created", "")
	writeJSON(w, 200, map[string]string{"url": strings.TrimRight(s.PublicURL, "/") + "/hook/" + token})
}

func (s *Server) handleHookDelete(w http.ResponseWriter, r *http.Request, owner int64) {
	if err := s.Store.DeleteHook(r.Context(), owner); err != nil {
		httpErr(w, 500, "xatolik")
		return
	}
	s.Store.Audit(r.Context(), owner, "hook_deleted", "")
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// handleHook — telefon avtomatikasi chaqiradi (initData yo'q, egani maxfiy token belgilaydi).
// Parametrlar: mode=draft|auto|off, away=<holat matni> (bo'sh — tozalash), action=pause|resume.
func (s *Server) handleHook(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	if len(token) < 20 || len(token) > 64 {
		s.hookFail(w)
		return
	}
	owner, err := s.Store.OwnerByHook(r.Context(), hookHash(token))
	if err != nil {
		s.hookFail(w)
		return
	}
	if !s.allow(owner) {
		httpErr(w, http.StatusTooManyRequests, "juda tez-tez so'rov")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	_ = r.ParseForm() // GET query va POST form ikkalasi ham
	q := r.Form
	st, err := s.Store.GetSettings(r.Context(), owner)
	if err != nil {
		httpErr(w, 500, "xatolik")
		return
	}
	changed := false
	if v := q.Get("mode"); v != "" {
		st.Mode, changed = config.Mode(v), true
	}
	if _, ok := q["away"]; ok {
		st.Away, changed = strings.Join(strings.Fields(q.Get("away")), " "), true
	}
	if changed {
		if err := st.Validate(); err != nil {
			httpErr(w, 400, err.Error())
			return
		}
		if err := s.Store.SaveSettings(r.Context(), owner, st); err != nil {
			httpErr(w, 500, "xatolik")
			return
		}
		s.Store.Audit(r.Context(), owner, "hook", "mode="+string(st.Mode))
	}
	switch q.Get("action") {
	case "":
	case "pause":
		s.Ctl.Pause(owner)
		_ = s.Store.SetStatus(r.Context(), owner, store.StatusPaused, "avtomatika to'xtatdi")
		s.Store.Audit(r.Context(), owner, "paused", "hook")
	case "resume":
		acc, err := s.Store.GetAccount(r.Context(), owner)
		if err != nil || acc.ConsentAt == nil || acc.Status == store.StatusPending || acc.Status == store.StatusRevoked {
			httpErr(w, 400, "avval Telegram akkauntni ulang")
			return
		}
		if err := s.Ctl.Resume(context.WithoutCancel(r.Context()), owner); err != nil {
			httpErr(w, 400, "davom ettirib bo'lmadi")
			return
		}
		_ = s.Store.SetStatus(r.Context(), owner, store.StatusActive, "")
		s.Store.Audit(r.Context(), owner, "resumed", "hook")
	default:
		httpErr(w, 400, "action: pause yoki resume")
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "mode": st.Mode, "away": st.Away})
}

func (s *Server) hookFail(w http.ResponseWriter) {
	if !s.allowAuthFailure() {
		httpErr(w, http.StatusTooManyRequests, "juda tez-tez so'rov")
		return
	}
	httpErr(w, http.StatusNotFound, "havola yaroqsiz")
}

// handleICS — egasi tasdiqlagan uchrashuvning .ics fayli (imzolangan havola, 7 kun amal qiladi:
// amallar shundan keyin o'chiriladi). Telefon taqvimi faylni ochib "qo'shish"ni taklif qiladi.
func (s *Server) handleICS(w http.ResponseWriter, r *http.Request) {
	if s.ICS == nil {
		http.NotFound(w, r)
		return
	}
	owner, id, ok := s.ICS.Verify(r.PathValue("owner"), r.PathValue("id"), r.PathValue("sig"))
	if !ok {
		s.hookFail(w)
		return
	}
	a, err := s.Store.GetAction(r.Context(), owner, id)
	if err != nil || a.Kind != store.ActionEvent || a.Status != store.ActionDone {
		httpErr(w, 404, "topilmadi")
		return
	}
	var p google.EventPayload
	if err := json.Unmarshal(a.Payload, &p); err != nil {
		httpErr(w, 500, "xatolik")
		return
	}
	start, err := time.Parse(time.RFC3339, p.Start)
	if err != nil {
		httpErr(w, 500, "xatolik")
		return
	}
	note := "Telegram kotibi taklif qildi, siz tasdiqladingiz."
	if p.With != "" {
		note = "Kim bilan: " + p.With + ". " + note
	}
	host := strings.TrimPrefix(strings.TrimPrefix(s.PublicURL, "https://"), "http://")
	body := ics.Build(ics.Event{
		UID: fmt.Sprintf("kotib-%d-%d@%s", owner, id, host), Title: p.Title, Start: start,
		Duration: time.Duration(p.Duration) * time.Minute, Note: note,
	}, time.Now())
	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	w.Header().Set("Content-Disposition", `inline; filename="kotib.ics"`)
	_, _ = w.Write(body)
}
