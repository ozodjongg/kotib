package miniapp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
)

// appOnly — bot chatidan (Invoke) bajarilmaydigan amallar: kirish kodlari va parol, rozilik,
// akkauntni o'chirish va maxfiy avtomatlashtirish havolasi faqat ilovaning o'zida.
var appOnly = map[string]bool{
	"POST /api/consent":        true,
	"POST /api/login/phone":    true,
	"POST /api/login/cancel":   true,
	"POST /api/login/code":     true,
	"POST /api/login/password": true,
	"DELETE /api/account":      true,
	"POST /api/hook":           true,
}

type invokeUser struct{}

// Invoke — Mini App API'sini bot chatidan chaqirish: user — Telegram tasdiqlagan foydalanuvchi
// (bot update'idan), account — tanlangan qo'shimcha akkaunt (0 — asosiy). Brauzerdagi bilan bir
// xil tekshiruvlar (taqiq, qo'shimcha akkaunt, tezlik) qo'llanadi; appOnly amallar 403.
func (s *Server) Invoke(ctx context.Context, user, account int64, method, path string, body any) (int, []byte) {
	var rd *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return http.StatusBadRequest, []byte(`{"error":"JSON noto'g'ri"}`)
		}
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	r, err := http.NewRequestWithContext(context.WithValue(ctx, invokeUser{}, user), method, path, rd)
	if err != nil {
		return http.StatusBadRequest, []byte(`{"error":"so'rov noto'g'ri"}`)
	}
	if account != 0 && account != user {
		r.Header.Set(AccountHeader, strconv.FormatInt(account, 10))
	}
	w := httptest.NewRecorder()
	s.invokeHandler().ServeHTTP(w, r)
	return w.Code, w.Body.Bytes()
}

func (s *Server) invokeHandler() *http.ServeMux {
	s.invokeOnce.Do(func() {
		mux := http.NewServeMux()
		for _, rt := range s.apiRoutes() {
			if appOnly[rt.pattern] {
				mux.HandleFunc(rt.pattern, func(w http.ResponseWriter, _ *http.Request) {
					httpErr(w, http.StatusForbidden, "bu amal faqat ilovada (Mini App) bajariladi")
				})
				continue
			}
			h, main := rt.h, rt.main
			mux.HandleFunc(rt.pattern, func(w http.ResponseWriter, r *http.Request) {
				user, ok := r.Context().Value(invokeUser{}).(int64)
				if !ok || user <= 0 {
					httpErr(w, http.StatusUnauthorized, "avtorizatsiya kerak")
					return
				}
				s.serveAs(w, r, user, h, main)
			})
		}
		s.invokeMux = mux
	})
	return s.invokeMux
}
