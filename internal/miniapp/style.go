package miniapp

import (
	"net/http"
	"time"

	"github.com/ozodjongg/secretar_ai_agent/internal/config"
)

// handleStyleLearn — "Uslubimni o'rgan": oxirgi yozishmalardan profil fonda yaratiladi (Pro),
// tayyor bo'lganda bot xabar beradi.
func (s *Server) handleStyleLearn(w http.ResponseWriter, r *http.Request, owner int64) {
	if !s.requireConsent(w, r, owner) {
		return
	}
	if !s.hasPro(r.Context(), owner) {
		httpErr(w, http.StatusPaymentRequired, errNeedPro)
		return
	}
	if err := s.Ctl.StartLearnStyle(r.Context(), owner); err != nil {
		httpErr(w, http.StatusConflict, err.Error())
		return
	}
	s.Store.Audit(r.Context(), owner, "style_learn_started", "")
	writeJSON(w, http.StatusAccepted, map[string]bool{"ok": true})
}

// handleStyleShadow — soya rejimini days kunga yoqadi (0 — o'chiradi). Yoqilganda o'qitish
// rejimi ham yoqiladi: soya rejimining maqsadi — o'rganish (Pro).
func (s *Server) handleStyleShadow(w http.ResponseWriter, r *http.Request, owner int64) {
	var in struct {
		Days int `json:"days"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if in.Days < 0 || in.Days > config.MaxShadowDays {
		httpErr(w, 400, "kunlar 0–14")
		return
	}
	if !s.requireConsent(w, r, owner) {
		return
	}
	if in.Days > 0 && !s.hasPro(r.Context(), owner) {
		httpErr(w, http.StatusPaymentRequired, errNeedPro)
		return
	}
	st, err := s.Store.GetSettings(r.Context(), owner)
	if err != nil {
		httpErr(w, 500, "xatolik")
		return
	}
	st.ShadowUntil = nil
	if in.Days > 0 {
		until := time.Now().AddDate(0, 0, in.Days).Truncate(time.Second)
		st.ShadowUntil, st.Learn = &until, true
	}
	if err := st.Validate(); err != nil {
		httpErr(w, 400, err.Error())
		return
	}
	if err := s.Store.SaveSettings(r.Context(), owner, st); err != nil {
		httpErr(w, 500, "xatolik")
		return
	}
	s.Store.Audit(r.Context(), owner, "shadow_set", "")
	writeJSON(w, 200, map[string]any{"ok": true, "shadow_until": st.ShadowUntil, "learn": st.Learn})
}
