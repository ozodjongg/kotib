package miniapp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/ozodjongg/secretar_ai_agent/internal/config"
	"github.com/ozodjongg/secretar_ai_agent/internal/ratelimit"
	"github.com/ozodjongg/secretar_ai_agent/internal/store"
)

// ErrTooManyAttempts — Controller login urinishlari chegarasidan oshganda qaytaradi.
var ErrTooManyAttempts = errors.New("juda ko'p urinish: birozdan keyin qayta urinib ko'ring")

// LoginState — Telegram akkauntni ulash jarayonining holati.
type LoginState struct {
	Step  string `json:"step"` // idle | wait_code | wait_password | done | error
	Error string `json:"error,omitempty"`
}

// ContactInfo — kontakt tanlash ro'yxati uchun (faqat egasiga ko'rsatiladi).
type ContactInfo struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// Controller — Mini App userbot menejeriga murojaat qiladigan interfeys.
type Controller interface {
	BeginLogin(ctx context.Context, owner int64, phone string) error
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

type Server struct {
	BotToken string
	Store    store.Store
	Ctl      Controller
	Log      *slog.Logger
	// Static — Mini App fayllari (index.html...) joylashgan papka.
	StaticDir string

	mu        sync.Mutex
	limiters  map[int64]*limiterEntry
	lastSweep time.Time
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
		u, err := ValidateInitData(strings.TrimPrefix(hdr, pfx), s.BotToken, 24*time.Hour, time.Now())
		if err != nil {
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
	writeJSON(w, 200, map[string]any{
		"owner_id":  owner,
		"status":    acc.Status,
		"note":      acc.StatusNote,
		"consented": acc.ConsentAt != nil,
		"presets":   config.Presets,
	})
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
	if err := s.Ctl.Revoke(context.WithoutCancel(r.Context()), owner); err != nil {
		// Telegram tomonda uzib bo'lmasa ham lokal ma'lumot o'chiriladi; mijozga ogohlantiramiz.
		s.logger().Warn("revoke", "owner", owner, "err", err)
	}
	if err := s.Store.DeleteAccount(r.Context(), owner); err != nil {
		httpErr(w, 500, "o'chirishda xatolik")
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
