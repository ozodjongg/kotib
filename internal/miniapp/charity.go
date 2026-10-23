package miniapp

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/ozodjongg/secretar_ai_agent/internal/billing"
	"github.com/ozodjongg/secretar_ai_agent/internal/store"
)

// Hayriya: hisobot hamma uchun ochiq (shaffoflik), xayriya — bir martalik Stars to'lovi;
// o'tkazmani belgilash va qo'shimcha xayriya — faqat admin.

func (s *Server) charityOn() bool { return s.Billing != nil && s.Billing.CharityEnabled() }

func (s *Server) handleCharity(w http.ResponseWriter, r *http.Request, _ int64) {
	if s.Billing == nil {
		writeJSON(w, 200, billing.CharityReport{Payouts: []billing.CharityPayoutView{}})
		return
	}
	rep, err := s.Billing.CharityReport(r.Context())
	if err != nil {
		s.logger().Warn("charity report", "err", err)
		httpErr(w, 500, "xatolik")
		return
	}
	writeJSON(w, 200, rep)
}

func (s *Server) handleCharityDonate(w http.ResponseWriter, r *http.Request, owner int64) {
	var in struct {
		Stars int `json:"stars"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if !s.charityOn() {
		httpErr(w, 404, "hayriya yoqilmagan")
		return
	}
	link, err := s.Billing.DonationLink(r.Context(), owner, in.Stars)
	switch {
	case errors.Is(err, billing.ErrDonationAmount):
		httpErr(w, 400, err.Error())
		return
	case errors.Is(err, billing.ErrCharityDisabled):
		httpErr(w, 404, "hayriya yoqilmagan")
		return
	case err != nil:
		s.logger().Warn("donation link", "owner", owner, "err", err)
		httpErr(w, 502, "to'lov havolasini yaratib bo'lmadi")
		return
	}
	writeJSON(w, 200, map[string]string{"link": link})
}

func (s *Server) handleAdminCharityPaid(w http.ResponseWriter, r *http.Request, admin int64) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		httpErr(w, 400, "ID noto'g'ri")
		return
	}
	var in struct {
		Proof string `json:"proof"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if !s.charityOn() {
		httpErr(w, 409, "hayriya yoqilmagan")
		return
	}
	if err := s.Billing.MarkCharityPaid(r.Context(), id, in.Proof, admin); err != nil {
		httpErr(w, 400, err.Error())
		return
	}
	s.Store.Audit(r.Context(), admin, "charity_paid", "#"+strconv.FormatInt(id, 10))
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) handleAdminCharityManual(w http.ResponseWriter, r *http.Request, admin int64) {
	var in struct {
		Stars int    `json:"stars"`
		Note  string `json:"note"`
		Proof string `json:"proof"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if !s.charityOn() {
		httpErr(w, 409, "hayriya yoqilmagan")
		return
	}
	id, err := s.Billing.AddManualCharity(r.Context(), admin, in.Stars, in.Note, in.Proof)
	if err != nil {
		httpErr(w, 400, err.Error())
		return
	}
	s.Store.Audit(r.Context(), admin, "charity_manual", strconv.Itoa(in.Stars))
	writeJSON(w, 200, map[string]any{"ok": true, "id": id})
}

// refundText — to'lov qaytarilgani haqida mijozga xabar (botcmd.RefundText bilan bir xil).
func refundText(p store.Payment) string {
	if p.IsDonation() {
		return fmt.Sprintf("%d ⭐ hisobingizga qaytarildi (hayriya xayriyasi).", p.Amount)
	}
	return fmt.Sprintf("%d ⭐ hisobingizga qaytarildi. Obuna to'xtatildi.", p.Amount)
}
