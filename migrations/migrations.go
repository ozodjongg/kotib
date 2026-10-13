// Package migrations: Postgres sxemasi (SQL fayllar binary ichiga joylanadi).
//
// Apply ilova ishga tushganda chaqiriladi: hali qo'llanmagan migratsiyalarni nom
// tartibida, har birini alohida tranzaksiyada bajaradi va schema_migrations'ga yozadi.
// Bir nechta nusxa bir vaqtda ishga tushsa — advisory lock ularni navbatga qo'yadi.
package migrations

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"sort"
)

//go:embed *.sql
var files embed.FS

// lockID — pg_advisory_lock uchun ixtiyoriy doimiy son.
const lockID = 7_311_002_401

func Apply(ctx context.Context, db *sql.DB) error {
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, lockID); err != nil {
		return fmt.Errorf("migratsiya lock: %w", err)
	}
	defer conn.ExecContext(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock($1)`, lockID)

	if _, err := conn.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		name       TEXT PRIMARY KEY,
		applied_at TIMESTAMPTZ NOT NULL DEFAULT now())`); err != nil {
		return err
	}

	names, err := fs.Glob(files, "*.sql")
	if err != nil {
		return err
	}
	sort.Strings(names)
	for _, name := range names {
		var done bool
		if err := conn.QueryRowContext(ctx,
			`SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE name = $1)`, name).Scan(&done); err != nil {
			return err
		}
		if done {
			continue
		}
		body, err := files.ReadFile(name)
		if err != nil {
			return err
		}
		tx, err := conn.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, string(body)); err != nil {
			tx.Rollback()
			return fmt.Errorf("migratsiya %s: %w", name, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (name) VALUES ($1)`, name); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// Names qo'llaniladigan migratsiya fayllari (test uchun).
func Names() []string {
	names, _ := fs.Glob(files, "*.sql")
	sort.Strings(names)
	return names
}
