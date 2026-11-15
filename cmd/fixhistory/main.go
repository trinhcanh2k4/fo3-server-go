// fixhistory repairs match-history entries whose opponent was recorded
// as the player themself (matchEnd read the opponent from the match's
// right side unconditionally, so a guest/right-side caller's history
// showed them playing against themself). For every self-opponent entry
// it finds the mirror entry in the real opponent's history (same type,
// dates within two minutes) and copies that entry's team block over the
// broken opponent block. Scores and the entry's own team stay as-is.
// Usage: fixhistory (scans all users; saves only users it changed).
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"fo3server/internal/config"
	"fo3server/internal/game"
	"fo3server/internal/store"
)

const w3c = "2006-01-02T15:04:05.000Z"

func firstCoachID(block map[string]any) string {
	coaches := game.List(block["coaches"])
	if len(coaches) == 0 {
		return ""
	}
	return game.Str(game.Map(coaches[0])["coachid"])
}

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

	users, err := game.AllUsers(ctx, st)
	if err != nil {
		log.Fatal(err)
	}
	// Load every state once; mirrors are looked up across users.
	states := map[int64]game.State{}
	changed := map[int64]bool{}
	for _, u := range users {
		s, err := game.LoadState(ctx, st, u.ID)
		if err != nil {
			log.Fatalf("load %d: %v", u.ID, err)
		}
		states[u.ID] = s
	}

	for _, u := range users {
		s := states[u.ID]
		for _, coach := range game.Coaches(s) {
			hist := game.List(coach["multiplay"])
			for i, ev := range hist {
				e := game.Map(ev)
				team := game.Map(e["team"])
				opp := game.Map(e["opponent"])
				if team == nil || opp == nil {
					continue
				}
				me, op := firstCoachID(team), firstCoachID(opp)
				if me == "" || op == "" || me != op {
					continue
				}
				// Self-opponent entry: find the mirror in another user.
				et, _ := time.Parse(w3c, game.Str(e["date"]))
				fixed := false
				for _, v := range users {
					if v.ID == u.ID {
						continue
					}
					for _, vc := range game.Coaches(states[v.ID]) {
						for _, mv := range game.List(vc["multiplay"]) {
							m := game.Map(mv)
							if game.Str(m["type"]) != game.Str(e["type"]) {
								continue
							}
							mt, _ := time.Parse(w3c, game.Str(m["date"]))
							d := et.Sub(mt)
							if d < 0 {
								d = -d
							}
							if d > 2*time.Minute {
								continue
							}
							mTeam := game.Map(m["team"])
							mOpp := game.Map(m["opponent"])
							if firstCoachID(mTeam) == me || firstCoachID(mOpp) != me {
								continue
							}
							// Mirror found: its team is the true opponent.
							opp["teamid"] = mTeam["teamid"]
							opp["coaches"] = mTeam["coaches"]
							hist[i] = e
							changed[u.ID] = true
							fixed = true
							fmt.Printf("uid %d: entry %s (%s) opponent -> teamid %v coach %s\n",
								u.ID, game.Str(e["date"]), game.Str(e["type"]),
								mTeam["teamid"], firstCoachID(mTeam))
							break
						}
						if fixed {
							break
						}
					}
					if fixed {
						break
					}
				}
				if !fixed {
					fmt.Printf("uid %d: entry %s (%s) self-opponent, NO mirror found (left as-is)\n",
						u.ID, game.Str(e["date"]), game.Str(e["type"]))
				}
			}
		}
	}
	for uid := range changed {
		if err := game.SaveState(ctx, st, uid, states[uid]); err != nil {
			log.Fatalf("save %d: %v", uid, err)
		}
		fmt.Printf("saved uid %d\n", uid)
	}
	fmt.Println("done")
}
