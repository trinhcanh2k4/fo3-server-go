// statedump prints a structural view of one user's decrypted core
// state. Usage: statedump <uid>
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"sort"
	"strconv"

	"fo3server/internal/config"
	"fo3server/internal/game"
	"fo3server/internal/store"
)

func shape(v any) string {
	switch t := v.(type) {
	case []any:
		return fmt.Sprintf("list(%d)", len(t))
	case map[string]any:
		return fmt.Sprintf("map(%d)", len(t))
	case nil:
		return "nil"
	default:
		return fmt.Sprintf("%T", v)
	}
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
	uid, _ := strconv.ParseInt(os.Args[1], 10, 64)
	s, err := game.LoadState(ctx, st, uid)
	if err != nil {
		log.Fatal(err)
	}
	keys := []string{}
	for k := range s {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	fmt.Println("state keys:")
	for _, k := range keys {
		fmt.Printf("  %s: %s\n", k, shape(s[k]))
	}
	for i, coach := range game.Coaches(s) {
		fmt.Printf("coach[%d] name=%v ep=%v:\n", i, coach["name"], coach["ep"])
		ck := []string{}
		for k := range coach {
			ck = append(ck, k)
		}
		sort.Strings(ck)
		for _, k := range ck {
			fmt.Printf("  %s: %s\n", k, shape(coach[k]))
		}
	}
	teams := game.Map(s["teams"])
	for tid, tv := range teams {
		tm := game.Map(tv)
		fmt.Printf("team %s: formation=%v positions=%d players=%s\n",
			tid, tm["formation"], len(game.Map(tm["positions"])), shape(tm["players"]))
	}
}
