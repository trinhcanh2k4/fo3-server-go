// importfriends copies the legacy Python friendships (SQLite friends
// table) into MariaDB and each user's contact_groups from the Python
// state into the Go state. Usage: importfriends <path-to-fo3_users.db>
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
	rows, err := db.Query("SELECT coach_id, friend_id, friend_name, group_name, status, created_at FROM friends")
	if err != nil {
		log.Fatal(err)
	}
	n := 0
	for rows.Next() {
		var cid, fid string
		var name, group, status, created *string
		if rows.Scan(&cid, &fid, &name, &group, &status, &created) != nil {
			continue
		}
		stat := "accepted"
		if status != nil && *status != "" {
			stat = *status
		}
		_, err := st.DB.ExecContext(ctx,
			`INSERT INTO friends (coach_id, friend_coach_id, friend_name, group_name, status, created_at)
			 VALUES (?, ?, ?, ?, ?, ?) ON DUPLICATE KEY UPDATE
			 friend_name = VALUES(friend_name), group_name = VALUES(group_name),
			 status = VALUES(status), created_at = VALUES(created_at)`,
			cid, fid, name, group, stat, created)
		if err != nil {
			fmt.Printf("row %s->%s failed: %v\n", cid, fid, err)
			continue
		}
		n++
	}
	rows.Close()
	fmt.Printf("friends imported: %d rows\n", n)

	urows, err := db.Query("SELECT username, state_json FROM users")
	if err != nil {
		log.Fatal(err)
	}
	type legacy struct {
		username string
		groups   []any
	}
	var leg []legacy
	for urows.Next() {
		var un, sj string
		if urows.Scan(&un, &sj) != nil {
			continue
		}
		m := map[string]any{}
		if json.Unmarshal([]byte(sj), &m) == nil {
			if g, isList := m["contact_groups"].([]any); isList && len(g) > 0 {
				leg = append(leg, legacy{un, g})
			}
		}
	}
	urows.Close()
	for _, l := range leg {
		u, err := game.UserByUsername(ctx, st, l.username)
		if err != nil || u == nil {
			continue
		}
		gs, err := game.LoadState(ctx, st, u.ID)
		if err != nil || gs == nil {
			continue
		}
		gs["contact_groups"] = l.groups
		if err := game.SaveState(ctx, st, u.ID, gs); err != nil {
			fmt.Printf("%s: group save failed %v\n", l.username, err)
			continue
		}
		fmt.Printf("%s: contact_groups %v\n", l.username, l.groups)
	}
}
