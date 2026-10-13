package miniapp

import (
	"encoding/hex"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// signInitData test uchun to'g'ri imzolangan initData yasaydi.
func signInitData(botToken string, authAt time.Time, userJSON string) string {
	vals := url.Values{}
	vals.Set("auth_date", strconv.FormatInt(authAt.Unix(), 10))
	vals.Set("query_id", "AAH")
	vals.Set("user", userJSON)

	var pairs []string
	for k, v := range vals {
		pairs = append(pairs, k+"="+v[0])
	}
	sort.Strings(pairs)
	secret := hmacSHA256([]byte("WebAppData"), []byte(botToken))
	vals.Set("hash", hex.EncodeToString(hmacSHA256(secret, []byte(strings.Join(pairs, "\n")))))
	return vals.Encode()
}

const tok = "123456:TEST-TOKEN"
const userJSON = `{"id":777,"first_name":"Ali","username":"ali"}`

func TestValidateOK(t *testing.T) {
	now := time.Now()
	u, err := ValidateInitData(signInitData(tok, now, userJSON), tok, time.Hour, now)
	if err != nil || u.ID != 777 {
		t.Fatalf("%v %+v", err, u)
	}
}

func TestValidateWrongToken(t *testing.T) {
	now := time.Now()
	if _, err := ValidateInitData(signInitData(tok, now, userJSON), "999:OTHER", time.Hour, now); err != ErrBadInitData {
		t.Fatalf("boshqa token bilan o'tmasligi kerak: %v", err)
	}
}

func TestValidateTamperedUser(t *testing.T) {
	now := time.Now()
	good := signInitData(tok, now, userJSON)
	evil := strings.Replace(good, url.QueryEscape(`"id":777`), url.QueryEscape(`"id":1`), 1)
	if evil == good {
		t.Fatal("test noto'g'ri tuzilgan")
	}
	if _, err := ValidateInitData(evil, tok, time.Hour, now); err != ErrBadInitData {
		t.Fatalf("user ID almashtirilsa o'tmasligi kerak: %v", err)
	}
}

func TestValidateExpired(t *testing.T) {
	now := time.Now()
	old := signInitData(tok, now.Add(-2*time.Hour), userJSON)
	if _, err := ValidateInitData(old, tok, time.Hour, now); err != ErrExpired {
		t.Fatalf("eski initData o'tmasligi kerak: %v", err)
	}
}

func TestValidateMissingHash(t *testing.T) {
	if _, err := ValidateInitData("auth_date=1&user=%7B%7D", tok, time.Hour, time.Now()); err == nil {
		t.Fatal("hash'siz o'tmasligi kerak")
	}
}
