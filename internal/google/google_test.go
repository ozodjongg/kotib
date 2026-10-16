package google

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ozodjongg/secretar_ai_agent/internal/crypto"
	"github.com/ozodjongg/secretar_ai_agent/internal/llm"
	"github.com/ozodjongg/secretar_ai_agent/internal/store"
)

// fakeGoogle — OAuth va API endpoint'larining soxta serveri.
type fakeGoogle struct {
	mu        sync.Mutex
	scope     string
	verifier  string // oxirgi code_verifier
	refreshes int
	revoked   []string
	badGrant  bool
	events    []map[string]any
	tasks     []map[string]any
	queries   []string
	freebusy  int
}

func (f *fakeGoogle) handler(t *testing.T) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.Form.Get("client_secret") != "sec" {
			w.WriteHeader(401)
			return
		}
		switch r.Form.Get("grant_type") {
		case "authorization_code":
			f.verifier = r.Form.Get("code_verifier")
			claims, _ := json.Marshal(map[string]string{"email": "ali@example.com"})
			idt := "x." + base64.RawURLEncoding.EncodeToString(claims) + ".y"
			json.NewEncoder(w).Encode(map[string]any{
				"access_token": "AT1", "expires_in": 3600, "refresh_token": "RT-SECRET",
				"scope": f.scope, "id_token": idt,
			})
		case "refresh_token":
			if f.badGrant || r.Form.Get("refresh_token") != "RT-SECRET" {
				w.WriteHeader(400)
				w.Write([]byte(`{"error":"invalid_grant"}`))
				return
			}
			f.refreshes++
			json.NewEncoder(w).Encode(map[string]any{"access_token": "AT2", "expires_in": 3600})
		}
	})
	mux.HandleFunc("POST /revoke", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		f.mu.Lock()
		f.revoked = append(f.revoked, r.Form.Get("token"))
		f.mu.Unlock()
	})
	auth := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer AT") {
				w.WriteHeader(401)
				return
			}
			next(w, r)
		}
	}
	mux.HandleFunc("POST /cal/freeBusy", auth(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.freebusy++
		f.mu.Unlock()
		w.Write([]byte(`{"calendars":{"primary":{"busy":[{"start":"2026-10-15T10:00:00Z","end":"2026-10-15T11:00:00Z"}]}}}`))
	}))
	mux.HandleFunc("POST /cal/calendars/primary/events", auth(func(w http.ResponseWriter, r *http.Request) {
		var m map[string]any
		_ = json.NewDecoder(r.Body).Decode(&m)
		f.mu.Lock()
		f.events = append(f.events, m)
		f.mu.Unlock()
		w.Write([]byte(`{"htmlLink":"https://calendar.google.com/e/1"}`))
	}))
	mux.HandleFunc("POST /tasks/lists/@default/tasks", auth(func(w http.ResponseWriter, r *http.Request) {
		var m map[string]any
		_ = json.NewDecoder(r.Body).Decode(&m)
		f.mu.Lock()
		f.tasks = append(f.tasks, m)
		f.mu.Unlock()
		w.Write([]byte(`{}`))
	}))
	mux.HandleFunc("GET /people/people/me/connections", auth(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("personFields") != "names,birthdays,phoneNumbers" {
			w.WriteHeader(400)
			return
		}
		if r.URL.Query().Get("pageToken") == "" {
			w.Write([]byte(`{"connections":[` +
				`{"names":[{"displayName":"Aziz Karimov"}],"birthdays":[{"date":{"year":1990,"month":10,"day":15}}],` +
				`"phoneNumbers":[{"value":"90 123 45 67","canonicalForm":"+998901234567"}]},` +
				`{"names":[{"displayName":"Vali"}],"birthdays":[{"date":{"month":3,"day":1}}]}],"nextPageToken":"p2"}`))
			return
		}
		w.Write([]byte(`{"connections":[{"names":[{"displayName":"Onam"}],"birthdays":[{"date":{"month":10,"day":15}}]}]}`))
	}))
	mux.HandleFunc("GET /people/people:searchContacts", auth(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("query")
		f.mu.Lock()
		f.queries = append(f.queries, q)
		f.mu.Unlock()
		if q == "+998901234567" {
			w.Write([]byte(`{"results":[{"person":{"names":[{"displayName":"Aziz Karimov"}],` +
				`"biographies":[{"value":"Hamkor, juma kunlari band"}],` +
				`"phoneNumbers":[{"value":"90 123 45 67","canonicalForm":"+998901234567"}]}}]}`))
			return
		}
		w.Write([]byte(`{}`))
	}))
	return mux
}

func newClient(t *testing.T, scope string) (*Client, *fakeGoogle, *store.Memory) {
	t.Helper()
	f := &fakeGoogle{scope: scope}
	srv := httptest.NewServer(f.handler(t))
	t.Cleanup(srv.Close)
	keys, err := crypto.NewStaticKeyFromBase64(base64.StdEncoding.EncodeToString(make([]byte, 32)))
	if err != nil {
		t.Fatal(err)
	}
	st := store.NewMemory()
	_ = st.UpsertAccount(context.Background(), store.Account{OwnerID: 7, Status: store.StatusActive})
	c := New("cid", "sec", "https://kotib.example/oauth/google/callback", st, crypto.NewVault(keys), nil)
	c.Endpoints = Endpoints{
		Auth: srv.URL + "/auth", Token: srv.URL + "/token", Revoke: srv.URL + "/revoke",
		Calendar: srv.URL + "/cal", Tasks: srv.URL + "/tasks", People: srv.URL + "/people",
	}
	return c, f, st
}

const fullScope = "openid email https://www.googleapis.com/auth/calendar.events https://www.googleapis.com/auth/calendar.freebusy https://www.googleapis.com/auth/contacts.readonly https://www.googleapis.com/auth/tasks"

func connect(t *testing.T, c *Client) {
	t.Helper()
	u, err := c.AuthURL(7)
	if err != nil {
		t.Fatal(err)
	}
	pu, _ := url.Parse(u)
	owner, err := c.Callback(context.Background(), pu.Query().Get("state"), "CODE")
	if err != nil || owner != 7 {
		t.Fatalf("callback: %d %v", owner, err)
	}
}

func TestOAuthFlow(t *testing.T) {
	c, f, st := newClient(t, fullScope)
	ctx := context.Background()
	u, err := c.AuthURL(7)
	if err != nil {
		t.Fatal(err)
	}
	pu, _ := url.Parse(u)
	q := pu.Query()
	if q.Get("access_type") != "offline" || q.Get("code_challenge_method") != "S256" || q.Get("client_id") != "cid" {
		t.Fatalf("auth url: %s", u)
	}
	state := q.Get("state")
	if _, err := c.Callback(ctx, "begona", "CODE"); !errors.Is(err, ErrBadState) {
		t.Fatal("noma'lum state rad etilishi kerak")
	}
	owner, err := c.Callback(ctx, state, "CODE")
	if err != nil || owner != 7 {
		t.Fatalf("%d %v", owner, err)
	}
	// PKCE: serverga yuborilgan verifier challenge'ga mos.
	sum := sha256.Sum256([]byte(f.verifier))
	if base64.RawURLEncoding.EncodeToString(sum[:]) != q.Get("code_challenge") {
		t.Fatal("PKCE verifier mos emas")
	}
	if _, err := c.Callback(ctx, state, "CODE"); !errors.Is(err, ErrBadState) {
		t.Fatal("state bir martalik bo'lishi kerak")
	}
	l, err := st.GetGoogleLink(ctx, 7)
	if err != nil || l.Email != "ali@example.com" {
		t.Fatalf("link: %+v %v", l, err)
	}
	if strings.Contains(string(l.Token), "RT-SECRET") {
		t.Fatal("refresh token ochiq saqlanmasligi kerak")
	}
	if ok, email := c.Status(ctx, 7); !ok || email != "ali@example.com" {
		t.Fatal("status")
	}
}

func TestStateExpires(t *testing.T) {
	c, _, _ := newClient(t, fullScope)
	now := time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC)
	c.Now = func() time.Time { return now }
	u, _ := c.AuthURL(7)
	pu, _ := url.Parse(u)
	now = now.Add(16 * time.Minute)
	if _, err := c.Callback(context.Background(), pu.Query().Get("state"), "CODE"); !errors.Is(err, ErrBadState) {
		t.Fatal("eskirgan state rad etilishi kerak")
	}
}

func TestCalendarScopeRequired(t *testing.T) {
	c, f, st := newClient(t, "openid email https://www.googleapis.com/auth/tasks")
	u, _ := c.AuthURL(7)
	pu, _ := url.Parse(u)
	if _, err := c.Callback(context.Background(), pu.Query().Get("state"), "CODE"); !errors.Is(err, ErrNoScope) {
		t.Fatalf("taqvim ruxsatisiz ulanmasligi kerak: %v", err)
	}
	if _, err := st.GetGoogleLink(context.Background(), 7); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("saqlanmasligi kerak")
	}
	if len(f.revoked) != 1 {
		t.Fatal("token bekor qilinishi kerak")
	}
}

func TestRefreshAndRevokedGrant(t *testing.T) {
	c, f, st := newClient(t, fullScope)
	ctx := context.Background()
	connect(t, c)
	c.mu.Lock()
	c.tokens = map[int64]accessToken{} // access token eskirdi
	c.mu.Unlock()
	if _, err := c.Busy(ctx, 7, time.Now(), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if f.refreshes != 1 {
		t.Fatalf("refresh: %d", f.refreshes)
	}
	// Mijoz Google'da ruxsatni olib tashladi.
	f.badGrant = true
	c.mu.Lock()
	c.tokens = map[int64]accessToken{}
	c.busy = map[int64]busyCache{}
	c.mu.Unlock()
	if _, err := c.Busy(ctx, 7, time.Now(), time.Now().Add(time.Hour)); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("invalid_grant: %v", err)
	}
	if _, err := st.GetGoogleLink(ctx, 7); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("bekor qilingan ulanish o'chirilishi kerak")
	}
}

func TestBusyCached(t *testing.T) {
	c, f, _ := newClient(t, fullScope)
	connect(t, c)
	for i := 0; i < 3; i++ {
		b, err := c.Busy(context.Background(), 7, time.Now(), time.Now().Add(time.Hour))
		if err != nil || len(b) != 1 || b[0].End.Sub(b[0].Start) != time.Hour {
			t.Fatalf("%v %v", b, err)
		}
	}
	if f.freebusy != 1 {
		t.Fatalf("kesh ishlamadi: %d", f.freebusy)
	}
}

func TestContactByPhone(t *testing.T) {
	c, f, _ := newClient(t, fullScope)
	connect(t, c)
	name, note := c.Contact(context.Background(), 7, 100, "Aziz", "998901234567")
	if name != "Aziz Karimov" || note != "Hamkor, juma kunlari band" {
		t.Fatalf("%q %q", name, note)
	}
	if f.queries[0] != "" {
		t.Fatal("birinchi so'rov keshni isitishi kerak")
	}
	n := len(f.queries)
	c.Contact(context.Background(), 7, 100, "Aziz", "998901234567")
	if len(f.queries) != n {
		t.Fatal("natija keshlanishi kerak")
	}
	if name, _ := c.Contact(context.Background(), 7, 101, "Noma'lum", ""); name != "" {
		t.Fatal("topilmasa bo'sh")
	}
}

func TestExecuteAndDisconnect(t *testing.T) {
	c, f, _ := newClient(t, fullScope)
	ctx := context.Background()
	connect(t, c)
	loc := time.FixedZone("Asia/Tashkent", 5*3600)
	ev, _ := NewEventPayload(llm.Event{Title: "Aziz bilan uchrashuv", Start: time.Date(2026, 10, 15, 15, 0, 0, 0, loc), Duration: time.Hour}, "Aziz")
	a := store.Action{OwnerID: 7, Kind: store.ActionEvent, Payload: ev}
	if d := Describe(a); !strings.Contains(d, "15.10.2026, 15:00–16:00") || !strings.Contains(d, "Aziz") {
		t.Fatalf("describe: %q", d)
	}
	if _, err := c.Execute(ctx, a); err != nil {
		t.Fatal(err)
	}
	start := f.events[0]["start"].(map[string]any)
	if f.events[0]["summary"] != "Aziz bilan uchrashuv" || start["dateTime"] != "2026-10-15T15:00:00+05:00" {
		t.Fatalf("event: %v", f.events[0])
	}
	tk, _ := NewTaskPayload(llm.Task{Title: "Hujjatni yuborish", Due: time.Date(2026, 10, 16, 0, 0, 0, 0, loc)}, "Aziz")
	if _, err := c.Execute(ctx, store.Action{OwnerID: 7, Kind: store.ActionTask, Payload: tk}); err != nil {
		t.Fatal(err)
	}
	if f.tasks[0]["due"] != "2026-10-16T00:00:00Z" {
		t.Fatalf("task: %v", f.tasks[0])
	}
	if err := c.Disconnect(ctx, 7); err != nil {
		t.Fatal(err)
	}
	if len(f.revoked) != 1 || f.revoked[0] != "RT-SECRET" {
		t.Fatalf("revoke: %v", f.revoked)
	}
	if c.Connected(ctx, 7) {
		t.Fatal("uzilgan bo'lishi kerak")
	}
	if _, err := c.Execute(ctx, a); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("uzilgandan keyin: %v", err)
	}
}

func TestErrorsDoNotLeakBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.WriteHeader(500)
		w.Write([]byte("SECRET-DETAIL"))
	}))
	defer srv.Close()
	c, _, _ := newClient(t, fullScope)
	connect(t, c)
	c.Endpoints.Calendar = srv.URL
	_, err := c.Busy(context.Background(), 7, time.Now(), time.Now().Add(time.Hour))
	if err == nil || strings.Contains(err.Error(), "SECRET") {
		t.Fatalf("%v", err)
	}
}

func TestBirthdays(t *testing.T) {
	c, _, _ := newClient(t, fullScope)
	connect(t, c)
	bs, err := c.Birthdays(context.Background(), 7, time.October, 15)
	if err != nil || len(bs) != 2 {
		t.Fatalf("%+v %v", bs, err)
	}
	if bs[0].Name != "Aziz Karimov" || bs[0].Phone != "998901234567" || bs[1].Name != "Onam" || bs[1].Phone != "" {
		t.Fatalf("%+v", bs)
	}
	if bs, _ := c.Birthdays(context.Background(), 7, time.March, 2); len(bs) != 0 {
		t.Fatal("boshqa kun")
	}
}
