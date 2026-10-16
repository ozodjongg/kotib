// Package google: mijozning Google akkaunti bilan integratsiya (faqat stdlib, REST).
//
//   - OAuth 2.0 (authorization code + PKCE). Refresh token faqat shifrlangan holda
//     saqlanadi (aad = google:<owner>), access token faqat xotirada.
//   - Calendar: band vaqtlar (freeBusy) kotibga kontekst sifatida, tadbir qo'shish.
//   - Contacts: kontaktning ismi va egasining eslatmasi (faqat o'qish).
//   - Tasks: vazifa qo'shish.
//
// Google'ga YOZISH faqat egasi bot orqali tasdiqlagan amallarda bo'ladi (Execute).
package google

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ozodjongg/secretar_ai_agent/internal/store"
)

var (
	ErrNotConnected = errors.New("google ulanmagan")
	ErrBadState     = errors.New("ulanish havolasi eskirgan yoki yaroqsiz")
	ErrNoScope      = errors.New("google ruxsati berilmagan")
)

// Scopes — so'raladigan ruxsatlar. Hammasi "sensitive" toifada (Gmail kabi "restricted" emas).
var Scopes = []string{
	"openid", "email",
	"https://www.googleapis.com/auth/calendar.events",
	"https://www.googleapis.com/auth/calendar.freebusy",
	"https://www.googleapis.com/auth/contacts.readonly",
	"https://www.googleapis.com/auth/tasks",
}

// PluginScopes — plaginlarni Google akkaunti orqali ulash (alohida token, alohida rozilik):
// mijoz API kalit bilan ovora bo'lmaydi, «YouTube akkauntini ulash» tugmasini bosadi xolos.
var PluginScopes = map[string][]string{
	"youtube": {"openid", "email", "https://www.googleapis.com/auth/youtube.readonly"},
}

// Endpoints — Google manzillari (testda almashtiriladi).
type Endpoints struct {
	Auth, Token, Revoke, Calendar, Tasks, People string
}

var DefaultEndpoints = Endpoints{
	Auth:     "https://accounts.google.com/o/oauth2/v2/auth",
	Token:    "https://oauth2.googleapis.com/token",
	Revoke:   "https://oauth2.googleapis.com/revoke",
	Calendar: "https://www.googleapis.com/calendar/v3",
	Tasks:    "https://tasks.googleapis.com/tasks/v1",
	People:   "https://people.googleapis.com/v1",
}

// Vault — refresh tokenni shifrlash (crypto.Vault).
type Vault interface {
	Seal(plaintext, aad []byte) ([]byte, error)
	Open(blob, aad []byte) ([]byte, error)
}

type Client struct {
	ClientID, ClientSecret string
	RedirectURL            string // https://<domen>/oauth/google/callback
	Store                  store.Store
	Vault                  Vault
	HTTP                   *http.Client
	Log                    *slog.Logger
	Endpoints              Endpoints
	Now                    func() time.Time
	// OnPluginToken — plagin akkaunti ulandi (PluginAuthURL orqali): refresh tokenni
	// plagin sozlamasiga (shifrlab) saqlash. nil → plagin ulanishi o'chiq.
	OnPluginToken func(ctx context.Context, owner int64, plugin, refreshToken, account string) error

	mu       sync.Mutex
	ptokens  map[string]accessToken // plagin refresh token xeshi → access token
	pending  map[string]pendingAuth // state → kutilayotgan ulanish (bir martalik)
	tokens   map[int64]accessToken
	busy     map[int64]busyCache
	contacts map[contactKey]contactCache
	warmed   map[int64]bool
}

type pendingAuth struct {
	owner    int64
	verifier string
	exp      time.Time
	plugin   string // bo'sh — asosiy Google ulanishi; aks holda plagin (youtube)
}

type accessToken struct {
	token string
	exp   time.Time
}

const (
	stateTTL      = 15 * time.Minute
	maxPending    = 1000
	busyTTL       = 2 * time.Minute
	contactTTL    = time.Hour
	maxCacheItems = 20000
)

// New — ClientID bo'sh bo'lsa nil (integratsiya o'chiq).
func New(clientID, clientSecret, redirectURL string, st store.Store, v Vault, log *slog.Logger) *Client {
	if clientID == "" {
		return nil
	}
	return &Client{
		ClientID: clientID, ClientSecret: clientSecret, RedirectURL: redirectURL,
		Store: st, Vault: v, Log: log,
		HTTP:      &http.Client{Timeout: 15 * time.Second},
		Endpoints: DefaultEndpoints,
	}
}

func (c *Client) Enabled() bool { return c != nil && c.ClientID != "" }

func (c *Client) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c *Client) log() *slog.Logger {
	if c.Log != nil {
		return c.Log
	}
	return slog.Default()
}

func (c *Client) init() {
	if c.pending == nil {
		c.pending = map[string]pendingAuth{}
		c.tokens = map[int64]accessToken{}
		c.busy = map[int64]busyCache{}
		c.contacts = map[contactKey]contactCache{}
		c.warmed = map[int64]bool{}
		c.ptokens = map[string]accessToken{}
	}
}

func randomString(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// AuthURL egasi uchun Google rozilik sahifasi havolasini yaratadi. state bir martalik,
// 15 daqiqa amal qiladi va egaga bog'langan (PKCE verifier serverda qoladi).
func (c *Client) AuthURL(owner int64) (string, error) {
	return c.authURL(owner, "", Scopes)
}

// PluginAuthURL — plaginni (masalan, YouTube) Google akkaunti orqali ulash havolasi.
func (c *Client) PluginAuthURL(owner int64, plugin string) (string, error) {
	scopes, ok := PluginScopes[plugin]
	if !ok || c.OnPluginToken == nil {
		return "", errors.New("bu plaginni akkaunt orqali ulab bo'lmaydi")
	}
	return c.authURL(owner, plugin, scopes)
}

// StatePurpose — state qaysi ulanishga tegishli (bo'sh — asosiy Google yoki noma'lum).
func (c *Client) StatePurpose(state string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.pending[state].plugin
}

func (c *Client) authURL(owner int64, plugin string, scopes []string) (string, error) {
	state, err := randomString(32)
	if err != nil {
		return "", err
	}
	verifier, err := randomString(48)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(verifier))
	now := c.now()

	c.mu.Lock()
	c.init()
	for k, p := range c.pending {
		if now.After(p.exp) || (p.owner == owner && p.plugin == plugin) { // eskilar va shu egasining oldingi havolasi
			delete(c.pending, k)
		}
	}
	if len(c.pending) >= maxPending {
		c.mu.Unlock()
		return "", errors.New("juda ko'p kutilayotgan ulanish, birozdan keyin urinib ko'ring")
	}
	c.pending[state] = pendingAuth{owner: owner, verifier: verifier, exp: now.Add(stateTTL), plugin: plugin}
	c.mu.Unlock()

	q := url.Values{
		"client_id":     {c.ClientID},
		"redirect_uri":  {c.RedirectURL},
		"response_type": {"code"},
		"scope":         {strings.Join(scopes, " ")},
		"access_type":   {"offline"},
		"prompt":        {"consent"}, // refresh token har safar qaytsin
		// Plagin tokeni faqat o'z ruxsati bilan (taqvim ruxsatlari unga qo'shilmasin).
		"include_granted_scopes": {strconv.FormatBool(plugin == "")},
		"state":                  {state},
		"code_challenge":         {base64.RawURLEncoding.EncodeToString(sum[:])},
		"code_challenge_method":  {"S256"},
	}
	return c.Endpoints.Auth + "?" + q.Encode(), nil
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
	IDToken      string `json:"id_token"`
	Error        string `json:"error"`
}

// Callback Google qaytargan code'ni tokenga almashtiradi va ulanishni saqlaydi.
// Qaysi egaga tegishli ekanini faqat server bergan state belgilaydi.
func (c *Client) Callback(ctx context.Context, state, code string) (int64, error) {
	c.mu.Lock()
	c.init()
	p, ok := c.pending[state]
	delete(c.pending, state) // bir martalik
	c.mu.Unlock()
	if !ok || state == "" || c.now().After(p.exp) {
		return 0, ErrBadState
	}
	if code == "" {
		return p.owner, errors.New("google rozilik bermadi")
	}
	tr, err := c.tokenRequest(ctx, url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {c.RedirectURL},
		"code_verifier": {p.verifier},
	})
	if err != nil {
		return p.owner, err
	}
	if tr.RefreshToken == "" {
		return p.owner, errors.New("google refresh token bermadi")
	}
	if p.plugin != "" {
		return p.owner, c.pluginCallback(ctx, p, tr)
	}
	if !hasScope(tr.Scope, "https://www.googleapis.com/auth/calendar.events") {
		c.revoke(ctx, tr.RefreshToken)
		return p.owner, ErrNoScope
	}
	sealed, err := c.Vault.Seal([]byte(tr.RefreshToken), aad(p.owner))
	if err != nil {
		return p.owner, err
	}
	if err := c.Store.SaveGoogleLink(ctx, store.GoogleLink{OwnerID: p.owner, Email: emailFromIDToken(tr.IDToken), Token: sealed}); err != nil {
		return p.owner, err
	}
	c.mu.Lock()
	c.tokens[p.owner] = accessToken{token: tr.AccessToken, exp: c.now().Add(time.Duration(tr.ExpiresIn)*time.Second - time.Minute)}
	c.forgetLocked(p.owner, false)
	c.mu.Unlock()
	c.Store.Audit(ctx, p.owner, "google_connected", "")
	return p.owner, nil
}

// pluginCallback — plagin ruxsati olindi: kerakli ruxsat berilganini tekshirib, tokenni plaginga beradi.
func (c *Client) pluginCallback(ctx context.Context, p pendingAuth, tr tokenResponse) error {
	scopes := PluginScopes[p.plugin]
	if len(scopes) == 0 || !hasScope(tr.Scope, scopes[len(scopes)-1]) || c.OnPluginToken == nil {
		c.revoke(ctx, tr.RefreshToken)
		return ErrNoScope
	}
	if err := c.OnPluginToken(ctx, p.owner, p.plugin, tr.RefreshToken, emailFromIDToken(tr.IDToken)); err != nil {
		c.revoke(ctx, tr.RefreshToken)
		return err
	}
	c.Store.Audit(ctx, p.owner, "plugin_connected", p.plugin)
	return nil
}

// PluginAccessToken — plagin refresh tokeni bo'yicha access token (keshlanadi). Mijoz
// ruxsatni Google'da olib tashlagan bo'lsa — ErrNotConnected.
func (c *Client) PluginAccessToken(ctx context.Context, refreshToken string) (string, error) {
	sum := sha256.Sum256([]byte(refreshToken))
	key := string(sum[:])
	c.mu.Lock()
	c.init()
	t, ok := c.ptokens[key]
	c.mu.Unlock()
	if ok && c.now().Before(t.exp) {
		return t.token, nil
	}
	tr, err := c.tokenRequest(ctx, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refreshToken}})
	if err != nil {
		return "", err
	}
	c.mu.Lock()
	if len(c.ptokens) >= maxCacheItems {
		c.ptokens = map[string]accessToken{}
	}
	c.ptokens[key] = accessToken{token: tr.AccessToken, exp: c.now().Add(time.Duration(tr.ExpiresIn)*time.Second - time.Minute)}
	c.mu.Unlock()
	return tr.AccessToken, nil
}

// RevokeToken Google'da tokenni bekor qiladi (plagin uzilganda).
func (c *Client) RevokeToken(ctx context.Context, token string) {
	sum := sha256.Sum256([]byte(token))
	c.mu.Lock()
	c.init()
	delete(c.ptokens, string(sum[:]))
	c.mu.Unlock()
	c.revoke(ctx, token)
}

func hasScope(granted, want string) bool {
	for _, s := range strings.Fields(granted) {
		if s == want {
			return true
		}
	}
	return false
}

// emailFromIDToken: id_token to'g'ridan-to'g'ri Google token endpoint'idan TLS orqali
// olingan (OIDC Core 3.1.3.7) — faqat ko'rsatish uchun email o'qiladi, imzo tekshirilmaydi.
func emailFromIDToken(tok string) string {
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		return ""
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var claims struct {
		Email string `json:"email"`
	}
	if json.Unmarshal(raw, &claims) != nil || len(claims.Email) > 254 {
		return ""
	}
	return claims.Email
}

func aad(owner int64) []byte { return []byte("google:" + strconv.FormatInt(owner, 10)) }

func (c *Client) tokenRequest(ctx context.Context, form url.Values) (tokenResponse, error) {
	form.Set("client_id", c.ClientID)
	form.Set("client_secret", c.ClientSecret)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Endpoints.Token, strings.NewReader(form.Encode()))
	if err != nil {
		return tokenResponse{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return tokenResponse{}, fmt.Errorf("google token: tarmoq xatosi")
	}
	defer resp.Body.Close()
	var tr tokenResponse
	_ = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&tr)
	if resp.StatusCode != http.StatusOK || tr.AccessToken == "" {
		if tr.Error == "invalid_grant" {
			return tr, ErrNotConnected
		}
		return tr, fmt.Errorf("google token: %d %s", resp.StatusCode, tr.Error)
	}
	return tr, nil
}

// Status — Mini App uchun: ulanganmi va qaysi email.
func (c *Client) Status(ctx context.Context, owner int64) (bool, string) {
	l, err := c.Store.GetGoogleLink(ctx, owner)
	if err != nil {
		return false, ""
	}
	return true, l.Email
}

// Connected — kotib Google kontekstidan foydalanishi mumkinmi.
func (c *Client) Connected(ctx context.Context, owner int64) bool {
	if !c.Enabled() {
		return false
	}
	ok, _ := c.Status(ctx, owner)
	return ok
}

// Disconnect Google'da tokenni bekor qiladi va ulanishni o'chiradi.
func (c *Client) Disconnect(ctx context.Context, owner int64) error {
	l, err := c.Store.GetGoogleLink(ctx, owner)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if rt, err := c.Vault.Open(l.Token, aad(owner)); err == nil {
		c.revoke(ctx, string(rt))
	}
	c.mu.Lock()
	c.init()
	c.forgetLocked(owner, true)
	c.mu.Unlock()
	if err := c.Store.DeleteGoogleLink(ctx, owner); err != nil {
		return err
	}
	c.Store.Audit(ctx, owner, "google_disconnected", "")
	return nil
}

// forgetLocked egasining keshlarini tozalaydi (c.mu ushlangan holda).
func (c *Client) forgetLocked(owner int64, tokens bool) {
	if tokens {
		delete(c.tokens, owner)
	}
	delete(c.busy, owner)
	delete(c.warmed, owner)
	for k := range c.contacts {
		if k.owner == owner {
			delete(c.contacts, k)
		}
	}
}

func (c *Client) revoke(ctx context.Context, token string) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Endpoints.Revoke,
		strings.NewReader(url.Values{"token": {token}}.Encode()))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if resp, err := c.HTTP.Do(req); err == nil {
		resp.Body.Close()
	} else {
		c.log().Warn("google revoke", "err", "tarmoq xatosi")
	}
}

// accessToken keshdan yoki refresh token orqali access token beradi. Google refresh
// tokenni bekor qilgan bo'lsa (invalid_grant), ulanish o'chiriladi.
func (c *Client) accessToken(ctx context.Context, owner int64) (string, error) {
	c.mu.Lock()
	c.init()
	t, ok := c.tokens[owner]
	c.mu.Unlock()
	if ok && c.now().Before(t.exp) {
		return t.token, nil
	}
	l, err := c.Store.GetGoogleLink(ctx, owner)
	if errors.Is(err, store.ErrNotFound) {
		return "", ErrNotConnected
	}
	if err != nil {
		return "", err
	}
	rt, err := c.Vault.Open(l.Token, aad(owner))
	if err != nil {
		return "", fmt.Errorf("google token ochilmadi: %w", err)
	}
	tr, err := c.tokenRequest(ctx, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {string(rt)}})
	if errors.Is(err, ErrNotConnected) {
		// Mijoz Google'da ruxsatni olib tashlagan: ulanishni o'chiramiz.
		_ = c.Store.DeleteGoogleLink(ctx, owner)
		c.Store.Audit(ctx, owner, "google_revoked", "")
		return "", ErrNotConnected
	}
	if err != nil {
		return "", err
	}
	c.mu.Lock()
	if len(c.tokens) < maxCacheItems {
		c.tokens[owner] = accessToken{token: tr.AccessToken, exp: c.now().Add(time.Duration(tr.ExpiresIn)*time.Second - time.Minute)}
	}
	c.mu.Unlock()
	return tr.AccessToken, nil
}

// apiError — Google API'dan 2xx bo'lmagan javob (tana sizdirilmaydi).
type apiError struct{ code int }

func (e *apiError) Error() string { return fmt.Sprintf("google api %d", e.code) }

// do API so'rovini bajaradi; 401 bo'lsa tokenni yangilab bir marta qayta urinadi.
func (c *Client) do(ctx context.Context, owner int64, method, endpoint string, in, out any) error {
	var body []byte
	if in != nil {
		body, _ = json.Marshal(in)
	}
	for attempt := 0; attempt < 2; attempt++ {
		tok, err := c.accessToken(ctx, owner)
		if err != nil {
			return err
		}
		req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+tok)
		if in != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := c.HTTP.Do(req)
		if err != nil {
			return fmt.Errorf("google api: tarmoq xatosi")
		}
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
		resp.Body.Close()
		switch {
		case resp.StatusCode == http.StatusUnauthorized && attempt == 0:
			c.mu.Lock()
			delete(c.tokens, owner)
			c.mu.Unlock()
			continue
		case resp.StatusCode == http.StatusForbidden:
			return ErrNoScope
		case resp.StatusCode/100 != 2:
			return &apiError{code: resp.StatusCode}
		}
		if out != nil {
			return json.Unmarshal(data, out)
		}
		return nil
	}
	return ErrNotConnected
}
