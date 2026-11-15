// importleague copies each user's league2 season (and archives,
// tactics, and any in-flight match) from the legacy Python state
// (SQLite) into the Go state, so the Go league domain takes over
// running seasons without losing progress.
// Usage: importleague <path-to-fo3_users.db>
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"

	_ "modernc.org/sqlite"

	"fo3server/internal/config"
	"fo3server/internal/game"
	"fo3server/internal/store"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}
	cipher, err := store.NewCipher(cfg.MasterKey)
	if err != nil {
		log.Fatal(err)
	}
	ctx := context.Background()
	st, err := store.Open(ctx, cfg.DBDSN, cfg.RedisAddr, cfg.RedisPass, cipher)
	if err != nil {
		log.Fatal(err)
	}
	defer st.DB.Close()
	db, err := sql.Open("sqlite", "file:"+os.Args[1]+"?mode=ro")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Query("SELECT username, state_json FROM users")
	if err != nil {
		log.Fatal(err)
	}
	type legacy struct {
		username string
		state    map[string]any
	}
	var leg []legacy
	for rows.Next() {
		var un, sj string
		if rows.Scan(&un, &sj) != nil {
			continue
		}
		m := map[string]any{}
		if json.Unmarshal([]byte(sj), &m) == nil {
			leg = append(leg, legacy{un, m})
		}
	}
	rows.Close()
	for _, l := range leg {
		u, err := game.UserByUsername(ctx, st, l.username)
		if err != nil || u == nil {
			fmt.Printf("%s: no Go user, skipped\n", l.username)
			continue
		}
		gs, err := game.LoadState(ctx, st, u.ID)
		if err != nil || gs == nil {
			fmt.Printf("%s: no Go state, skipped\n", l.username)
			continue
		}
		copied := []string{}
		for _, key := range []string{"league2", "league2_archives", "tactics", "match"} {
			if v, ok := l.state[key]; ok {
				gs[key] = v
				copied = append(copied, key)
			}
		}
		if len(copied) == 0 {
			fmt.Printf("%s: nothing to import\n", l.username)
			continue
		}
		if err := game.SaveState(ctx, st, u.ID, gs); err != nil {
			log.Fatalf("%s: save: %v", l.username, err)
		}
		fmt.Printf("%s: imported %v\n", l.username, copied)
	}
}
