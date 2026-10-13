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

var _ Store = (*Postgres)(nil)
var _ Store = (*Memory)(nil)
