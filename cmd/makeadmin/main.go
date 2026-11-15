// makeadmin creates or resets an admin account for the web admin.
// Usage: makeadmin <username> <password> [role]
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"fo3server/internal/config"
	"fo3server/internal/store"
)

func main() {
	if len(os.Args) < 3 {
		log.Fatal("usage: makeadmin <username> <password> [role]")
	}
	role := "admin"
	if len(os.Args) > 3 {
		role = os.Args[3]
	}
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
	hash, err := store.HashPassword(os.Args[2])
	if err != nil {
		log.Fatal(err)
	}
	_, err = st.DB.ExecContext(ctx,
		"INSERT INTO admins (username, pass_hash, role) VALUES (?, ?, ?) ON DUPLICATE KEY UPDATE pass_hash = VALUES(pass_hash), role = VALUES(role)",
		os.Args[1], hash, role)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("admin %q (%s) ready\n", os.Args[1], role)
}
