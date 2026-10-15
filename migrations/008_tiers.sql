-- Ikki tarif: advanced va pro. Bo'sh qiymat — tariflardan oldingi (eski) obuna: Pro sifatida
-- saqlanadi, mavjud mijozlar funksiyalarini yo'qotmasin.
ALTER TABLE subscriptions ADD COLUMN IF NOT EXISTS tier TEXT NOT NULL DEFAULT '';
