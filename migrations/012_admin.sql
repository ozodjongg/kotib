-- Admin paneli: foydalanuvchining Telegram ismi/username'i va Mini App'ni oxirgi ochgan vaqti
-- (ro'yxatda tanib olish uchun), hamda bloklash (ban).
ALTER TABLE accounts ADD COLUMN IF NOT EXISTS tg_name     TEXT NOT NULL DEFAULT '';
ALTER TABLE accounts ADD COLUMN IF NOT EXISTS tg_username TEXT NOT NULL DEFAULT '';
ALTER TABLE accounts ADD COLUMN IF NOT EXISTS seen_at     TIMESTAMPTZ;

-- bans akkauntga FK bilan bog'lanmagan: akkauntni o'chirib qayta ochish blokni olib tashlamaydi.
CREATE TABLE IF NOT EXISTS bans (
    owner_id   BIGINT PRIMARY KEY,
    reason     TEXT NOT NULL DEFAULT '',
    by_admin   BIGINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
