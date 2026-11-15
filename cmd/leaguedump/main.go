// leaguedump prints one user's league2 document (values, not just
// shape) plus the current-match slot, for diagnosing settlement
// issues. Usage: leaguedump <uid>
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strconv"

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
	uid, _ := strconv.ParseInt(os.Args[1], 10, 64)
	s, err := game.LoadState(ctx, st, uid)
	if err != nil {
		log.Fatal(err)
	}
	lg := game.Map(s["league2"])
	fmt.Println("league2 nil:", lg == nil)
	if lg != nil {
		keys := []string{}
		for k := range lg {
			keys = append(keys, k)
		}
		fmt.Println("league2 keys:", keys)
		fmt.Println("pending:", mustJSON(lg["pending"]))
		league := game.Map(lg["league"])
		if league == nil {
			fmt.Println("league: nil")
		} else {
			fmt.Println("nextround:", league["nextround"],
				"records:", len(game.List(league["matchrecords"])),
				"teams:", len(game.List(league["teams"])))
			tl := game.List(league["teams"])
			for i, tv := range tl {
				if i >= 3 {
					break
				}
				fmt.Printf("team[%d]: %T %v\n", i, tv, tv)
			}
			fmt.Println("matchrecords type:", fmt.Sprintf("%T", league["matchrecords"]))
			fmt.Println("statistics type:", fmt.Sprintf("%T", league["statistics"]))
		}
		fmt.Println("difficultyselect:", lg["difficultyselect"],
			"laststatus:", lg["laststatus"])
	}
	m := game.Map(s["match"])
	if m == nil {
		fmt.Println("match: nil")
	} else {
		fmt.Printf("match id: %v (type %T) stage: %v type: %v human: %v\n",
			m["id"], m["id"], m["stage"], m["type"], m["_human"])
	}
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(b)
}
