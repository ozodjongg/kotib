package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/ozodjongg/secretar_ai_agent/internal/config"
)

// Postgres — database/sql asosida (driver tashqarida ro'yxatdan o'tadi: pgx stdlib).
// Sxema: migrations/001_init.sql.
type Postgres struct{ DB *sql.DB }

func NewPostgres(db *sql.DB) *Postgres { return &Postgres{DB: db} }

func (p *Postgres) UpsertAccount(ctx context.Context, a Account) error {
	_, err := p.DB.ExecContext(ctx, `
		INSERT INTO accounts (owner_id, status, status_note, consent_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (owner_id) DO UPDATE SET
			status = EXCLUDED.status,
			status_note = EXCLUDED.status_note,
			consent_at = COALESCE(EXCLUDED.consent_at, accounts.consent_at)`,
		a.OwnerID, string(a.Status), a.StatusNote, a.ConsentAt)
	return err
}

func (p *Postgres) GetAccount(ctx context.Context, owner int64) (Account, error) {
	var a Account
	var st string
	var consent sql.NullTime
	err := p.DB.QueryRowContext(ctx,
		`SELECT owner_id, status, status_note, consent_at, created_at FROM accounts WHERE owner_id = $1`, owner).
		Scan(&a.OwnerID, &st, &a.StatusNote, &consent, &a.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Account{}, ErrNotFound
	}
	if err != nil {
		return Account{}, err
	}
	a.Status = AccountStatus(st)
	if consent.Valid {
		t := consent.Time
		a.ConsentAt = &t
	}
	return a, nil
}

func (p *Postgres) SetStatus(ctx context.Context, owner int64, st AccountStatus, note string) error {
	res, err := p.DB.ExecContext(ctx, `UPDATE accounts SET status = $2, status_note = $3 WHERE owner_id = $1`,
		owner, string(st), note)
	return affected(res, err)
}

func (p *Postgres) ListActiveOwners(ctx context.Context) ([]int64, error) {
	rows, err := p.DB.QueryContext(ctx,
		`SELECT owner_id FROM accounts WHERE status = 'active' AND consent_at IS NOT NULL ORDER BY owner_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func (p *Postgres) DeleteAccount(ctx context.Context, owner int64) error {
	// sessions/settings/drafts/audit — ON DELETE CASCADE
	_, err := p.DB.ExecContext(ctx, `DELETE FROM accounts WHERE owner_id = $1`, owner)
	return err
}

func (p *Postgres) SaveSession(ctx context.Context, owner int64, blob []byte) error {
	_, err := p.DB.ExecContext(ctx, `
		INSERT INTO sessions (owner_id, blob, updated_at) VALUES ($1, $2, now())
		ON CONFLICT (owner_id) DO UPDATE SET blob = EXCLUDED.blob, updated_at = now()`, owner, blob)
	return err
}

func (p *Postgres) LoadSession(ctx context.Context, owner int64) ([]byte, error) {
	var b []byte
	err := p.DB.QueryRowContext(ctx, `SELECT blob FROM sessions WHERE owner_id = $1`, owner).Scan(&b)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return b, err
}

func (p *Postgres) GetSettings(ctx context.Context, owner int64) (config.Settings, error) {
	var raw []byte
	err := p.DB.QueryRowContext(ctx, `SELECT data FROM settings WHERE owner_id = $1`, owner).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return config.Default(), nil
	}
	if err != nil {
		return config.Settings{}, err
	}
	var s config.Settings
	if err := json.Unmarshal(raw, &s); err != nil {
		return config.Settings{}, err
	}
	return s, nil
}

func (p *Postgres) SaveSettings(ctx context.Context, owner int64, s config.Settings) error {
	raw, err := json.Marshal(s)
	if err != nil {
		return err
	}
	_, err = p.DB.ExecContext(ctx, `
		INSERT INTO settings (owner_id, data, updated_at) VALUES ($1, $2, now())
		ON CONFLICT (owner_id) DO UPDATE SET data = EXCLUDED.data, updated_at = now()`, owner, raw)
	return err
}

func (p *Postgres) CreateDraft(ctx context.Context, d Draft) (int64, error) {
	var id int64
	err := p.DB.QueryRowContext(ctx, `
		INSERT INTO drafts (owner_id, chat_id, reply_to, incoming, reply, reason)
		VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`,
		d.OwnerID, d.ChatID, d.ReplyToMsg, d.Incoming, d.Reply, d.Reason).Scan(&id)
	return id, err
}

const draftCols = `id, owner_id, chat_id, reply_to, incoming, reply, reason, status, created_at`

func scanDraft(sc interface{ Scan(...any) error }) (Draft, error) {
	var d Draft
	var st string
	err := sc.Scan(&d.ID, &d.OwnerID, &d.ChatID, &d.ReplyToMsg, &d.Incoming, &d.Reply, &d.Reason, &st, &d.CreatedAt)
	d.Status = DraftStatus(st)
	return d, err
}

func (p *Postgres) GetDraft(ctx context.Context, owner, id int64) (Draft, error) {
	d, err := scanDraft(p.DB.QueryRowContext(ctx,
		`SELECT `+draftCols+` FROM drafts WHERE id = $1 AND owner_id = $2`, id, owner))
	if errors.Is(err, sql.ErrNoRows) {
		return Draft{}, ErrNotFound
	}
	return d, err
}

func (p *Postgres) ListDrafts(ctx context.Context, owner int64, st DraftStatus, limit int) ([]Draft, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := p.DB.QueryContext(ctx,
		`SELECT `+draftCols+` FROM drafts WHERE owner_id = $1 AND status = $2 ORDER BY id DESC LIMIT $3`,
		owner, string(st), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Draft
	for rows.Next() {
		d, err := scanDraft(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// ResolveDraft faqat 'pending' holatdagi draftni o'zgartiradi (ikki marta yuborishdan himoya).
func (p *Postgres) ResolveDraft(ctx context.Context, owner, id int64, st DraftStatus) error {
	res, err := p.DB.ExecContext(ctx,
		`UPDATE drafts SET status = $3 WHERE id = $1 AND owner_id = $2 AND status = 'pending'`, id, owner, string(st))
	return affected(res, err)
}

func (p *Postgres) ReopenDraft(ctx context.Context, owner, id int64) error {
	res, err := p.DB.ExecContext(ctx,
		`UPDATE drafts SET status = 'pending' WHERE id = $1 AND owner_id = $2 AND status = 'sent'`, id, owner)
	return affected(res, err)
}

// ExpireDrafts: eski pending draftlar "expired" bo'ladi va matni tozalanadi (maxfiylik);
// hal qilingan (sent/rejected/expired) eski draftlar butunlay o'chiriladi.
func (p *Postgres) ExpireDrafts(ctx context.Context, before time.Time) (int64, error) {
	tx, err := p.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	r1, err := tx.ExecContext(ctx,
		`DELETE FROM drafts WHERE created_at < $1 AND status <> 'pending'`, before)
	if err != nil {
		return 0, err
	}
	r2, err := tx.ExecContext(ctx,
		`UPDATE drafts SET status = 'expired', incoming = '', reply = '' WHERE created_at < $1 AND status = 'pending'`, before)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	n1, _ := r1.RowsAffected()
	n2, _ := r2.RowsAffected()
	return n1 + n2, nil
}

func (p *Postgres) PruneAudit(ctx context.Context, before time.Time) (int64, error) {
	res, err := p.DB.ExecContext(ctx, `DELETE FROM audit WHERE at < $1`, before)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (p *Postgres) Audit(ctx context.Context, owner int64, event, detail string) {
	c, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	_, _ = p.DB.ExecContext(c, `INSERT INTO audit (owner_id, event, detail) VALUES ($1, $2, $3)`, owner, event, detail)
}

func (p *Postgres) ListAudit(ctx context.Context, owner int64, limit int) ([]AuditEntry, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := p.DB.QueryContext(ctx,
		`SELECT at, event, detail FROM audit WHERE owner_id = $1 ORDER BY at DESC, id DESC LIMIT $2`, owner, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AuditEntry
	for rows.Next() {
		var e AuditEntry
		if err := rows.Scan(&e.At, &e.Event, &e.Detail); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ---- Billing ----

func nullTime(t time.Time) sql.NullTime { return sql.NullTime{Time: t, Valid: !t.IsZero()} }

const subCols = `owner_id, trial_until, paid_until, charge_id, canceled, reminded_for, bonus_until, tier`

func scanSub(sc interface{ Scan(...any) error }) (Subscription, error) {
	var s Subscription
	var trial, paid, reminded, bonus sql.NullTime
	err := sc.Scan(&s.OwnerID, &trial, &paid, &s.ChargeID, &s.Canceled, &reminded, &bonus, &s.Tier)
	s.TrialUntil, s.PaidUntil, s.RemindedFor, s.BonusUntil = trial.Time, paid.Time, reminded.Time, bonus.Time
	return s, err
}

func (p *Postgres) GetSubscription(ctx context.Context, owner int64) (Subscription, error) {
	s, err := scanSub(p.DB.QueryRowContext(ctx, `SELECT `+subCols+` FROM subscriptions WHERE owner_id = $1`, owner))
	if errors.Is(err, sql.ErrNoRows) {
		return Subscription{}, ErrNotFound
	}
	return s, err
}

func (p *Postgres) SaveSubscription(ctx context.Context, s Subscription) error {
	_, err := p.DB.ExecContext(ctx, `
		INSERT INTO subscriptions (`+subCols+`, updated_at) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, now())
		ON CONFLICT (owner_id) DO UPDATE SET
			trial_until = EXCLUDED.trial_until, paid_until = EXCLUDED.paid_until,
			charge_id = EXCLUDED.charge_id, canceled = EXCLUDED.canceled,
			reminded_for = EXCLUDED.reminded_for, bonus_until = EXCLUDED.bonus_until, tier = EXCLUDED.tier,
			updated_at = now()`,
		s.OwnerID, nullTime(s.TrialUntil), nullTime(s.PaidUntil), s.ChargeID, s.Canceled, nullTime(s.RemindedFor),
		nullTime(s.BonusUntil), s.Tier)
	return err
}

func (p *Postgres) AddPayment(ctx context.Context, pm Payment) (bool, error) {
	res, err := p.DB.ExecContext(ctx, `
		INSERT INTO payments (charge_id, owner_id, amount, currency, payload, recurring)
		VALUES ($1, $2, $3, $4, $5, $6) ON CONFLICT (charge_id) DO NOTHING`,
		pm.ChargeID, pm.OwnerID, pm.Amount, pm.Currency, pm.Payload, pm.Recurring)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

func (p *Postgres) GetPayment(ctx context.Context, chargeID string) (Payment, error) {
	var pm Payment
	err := p.DB.QueryRowContext(ctx, `
		SELECT charge_id, owner_id, amount, currency, payload, recurring, refunded, created_at
		FROM payments WHERE charge_id = $1`, chargeID).
		Scan(&pm.ChargeID, &pm.OwnerID, &pm.Amount, &pm.Currency, &pm.Payload, &pm.Recurring, &pm.Refunded, &pm.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Payment{}, ErrNotFound
	}
	return pm, err
}

func (p *Postgres) MarkRefunded(ctx context.Context, chargeID string) error {
	res, err := p.DB.ExecContext(ctx,
		`UPDATE payments SET refunded = true WHERE charge_id = $1 AND NOT refunded`, chargeID)
	return affected(res, err)
}

func (p *Postgres) ListSubscriptionsEnding(ctx context.Context, from, to time.Time) ([]Subscription, error) {
	rows, err := p.DB.QueryContext(ctx, `
		SELECT `+subCols+` FROM subscriptions
		WHERE GREATEST(COALESCE(trial_until, '-infinity'), COALESCE(paid_until, '-infinity'), COALESCE(bonus_until, '-infinity')) >= $1
		  AND GREATEST(COALESCE(trial_until, '-infinity'), COALESCE(paid_until, '-infinity'), COALESCE(bonus_until, '-infinity')) < $2
		ORDER BY owner_id`, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Subscription
	for rows.Next() {
		s, err := scanSub(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (p *Postgres) Stats(ctx context.Context, now time.Time) (Stats, error) {
	var st Stats
	err := p.DB.QueryRowContext(ctx, `
		SELECT
			(SELECT count(*) FROM accounts),
			(SELECT count(*) FROM accounts WHERE status = 'active'),
			(SELECT count(*) FROM subscriptions WHERE paid_until > $1),
			(SELECT COALESCE(sum(amount), 0) FROM payments WHERE NOT refunded),
			(SELECT count(*) FROM referrals),
			(SELECT count(*) FROM referrals WHERE paid_at IS NOT NULL)`, now).
		Scan(&st.Accounts, &st.Active, &st.PaidActive, &st.StarsTotal, &st.Referrals, &st.RefPaid)
	return st, err
}

// ---- Referal ----

func (p *Postgres) EnsureReferralCode(ctx context.Context, owner int64, candidate string) (string, error) {
	// ON CONFLICT maqsadsiz: owner_id ham, code ham band bo'lsa — hech narsa qilinmaydi.
	if _, err := p.DB.ExecContext(ctx,
		`INSERT INTO referral_codes (owner_id, code) VALUES ($1, $2) ON CONFLICT DO NOTHING`, owner, candidate); err != nil {
		return "", err
	}
	var code string
	err := p.DB.QueryRowContext(ctx, `SELECT code FROM referral_codes WHERE owner_id = $1`, owner).Scan(&code)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound // kod boshqa egada band edi
	}
	return code, err
}

func (p *Postgres) ReferrerByCode(ctx context.Context, code string) (int64, error) {
	var owner int64
	err := p.DB.QueryRowContext(ctx, `SELECT owner_id FROM referral_codes WHERE code = $1`, code).Scan(&owner)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	return owner, err
}

func (p *Postgres) AddReferral(ctx context.Context, invitee, referrer int64) (bool, error) {
	if invitee == referrer {
		return false, nil
	}
	res, err := p.DB.ExecContext(ctx,
		`INSERT INTO referrals (invitee_id, referrer_id) VALUES ($1, $2) ON CONFLICT (invitee_id) DO NOTHING`,
		invitee, referrer)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

func (p *Postgres) GetReferral(ctx context.Context, invitee int64) (Referral, error) {
	var r Referral
	var act, paid sql.NullTime
	err := p.DB.QueryRowContext(ctx, `
		SELECT invitee_id, referrer_id, created_at, activated_at, paid_at FROM referrals WHERE invitee_id = $1`, invitee).
		Scan(&r.InviteeID, &r.ReferrerID, &r.CreatedAt, &act, &paid)
	if errors.Is(err, sql.ErrNoRows) {
		return Referral{}, ErrNotFound
	}
	r.ActivatedAt, r.PaidAt = act.Time, paid.Time
	return r, err
}

func (p *Postgres) MarkReferral(ctx context.Context, invitee int64, stage ReferralStage, at time.Time) (bool, error) {
	var q string
	switch stage {
	case ReferralActivated:
		q = `UPDATE referrals SET activated_at = $2 WHERE invitee_id = $1 AND activated_at IS NULL`
	case ReferralPaid:
		q = `UPDATE referrals SET paid_at = $2 WHERE invitee_id = $1 AND paid_at IS NULL`
	default:
		return false, nil
	}
	res, err := p.DB.ExecContext(ctx, q, invitee, at)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

func (p *Postgres) ReferralStats(ctx context.Context, referrer int64) (ReferralStats, error) {
	var st ReferralStats
	err := p.DB.QueryRowContext(ctx, `
		SELECT count(*), count(activated_at), count(paid_at) FROM referrals WHERE referrer_id = $1`, referrer).
		Scan(&st.Invited, &st.Activated, &st.Paid)
	return st, err
}

func affected(res sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// ---- Bir nechta akkaunt ----

func (p *Postgres) SaveLinkToken(ctx context.Context, hash []byte, t LinkToken) error {
	_, err := p.DB.ExecContext(ctx, `
		INSERT INTO link_tokens (token_hash, parent_id, label, expires_at) VALUES ($1, $2, $3, $4)
		ON CONFLICT (token_hash) DO UPDATE SET parent_id = EXCLUDED.parent_id, label = EXCLUDED.label,
			expires_at = EXCLUDED.expires_at`, hash, t.ParentID, t.Label, t.ExpiresAt)
	return err
}

func (p *Postgres) GetLinkToken(ctx context.Context, hash []byte) (LinkToken, error) {
	var t LinkToken
	err := p.DB.QueryRowContext(ctx, `SELECT parent_id, label, expires_at FROM link_tokens WHERE token_hash = $1`, hash).
		Scan(&t.ParentID, &t.Label, &t.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return LinkToken{}, ErrNotFound
	}
	return t, err
}

func (p *Postgres) DeleteLinkToken(ctx context.Context, hash []byte) error {
	// Eskirgan tokenlar ham shu yerda tozalanadi (jadval o'smasin).
	_, err := p.DB.ExecContext(ctx, `DELETE FROM link_tokens WHERE token_hash = $1 OR expires_at < now() - interval '1 day'`, hash)
	return err
}

func (p *Postgres) AddLink(ctx context.Context, l Link) (bool, error) {
	if l.ChildID == l.ParentID {
		return false, nil
	}
	res, err := p.DB.ExecContext(ctx, `
		INSERT INTO linked_accounts (child_id, parent_id, label) VALUES ($1, $2, $3)
		ON CONFLICT (child_id) DO NOTHING`, l.ChildID, l.ParentID, l.Label)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

func (p *Postgres) GetLink(ctx context.Context, child int64) (Link, error) {
	var l Link
	err := p.DB.QueryRowContext(ctx, `SELECT child_id, parent_id, label, created_at FROM linked_accounts WHERE child_id = $1`, child).
		Scan(&l.ChildID, &l.ParentID, &l.Label, &l.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Link{}, ErrNotFound
	}
	return l, err
}

func (p *Postgres) ListLinks(ctx context.Context, parent int64) ([]Link, error) {
	rows, err := p.DB.QueryContext(ctx, `
		SELECT child_id, parent_id, label, created_at FROM linked_accounts WHERE parent_id = $1
		ORDER BY created_at, child_id`, parent)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Link
	for rows.Next() {
		var l Link
		if err := rows.Scan(&l.ChildID, &l.ParentID, &l.Label, &l.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func (p *Postgres) DeleteLink(ctx context.Context, child int64) error {
	_, err := p.DB.ExecContext(ctx, `DELETE FROM linked_accounts WHERE child_id = $1`, child)
	return err
}

func (p *Postgres) ListAuditSince(ctx context.Context, owner int64, events []string, since time.Time, limit int) ([]AuditEntry, error) {
	if limit <= 0 {
		limit = 50000
	}
	rows, err := p.DB.QueryContext(ctx, `
		SELECT at, event, detail FROM audit
		WHERE owner_id = $1 AND event = ANY($2) AND at >= $3
		ORDER BY at, id LIMIT $4`, owner, events, since, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AuditEntry
	for rows.Next() {
		var e AuditEntry
		if err := rows.Scan(&e.At, &e.Event, &e.Detail); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

var _ Store = (*Postgres)(nil)
var _ Store = (*Memory)(nil)

func (p *Postgres) UseDaily(ctx context.Context, owner int64, day string, limit int) (bool, error) {
	if limit <= 0 {
		return false, nil
	}
	var used int
	err := p.DB.QueryRowContext(ctx, `
		INSERT INTO usage_daily (owner_id, day, used) VALUES ($1, $2::date, 1)
		ON CONFLICT (owner_id, day) DO UPDATE SET used = usage_daily.used + 1
		WHERE usage_daily.used < $3
		RETURNING used`, owner, day, limit).Scan(&used)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil // limitga yetgan: UPDATE sharti bajarilmadi
	}
	return err == nil, err
}

func (p *Postgres) SaveGoogleLink(ctx context.Context, l GoogleLink) error {
	_, err := p.DB.ExecContext(ctx, `
		INSERT INTO google_links (owner_id, email, token, created_at) VALUES ($1, $2, $3, now())
		ON CONFLICT (owner_id) DO UPDATE SET email = EXCLUDED.email, token = EXCLUDED.token, created_at = now()`,
		l.OwnerID, l.Email, l.Token)
	return err
}

func (p *Postgres) GetGoogleLink(ctx context.Context, owner int64) (GoogleLink, error) {
	l := GoogleLink{OwnerID: owner}
	err := p.DB.QueryRowContext(ctx,
		`SELECT email, token, created_at FROM google_links WHERE owner_id = $1`, owner).
		Scan(&l.Email, &l.Token, &l.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return GoogleLink{}, ErrNotFound
	}
	return l, err
}

func (p *Postgres) DeleteGoogleLink(ctx context.Context, owner int64) error {
	_, err := p.DB.ExecContext(ctx, `DELETE FROM google_links WHERE owner_id = $1`, owner)
	return err
}

func (p *Postgres) CreateAction(ctx context.Context, a Action) (int64, error) {
	var id int64
	err := p.DB.QueryRowContext(ctx,
		`INSERT INTO actions (owner_id, kind, payload) VALUES ($1, $2, $3) RETURNING id`,
		a.OwnerID, string(a.Kind), a.Payload).Scan(&id)
	return id, err
}

func (p *Postgres) GetAction(ctx context.Context, owner, id int64) (Action, error) {
	a := Action{ID: id, OwnerID: owner}
	var kind, st string
	err := p.DB.QueryRowContext(ctx,
		`SELECT kind, payload, status, created_at FROM actions WHERE id = $1 AND owner_id = $2`, id, owner).
		Scan(&kind, &a.Payload, &st, &a.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Action{}, ErrNotFound
	}
	a.Kind, a.Status = ActionKind(kind), ActionStatus(st)
	return a, err
}

func (p *Postgres) SetActionStatus(ctx context.Context, owner, id int64, from, to ActionStatus) error {
	res, err := p.DB.ExecContext(ctx,
		`UPDATE actions SET status = $4 WHERE id = $1 AND owner_id = $2 AND status = $3`,
		id, owner, string(from), string(to))
	return affected(res, err)
}

func (p *Postgres) PruneActions(ctx context.Context, before time.Time) (int64, error) {
	res, err := p.DB.ExecContext(ctx, `DELETE FROM actions WHERE created_at < $1`, before)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (p *Postgres) SetHook(ctx context.Context, owner int64, tokenHash []byte) error {
	_, err := p.DB.ExecContext(ctx, `
		INSERT INTO hooks (owner_id, token_hash) VALUES ($1, $2)
		ON CONFLICT (owner_id) DO UPDATE SET token_hash = EXCLUDED.token_hash, created_at = now()`, owner, tokenHash)
	return err
}

func (p *Postgres) DeleteHook(ctx context.Context, owner int64) error {
	_, err := p.DB.ExecContext(ctx, `DELETE FROM hooks WHERE owner_id = $1`, owner)
	return err
}

func (p *Postgres) HasHook(ctx context.Context, owner int64) (bool, error) {
	var ok bool
	err := p.DB.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM hooks WHERE owner_id = $1)`, owner).Scan(&ok)
	return ok, err
}

func (p *Postgres) OwnerByHook(ctx context.Context, tokenHash []byte) (int64, error) {
	var owner int64
	err := p.DB.QueryRowContext(ctx, `SELECT owner_id FROM hooks WHERE token_hash = $1`, tokenHash).Scan(&owner)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	return owner, err
}

func (p *Postgres) CountAudit(ctx context.Context, owner int64, events []string, since time.Time) (int, error) {
	var n int
	err := p.DB.QueryRowContext(ctx,
		`SELECT count(*) FROM audit WHERE owner_id = $1 AND event = ANY($2) AND at >= $3`,
		owner, events, since).Scan(&n)
	return n, err
}

func (p *Postgres) SaveFollowup(ctx context.Context, f Followup) error {
	_, err := p.DB.ExecContext(ctx, `
		INSERT INTO followups (owner_id, chat_id, hash, due_at, status, created_at) VALUES ($1, $2, $3, $4, 'pending', now())
		ON CONFLICT (owner_id, chat_id) DO UPDATE SET hash = EXCLUDED.hash, due_at = EXCLUDED.due_at,
			status = 'pending', created_at = now()`,
		f.OwnerID, f.ChatID, f.Hash, f.DueAt)
	return err
}

func (p *Postgres) scanFollowups(rows *sql.Rows, err error) ([]Followup, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Followup
	for rows.Next() {
		var f Followup
		var st string
		if err := rows.Scan(&f.OwnerID, &f.ChatID, &f.Hash, &f.DueAt, &st, &f.CreatedAt); err != nil {
			return nil, err
		}
		f.Status = FollowupStatus(st)
		out = append(out, f)
	}
	return out, rows.Err()
}

const followupCols = `owner_id, chat_id, hash, due_at, status, created_at`

func (p *Postgres) ListDueFollowups(ctx context.Context, before time.Time, limit int) ([]Followup, error) {
	return p.scanFollowups(p.DB.QueryContext(ctx,
		`SELECT `+followupCols+` FROM followups WHERE status = 'pending' AND due_at <= $1 ORDER BY due_at LIMIT $2`, before, limit))
}

func (p *Postgres) ListOpenFollowups(ctx context.Context, owner int64) ([]Followup, error) {
	return p.scanFollowups(p.DB.QueryContext(ctx,
		`SELECT `+followupCols+` FROM followups WHERE owner_id = $1 AND status IN ('pending','reminded') ORDER BY created_at`, owner))
}

func (p *Postgres) SetFollowupStatus(ctx context.Context, owner, chatID int64, st FollowupStatus) error {
	res, err := p.DB.ExecContext(ctx,
		`UPDATE followups SET status = $3 WHERE owner_id = $1 AND chat_id = $2`, owner, chatID, string(st))
	return affected(res, err)
}

func (p *Postgres) PruneFollowups(ctx context.Context, before time.Time) (int64, error) {
	res, err := p.DB.ExecContext(ctx, `DELETE FROM followups WHERE created_at < $1`, before)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
