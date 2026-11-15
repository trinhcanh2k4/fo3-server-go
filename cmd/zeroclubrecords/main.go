// zeroclubrecords clears the records block of the named clubs
// (by club name, args). Written for the club-friendly launch:
// GMTeam and Aton carried FAKE seeded records from the record
// room debugging (2026-11-14), and the real feature must start
// from an honest zero. A nil records block renders zeroed
// statistics through ClubBody, and the match recorder creates
// a fresh block on the first real club match.
//
// Usage: zeroclubrecords <club name> [more names...]
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"fo3server/internal/config"
	"fo3server/internal/game"
	"fo3server/internal/store"
)

func main() {
	if len(os.Args) < 2 {
		log.Fatal("usage: zeroclubrecords <club name> [more names...]")
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
	for _, name := range os.Args[1:] {
		var clubid string
		err := st.DB.QueryRowContext(ctx,
			"SELECT clubid FROM clubs WHERE name = ?", name).Scan(&clubid)
		if err != nil {
			log.Fatalf("club %q: %v", name, err)
		}
		club, err := game.ClubGet(ctx, st, clubid)
		if err != nil || club == nil {
			log.Fatalf("club %q (%s): load failed", name, clubid)
		}
		had := club.Doc.Records != nil
		club.Doc.Records = nil
		if err := game.ClubSaveDoc(ctx, st, club); err != nil {
			log.Fatalf("club %q: save: %v", name, err)
		}
		fmt.Printf("club %s (%s): records cleared (had records: %v)\n", name, clubid, had)
	}
}
