package miniapp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ozodjongg/secretar_ai_agent/internal/billing"
	"github.com/ozodjongg/secretar_ai_agent/internal/crypto"
	"github.com/ozodjongg/secretar_ai_agent/internal/plugin"
	"github.com/ozodjongg/secretar_ai_agent/internal/schedule"
	"github.com/ozodjongg/secretar_ai_agent/internal/store"
)

type fakeSched struct {
	runs atomic.Int32
	err  error
}

func (f *fakeSched) RunNow(context.Context, int64, int64) error {
	f.runs.Add(1)
	return f.err
}

type stubSearch struct{}

func (stubSearch) WebSearch(context.Context, string) (string, error) { return "", nil }

func testVault(t *testing.T) *crypto.Vault {
	t.Helper()
	keys, err := crypto.NewStaticKeyFromBase64("MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=")
	if err != nil {
		t.Fatal(err)
	}
	return crypto.NewVault(keys)
}

func schedSetup(t *testing.T) (*httptest.Server, *store.Memory, *fakeSched, string) {
	t.Helper()
	st := store.NewMemory()
	fs := &fakeSched{}
	srv := httptest.NewServer((&Server{BotToken: tok, Store: st, Ctl: &fakeCtl{}, Schedules: fs,
		Plugins: plugin.Default(plugin.Options{Search: stubSearch{}}), Vault: testVault(t)}).Handler())
	t.Cleanup(srv.Close)
	a := userAuth("61")
	return srv, st, fs, a
}

func TestSchedulesAPI(t *testing.T) {
	srv, st, fs, a := schedSetup(t)
	body := `{"title":"","prompt":"Bugungi ob-havoni ayt","kind":"daily","at":"07:30","date":"","weekdays":0,"enabled":true}`
	if r, _ := do(t, "POST", srv.URL+"/api/schedules", a, body); r.StatusCode != 403 {
		t.Fatalf("rozilikdan oldin: %d", r.StatusCode)
	}
	do(t, "POST", srv.URL+"/api/consent", a, `{"accepted":true}`)
	r, m := do(t, "POST", srv.URL+"/api/schedules", a, body)
	if r.StatusCode != 200 || m["title"] != "Bugungi ob-havoni ayt" || m["next_run"] == nil || m["describe"] != "har kuni 07:30" {
		t.Fatalf("create: %d %v", r.StatusCode, m)
	}
	id := int64(m["id"].(float64))
	for _, bad := range []string{
		`{"prompt":"","kind":"daily","at":"07:30"}`,
		`{"prompt":"x","kind":"daily","at":"7:30"}`,
		`{"prompt":"x","kind":"weekly","at":"07:30","weekdays":0}`,
		`{"prompt":"x","kind":"once","at":"07:30","date":"2020-01-01"}`,
		`{"prompt":"x","kind":"daily","at":"07:30","hack":1}`,
	} {
		if r, m := do(t, "POST", srv.URL+"/api/schedules", a, bad); r.StatusCode != 400 {
			t.Fatalf("%s: %d %v", bad, r.StatusCode, m)
		}
	}
	// Tahrirlash: o'chirish → NextRun yo'q.
	r, m = do(t, "PUT", srv.URL+"/api/schedules/"+strconv.FormatInt(id, 10), a, `{"title":"Ob-havo","prompt":"p","kind":"weekly","at":"08:00","weekdays":34,"enabled":false}`)
	if r.StatusCode != 200 || m["enabled"] != false || m["next_run"] != nil || m["describe"] != "Du, Ju 08:00" {
		t.Fatalf("update: %d %v", r.StatusCode, m)
	}
	// Boshqa foydalanuvchi ko'rmaydi va o'zgartira olmaydi.
	b := userAuth("62")
	do(t, "POST", srv.URL+"/api/consent", b, `{"accepted":true}`)
	if r, _ := do(t, "PUT", srv.URL+"/api/schedules/"+strconv.FormatInt(id, 10), b, `{"prompt":"p","kind":"daily","at":"08:00"}`); r.StatusCode != 404 {
		t.Fatalf("begona update: %d", r.StatusCode)
	}
	if r, _ := do(t, "DELETE", srv.URL+"/api/schedules/"+strconv.FormatInt(id, 10), b, ``); r.StatusCode != 404 {
		t.Fatal("begona delete")
	}
	if list, _ := st.ListSchedules(context.Background(), 62); len(list) != 0 {
		t.Fatal("begonada vazifa yo'q")
	}
	if r, _ := do(t, "POST", srv.URL+"/api/schedules/"+strconv.FormatInt(id, 10)+"/run", a, ``); r.StatusCode != 200 || fs.runs.Load() != 1 {
		t.Fatalf("run: %d", r.StatusCode)
	}
	for err, code := range map[error]int{schedule.ErrBusy: 409, schedule.ErrNotEntitle: 402, schedule.ErrDailyLimit: 429, store.ErrNotFound: 404} {
		fs.err = err
		if r, _ := do(t, "POST", srv.URL+"/api/schedules/"+strconv.FormatInt(id, 10)+"/run", a, ``); r.StatusCode != code {
			t.Fatalf("%v: %d", err, r.StatusCode)
		}
	}
	r, m = do(t, "GET", srv.URL+"/api/schedules", a, "")
	if r.StatusCode != 200 || len(m["items"].([]any)) != 1 || m["max"].(float64) != schedule.MaxPerOwner || m["tz"] != "Asia/Tashkent" {
		t.Fatalf("list: %v", m)
	}
	if r, _ := do(t, "DELETE", srv.URL+"/api/schedules/"+strconv.FormatInt(id, 10), a, ``); r.StatusCode != 200 {
		t.Fatal("delete")
	}
	// Chegara.
	for i := 0; i < schedule.MaxPerOwner; i++ { // API tezlik chegarasiga tegmaslik uchun to'g'ridan-to'g'ri
		if _, err := st.CreateSchedule(context.Background(), store.Schedule{OwnerID: 61, Title: "x", Prompt: "x", Kind: store.ScheduleDaily, At: "09:00"}); err != nil {
			t.Fatal(err)
		}
	}
	if r, m := do(t, "POST", srv.URL+"/api/schedules", a, body); r.StatusCode != 400 || !strings.Contains(m["error"].(string), "ko'pi bilan") {
		t.Fatalf("chegara: %d %v", r.StatusCode, m)
	}
}

func TestPluginsAPI(t *testing.T) {
	srv, st, _, a := schedSetup(t)
	do(t, "POST", srv.URL+"/api/consent", a, `{"accepted":true}`)
	resp, list := doList(t, "GET", srv.URL+"/api/plugins", a)
	if resp != 200 || len(list) < 5 || list[0]["id"] != "web" || list[0]["enabled"] != false {
		t.Fatalf("katalog: %d %v", resp, list)
	}
	for _, p := range list {
		if p["id"] == "youtube" && p["soon"] != true {
			t.Fatal("Google sozlanmagan serverda YouTube «tez orada» bo'lishi kerak")
		}
	}
	r, m := do(t, "PUT", srv.URL+"/api/plugins/currency", a, `{"enabled":true,"config":{"codes":"USD,EUR"}}`)
	if r.StatusCode != 200 || m["config"].(map[string]any)["codes"] != "USD,EUR" {
		t.Fatalf("currency: %d %v", r.StatusCode, m)
	}
	// Bazada sozlama ochiq saqlanmaydi.
	recs, _ := st.ListPluginConfigs(context.Background(), 61)
	if len(recs) != 1 || strings.Contains(string(recs[0].Config), "USD") {
		t.Fatalf("shifrlanmagan: %v", recs)
	}
	if r, _ := do(t, "PUT", srv.URL+"/api/plugins/currency", a, `{"enabled":false,"config":{"codes":"RUB"}}`); r.StatusCode != 200 {
		t.Fatal("o'chirish")
	}
	_, list = doList(t, "GET", srv.URL+"/api/plugins", a)
	for _, p := range list {
		if p["id"] == "currency" {
			if p["enabled"] != false || p["configured"] != true || p["config"].(map[string]any)["codes"] != "RUB" {
				t.Fatalf("currency holati: %v", p)
			}
		}
	}
	if r, _ := do(t, "PUT", srv.URL+"/api/plugins/gmail", a, `{"enabled":true,"config":{}}`); r.StatusCode != 400 {
		t.Fatal("tez orada plagin yoqilmasligi kerak")
	}
	if r, _ := do(t, "PUT", srv.URL+"/api/plugins/nope", a, `{"enabled":true,"config":{}}`); r.StatusCode != 404 {
		t.Fatal("noma'lum plagin")
	}
	if r, _ := do(t, "POST", srv.URL+"/api/plugins/currency/connect", a, ``); r.StatusCode != 404 {
		t.Fatal("kalitsiz plagin akkaunt orqali ulanmaydi")
	}
	if r, _ := do(t, "DELETE", srv.URL+"/api/plugins/currency", a, ``); r.StatusCode != 200 {
		t.Fatal("delete")
	}
	if recs, _ := st.ListPluginConfigs(context.Background(), 61); len(recs) != 0 {
		t.Fatal("o'chirilishi kerak")
	}
	// Yoqilgan plaginlar vazifalar ro'yxatida ko'rsatiladi.
	do(t, "PUT", srv.URL+"/api/plugins/weather", a, `{"enabled":true,"config":{"city":"Samarqand"}}`)
	if _, m := do(t, "GET", srv.URL+"/api/schedules", a, ""); len(m["plugins"].([]any)) != 1 {
		t.Fatalf("plugins: %v", m)
	}
}

// fakePluginAuth — Google OAuth o'rnida (plagin akkaunti).
type fakePluginAuth struct {
	revoked []string
	purpose map[string]string
}

func (f *fakePluginAuth) PluginAuthURL(owner int64, plugin string) (string, error) {
	return "https://accounts.google.com/o/oauth2/v2/auth?state=good&scope=youtube.readonly&p=" + plugin, nil
}
func (f *fakePluginAuth) StatePurpose(state string) string { return f.purpose[state] }
func (f *fakePluginAuth) RevokeToken(_ context.Context, token string) {
	f.revoked = append(f.revoked, token)
}

// YouTube API kalitsiz — mijozning o'z akkaunti orqali ulanadi.
func TestYouTubeAccountConnect(t *testing.T) {
	st := store.NewMemory()
	ctx := context.Background()
	v := testVault(t)
	pa := &fakePluginAuth{purpose: map[string]string{"good": "youtube"}}
	var notified []string
	plugins := plugin.Default(plugin.Options{Search: stubSearch{}, YouTubeToken: func(context.Context, string) (string, error) { return "AT", nil }})
	s := &Server{BotToken: tok, Store: st, Ctl: &fakeCtl{}, Google: &fakeGoogle{}, Plugins: plugins, Vault: v, PluginAuth: pa,
		OnGoogle: func(context.Context, int64, error) { t.Fatal("YouTube ulanishi Google xabari bermasligi kerak") },
		OnPlugin: func(_ context.Context, owner int64, id string, err error) { notified = append(notified, id) }}
	srv := newTestServer(t, s)
	a := "tma " + signInitData(tok, time.Now(), userJSON) // owner 777
	if r, _ := do(t, "POST", srv.URL+"/api/plugins/youtube/connect", a, ``); r.StatusCode != 403 {
		t.Fatalf("rozilikdan oldin: %d", r.StatusCode)
	}
	do(t, "POST", srv.URL+"/api/consent", a, `{"accepted":true}`)
	_, list := doList(t, "GET", srv.URL+"/api/plugins", a)
	for _, p := range list {
		if p["id"] == "youtube" && (p["connect"] != "google" || p["soon"] == true || p["fields"] != nil) {
			t.Fatalf("youtube katalogda: %v", p)
		}
	}
	// Ulanmagan holda yoqib bo'lmaydi.
	if r, m := do(t, "PUT", srv.URL+"/api/plugins/youtube", a, `{"enabled":true,"config":{}}`); r.StatusCode != 400 || !strings.Contains(m["error"].(string), "akkauntni ulang") {
		t.Fatalf("ulanmagan: %d %v", r.StatusCode, m)
	}
	r, m := do(t, "POST", srv.URL+"/api/plugins/youtube/connect", a, ``)
	if r.StatusCode != 200 || !strings.Contains(m["url"].(string), "accounts.google.com") {
		t.Fatalf("connect: %d %v", r.StatusCode, m)
	}
	// Google rozilikdan qaytdi: google.Client.OnPluginToken tokenni saqlaydi (bu yerda — qo'lda).
	if err := plugin.SaveAccount(ctx, st, v, 777, "youtube", "RT-SECRET", "ali@gmail.com"); err != nil {
		t.Fatal(err)
	}
	resp, err := http.Get(srv.URL + "/oauth/google/callback?state=good&code=c")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(body), "YouTube akkauntingiz ulandi") || len(notified) != 1 {
		t.Fatalf("callback: %d %s %v", resp.StatusCode, body, notified)
	}
	_, list = doList(t, "GET", srv.URL+"/api/plugins", a)
	for _, p := range list {
		if p["id"] == "youtube" {
			raw, _ := json.Marshal(p)
			if p["enabled"] != true || p["account"] != "ali@gmail.com" || strings.Contains(string(raw), "RT-SECRET") {
				t.Fatalf("ulangan youtube: %s", raw)
			}
		}
	}
	// O'chirib qo'yish va qayta yoqish: token saqlanadi, Mini App'dan kelgan qiymat e'tiborsiz.
	do(t, "PUT", srv.URL+"/api/plugins/youtube", a, `{"enabled":false,"config":{"refresh_token":"hack"}}`)
	if r, m := do(t, "PUT", srv.URL+"/api/plugins/youtube", a, `{"enabled":true,"config":{}}`); r.StatusCode != 200 || m["account"] != "ali@gmail.com" {
		t.Fatalf("qayta yoqish: %d %v", r.StatusCode, m)
	}
	en := plugins.EnabledFor(ctx, st, v, 777)
	if len(en) != 1 || en[0].Config[plugin.KeyRefreshToken] != "RT-SECRET" {
		t.Fatalf("token o'zgarmasligi kerak: %+v", en)
	}
	// Uzish — Google'da token bekor qilinadi.
	if r, _ := do(t, "DELETE", srv.URL+"/api/plugins/youtube", a, ``); r.StatusCode != 200 || len(pa.revoked) != 1 || pa.revoked[0] != "RT-SECRET" {
		t.Fatalf("uzish: %d %v", r.StatusCode, pa.revoked)
	}
	// Akkaunt o'chirilsa ham plagin tokenlari bekor qilinadi.
	_ = plugin.SaveAccount(ctx, st, v, 777, "youtube", "RT-2", "ali@gmail.com")
	if r, _ := do(t, "DELETE", srv.URL+"/api/account", a, `{"confirm":"DELETE"}`); r.StatusCode != 200 || len(pa.revoked) != 2 || pa.revoked[1] != "RT-2" {
		t.Fatalf("akkaunt o'chirish: %d %v", r.StatusCode, pa.revoked)
	}
}

func TestSchedulesDisabled(t *testing.T) {
	srv, _, _ := setup(t)
	if r, _ := do(t, "GET", srv.URL+"/api/schedules", userAuth("5"), ""); r.StatusCode != 404 {
		t.Fatal(r.StatusCode)
	}
	if r, _ := do(t, "GET", srv.URL+"/api/plugins", userAuth("5"), ""); r.StatusCode != 404 {
		t.Fatal(r.StatusCode)
	}
}

// doList — {"items": [...]} qaytaradigan so'rov.
func doList(t *testing.T, method, url, auth string) (int, []map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(method, url, nil)
	req.Header.Set("Authorization", auth)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out struct {
		Items []map[string]any `json:"items"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out.Items
}

// Vazifalar — Advanced (va undan yuqori), plaginlar — faqat Pro.
func TestTierGates(t *testing.T) {
	st := store.NewMemory()
	ctx := context.Background()
	bill := &billing.Service{Store: st, API: &fakeStarsAPI{}, Price: 150, ProPrice: 250, TrialDays: 7}
	fs := &fakeSched{}
	srv := httptest.NewServer((&Server{BotToken: tok, Store: st, Ctl: &fakeCtl{}, Billing: bill, Schedules: fs,
		Plugins: plugin.Default(plugin.Options{Search: stubSearch{}}), Vault: testVault(t)}).Handler())
	t.Cleanup(srv.Close)
	body := `{"prompt":"ob-havo","kind":"daily","at":"07:30"}`
	setTier := func(id int64, tier string, active bool) {
		until := time.Now().Add(-time.Hour)
		if active {
			until = time.Now().Add(240 * time.Hour)
		}
		if err := st.SaveSubscription(ctx, store.Subscription{OwnerID: id, PaidUntil: until, Tier: tier}); err != nil {
			t.Fatal(err)
		}
	}
	consent := func(id string) string {
		a := userAuth(id)
		do(t, "POST", srv.URL+"/api/consent", a, `{"accepted":true}`)
		return a
	}
	plug := `{"enabled":true,"config":{"city":"Toshkent"}}`

	// Obuna tugagan: vazifa yaratib/bajarib bo'lmaydi, plagin ham yoqilmaydi.
	none := consent("71")
	setTier(71, "", false)
	if r, m := do(t, "POST", srv.URL+"/api/schedules", none, body); r.StatusCode != 402 || !strings.Contains(m["error"].(string), "Advanced") {
		t.Fatalf("obunasiz vazifa: %d %v", r.StatusCode, m)
	}
	if _, m := do(t, "GET", srv.URL+"/api/schedules", none, ""); m["allowed"] != false || m["plugins_allowed"] != false {
		t.Fatalf("obunasiz holat: %v", m)
	}

	// Advanced: vazifalar bor, plaginlar yo'q.
	adv := consent("72")
	setTier(72, "advanced", true)
	r, m := do(t, "POST", srv.URL+"/api/schedules", adv, body)
	if r.StatusCode != 200 {
		t.Fatalf("advanced vazifa: %d %v", r.StatusCode, m)
	}
	id := strconv.FormatInt(int64(m["id"].(float64)), 10)
	if r, _ := do(t, "POST", srv.URL+"/api/schedules/"+id+"/run", adv, ``); r.StatusCode != 200 {
		t.Fatalf("advanced run: %d", r.StatusCode)
	}
	if r, m := do(t, "PUT", srv.URL+"/api/plugins/weather", adv, plug); r.StatusCode != 402 || !strings.Contains(m["error"].(string), "Pro") {
		t.Fatalf("advanced plagin: %d %v", r.StatusCode, m)
	}
	if _, m := do(t, "GET", srv.URL+"/api/plugins", adv, ""); m["allowed"] != false {
		t.Fatalf("advanced plaginlar: %v", m)
	}
	// O'chirib qo'yish va uzish Pro'siz ham mumkin.
	if r, _ := do(t, "PUT", srv.URL+"/api/plugins/weather", adv, `{"enabled":false,"config":{}}`); r.StatusCode != 200 {
		t.Fatal("o'chirib qo'yish")
	}
	if r, _ := do(t, "DELETE", srv.URL+"/api/plugins/weather", adv, ``); r.StatusCode != 200 {
		t.Fatal("uzish")
	}

	// Obuna tugadi: tahrirlash va bajarish yopiladi, o'chirish mumkin.
	setTier(72, "advanced", false)
	if r, _ := do(t, "PUT", srv.URL+"/api/schedules/"+id, adv, body); r.StatusCode != 402 {
		t.Fatalf("muddati o'tgan update: %d", r.StatusCode)
	}
	if r, _ := do(t, "POST", srv.URL+"/api/schedules/"+id+"/run", adv, ``); r.StatusCode != 402 {
		t.Fatalf("muddati o'tgan run: %d", r.StatusCode)
	}
	if r, _ := do(t, "DELETE", srv.URL+"/api/schedules/"+id, adv, ``); r.StatusCode != 200 {
		t.Fatal("o'chirish obunasiz ham mumkin")
	}

	// Pro: hammasi.
	pro := consent("73")
	setTier(73, "pro", true)
	if r, _ := do(t, "PUT", srv.URL+"/api/plugins/weather", pro, plug); r.StatusCode != 200 {
		t.Fatalf("pro plagin: %d", r.StatusCode)
	}
	if _, m := do(t, "GET", srv.URL+"/api/schedules", pro, ""); m["plugins_allowed"] != true || len(m["plugins"].([]any)) != 1 {
		t.Fatalf("pro holat: %v", m)
	}
}
