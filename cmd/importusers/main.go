// importusers imports the Python-era accounts from a SQLite backup of
// fo3_users.db into MariaDB. Passwords arrive as the legacy unsalted
// SHA-256 hex (users.legacy_hash) and are upgraded to Argon2id at the
// first successful web login. Game state is NOT imported: the Go
// system starts clean (the agreed data reset); coaches are re-seeded
// by cmd/seedcoaches from the account metadata (name, team, elo).
package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"

	_ "modernc.org/sqlite"

	"fo3server/internal/config"
	"fo3server/internal/store"
)

func main() {
	if len(os.Args) < 2 {
		log.Fatal("usage: importusers <fo3_users.db>")
	}
	src, err := sql.Open("sqlite", "file:"+os.Args[1]+"?mode=ro")
	if err != nil {
		log.Fatal(err)
	}
	defer src.Close()
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	cipher, err := store.NewCipher(cfg.MasterKey)
	if err != nil {
		log.Fatal(err)
	}
	ctx := context.Background()
	st, err := store.Open(ctx, cfg.DBDSN, cfg.RedisAddr, cfg.RedisPass, cipher)
	if err != nil {
		log.Fatalf("store: %v", err)
	}
	defer st.DB.Close()

	rows, err := src.Query("SELECT id, username, password_hash, IFNULL(coach_name,''), IFNULL(elo,1000) FROM users ORDER BY id")
	if err != nil {
		log.Fatalf("read sqlite users: %v", err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var id int64
		var username, pwHash, coachName string
		var elo int
		if err := rows.Scan(&id, &username, &pwHash, &coachName, &elo); err != nil {
			log.Fatal(err)
		}
		_, err := st.DB.ExecContext(ctx,
			`INSERT INTO users (id, username, pass_hash, legacy_hash, coach_name, elo)
			 VALUES (?, ?, '', ?, ?, ?)
			 ON DUPLICATE KEY UPDATE legacy_hash = VALUES(legacy_hash),
			   coach_name = VALUES(coach_name), elo = VALUES(elo)`,
			id, username, pwHash, coachName, elo)
		if err != nil {
			log.Fatalf("insert %s: %v", username, err)
		}
		fmt.Printf("imported %s (id %d, coach %q, elo %d)\n", username, id, coachName, elo)
		n++
	}
	fmt.Printf("done: %d users\n", n)
}
