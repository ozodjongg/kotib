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
	"path/filepath"
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
	"github.com/ozodjongg/secretar_ai_agent/internal/plugin"
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
	Step  string `json:"step"` // idle | connecting | wait_code | wait_password | done | error
	Error string `json:"error,omitempty"`
	// Via — kod qayerga yuborilgani (wait_code'da): app | sms | call | email | fragment.
	Via string `json:"via,omitempty"`
	// Hint — qo'shimcha: email shabloni (a***@gmail.com) yoki fragment havolasi.
	Hint string `json:"hint,omitempty"`
}

// ContactInfo — kontakt tanlash ro'yxati uchun (faqat egasiga ko'rsatiladi).
type ContactInfo struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	// Phone — faqat raqamlar (tug'ilgan kunni Google kontakti bilan moslash uchun); Mini App'ga berilmaydi.
	Phone string `json:"-"`
}

// Controller — Mini App userbot menejeriga murojaat qiladigan interfeys.
type Controller interface {
	// BeginLogin telefon raqam bilan ulanishni boshlaydi: Telegram kod yuboradi (SMS yoki ilova).
	BeginLogin(ctx context.Context, owner int64, phone string) error
	// CancelLogin jarayondagi ulanishni bekor qiladi (raqamni o'zgartirish, «Orqaga»).
	CancelLogin(owner int64)
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
	// StartLearnStyle oxirgi yozishmalardan uslub profilini fonda yaratadi (natija bot orqali).
	// Xato matni foydalanuvchiga ko'rsatiladi.
	StartLearnStyle(ctx context.Context, owner int64) error
}

// Billing — Telegram Stars obunasi (billing.Service). nil → to'lov o'chiq.
type Billing interface {
	Status(ctx context.Context, owner int64) (billing.Status, error)
	StartTrial(ctx context.Context, owner int64) error
	InvoiceLink(ctx context.Context, owner int64, tier billing.Tier) (string, error)
	// HasPro — Pro funksiyalari (ovoz, Google, avtomatika) ochiqmi.
	HasPro(ctx context.Context, owner int64) bool
	// CanLink — asosiy akkaunt yana qo'shimcha akkaunt ulay oladimi (Pro va chegara).
	CanLink(ctx context.Context, parent int64, existing int) bool
	Cancel(ctx context.Context, owner int64) error
	Renew(ctx context.Context, owner int64) error
	Referral(ctx context.Context, owner int64) (billing.ReferralInfo, error)
	// Admin paneli
	Enabled() bool
	Grant(ctx context.Context, owner int64, days int, tier billing.Tier) error
	RevokeGrant(ctx context.Context, owner int64) error
	Refund(ctx context.Context, chargeID string) (store.Payment, error)
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
	// Schedules — rejalashtirilgan vazifalarni darhol bajarish (schedule.Runner); nil → funksiya o'chiq.
	Schedules Scheduler
	// Plugins — connector'lar katalogi; Vault — ularning sozlamalarini shifrlash. nil → o'chiq.
	Plugins *plugin.Registry
	Vault   plugin.Vault
	// PluginAuth — plaginlarni akkaunt orqali ulash (YouTube); nil → bunday plaginlar ulanmaydi.
	PluginAuth PluginAuth
	// OnPlugin — plagin akkaunti ulanishi natijasi haqida egasiga bot xabari (ixtiyoriy).
	OnPlugin func(ctx context.Context, owner int64, plugin string, err error)
	// OnGoogle — ulanish natijasi haqida egasiga bot xabari (ixtiyoriy).
	OnGoogle func(ctx context.Context, owner int64, err error)
	Log      *slog.Logger
	// Admins — admin paneliga kirish huquqi (ADMIN_IDS).
	Admins map[int64]bool
	// Notify — foydalanuvchiga bot orqali xabar (admin amallari); nil → xabar yuborilmaydi.
	Notify func(ctx context.Context, owner int64, text string, openApp bool)
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
	mux.HandleFunc("POST /api/consent", s.authMain(s.handleConsent))
	mux.HandleFunc("GET /api/settings", s.auth(s.handleGetSettings))
	mux.HandleFunc("PUT /api/settings", s.auth(s.handlePutSettings))
	mux.HandleFunc("POST /api/login/phone", s.auth(s.handleLoginPhone))
	mux.HandleFunc("POST /api/login/cancel", s.auth(s.handleLoginCancel))
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
	mux.HandleFunc("GET /api/billing", s.authMain(s.handleBilling))
	mux.HandleFunc("POST /api/billing/invoice", s.authMain(s.handleInvoice))
	mux.HandleFunc("POST /api/billing/cancel", s.authMain(s.handleBillingCancel))
	mux.HandleFunc("POST /api/billing/renew", s.authMain(s.handleBillingRenew))
	mux.HandleFunc("GET /api/referral", s.authMain(s.handleReferral))
	mux.HandleFunc("POST /api/away", s.auth(s.handleAway))
	mux.HandleFunc("POST /api/style/learn", s.auth(s.handleStyleLearn))
	mux.HandleFunc("POST /api/style/shadow", s.auth(s.handleStyleShadow))
	mux.HandleFunc("GET /api/stats", s.auth(s.handleStats))
	mux.HandleFunc("GET /api/accounts", s.authMain(s.handleAccounts))
	mux.HandleFunc("POST /api/accounts/link", s.authMain(s.handleAccountLink))
	mux.HandleFunc("DELETE /api/accounts/{id}", s.authMain(s.handleAccountUnlink))
	mux.HandleFunc("POST /api/google/connect", s.auth(s.handleGoogleConnect))
	mux.HandleFunc("POST /api/google/disconnect", s.auth(s.handleGoogleDisconnect))
	mux.HandleFunc("GET /oauth/google/callback", s.handleGoogleCallback)
	mux.HandleFunc("GET /ics/{owner}/{id}/{sig}", s.handleICS)
	mux.HandleFunc("POST /api/hook", s.auth(s.handleHookCreate))
	mux.HandleFunc("DELETE /api/hook", s.auth(s.handleHookDelete))
	mux.HandleFunc("GET /hook/{token}", s.handleHook)
	mux.HandleFunc("POST /hook/{token}", s.handleHook)
	mux.HandleFunc("GET /api/schedules", s.auth(s.handleListSchedules))
	mux.HandleFunc("POST /api/schedules", s.auth(s.handleCreateSchedule))
	mux.HandleFunc("PUT /api/schedules/{id}", s.auth(s.handleUpdateSchedule))
	mux.HandleFunc("DELETE /api/schedules/{id}", s.auth(s.handleDeleteSchedule))
	mux.HandleFunc("POST /api/schedules/{id}/run", s.auth(s.handleRunSchedule))
	mux.HandleFunc("GET /api/plugins", s.auth(s.handleListPlugins))
	mux.HandleFunc("PUT /api/plugins/{id}", s.auth(s.handleSavePlugin))
	mux.HandleFunc("DELETE /api/plugins/{id}", s.auth(s.handleDeletePlugin))
	mux.HandleFunc("POST /api/plugins/{id}/connect", s.auth(s.handleConnectPlugin))
	s.adminRoutes(mux)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })
	if s.StaticDir != "" {
		// Huquqiy sahifalar toza manzilda (Google OAuth consent screen havolalari).
		for _, page := range []string{"privacy", "terms"} {
			file := filepath.Join(s.StaticDir, page+".html")
			mux.HandleFunc("GET /"+page, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				http.ServeFile(w, r, file)
			})
		}
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

// auth: "Authorization: tma <initData>" sarlavhasini tekshiradi. Handler'ga tanlangan akkaunt
// (X-Kotib-Account — asosiy yoki unga bog'langan qo'shimcha akkaunt) beriladi; asosiy akkaunt
// kontekstda (mainOwner).
func (s *Server) auth(next func(http.ResponseWriter, *http.Request, int64)) http.HandlerFunc {
	return s.authWith(next, false)
}

// authMain — har doim asosiy akkaunt (obuna, referal, akkauntlar ro'yxati, rozilik).
func (s *Server) authMain(next func(http.ResponseWriter, *http.Request, int64)) http.HandlerFunc {
	return s.authWith(next, true)
}

func (s *Server) authWith(next func(http.ResponseWriter, *http.Request, int64), mainOnly bool) http.HandlerFunc {
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
		// Qo'shimcha akkaunt Mini App'ni o'zi ochsa — boshqaruv asosiy akkauntda.
		if _, err := s.Store.GetLink(r.Context(), u.ID); err == nil {
			httpErr(w, http.StatusForbidden, "bu akkaunt asosiy akkauntingizdagi Kotib orqali boshqariladi")
			return
		}
		// Bloklangan foydalanuvchi faqat holatini ko'radi va ma'lumotlarini o'chira oladi.
		if !s.Admins[u.ID] && !bannedAllowed(r) {
			if _, err := s.Store.GetBan(r.Context(), u.ID); err == nil {
				httpErr(w, http.StatusForbidden, "akkauntingiz administrator tomonidan bloklangan")
				return
			}
		}
		if r.Method == http.MethodGet && r.URL.Path == "/api/me" {
			// Admin ro'yxatida tanib olish uchun (akkaunt bo'lmasa — hech narsa yozilmaydi).
			if err := s.Store.TouchAccount(r.Context(), u.ID, u.FirstName, u.Username, time.Now()); err != nil {
				s.logger().Warn("touch account", "owner", u.ID, "err", err)
			}
		}
		owner := u.ID
		if v := r.Header.Get(AccountHeader); v != "" && !mainOnly {
			id, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				httpErr(w, 400, "akkaunt noto'g'ri")
				return
			}
			if id != u.ID {
				if l, err := s.Store.GetLink(r.Context(), id); err != nil || l.ParentID != u.ID {
					httpErr(w, http.StatusForbidden, "bu akkaunt sizga tegishli emas")
					return
				}
				owner = id
			}
		}
		next(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, u.ID)), owner)
	}
}

// bannedAllowed — bloklangan foydalanuvchiga ruxsat etilgan so'rovlar.
func bannedAllowed(r *http.Request) bool {
	return (r.Method == http.MethodGet && r.URL.Path == "/api/me") ||
		(r.Method == http.MethodDelete && r.URL.Path == "/api/account")
}

// AccountHeader — Mini App tanlagan akkaunt (bo'sh — asosiy).
const AccountHeader = "X-Kotib-Account"

// mainOwner — so'rov egasining asosiy akkaunti (auth qo'yadi).
func mainOwner(r *http.Request, fallback int64) int64 {
	if v, ok := r.Context().Value(ctxKey{}).(int64); ok {
		return v
	}
	return fallback
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
	// Tejalgan vaqt: bu oy kotib orqali yuborilgan javoblar.
	loc := time.UTC
	if st, err := s.Store.GetSettings(r.Context(), owner); err == nil {
		loc = st.Location()
	}
	if n, saved, err := store.ReplyStats(r.Context(), s.Store, owner, store.MonthStart(time.Now(), loc)); err == nil {
		out["stats"] = map[string]any{"month_sent": n, "month_saved_min": int(saved / time.Minute), "month_saved": store.FormatSaved(saved)}
	}
	out["bot_username"] = s.BotUsername
	main0 := mainOwner(r, owner)
	out["admin"] = s.Admins[main0]
	if b, err := s.Store.GetBan(r.Context(), main0); err == nil && !s.Admins[main0] {
		out["banned"] = map[string]any{"reason": b.Reason, "at": b.At}
	}
	// Bir nechta akkaunt: tanlangan akkaunt va ro'yxat (almashtirgich uchun).
	main := mainOwner(r, owner)
	list, _ := s.accountList(r.Context(), main)
	out["accounts"] = list
	out["account_id"] = owner
	out["main_id"] = main
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
	// Faqat server yozadigan maydonlar: Mini App'dagi eski nusxa ularni bosib ketmasin.
	if old, err := s.Store.GetSettings(r.Context(), owner); err == nil {
		st.StyleUpdatedAt, st.StyleScore, st.ShadowUntil = old.StyleUpdatedAt, old.StyleScore, old.ShadowUntil
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

// handleLoginCancel — «Orqaga»: jarayondagi ulanish bekor bo'ladi, Mini App raqam kiritish
// sahifasiga qaytadi (kod kelmasa yoki raqam xato kiritilgan bo'lsa, ilova qotib qolmasin).
func (s *Server) handleLoginCancel(w http.ResponseWriter, r *http.Request, owner int64) {
	s.Ctl.CancelLogin(owner)
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
	if main := mainOwner(r, owner); main != owner {
		// Qo'shimcha akkaunt tanlangan: faqat o'sha akkaunt uziladi, asosiysi tegilmaydi.
		if err := s.unlink(r.Context(), main, owner); err != nil {
			httpErr(w, 500, "o'chirishda xatolik")
			return
		}
		writeJSON(w, 200, map[string]bool{"ok": true})
		return
	}
	if ls, err := s.Store.ListLinks(r.Context(), owner); err == nil {
		for _, l := range ls {
			if err := s.unlink(r.Context(), owner, l.ChildID); err != nil {
				s.logger().Warn("unlink on delete", "owner", owner, "child", l.ChildID, "err", err)
			}
		}
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
	// Plaginlarning akkaunt tokenlari (YouTube) ham Google'da bekor qilinsin.
	s.revokeAllPluginTokens(context.WithoutCancel(r.Context()), owner)
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

// handleAway — "hozirgi holat"ni tez almashtirish (bosh sahifa): boshqa sozlamalarga tegmaydi.
// minutes > 0 — holat shuncha vaqtdan keyin o'zi tugaydi; away bo'sh — tozalash.
func (s *Server) handleAway(w http.ResponseWriter, r *http.Request, owner int64) {
	var in struct {
		Away    string `json:"away"`
		Minutes int    `json:"minutes"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if in.Minutes < 0 || in.Minutes > config.MaxAwayMinutes {
		httpErr(w, 400, "muddat 0–1440 daqiqa")
		return
	}
	if !s.requireConsent(w, r, owner) {
		return
	}
	st, err := s.Store.GetSettings(r.Context(), owner)
	if err != nil {
		httpErr(w, 500, "xatolik")
		return
	}
	st.SetAway(in.Away, in.Minutes, time.Now())
	if err := st.Validate(); err != nil {
		httpErr(w, 400, err.Error())
		return
	}
	if err := s.Store.SaveSettings(r.Context(), owner, st); err != nil {
		httpErr(w, 500, "xatolik")
		return
	}
	s.Store.Audit(r.Context(), owner, "away_set", "")
	writeJSON(w, 200, map[string]any{"ok": true, "away": st.Away, "away_until": st.AwayUntil})
}

// handleReferral — taklif havolasi va natijalar. To'lov o'chiq bo'lsa {enabled:false}.
func (s *Server) handleReferral(w http.ResponseWriter, r *http.Request, owner int64) {
	if s.Billing == nil {
		writeJSON(w, 200, map[string]any{"enabled": false})
		return
	}
	if !s.requireConsent(w, r, owner) {
		return
	}
	info, err := s.Billing.Referral(r.Context(), owner)
	if err != nil {
		s.logger().Warn("referral", "owner", owner, "err", err)
		httpErr(w, 500, "xatolik")
		return
	}
	writeJSON(w, 200, struct {
		billing.ReferralInfo
		Link string `json:"link"`
	}{info, billing.ReferralLink(s.BotUsername, info.Code)})
}

func (s *Server) handleInvoice(w http.ResponseWriter, r *http.Request, owner int64) {
	if s.Billing == nil {
		httpErr(w, 404, "to'lov yoqilmagan")
		return
	}
	if !s.requireConsent(w, r, owner) {
		return
	}
	in := struct {
		Tier billing.Tier `json:"tier"`
	}{Tier: billing.TierAdvanced} // eski klient (tanasiz so'rov) — Advanced
	if err := readJSONOptional(r, &in); err != nil {
		httpErr(w, 400, "JSON noto'g'ri")
		return
	}
	link, err := s.Billing.InvoiceLink(r.Context(), owner, in.Tier)
	switch {
	case errors.Is(err, billing.ErrUnknownTier):
		httpErr(w, 400, "bunday tarif yo'q")
		return
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

// errNeedPro — Pro funksiyasi tarifda yo'q (402 Payment Required).
const errNeedPro = "bu funksiya Pro tarifda"

// hasPro — to'lov o'chiq bo'lsa hamma funksiya ochiq.
func (s *Server) hasPro(ctx context.Context, owner int64) bool {
	return s.Billing == nil || s.Billing.HasPro(ctx, owner)
}

func (s *Server) handleGoogleConnect(w http.ResponseWriter, r *http.Request, owner int64) {
	if s.Google == nil {
		httpErr(w, 404, "Google integratsiyasi yoqilmagan")
		return
	}
	if !s.hasPro(r.Context(), owner) {
		httpErr(w, http.StatusPaymentRequired, errNeedPro)
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
	purpose := ""
	if s.PluginAuth != nil {
		purpose = s.PluginAuth.StatePurpose(state)
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
	if purpose != "" {
		// Plagin (YouTube) akkaunti: token plagin sozlamasiga saqlangan (google.Client.OnPluginToken).
		if s.OnPlugin != nil {
			s.OnPlugin(context.WithoutCancel(r.Context()), owner, purpose, err)
		}
		if err != nil {
			s.logger().Warn("plugin callback", "owner", owner, "plugin", purpose, "err", err)
			googlePage(w, 400, "Ulanmadi. Telegram'ga qaytib, «Ulash»ni qayta bosing va ruxsat bering.")
			return
		}
		googlePage(w, 200, pluginName(purpose)+" akkauntingiz ulandi ✅ Endi Telegram'ga qaytishingiz mumkin.")
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

// pluginName — callback sahifasi uchun plagin nomi.
func pluginName(id string) string {
	switch id {
	case "youtube":
		return "YouTube"
	case "sheets":
		return "Google Sheets"
	}
	return "Plagin"
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
	if !s.hasPro(r.Context(), owner) {
		httpErr(w, http.StatusPaymentRequired, errNeedPro)
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
// Parametrlar: mode=draft|auto|off, away=<holat matni> (bo'sh — tozalash), for=<daqiqa> (holat muddati),
// action=pause|resume.
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
	if !s.hasPro(r.Context(), owner) {
		httpErr(w, http.StatusPaymentRequired, errNeedPro)
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
		// for=<daqiqa> — holat shuncha vaqtdan keyin o'zi tugaydi (ixtiyoriy).
		mins, _ := strconv.Atoi(q.Get("for"))
		st.SetAway(q.Get("away"), mins, time.Now())
		changed = true
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
