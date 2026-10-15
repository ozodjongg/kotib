-- Bir nechta Telegram akkaunt (Pro): qo'shimcha akkaunt asosiy (to'lovchi) akkauntga bog'lanadi.
-- Qo'shimcha akkaunt o'zi ham accounts'da alohida ega (owner_id = uning Telegram ID'si) — sessiya,
-- sozlama, qoralamalar alohida; obuna va boshqaruv asosiy akkaunt orqali.
CREATE TABLE IF NOT EXISTS linked_accounts (
    child_id   BIGINT PRIMARY KEY,           -- har akkaunt faqat bitta asosiyga bog'lanadi
    parent_id  BIGINT NOT NULL CHECK (parent_id <> child_id),
    label      TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS linked_accounts_parent ON linked_accounts (parent_id);

-- Bog'lash havolasi (t.me/<bot>?start=link_<token>): qo'shiladigan akkaunt egasi botda o'zi
-- rozilik bildirishi uchun. Token ochiq saqlanmaydi — faqat SHA-256 xeshi; 30 daqiqa amal qiladi.
CREATE TABLE IF NOT EXISTS link_tokens (
    token_hash BYTEA PRIMARY KEY,
    parent_id  BIGINT NOT NULL,
    label      TEXT NOT NULL DEFAULT '',
    expires_at TIMESTAMPTZ NOT NULL
);
