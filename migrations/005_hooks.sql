-- Avtomatlashtirish havolasi (iPhone Shortcuts / Android MacroDroid, Tasker).
-- Token ochiq saqlanmaydi — faqat SHA-256 xeshi; yangi havola eskisini bekor qiladi.
CREATE TABLE IF NOT EXISTS hooks (
    owner_id   BIGINT PRIMARY KEY REFERENCES accounts(owner_id) ON DELETE CASCADE,
    token_hash BYTEA NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
