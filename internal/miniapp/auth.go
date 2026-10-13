// Package miniapp: Telegram Mini App uchun HTTP API va initData autentifikatsiyasi.
package miniapp

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

var (
	ErrBadInitData = errors.New("initData noto'g'ri")
	ErrExpired     = errors.New("initData muddati o'tgan")
)

type User struct {
	ID        int64  `json:"id"`
	FirstName string `json:"first_name"`
	Username  string `json:"username"`
	Language  string `json:"language_code"`
}

// ValidateInitData Telegram hujjatiga mos tekshiradi:
//
//	secret = HMAC_SHA256(key="WebAppData", msg=botToken)
//	hash   = hex(HMAC_SHA256(key=secret, msg=data_check_string))
//
// data_check_string — "hash"dan boshqa barcha key=value juftliklari, kalit bo'yicha
// tartiblangan, "\n" bilan ulangan. maxAge dan eski auth_date rad etiladi (replay himoyasi).
func ValidateInitData(initData, botToken string, maxAge time.Duration, now time.Time) (User, error) {
	vals, err := url.ParseQuery(initData)
	if err != nil {
		return User{}, ErrBadInitData
	}
	got := vals.Get("hash")
	if got == "" {
		return User{}, ErrBadInitData
	}
	vals.Del("hash")

	pairs := make([]string, 0, len(vals))
	for k, v := range vals {
		if len(v) != 1 {
			return User{}, ErrBadInitData
		}
		pairs = append(pairs, k+"="+v[0])
	}
	sort.Strings(pairs)
	dcs := strings.Join(pairs, "\n")

	secret := hmacSHA256([]byte("WebAppData"), []byte(botToken))
	want := hex.EncodeToString(hmacSHA256(secret, []byte(dcs)))
	if !hmac.Equal([]byte(want), []byte(strings.ToLower(got))) {
		return User{}, ErrBadInitData
	}

	ts, err := strconv.ParseInt(vals.Get("auth_date"), 10, 64)
	if err != nil {
		return User{}, ErrBadInitData
	}
	authAt := time.Unix(ts, 0)
	if now.Sub(authAt) > maxAge || authAt.Sub(now) > 5*time.Minute {
		return User{}, ErrExpired
	}

	var u User
	if err := json.Unmarshal([]byte(vals.Get("user")), &u); err != nil || u.ID <= 0 {
		return User{}, ErrBadInitData
	}
	return u, nil
}

func hmacSHA256(key, msg []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(msg)
	return h.Sum(nil)
}
