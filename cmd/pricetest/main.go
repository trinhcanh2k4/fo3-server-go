package main

import (
	"fmt"
	"os"

	"fo3server/internal/game"
)

func main() {
	r, err := game.OpenRoster("/opt/fo3-go/gamedata", "/opt/fo3-go/responses")
	if err != nil {
		panic(err)
	}
	for _, spid := range []int{88224917, 100000001, 101000001, 93000001, 88000001} {
		fmt.Println("card", spid, "ovr", r.CardOvr(spid), "price1", int(r.CardPrice(spid, 1)))
	}
	for _, pid := range []int{45011, 45054, 70000001, 82214180, 28006} {
		fmt.Println("pack", pid, "price", r.PackPrice(pid), "pool", len(r.PackPool(pid)))
	}
	_ = os.Stdout
}
