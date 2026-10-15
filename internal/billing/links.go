package billing

import "context"

// MaxLinkedAccounts — Pro tarifda asosiy akkauntga qo'shimcha ulanadigan Telegram akkauntlar soni.
const MaxLinkedAccounts = 2

// payer — qo'shimcha (bog'langan) akkaunt uchun asosiy, to'lovchi akkaunt. Obuna, sinov limiti,
// tarif va referal — hammasi asosiy akkaunt bo'yicha; qo'shimcha akkaunt faqat asosiysi Pro
// funksiyalariga ega bo'lsa ishlaydi.
func (s *Service) payer(ctx context.Context, owner int64) (int64, bool) {
	if s == nil || s.Store == nil {
		return owner, false
	}
	if l, err := s.Store.GetLink(ctx, owner); err == nil {
		return l.ParentID, true
	}
	return owner, false
}

// CanLink — asosiy akkaunt yana qo'shimcha akkaunt ulay oladimi (Pro va chegara).
func (s *Service) CanLink(ctx context.Context, parent int64, existing int) bool {
	if _, linked := s.payer(ctx, parent); linked {
		return false // qo'shimcha akkaunt o'zi boshqa akkaunt ulay olmaydi
	}
	return s.HasPro(ctx, parent) && existing < MaxLinkedAccounts
}
