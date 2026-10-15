package miniapp

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ozodjongg/secretar_ai_agent/internal/billing"
	"github.com/ozodjongg/secretar_ai_agent/internal/store"
)

// Bir nechta Telegram akkaunt (Pro). Qo'shish oqimi botcmd/accounts.go'da tasvirlangan:
// bu yerda havola beriladi, ro'yxat ko'rsatiladi va akkaunt uziladi. Tanlangan akkaunt bilan
// ishlash — X-Kotib-Account sarlavhasi (auth).

const (
	linkTTL      = 30 * time.Minute
	maxLabelLen  = 32
	linkPrefix   = "link_"
	defaultLabel = "Qo'shimcha akkaunt"
)

// maxLinked — billing o'chiq bo'lsa ham shu chegara.
const maxLinked = billing.MaxLinkedAccounts

type accountInfo struct {
	ID     int64               `json:"id"`
	Label  string              `json:"label"`
	Main   bool                `json:"main"`
	Status store.AccountStatus `json:"status"`
}

func (s *Server) canLink(ctx context.Context, main int64, existing int) bool {
	if s.Billing == nil {
		return existing < maxLinked
	}
	return s.Billing.CanLink(ctx, main, existing)
}

func (s *Server) accountList(ctx context.Context, main int64) ([]accountInfo, []store.Link) {
	out := []accountInfo{{ID: main, Label: "Asosiy", Main: true, Status: store.StatusPending}}
	if acc, err := s.Store.GetAccount(ctx, main); err == nil {
		out[0].Status = acc.Status
	}
	links, _ := s.Store.ListLinks(ctx, main)
	for _, l := range links {
		a := accountInfo{ID: l.ChildID, Label: l.Label, Status: store.StatusPending}
		if acc, err := s.Store.GetAccount(ctx, l.ChildID); err == nil {
			a.Status = acc.Status
		}
		out = append(out, a)
	}
	return out, links
}

func (s *Server) handleAccounts(w http.ResponseWriter, r *http.Request, main int64) {
	list, links := s.accountList(r.Context(), main)
	writeJSON(w, 200, map[string]any{
		"accounts": list, "max": maxLinked, "can_add": s.canLink(r.Context(), main, len(links)),
	})
}

// handleAccountLink — qo'shiladigan akkaunt botda rozilik beradigan havola (30 daqiqa).
func (s *Server) handleAccountLink(w http.ResponseWriter, r *http.Request, main int64) {
	var in struct {
		Label string `json:"label"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	label := strings.Join(strings.Fields(in.Label), " ")
	if label == "" {
		label = defaultLabel
	}
	if utf8.RuneCountInString(label) > maxLabelLen {
		httpErr(w, 400, "nom juda uzun")
		return
	}
	if !s.requireConsent(w, r, main) {
		return
	}
	if s.BotUsername == "" {
		httpErr(w, 503, "bot nomi noma'lum, keyinroq urinib ko'ring")
		return
	}
	if links, _ := s.Store.ListLinks(r.Context(), main); !s.canLink(r.Context(), main, len(links)) {
		httpErr(w, http.StatusPaymentRequired, "qo'shimcha akkauntlar Pro tarifda (ko'pi bilan 2 ta)")
		return
	}
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		httpErr(w, 500, "xatolik")
		return
	}
	token := base64.RawURLEncoding.EncodeToString(b) // 22 belgi: /start payload va callback_data'ga sig'adi
	exp := time.Now().Add(linkTTL)
	if err := s.Store.SaveLinkToken(r.Context(), store.TokenHash(token), store.LinkToken{ParentID: main, Label: label, ExpiresAt: exp}); err != nil {
		httpErr(w, 500, "xatolik")
		return
	}
	s.Store.Audit(r.Context(), main, "link_created", label)
	writeJSON(w, 200, map[string]any{"link": "https://t.me/" + s.BotUsername + "?start=" + linkPrefix + token, "expires_at": exp})
}

func (s *Server) handleAccountUnlink(w http.ResponseWriter, r *http.Request, main int64) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpErr(w, 400, "id noto'g'ri")
		return
	}
	if l, err := s.Store.GetLink(r.Context(), id); err != nil || l.ParentID != main {
		httpErr(w, 404, "akkaunt topilmadi")
		return
	}
	if err := s.unlink(r.Context(), main, id); err != nil {
		httpErr(w, 500, "xatolik")
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// unlink qo'shimcha akkauntni uzadi: Telegram sessiyasi tugatiladi, ma'lumotlari o'chiriladi.
func (s *Server) unlink(ctx context.Context, main, child int64) error {
	ctx = context.WithoutCancel(ctx)
	if err := s.Ctl.Revoke(ctx, child); err != nil {
		s.logger().Warn("unlink revoke", "child", child, "err", err)
	}
	if s.Google != nil {
		_ = s.Google.Disconnect(ctx, child)
	}
	if err := s.Store.DeleteAccount(ctx, child); err != nil {
		return err
	}
	if err := s.Store.DeleteLink(ctx, child); err != nil {
		return err
	}
	s.Store.Audit(ctx, main, "account_unlinked", "")
	return nil
}
