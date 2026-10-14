package miniapp

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ozodjongg/secretar_ai_agent/internal/store"
)

type fakeGoogle struct {
	connected    bool
	disconnected int
}

func (g *fakeGoogle) AuthURL(owner int64) (string, error) {
	return "https://accounts.google.com/o/oauth2/v2/auth?state=s", nil
}
func (g *fakeGoogle) Callback(_ context.Context, state, code string) (int64, error) {
	if state != "good" {
		return 0, errors.New("bad state")
	}
	g.connected = true
	return 777, nil
}
func (g *fakeGoogle) Status(context.Context, int64) (bool, string) { return g.connected, "a@x.uz" }
func (g *fakeGoogle) Disconnect(context.Context, int64) error {
	g.disconnected++
	g.connected = false
	return nil
}

func TestGoogleEndpoints(t *testing.T) {
	st := store.NewMemory()
	g := &fakeGoogle{}
	var notified []int64
	s := &Server{BotToken: tok, Store: st, Google: g,
		OnGoogle: func(_ context.Context, owner int64, err error) { notified = append(notified, owner) }}
	srv := newTestServer(t, s)
	auth := "tma " + signInitData(tok, time.Now(), userJSON)

	// Rozilik berilmagan bo'lsa ulash havolasi berilmaydi.
	if code := doReq(t, srv.URL+"/api/google/connect", "POST", auth); code != 400 {
		t.Fatalf("rozisiz: %d", code)
	}
	now := time.Now()
	_ = st.UpsertAccount(context.Background(), store.Account{OwnerID: 777, Status: store.StatusActive, ConsentAt: &now})
	if code := doReq(t, srv.URL+"/api/google/connect", "POST", auth); code != 200 {
		t.Fatalf("connect: %d", code)
	}
	if code := doReq(t, srv.URL+"/api/google/connect", "POST", ""); code != 401 {
		t.Fatal("avtorizatsiyasiz bo'lmasligi kerak")
	}

	resp, err := http.Get(srv.URL + "/oauth/google/callback?state=bad&code=c")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 400 || len(notified) != 0 {
		t.Fatalf("yaroqsiz state: %d", resp.StatusCode)
	}
	resp, _ = http.Get(srv.URL + "/oauth/google/callback?state=good&code=c")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(body), "ulandi") || len(notified) != 1 {
		t.Fatalf("callback: %d %s", resp.StatusCode, body)
	}

	if code := doReq(t, srv.URL+"/api/google/disconnect", "POST", auth); code != 200 || g.disconnected != 1 {
		t.Fatalf("disconnect: %d", code)
	}
}

func doReq(t *testing.T, url, method, auth string) int {
	t.Helper()
	req, _ := http.NewRequest(method, url, nil)
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}
