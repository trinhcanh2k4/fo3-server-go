package main

import (
	"fmt"

	"fo3server/internal/game"
)

func main() {
	t := game.Achieve("/opt/fo3-go/gamedata")
	fmt.Println("list", len(t.List), "mixed", len(t.Mixed), "badges", len(t.Badges), "staticbadges", len(t.StaticBadges()))
	for _, w := range []int64{1763312400, 1763226000} {
		fmt.Println(w, game.SamplePy(fmt.Sprintf("quests/%d", w), 8, 5))
	}
}
