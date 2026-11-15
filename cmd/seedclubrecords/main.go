// seedclubrecords writes a FAKE club-records block (Phòng thành
// tích data) into one club's document so the club record room can
// be debugged in the real client. The numbers are invented debug
// data (owner request 2026-11-14); the mechanism is the generic
// records layer any club can carry — nothing is keyed to a
// specific club in the server code. Opponent/recent rows point at
// the first other club in the database when one exists.
//
// Usage: seedclubrecords [club name]   (default "GMTeam")
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"fo3server/internal/config"
	"fo3server/internal/game"
	"fo3server/internal/store"
)

func main() {
	name := "GMTeam"
	if len(os.Args) > 1 {
		name = os.Args[1]
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

	var clubid string
	err = st.DB.QueryRowContext(ctx,
		"SELECT clubid FROM clubs WHERE name = ?", name).Scan(&clubid)
	if err != nil {
		log.Fatalf("club %q not found: %v", name, err)
	}
	club, err := game.ClubGet(ctx, st, clubid)
	if err != nil || club == nil {
		log.Fatalf("load club: %v", err)
	}

	// Members in room order (grade desc, joined asc — ClubViewOf's
	// order), so the fake per-member stats land on the right rows.
	rows, err := st.DB.QueryContext(ctx,
		"SELECT coach_id FROM club_members WHERE clubid = ? ORDER BY grade DESC, joined_at ASC", clubid)
	if err != nil {
		log.Fatal(err)
	}
	members := []string{}
	for rows.Next() {
		var cid string
		if rows.Scan(&cid) == nil {
			members = append(members, cid)
		}
	}
	rows.Close()
	if len(members) == 0 {
		log.Fatalf("club %q has no members", name)
	}

	// First other club, as the fake opponent.
	var oppID, oppName string
	_ = st.DB.QueryRowContext(ctx,
		"SELECT clubid, name FROM clubs WHERE clubid <> ? ORDER BY name LIMIT 1", clubid).Scan(&oppID, &oppName)

	nowICT := time.Now().In(time.FixedZone("ICT", 7*3600))
	month := nowICT.Year()*100 + int(nowICT.Month())

	type mstat struct{ fw, ffw, rw, rl int }
	profiles := []mstat{{8, 1, 9, 4}, {6, 1, 7, 6}, {2, 0, 3, 3}}
	memberStats := map[string]any{}
	for i, cid := range members {
		p := profiles[min(i, len(profiles)-1)]
		memberStats[cid] = map[string]any{
			"friendly_wins": p.fw, "friendly_forfeitwins": p.ffw,
			"ranked_wins": p.rw, "ranked_losses": p.rl,
		}
	}

	rec := map[string]any{
		"month": month,
		// Club-level totals (leaderboard + club info statistics).
		"wins": 14, "forfeitwins": 2, "totalwins": 21,
		"totalforfeitwins": 3, "total": 26, "wp": 61,
		"friendly_wins": 14, "friendly_total": 20, "friendly_forfeitwins": 2,
		"ranked_wins": 16, "ranked_losses": 10, "ranked_total": 26,
		"ranked_wp": 61, "ranked_wprating": 58,
		"members": memberStats,
	}
	if oppID != "" {
		rec["opponents"] = []any{
			map[string]any{"clubid": oppID, "name": oppName,
				"wins": 5, "draws": 2, "losses": 3, "forfeitwins": 1,
				"total": 10, "wp": 17},
		}
		y := nowICT.Year()
		mo := int(nowICT.Month())
		mk := func(day, hs, as int, typ string) map[string]any {
			return map[string]any{
				"date":         fmt.Sprintf("%04d-%02d-%02dT13:00:00.000Z", y, mo, day),
				"type":         typ,
				"homeclubid":   clubid, "homeclubname": name,
				"awayclubid":   oppID, "awayclubname": oppName,
				"homescore":    hs, "awayscore": as,
			}
		}
		rec["recent"] = []any{
			mk(3, 3, 1, "friendly"),
			mk(5, 1, 1, "friendly"),
			mk(8, 0, 2, "friendly"),
			mk(11, 2, 0, "ranked"),
		}
	}

	fmt.Printf("club %s (%s): old records nil=%v, members=%d, opponent=%q\n",
		name, clubid, club.Doc.Records == nil, len(members), oppName)
	club.Doc.Records = rec
	if err := game.ClubSaveDoc(ctx, st, club); err != nil {
		log.Fatal(err)
	}
	fmt.Println("seeded fake records for month", month)
}
