package main

import (
	// Postgres drayveri ("pgx" nomi bilan ro'yxatdan o'tadi).
	_ "github.com/jackc/pgx/v5/stdlib"

	// Vaqt mintaqalari bazasi binary ichida (~450 KB): server qayerda bo'lishidan
	// (Amsterdam, UTC) va konteynerda /usr/share/zoneinfo bor-yo'qligidan qat'i nazar,
	// "Asia/Tashkent" kabi tinch soatlar mintaqasi doim to'g'ri ishlaydi.
	_ "time/tzdata"
)
