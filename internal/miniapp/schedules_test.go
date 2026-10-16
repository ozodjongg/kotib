package miniapp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

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
	if r, m := do(t, "PUT", srv.URL+"/api/plugins/youtube", a, `{"enabled":true,"config":{}}`); r.StatusCode != 400 || !strings.Contains(m["error"].(string), "kalit") {
		t.Fatalf("kalitsiz youtube: %d %v", r.StatusCode, m)
	}
	r, m := do(t, "PUT", srv.URL+"/api/plugins/youtube", a, `{"enabled":true,"config":{"api_key":"AIzaSyVERYSECRET9876","region":"UZ"}}`)
	if r.StatusCode != 200 || m["config"].(map[string]any)["api_key"] != "••••9876" {
		t.Fatalf("youtube: %d %v", r.StatusCode, m)
	}
	// Bazada kalit ochiq saqlanmaydi.
	recs, _ := st.ListPluginConfigs(context.Background(), 61)
	if len(recs) != 1 || strings.Contains(string(recs[0].Config), "VERYSECRET") {
		t.Fatalf("shifrlanmagan: %v", recs)
	}
	// Kalitni qayta yubormasdan o'chirib qo'yish — kalit saqlanadi.
	if r, _ := do(t, "PUT", srv.URL+"/api/plugins/youtube", a, `{"enabled":false,"config":{"region":"RU"}}`); r.StatusCode != 200 {
		t.Fatal("o'chirish")
	}
	_, list = doList(t, "GET", srv.URL+"/api/plugins", a)
	for _, p := range list {
		if p["id"] == "youtube" {
			cfg := p["config"].(map[string]any)
			if p["enabled"] != false || p["configured"] != true || cfg["api_key"] != "••••9876" || cfg["region"] != "RU" {
				t.Fatalf("youtube holati: %v", p)
			}
		}
	}
	if r, _ := do(t, "PUT", srv.URL+"/api/plugins/gmail", a, `{"enabled":true,"config":{}}`); r.StatusCode != 400 {
		t.Fatal("tez orada plagin yoqilmasligi kerak")
	}
	if r, _ := do(t, "PUT", srv.URL+"/api/plugins/nope", a, `{"enabled":true,"config":{}}`); r.StatusCode != 404 {
		t.Fatal("noma'lum plagin")
	}
	if r, _ := do(t, "DELETE", srv.URL+"/api/plugins/youtube", a, ``); r.StatusCode != 200 {
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

func TestSchedulesDisabled(t *testing.T) {
	srv, _, _ := setup(t)
	if r, _ := do(t, "GET", srv.URL+"/api/schedules", userAuth("5"), ""); r.StatusCode != 404 {
		t.Fatal(r.StatusCode)
	}
	if r, _ := do(t, "GET", srv.URL+"/api/plugins", userAuth("5"), ""); r.StatusCode != 404 {
		t.Fatal(r.StatusCode)
	}
}

// doList — JSON massiv qaytaradigan so'rov.
func doList(t *testing.T, method, url, auth string) (int, []map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(method, url, nil)
	req.Header.Set("Authorization", auth)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out []map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}
