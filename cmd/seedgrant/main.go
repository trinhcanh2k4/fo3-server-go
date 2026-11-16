// seedgrant grants grade-1 players to one user's default team and
// prints the new players' IIDs, one per line ("spid iid"). One-off
// ops tool (used for the one-time transfer-market seed, 2026-11-15):
// the listings themselves go through the real /transfer/offer API.
//
// Usage: seedgrant <uid> <spid[:grade],spid[:grade],...> (grade defaults to 1)
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"

	"fo3server/internal/config"
	"fo3server/internal/game"
	"fo3server/internal/store"
)

func main() {
	if len(os.Args) != 3 {
		log.Fatal("usage: seedgrant <uid> <spid,spid,...>")
	}
	uid, err := strconv.ParseInt(os.Args[1], 10, 64)
	if err != nil {
		log.Fatal(err)
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
	roster, err := game.OpenRoster("/opt/fo3-go/gamedata", "/opt/fo3-go/responses")
	if err != nil {
		log.Fatal(err)
	}
	s, err := game.LoadState(ctx, st, uid)
	if err != nil || s == nil {
		log.Fatalf("load state uid %d: %v", uid, err)
	}
	team := game.TeamOf(s, game.DefaultSlot(s))
	if team == nil {
		log.Fatalf("uid %d has no team", uid)
	}
	for _, part := range strings.Split(os.Args[2], ",") {
		part = strings.TrimSpace(part)
		spidStr, grade := part, 1
		if i := strings.Index(part, ":"); i >= 0 {
			spidStr = part[:i]
			g, err := strconv.Atoi(part[i+1:])
			if err != nil || g < 1 || g > 10 {
				log.Fatalf("bad grade in %q", part)
			}
			grade = g
		}
		spid, err := strconv.Atoi(spidStr)
		if err != nil || spid == 0 {
			log.Fatalf("bad spid %q", part)
		}
		p := roster.AddPlayer(team, spid, grade)
		fmt.Printf("%d %d %s\n", spid, grade, game.IID(p))
	}
	if err := game.SaveState(ctx, st, uid, s); err != nil {
		log.Fatal(err)
	}
}
