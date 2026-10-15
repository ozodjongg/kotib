package miniapp

import (
	"context"
	"net/http"
	"time"

	"github.com/ozodjongg/secretar_ai_agent/internal/store"
)

// handleStats — kengaytirilgan statistika (Pro): ?days=7|30, tanlangan akkaunt bo'yicha.
func (s *Server) handleStats(w http.ResponseWriter, r *http.Request, owner int64) {
	if !s.hasPro(r.Context(), owner) {
		httpErr(w, http.StatusPaymentRequired, errNeedPro)
		return
	}
	days := 7
	switch r.URL.Query().Get("days") {
	case "", "7":
	case "30":
		days = 30
	default:
		httpErr(w, 400, "days: 7 yoki 30")
		return
	}
	loc := time.UTC
	if st, err := s.Store.GetSettings(r.Context(), owner); err == nil {
		loc = st.Location()
	}
	out, err := store.ExtendedStats(r.Context(), s.Store, owner, time.Now(), days, loc)
	if err != nil {
		httpErr(w, 500, "xatolik")
		return
	}
	// Kontakt ismlari — jonli sessiyadan (bo'lmasa ID o'rniga "Kontakt").
	if len(out.TopChats) > 0 {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		names := map[int64]string{}
		if cs, err := s.Ctl.Contacts(ctx, owner); err == nil {
			for _, c := range cs {
				names[c.ID] = c.Name
			}
		}
		cancel()
		for i := range out.TopChats {
			if n := names[out.TopChats[i].ChatID]; n != "" {
				out.TopChats[i].Name = n
			} else {
				out.TopChats[i].Name = "Kontakt"
			}
		}
	}
	writeJSON(w, 200, out)
}
