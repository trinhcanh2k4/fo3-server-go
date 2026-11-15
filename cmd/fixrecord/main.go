// fixrecord repairs one league matchrecord's goals in a user's
// state and recomputes the league statistics from all records.
// Usage: fixrecord <uid> <round> <homeGoals> <awayGoals>
package main

import (
	"context"
	"fmt"
	"os"
	"strconv"

	"fo3server/internal/config"
	"fo3server/internal/game"
	"fo3server/internal/store"
)

func main() {
	uid, _ := strconv.Atoi(os.Args[1])
	rnd, _ := strconv.Atoi(os.Args[2])
	hg, _ := strconv.Atoi(os.Args[3])
	ag, _ := strconv.Atoi(os.Args[4])
	cfg, err := config.Load()
	if err != nil {
		panic(err)
	}
	cipher, err := store.NewCipher(cfg.MasterKey)
	if err != nil {
		panic(err)
	}
	st, err := store.Open(context.Background(), cfg.DBDSN, cfg.RedisAddr, cfg.RedisPass, cipher)
	if err != nil {
		panic(err)
	}
	state, err := game.LoadState(context.Background(), st, int64(uid))
	if err != nil {
		panic(err)
	}
	me := game.MyTeamID(state)
	lg := game.Map(state["league2"])
	league := game.Map(lg["league"])
	records := []map[string]any{}
	fixed := 0
	for _, rv := range game.List(league["matchrecords"]) {
		rec := game.Map(rv)
		if rec == nil {
			continue
		}
		if game.Int(rec["round"]) == rnd &&
			(game.Int(rec["home"]) == me || game.Int(rec["away"]) == me) {
			fmt.Printf("before: round %d home %d away %d goals %v\n",
				rnd, game.Int(rec["home"]), game.Int(rec["away"]), rec["goals"])
			rec["goals"] = map[string]any{"home": hg, "away": ag}
			fixed++
		}
		records = append(records, rec)
	}
	if fixed != 1 {
		panic(fmt.Sprintf("expected exactly 1 record to fix, got %d", fixed))
	}
	teams := []int{}
	for _, tv := range game.List(league["teams"]) {
		teams = append(teams, game.Int(tv))
	}
	league["statistics"] = game.LeagueStats(teams, records, game.Map(league["statistics"]))
	if err := game.SaveState(context.Background(), st, int64(uid), state); err != nil {
		panic(err)
	}
	fmt.Println("record fixed; statistics recomputed")
}
