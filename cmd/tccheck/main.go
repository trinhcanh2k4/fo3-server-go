// tccheck prints the active team colors of one user's teams (via
// Roster.OwnedTeam, the same path /coach/me uses) plus the teamcolor
// catalog id list. Read-only. Usage: tccheck <uid>
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sort"
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
	roster, err := game.OpenRoster("/opt/fo3-go/gamedata", "/opt/fo3-go/responses")
	if err != nil {
		log.Fatal(err)
	}
	teams := game.Map(s["teams"])
	keys := []string{}
	for k := range teams {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, tid := range keys {
		team := roster.OwnedTeam(game.Map(teams[tid]))
		b, _ := json.Marshal(team["teamcolors"])
		fmt.Printf("team %s teamcolors: %s\n", tid, b)
	}
	ids := []int{}
	names := map[int]string{}
	for _, c := range roster.TeamColorsCatalog() {
		cm := game.Map(c)
		ids = append(ids, game.Int(cm["teamcolorid"]))
		names[game.Int(cm["teamcolorid"])] = game.Str(cm["name"])
	}
	sort.Ints(ids)
	fmt.Printf("catalog (%d):", len(ids))
	for _, id := range ids {
		fmt.Printf(" %d=%s;", id, names[id])
	}
	fmt.Println()
}
