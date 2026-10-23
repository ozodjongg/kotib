package store

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"strings"
	"time"
)

// DonationPrefix — hayriya xayriyasi to'lovining payload boshi ("don:<owner>:<nonce>").
// Bunday to'lov obuna tushumiga qo'shilmaydi va to'liq hayriyaga ketadi.
const DonationPrefix = "don:"

// IsDonation — to'lov hayriya xayriyasimi (obuna emas).
func (p Payment) IsDonation() bool { return strings.HasPrefix(p.Payload, DonationPrefix) }

// Hayriya o'tkazmasi turlari.
const (
	CharityMonthly = "monthly" // oylik hisob: obunalardan ulush + Stars xayriyalari
	CharityManual  = "manual"  // admin to'g'ridan-to'g'ri (pul/TON) qilgan qo'shimcha xayriya
)

// CharityPayout — hayriyaga o'tkazma (yoki o'tkazilishi kerak bo'lgan oylik summa).
type CharityPayout struct {
	ID      int64
	Kind    string
	Period  string // "2026-10" (monthly); manual — bo'sh
	Share   int    // shu oy obunalaridan ulush
	Donated int    // shu oy Stars xayriyalari
	Adjust  int    // oldingi oylar tuzatishi (refund, yaxlitlash)
	Stars   int    // o'tkaziladigan jami
	Note    string
	Proof   string // TON tx hash / kvitansiya havolasi
	Paid    bool
	ByAdmin int64
	// CreatedAt — yozuv vaqti; PaidAt — o'tkazilgan vaqt (to'lanmagan bo'lsa nil).
	CreatedAt time.Time
	PaidAt    *time.Time
}

// ---- Memory ----

func (m *Memory) CharityStart(_ context.Context, candidate time.Time) (time.Time, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.charityStart.IsZero() {
		m.charityStart = usec(candidate)
	}
	return m.charityStart, nil
}

func (m *Memory) PaymentSums(_ context.Context, from, to time.Time) (int64, int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var subs, dons int64
	for _, p := range m.payments {
		if p.Refunded || p.CreatedAt.Before(from) || !p.CreatedAt.Before(to) {
			continue
		}
		if p.IsDonation() {
			dons += int64(p.Amount)
		} else {
			subs += int64(p.Amount)
		}
	}
	return subs, dons, nil
}

func (m *Memory) SubscriberSums(_ context.Context, from, to time.Time) (map[int64]int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[int64]int64{}
	for _, p := range m.payments {
		if !p.Refunded && !p.IsDonation() && !p.CreatedAt.Before(from) && p.CreatedAt.Before(to) {
			out[p.OwnerID] += int64(p.Amount)
		}
	}
	return out, nil
}

func (m *Memory) OwnerPaymentSums(_ context.Context, owner int64, from, to time.Time) (int64, int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var subs, dons int64
	for _, p := range m.payments {
		if p.OwnerID != owner || p.Refunded || p.CreatedAt.Before(from) || !p.CreatedAt.Before(to) {
			continue
		}
		if p.IsDonation() {
			dons += int64(p.Amount)
		} else {
			subs += int64(p.Amount)
		}
	}
	return subs, dons, nil
}

func (m *Memory) AddCharityPayout(_ context.Context, p CharityPayout) (int64, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if p.Kind == CharityMonthly {
		for _, x := range m.charity {
			if x.Kind == CharityMonthly && x.Period == p.Period {
				return 0, false, nil
			}
		}
	}
	m.nextID++
	p.ID, p.CreatedAt, p.PaidAt = m.nextID, usec(time.Now()), usecPtr(p.PaidAt)
	m.charity = append(m.charity, p)
	return p.ID, true, nil
}

func (m *Memory) ListCharityPayouts(_ context.Context, limit int) ([]CharityPayout, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]CharityPayout, len(m.charity))
	copy(out, m.charity)
	sort.SliceStable(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *Memory) MarkCharityPaid(_ context.Context, id int64, proof string, admin int64, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, x := range m.charity {
		if x.ID == id && !x.Paid {
			at := usec(at)
			m.charity[i].Paid, m.charity[i].Proof, m.charity[i].ByAdmin, m.charity[i].PaidAt = true, proof, admin, &at
			return nil
		}
	}
	return ErrNotFound
}

// ---- Postgres ----

func (p *Postgres) CharityStart(ctx context.Context, candidate time.Time) (time.Time, error) {
	if _, err := p.DB.ExecContext(ctx,
		`INSERT INTO charity_settings (id, start_at) VALUES (1, $1) ON CONFLICT (id) DO NOTHING`, candidate); err != nil {
		return time.Time{}, err
	}
	var t time.Time
	err := p.DB.QueryRowContext(ctx, `SELECT start_at FROM charity_settings WHERE id = 1`).Scan(&t)
	return t, err
}

func (p *Postgres) PaymentSums(ctx context.Context, from, to time.Time) (int64, int64, error) {
	var subs, dons int64
	err := p.DB.QueryRowContext(ctx, `
		SELECT COALESCE(sum(amount) FILTER (WHERE payload NOT LIKE 'don:%'), 0),
		       COALESCE(sum(amount) FILTER (WHERE payload LIKE 'don:%'), 0)
		FROM payments WHERE NOT refunded AND created_at >= $1 AND created_at < $2`, from, to).Scan(&subs, &dons)
	return subs, dons, err
}

func (p *Postgres) SubscriberSums(ctx context.Context, from, to time.Time) (map[int64]int64, error) {
	rows, err := p.DB.QueryContext(ctx, `
		SELECT owner_id, sum(amount) FROM payments
		WHERE NOT refunded AND payload NOT LIKE 'don:%' AND created_at >= $1 AND created_at < $2
		GROUP BY owner_id`, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]int64{}
	for rows.Next() {
		var owner, sum int64
		if err := rows.Scan(&owner, &sum); err != nil {
			return nil, err
		}
		out[owner] = sum
	}
	return out, rows.Err()
}

func (p *Postgres) OwnerPaymentSums(ctx context.Context, owner int64, from, to time.Time) (int64, int64, error) {
	var subs, dons int64
	err := p.DB.QueryRowContext(ctx, `
		SELECT COALESCE(sum(amount) FILTER (WHERE payload NOT LIKE 'don:%'), 0),
		       COALESCE(sum(amount) FILTER (WHERE payload LIKE 'don:%'), 0)
		FROM payments WHERE owner_id = $1 AND NOT refunded AND created_at >= $2 AND created_at < $3`, owner, from, to).Scan(&subs, &dons)
	return subs, dons, err
}

func (p *Postgres) AddCharityPayout(ctx context.Context, c CharityPayout) (int64, bool, error) {
	var id int64
	err := p.DB.QueryRowContext(ctx, `
		INSERT INTO charity_payouts (kind, period, share, donated, adjust, stars, note, proof, paid, by_admin, paid_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11) ON CONFLICT DO NOTHING RETURNING id`,
		c.Kind, c.Period, c.Share, c.Donated, c.Adjust, c.Stars, c.Note, c.Proof, c.Paid, c.ByAdmin, c.PaidAt).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil // shu oy allaqachon hisoblangan
	}
	return id, err == nil, err
}

func (p *Postgres) ListCharityPayouts(ctx context.Context, limit int) ([]CharityPayout, error) {
	q := `SELECT id, kind, period, share, donated, adjust, stars, note, proof, paid, by_admin, created_at, paid_at
		FROM charity_payouts ORDER BY id DESC`
	args := []any{}
	if limit > 0 {
		q += ` LIMIT $1`
		args = append(args, limit)
	}
	rows, err := p.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CharityPayout
	for rows.Next() {
		var c CharityPayout
		if err := rows.Scan(&c.ID, &c.Kind, &c.Period, &c.Share, &c.Donated, &c.Adjust, &c.Stars, &c.Note, &c.Proof,
			&c.Paid, &c.ByAdmin, &c.CreatedAt, &c.PaidAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (p *Postgres) MarkCharityPaid(ctx context.Context, id int64, proof string, admin int64, at time.Time) error {
	res, err := p.DB.ExecContext(ctx,
		`UPDATE charity_payouts SET paid = true, proof = $2, by_admin = $3, paid_at = $4 WHERE id = $1 AND NOT paid`,
		id, proof, admin, at)
	return affected(res, err)
}
