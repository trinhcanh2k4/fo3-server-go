// debugmove diagnoses playerMove internals for one user + player id.
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
	state, err := game.LoadState(ctx, st, 11)
	if err != nil {
		log.Fatal(err)
	}
	slot := game.DefaultSlot(state)
	team := game.TeamOf(state, slot)
	pid := os.Args[1]
	fmt.Println("slot:", slot, "| squad keys:", func() []string {
		ks := []string{}
		for k := range game.Map(team["squad"]) {
			ks = append(ks, k)
		}
		return ks
	}())
	fmt.Println("stored positions:", len(game.List(game.Map(team["squad"])["positions"])))
	for _, pv := range game.List(team["players"]) {
		p := game.Map(pv)
		if game.IID(p) == pid {
			fmt.Println("found player:", p["spid"], "team2:", p["team2"], "| InSquad:", game.InSquad(team, p), "| IsStarter:", game.IsStarter(team, p))
		}
	}
	for _, sv := range game.List(game.Map(team["squad"])["positions"]) {
		s := game.Map(sv)
		fmt.Println("pos entry: id=", fmt.Sprintf("%v", s["id"]), "spid=", s["spid"], "pos=", s["pos"])
	}
}
