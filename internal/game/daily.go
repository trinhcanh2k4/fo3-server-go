package game

// Daily login reward + Bag claiming, ported from hc (daily_login,
// bag_add/bag_apply, ep_bag_unpack/ep_bag_unpackall). The reward
// table lives in gamedata/daily_rewards.json and is re-read on every
// grant, so admin edits apply without a restart (as in Python).

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"time"
)

type DailyReward struct {
	EP    int `json:"ep"`
	Pack  int `json:"pack"`
	Count int `json:"count"`
}

var dailyDefault = []DailyReward{
	{EP: 10000}, {EP: 20000}, {Pack: 28007}, {EP: 30000}, {Pack: 28006},
	{EP: 50000}, {EP: 100000, Pack: 82214068},
}

func (r *Roster) DailyTable() []DailyReward {
	raw, err := readFileCached(filepath.Join(r.gamedataDir, "daily_rewards.json"))
	if err == nil {
		var table []DailyReward
		if json.Unmarshal(raw, &table) == nil && len(table) > 0 {
			return table
		}
	}
	return dailyDefault
}

// TodayWIB is hc.today_wib: the calendar day at UTC+7.
func TodayWIB(t time.Time) string {
	return t.Add(7 * time.Hour).UTC().Format("2006-01-02")
}

func rewardItems(rw DailyReward) []any {
	items := []any{}
	if rw.EP > 0 {
		items = append(items, map[string]any{"itemid": EPItemID, "count": rw.EP})
	}
	if rw.Pack > 0 {
		count := rw.Count
		if count <= 0 {
			count = 1
		}
		items = append(items, map[string]any{"itemid": rw.Pack, "count": count})
	}
	return items
}

// DailyLogin grants today's reward into the default coach's Bag on
// the first coach-default call of a WIB day. Returns the new entry
// or nil when already granted today.
func (r *Roster) DailyLogin(st State) map[string]any {
	coach := CoachBySlot(st, DefaultSlot(st))
	if coach == nil {
		return nil
	}
	d := Map(coach["daily"])
	if d == nil {
		d = map[string]any{"last": "", "streak": 0, "total": 0}
		coach["daily"] = d
	}
	now := time.Now()
	today := TodayWIB(now)
	if Str(d["last"]) == today {
		return nil
	}
	streak := 1
	if Str(d["last"]) == TodayWIB(now.Add(-24*time.Hour)) {
		streak = Int(d["streak"]) + 1
	}
	d["streak"] = streak
	d["total"] = Int(d["total"]) + 1
	d["last"] = today
	table := r.DailyTable()
	rw := table[(streak-1)%len(table)]
	return BagAdd(coach, fmt.Sprintf("Điểm danh hàng ngày - Ngày thứ %d", streak),
		rewardItems(rw),
		fmt.Sprintf("Đã điểm danh %d ngày liên tiếp. Hãy đăng nhập vào ngày mai để nhận quà ngày %d.", streak, streak+1))
}

// BagApply credits one bag entry (EP, items, players) and removes it
// from the coach's bag. Port of hc.bag_apply.
func (r *Roster) BagApply(st State, entry map[string]any) {
	coach := CoachBySlot(st, DefaultSlot(st))
	team := TeamOf(st, DefaultSlot(st))
	if coach == nil {
		return
	}
	t := Tables(r.gamedataDir)
	for _, raw := range List(entry["items"]) {
		im := Map(raw)
		if im == nil {
			continue
		}
		itemid, count := Int(im["itemid"]), Int(im["count"])
		switch {
		case itemid == EPItemID:
			coach["ep"] = Int64(coach["ep"]) + int64(count)
		default:
			if _, isPack := t.Packs[itemid]; isPack || r.ItemDef(itemid) != nil {
				InventoryAdd(coach, itemid, count)
			}
		}
	}
	if team != nil {
		for _, raw := range List(entry["players"]) {
			pm := Map(raw)
			if pm == nil {
				continue
			}
			grade := Int(pm["grade"])
			if grade <= 0 {
				grade = 1
			}
			r.AddPlayer(team, Int(pm["spid"]), grade)
		}
	}
	bag := List(coach["bag"])
	kept := make([]any, 0, len(bag))
	for _, raw := range bag {
		if e := Map(raw); e != nil && Str(e["_id"]) == Str(entry["_id"]) {
			continue
		}
		kept = append(kept, raw)
	}
	coach["bag"] = kept
}
