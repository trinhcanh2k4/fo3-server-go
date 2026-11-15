package game

// Achievements + weekly quests + GP, ported from fo3_ext.py
// (_build_achievement_me_payload, _quest_state, ep_static_badges,
// ep_static_achievement). The catalogue is extracted verbatim from
// the Python module AFTER its import-time mutations (expand, split,
// normalize, enrich) into gamedata/achieve_tables.json.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

type AchieveTables struct {
	List   []map[string]any
	Mixed  map[string][]string
	Badges []map[string]any
	byID   map[string]map[string]any
	dir    string
}

// RewardOverride returns the admin-authored reward entries for an
// achievement id (gamedata/achievement_rewards.json:
// {"<id>": [{"itemid": N, "count": N}]}), replacing the catalogue's
// authored rewards at serve + claim time (admin editor, 2026-11-13).
func (t *AchieveTables) RewardOverride(id string) ([]map[string]any, bool) {
	if t.dir == "" {
		return nil, false
	}
	b, err := readFileCached(filepath.Join(t.dir, "achievement_rewards.json"))
	if err != nil {
		return nil, false
	}
	var m map[string][]map[string]any
	if json.Unmarshal(b, &m) != nil {
		return nil, false
	}
	entries, ok := m[id]
	return entries, ok
}

var achTables *AchieveTables
var achOnce sync.Once

func Achieve(gamedataDir string) *AchieveTables {
	achOnce.Do(func() {
		t := &AchieveTables{Mixed: map[string][]string{}, byID: map[string]map[string]any{}, dir: gamedataDir}
		b, err := os.ReadFile(filepath.Join(gamedataDir, "achieve_tables.json"))
		if err == nil {
			var raw struct {
				Info struct {
					List  []map[string]any   `json:"list"`
					Mixed map[string][]string `json:"mixed"`
				} `json:"info"`
				Badges []map[string]any `json:"badges"`
			}
			if json.Unmarshal(b, &raw) == nil {
				t.List = raw.Info.List
				t.Mixed = raw.Info.Mixed
				t.Badges = raw.Badges
				for _, e := range t.List {
					t.byID[Str(e["id"])] = e
				}
			}
		}
		achTables = t
	})
	return achTables
}

func (t *AchieveTables) ByID(id string) map[string]any { return t.byID[id] }

// GroupOrder is the catalogue's group order after Python's
// _normalize_ach_catalogue: ach_1..ach_33, then misc.
func (t *AchieveTables) GroupOrder() []string {
	out := []string{}
	seen := map[string]bool{}
	for i := 1; i <= 33; i++ {
		gid := fmt.Sprintf("ach_%d", i)
		if _, ok := t.Mixed[gid]; ok {
			out = append(out, gid)
			seen[gid] = true
		}
	}
	if _, ok := t.Mixed["misc"]; ok {
		out = append(out, "misc")
		seen["misc"] = true
	}
	rest := []string{}
	for k := range t.Mixed {
		if !seen[k] {
			rest = append(rest, k)
		}
	}
	sort.Strings(rest)
	return append(out, rest...)
}

// StaticBadges ports fx.ep_static_badges.
func (t *AchieveTables) StaticBadges() []any {
	groupByBadge := map[int]string{}
	for _, e := range t.List {
		if e["badge"] != nil {
			if _, isGroup := t.Mixed[Str(e["id"])]; isGroup {
				groupByBadge[Int(e["badge"])] = Str(e["id"])
			}
		}
	}
	out := []any{}
	for _, b := range t.Badges {
		bid := Int(b["badgeid"])
		nb := map[string]any{}
		for k, v := range b {
			nb[k] = v
		}
		asset := groupByBadge[bid]
		if asset == "" {
			asset = Str(b["asset"])
		}
		nb["id"] = bid
		nb["itemid"] = bid
		nb["category"] = "BADGE"
		nb["itemicon"] = bid
		nb["itemback"] = "defaultblack"
		nb["asset"] = asset
		out = append(out, nb)
	}
	return out
}

// StaticAchievement ports fx.ep_static_achievement.
func (t *AchieveTables) StaticAchievement() map[string]any {
	list := []any{}
	for _, e := range t.List {
		ne := map[string]any{}
		for k, v := range e {
			ne[k] = v
		}
		sd := Str(e["shortdesc"])
		if sd == "" {
			sd = Str(e["desc"])
		}
		if sd == "" {
			sd = Str(e["name"])
		}
		ne["shortdesc"] = sd
		ne["longdesc"] = Str(e["desc"])
		ne["description"] = Str(e["desc"])
		ne["title"] = Str(e["name"])
		if ov, ok := t.RewardOverride(Str(e["id"])); ok {
			ne["rewards"] = rewardListWith(e["rewards"], ov)
		}
		list = append(list, ne)
	}
	mixed := map[string]any{}
	for k, v := range t.Mixed {
		arr := []any{}
		for _, s := range v {
			arr = append(arr, s)
		}
		mixed[k] = arr
	}
	return map[string]any{"list": list, "mixed": mixed}
}

// rewardListWith returns the catalogue rewards list with the first
// entry's "reward" replaced by the admin override entries.
func rewardListWith(authored any, override []map[string]any) []any {
	ov := []any{}
	for _, e := range override {
		ov = append(ov, e)
	}
	first := map[string]any{}
	if lst := List(authored); len(lst) > 0 {
		for k, v := range Map(lst[0]) {
			first[k] = v
		}
	}
	first["reward"] = ov
	return []any{first}
}

// --- quest configuration (admin-editable) --------------------------------
// gamedata/quest_config.json overrides the weekly quest pool, the
// GP milestones and the HLV quest catalogue rewards:
// {"pool": [{metric, target, gp}], "milestones": [{gp, ep, pack}],
//  "rewards": {"<quest id>": {"ep": N, "pack": N, "gp": N}}}.

type QuestPoolDef struct {
	Metric string `json:"metric"`
	Target int    `json:"target"`
	GP     int    `json:"gp"`
}

type GPMilestone struct {
	GP   int `json:"gp"`
	EP   int `json:"ep"`
	Pack int `json:"pack"`
}

var DefaultGPMilestones = []GPMilestone{{100, 1000000, 0}, {200, 2000000, 0}, {300, 5000000, 28001}}

type questCfgFile struct {
	Pool       []QuestPoolDef            `json:"pool"`
	Milestones []GPMilestone             `json:"milestones"`
	Rewards    map[string]map[string]int `json:"rewards"`
}

func loadQuestCfg(dir string) *questCfgFile {
	if dir == "" {
		return nil
	}
	b, err := readFileCached(filepath.Join(dir, "quest_config.json"))
	if err != nil {
		return nil
	}
	var cfg questCfgFile
	if json.Unmarshal(b, &cfg) != nil {
		return nil
	}
	return &cfg
}

// QuestPool returns the effective weekly quest pool.
func QuestPool(dir string) []QuestPoolDef {
	if cfg := loadQuestCfg(dir); cfg != nil && len(cfg.Pool) > 0 {
		return cfg.Pool
	}
	out := []QuestPoolDef{}
	for _, d := range questPoolDefs {
		out = append(out, QuestPoolDef{d.metric, d.target, d.gp})
	}
	return out
}

// GPMilestones returns the effective GP milestone table.
func GPMilestones(dir string) []GPMilestone {
	if cfg := loadQuestCfg(dir); cfg != nil && len(cfg.Milestones) > 0 {
		return cfg.Milestones
	}
	return DefaultGPMilestones
}

// QuestRewardOverrides returns per-quest reward overrides (may be nil).
func QuestRewardOverrides(dir string) map[string]map[string]int {
	if cfg := loadQuestCfg(dir); cfg != nil {
		return cfg.Rewards
	}
	return nil
}

// gpMilestonePayload builds the payload's questRewards list from the
// effective milestone table.
func gpMilestonePayload(dir string, gpClaimed map[int]bool) []any {
	out := []any{}
	for _, m := range GPMilestones(dir) {
		reward := []any{map[string]any{"itemid": 1, "count": m.EP}}
		if m.Pack != 0 {
			reward = append(reward, map[string]any{"itemid": m.Pack, "count": 1})
		}
		out = append(out, map[string]any{
			"gp": m.GP, "reward": reward, "received": gpClaimed[m.GP],
		})
	}
	return out
}

// --- weekly quests --------------------------------------------------------

var questPoolDefs = []struct {
	metric string
	target int
	gp     int
}{
	{"match", 10, 30}, {"win", 6, 30}, {"goal", 15, 25}, {"upgrade", 3, 20},
	{"rank", 5, 25}, {"rankwin", 3, 30}, {"match", 20, 50}, {"goal", 30, 45},
}

var questTextVI = map[string]string{
	"match": "Chơi %d trận", "win": "Thắng %d trận", "goal": "Ghi %d bàn",
	"rank": "Chơi %d trận Rank", "rankwin": "Thắng %d trận Rank",
	"upgrade": "Nâng cấp cầu thủ %d lần",
}

type WeekQuest struct {
	Name   string
	Metric string
	Target int
	GP     int
}

// WeekQuests draws the week's quests with the Python-compatible RNG
// (fx._week_quests: random.Random("quests/<week_end>").sample).
func WeekQuests(dir string, weekEnd int64) []WeekQuest {
	pool := QuestPool(dir)
	k := 5
	if len(pool) < k {
		k = len(pool)
	}
	idx := SamplePy(fmt.Sprintf("quests/%d", weekEnd), len(pool), k)
	out := []WeekQuest{}
	for _, i := range idx {
		d := pool[i]
		out = append(out, WeekQuest{
			Name:   fmt.Sprintf(questTextVI[d.Metric], d.Target),
			Metric: d.Metric, Target: d.Target, GP: d.GP,
		})
	}
	return out
}

func rankedOf(st State, key string) map[string]any {
	m := Map(st[key])
	if m == nil {
		return map[string]any{}
	}
	return m
}

// QuestMetrics ports fx._quest_metrics.
func QuestMetrics(st State) map[string]int {
	rk := rankedOf(st, "ranked")
	mgr := rankedOf(st, "ranked_mgr")
	car := Map(st["career"])
	rGames := Int(rk["wins"]) + Int(rk["draws"]) + Int(rk["losses"])
	mGames := Int(mgr["wins"]) + Int(mgr["draws"]) + Int(mgr["losses"])
	upgrades := 0
	for _, v := range Map(car["upgrades"]) {
		upgrades += Int(v)
	}
	return map[string]int{
		"match":   rGames + mGames,
		"win":     Int(rk["wins"]) + Int(mgr["wins"]),
		"goal":    Int(car["total_goals"]),
		"rank":    rGames,
		"rankwin": Int(rk["wins"]),
		"upgrade": upgrades,
	}
}

// QuestState ports fx._quest_state: roll the week over if needed and
// credit GP for quests finished since the last call. Mutates st.
func QuestState(dir string, st State) (map[string]any, []WeekQuest, []int) {
	week := int64(WeekEndTs())
	q := Map(st["quest"])
	if q == nil {
		q = map[string]any{}
		st["quest"] = q
	}
	metrics := QuestMetrics(st)
	if Int64(q["week"]) != week {
		base := map[string]any{}
		for k, v := range metrics {
			base[k] = v
		}
		q["week"] = week
		q["baseline"] = base
		q["done"] = map[string]any{}
		q["gp"] = 0
	}
	base := Map(q["baseline"])
	quests := WeekQuests(dir, week)
	done := Map(q["done"])
	if done == nil {
		done = map[string]any{}
		q["done"] = done
	}
	progress := []int{}
	for i, wq := range quests {
		cur := metrics[wq.Metric] - Int(base[wq.Metric])
		if cur < 0 {
			cur = 0
		}
		progress = append(progress, cur)
		key := itoa2g(i)
		if cur >= wq.Target {
			if _, seen := done[key]; !seen {
				done[key] = W3C(float64(nowUnix()))
				q["gp"] = Int(q["gp"]) + wq.GP
			}
		}
	}
	return q, quests, progress
}

func itoa2g(n int) string { return fmt.Sprint(n) }

// TeamValue ports fx._calc_team_value.
func (r *Roster) TeamValue(st State, slot int) int {
	team := TeamOf(st, slot)
	if team == nil {
		return 0
	}
	sum := 0.0
	for _, raw := range List(team["players"]) {
		p := Map(raw)
		if p == nil {
			continue
		}
		if g := GradeOf(p); g > 0 {
			sum += r.CardPrice(Int(p["spid"]), g)
		}
	}
	return int(sum)
}

// AchievementPayload ports fx._build_achievement_me_payload. It stamps
// first-completion dates and rolls the quest week over, so callers
// must save the state afterwards (as ep_achievement_me does).
// OwnedBadges ports the fx coach-body badge derivation (fo3_ext.py,
// "Owned badges = the badge of every completed achievement group"):
// for every completed achievement GROUP, the group's badge from the
// static catalogue, plus the coach's current badge, sorted ascending.
// hc /coach/me serves badges [] (kept as CoachMe parity); fx computes
// the owned list in ep_coach_by_id, so the Go popup path calls this.
// AchievementPayload may stamp completion bookkeeping into the
// in-memory state — callers must not save afterwards (coachByID
// doesn't).
func (r *Roster) OwnedBadges(st State) []any {
	t := Achieve(r.gamedataDir)
	badgeOf := map[string]int{}
	for gid := range t.Mixed {
		if e := t.ByID(gid); e != nil && e["badge"] != nil {
			badgeOf[gid] = Int(e["badge"])
		}
	}
	owned := map[int]bool{}
	payload := r.AchievementPayload(st)
	for _, raw := range List(payload["achievement"]) {
		e := Map(raw)
		if e["complete"] == true {
			if b, ok := badgeOf[Str(e["id"])]; ok {
				owned[b] = true
			}
		}
	}
	cb := 1001
	if coach := CoachBySlot(st, DefaultSlot(st)); coach != nil && Int(coach["currentbadge"]) > 0 {
		cb = Int(coach["currentbadge"])
	}
	owned[cb] = true
	ids := make([]int, 0, len(owned))
	for id := range owned {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	out := make([]any, len(ids))
	for i, id := range ids {
		out[i] = id
	}
	return out
}

func (r *Roster) AchievementPayload(st State) map[string]any {
	t := Achieve(r.gamedataDir)
	slot := DefaultSlot(st)
	coach := CoachBySlot(st, slot)
	coachLvl := 1
	if coach != nil {
		coachLvl = Int(coach["level"])
		if coachLvl == 0 {
			coachLvl = Int(CoachLevelinfo(coach)["level"])
		}
		if coachLvl == 0 {
			coachLvl = 1
		}
	}
	rk := rankedOf(st, "ranked")
	wins := Int(rk["wins"])
	totalGames := wins + Int(rk["draws"]) + Int(rk["losses"])
	ach := Map(st["achievements"])
	claimed := StrSet(ach["claimed"])
	gpClaimed := IntSet(ach["quest_gp_claimed"])

	team := TeamOf(st, slot)
	var teamPlayers []any
	if team != nil {
		teamPlayers = List(team["players"])
	}
	playerCount := len(teamPlayers)
	mgr := rankedOf(st, "ranked_mgr")
	mgrGames := Int(mgr["wins"]) + Int(mgr["draws"]) + Int(mgr["losses"])
	bestGrade := 1
	for _, raw := range teamPlayers {
		if p := Map(raw); p != nil {
			if g := GradeOf(p); g > bestGrade {
				bestGrade = g
			}
		}
	}
	cupRec := Map(Map(coachOrEmpty(coach)["record"])["cup"])
	teamValue := r.TeamValue(st, slot)
	car := Map(st["career"])
	metrics := map[string]int{
		"level": coachLvl, "login": 1,
		"ranked_wins": wins, "ranked_games": totalGames,
		"best_elo":    maxInt(Int(rk["highelo"]), Int(rk["elo"])),
		"mgr_games":   mgrGames, "mgr_wins": Int(mgr["wins"]),
		"total_games": totalGames + mgrGames, "total_wins": wins + Int(mgr["wins"]),
		"players_owned": playerCount, "best_grade": bestGrade,
		"ep_balance":    epOf(coach),
		"league_rounds": Int(Map(Map(st["league2"])["league"])["nextround"]),
		"cups_joined":   cupJoined(cupRec, st),
		"cup_wins":      Int(cupRec["totalwins"]),
		"cup_matches":   Int(cupRec["wins"]) + Int(cupRec["draws"]) + Int(cupRec["losses"]),
		"team_value":    teamValue,
		"ranked_goals":        Int(car["ranked_goals"]),
		"ranked_cleansheets":  Int(car["ranked_cleansheets"]),
		"ranked_best_streak":  Int(car["ranked_best_streak"]),
		"mgr_goals":           Int(car["mgr_goals"]),
		"total_goals":         Int(car["total_goals"]),
	}
	ups := Map(car["upgrades"])
	for g := 2; g <= 10; g++ {
		sum := 0
		for k, v := range ups {
			if Int(k) >= g {
				sum += Int(v)
			}
		}
		metrics[fmt.Sprintf("upg_ge_%d", g)] = sum
	}
	type childStat struct {
		done       bool
		cur, total int
	}
	childStatus := map[string]childStat{}
	for _, item := range t.List {
		aid := Str(item["id"])
		if !contains(aid, "_") || aid == "misc" {
			continue
		}
		target := Int(item["target"])
		if target == 0 && item["target"] == nil {
			target = 1
		}
		if aid == "ach_1_1" {
			childStatus[aid] = childStat{true, target, target}
			continue
		}
		metric := Str(item["metric"])
		if metric == "" {
			metric = "level"
		}
		cur := metrics[metric]
		childStatus[aid] = childStat{cur >= target, minInt(cur, target), target}
	}
	doneat := Map(ach["doneat"])
	if doneat == nil {
		doneat = map[string]any{}
		if ach == nil {
			ach = map[string]any{}
			st["achievements"] = ach
		}
		ach["doneat"] = doneat
	}
	nowW3C := W3C(float64(nowUnix()))
	allAchs := []any{}
	// Python iterates the mixed dict in catalogue order; the extracted
	// JSON object order follows the Python dict order (ach_1..ach_33,
	// then misc) -- preserve it via GroupOrder.
	for _, pID := range t.GroupOrder() {
		children := t.Mixed[pID]
		doneCount := 0
		for _, cid := range children {
			cs := childStatus[cid]
			if cs.done {
				doneCount++
			}
			entry := map[string]any{
				"id":       cid,
				"complete": cs.done,
				"progress": map[string]any{"grade": 1, "current": cs.cur, "total": cs.total,
					"todo": cs.total, "detail": map[string]any{"current": cs.cur, "total": cs.total}},
				"rewards": map[string]any{"available": cs.done && !claimed[cid], "grade": 1},
			}
			if cs.done {
				if _, ok := doneat[cid]; !ok {
					doneat[cid] = nowW3C
				}
				entry["completedat"] = doneat[cid]
			}
			allAchs = append(allAchs, entry)
		}
		pDone := doneCount >= len(children)
		// Deliberate divergence from fx: Python serves the misc
		// group's parent entry with id 0 (an int), but the client's
		// CoachAchievementBadgePopup looks the group up by the string
		// "misc" (ArrayUtil.findObject(myAchievements, "id", asset))
		// and dereferences the result's completedat — with id 0 the
		// lookup returns null, makeBadgeList throws #1009 (telemetry
		// 107, 2026-11-13) and the other-coach achievement tab renders
		// empty. Serve the string id like every other group.
		parent := map[string]any{
			"id":       pID,
			"complete": pDone,
			"progress": map[string]any{"grade": 1, "current": doneCount, "total": len(children),
				"todo": len(children), "detail": map[string]any{"current": doneCount, "total": len(children)}},
			"rewards": map[string]any{"available": pDone && !claimed[pID], "grade": 1},
		}
		if pDone {
			if _, ok := doneat[pID]; !ok {
				doneat[pID] = nowW3C
			}
			parent["completedat"] = doneat[pID]
		}
		allAchs = append(allAchs, parent)
	}
	badgeKeys := map[string]bool{}
	for k := range t.Mixed {
		badgeKeys[k] = true
	}
	badgesDone := 0
	for _, raw := range allAchs {
		a := Map(raw)
		idStr := fmt.Sprint(a["id"])
		if (badgeKeys[idStr] || a["id"] == 0 || idStr == "0") && a["complete"] == true {
			badgesDone++
		}
	}

	questsList, questInfos := questDefs(metrics, totalGames, wins, playerCount, coachLvl, claimed, QuestRewardOverrides(r.gamedataDir))
	qstate, wquests, wprogress := QuestState(r.gamedataDir, st)
	weeklyInfos := []any{}
	doneMap := Map(qstate["done"])
	for i, wq := range wquests {
		cur := wprogress[i]
		if cur > wq.Target {
			cur = wq.Target
		}
		entry := map[string]any{"id": i, "progress": map[string]any{"detail": map[string]any{"current": cur}}}
		if v, ok := doneMap[itoa2g(i)]; ok {
			entry["completedat"] = v
		}
		weeklyInfos = append(weeklyInfos, entry)
	}
	week := Int64(qstate["week"])
	return map[string]any{
		"summary":     map[string]any{"done": badgesDone, "total": len(badgeKeys)},
		"achievement": allAchs,
		"achievements": allAchs,
		"quest":       weeklyInfos,
		"quests":      questInfos,
		"questbank": map[string]any{
			"expire":  []any{W3C(float64(week)), W3C(float64(week + 7*86400))},
			"current": questsList, "next": questsList, "list": questsList,
		},
		"questRewards": gpMilestonePayload(r.gamedataDir, gpClaimed),
	}
}

func coachOrEmpty(c map[string]any) map[string]any {
	if c == nil {
		return map[string]any{}
	}
	return c
}

func epOf(coach map[string]any) int {
	if coach == nil {
		return 0
	}
	return Int(coach["ep"])
}

func cupJoined(cupRec map[string]any, st State) int {
	if v := Int(cupRec["totalchallenges"]); v != 0 {
		return v
	}
	return len(Map(st["cups"]))
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// StrSet reads a JSON list as a set of its string forms (Python's
// claimed list mixes strings; membership is by exact value, and the
// payload's quest check also tests the raw forms).
func StrSet(v any) map[string]bool {
	out := map[string]bool{}
	for _, raw := range List(v) {
		switch x := raw.(type) {
		case string:
			out[x] = true
		case float64:
			out[itoa2g(int(x))] = true
		}
	}
	return out
}

func IntSet(v any) map[int]bool {
	out := map[int]bool{}
	for _, raw := range List(v) {
		out[Int(raw)] = true
	}
	return out
}

type questDef struct {
	id     int
	name   string
	desc   string
	cur    int
	target int
	done   bool
	ep     int
	pack   int
	gp     int
	typ    string
}

// questDefs ports the QUEST_DEFS table of _build_achievement_me_payload.
// overrides (admin quest_config.json "rewards") patch ep/pack/gp per
// quest id after the authored table is built.
func questDefs(m map[string]int, totalGames, wins, playerCount, coachLvl int, claimed map[string]bool, overrides map[string]map[string]int) ([]any, []any) {
	oneIf := func(v int) int {
		if v > 0 {
			return 1
		}
		return 1
	}
	_ = oneIf
	defs := []questDef{
		{0, "Điểm Danh Hằng Ngày", "Đăng nhập và quản lý đội bóng hàng ngày.", 1, 1, true, 500000, 0, 10, "daily"},
		{1, "Thử Sức Thi Đấu", "Tham gia 1 trận đấu bất kỳ (PVP, Giả Lập hoặc Đấu Giải).", minInt(1, maxInt(totalGames, 1)), 1, true, 600000, 28001, 15, "daily"},
		{2, "Khát Khao Chiến Thắng", "Giành chiến thắng ít nhất 1 trận đấu.", minInt(1, maxInt(wins, 1)), 1, true, 800000, 28006, 20, "daily"},
		{3, "Đấu Trường Xếp Hạng", "Tham gia tranh tài ở chế độ Đấu Xếp Hạng 1v1.", minInt(1, maxInt(totalGames, 1)), 1, true, 1000000, 0, 20, "daily"},
		{4, "Chiến Thuật Bậc Thầy", "Thiết lập và lưu 6 chiến thuật đội bóng phù hợp.", 1, 1, true, 700000, 0, 15, "daily"},
		{5, "Nâng Cấp Đội Hình", "Nâng cấp và tăng cường sức mạnh cầu thủ trong đội.", 1, 1, true, 900000, 28001, 20, "daily"},
		{6, "Mở Rộng Câu Lạc Bộ", "Sở hữu tối thiểu 11 cầu thủ trong danh sách đội bóng.", minInt(playerCount, 11), 11, playerCount >= 11, 120000, 0, 10, "daily"},
		{7, "HLV Triển Vọng Lv.5", "Đạt cấp độ Huấn Luyện Viên Lv.5 để mở khóa kỹ năng.", minInt(coachLvl, 5), 5, coachLvl >= 5, 150000, 28006, 15, "daily"},
		{8, "HLV Chuyên Nghiệp Lv.10", "Đạt cấp độ Huấn Luyện Viên Lv.10 khẳng định vị thế.", minInt(coachLvl, 10), 10, coachLvl >= 10, 200000, 82214068, 25, "daily"},
		{9, "HLV Đẳng Cấp Lv.20", "Đạt cấp độ Huấn Luyện Viên Lv.20 và thống trị giải đấu.", minInt(coachLvl, 20), 20, coachLvl >= 20, 300000, 82214068, 30, "daily"},
		{10, "Chiến Binh Tuần", "Giành chiến thắng 3 trận đấu ở bất kỳ chế độ nào.", minInt(m["total_wins"], 3), 3, m["total_wins"] >= 3, 300000, 28006, 50, "weekly"},
		{11, "Săn Bàn Tuần", "Ghi 10 bàn thắng ở mọi chế độ thi đấu.", minInt(m["total_goals"], 10), 10, m["total_goals"] >= 10, 300000, 28001, 50, "weekly"},
		{12, "Đua Xếp Hạng Tuần", "Thi đấu 5 trận Xếp Hạng 1v1.", minInt(totalGames, 5), 5, totalGames >= 5, 500000, 45013, 80, "weekly"},
		{13, "Chinh Phục Cup Tuần", "Tham gia 1 giải đấu Cup.", minInt(m["cups_joined"], 1), 1, m["cups_joined"] >= 1, 500000, 28001, 80, "weekly"},
		{14, "Đại Gia Tuần", "Sở hữu số dư EP từ 10.000.000 trở lên.", minInt(m["ep_balance"], 10000000), 10000000, m["ep_balance"] >= 10000000, 1000000, 82214068, 100, "weekly"},
	}
	packNames := map[int]string{28001: "Thẻ Chỉ Định Top 100", 28006: "Thẻ Chỉ Định Top 30",
		82214068: "Gói Cầu Thủ Đặc Biệt", 45013: "Gói Top 30 Mùa Hiện Tại"}
	_ = packNames
	for i := range defs {
		if ov, ok := overrides[itoa2g(defs[i].id)]; ok {
			if v, has := ov["ep"]; has {
				defs[i].ep = v
			}
			if v, has := ov["pack"]; has {
				defs[i].pack = v
			}
			if v, has := ov["gp"]; has {
				defs[i].gp = v
			}
		}
	}
	questsList := []any{}
	questInfos := []any{}
	for _, q := range defs {
		isClaimed := claimed[fmt.Sprintf("quest_%d", q.id)] || claimed[itoa2g(q.id)]
		items := []any{}
		if q.pack != 0 {
			items = append(items, map[string]any{"itemid": q.pack, "count": 1})
		}
		questsList = append(questsList, map[string]any{
			"id": q.id, "name": q.name, "desc": q.desc, "type": q.typ, "platform": "pc",
			"gp": q.gp, "point": q.gp, "current": q.cur, "todo": q.target,
			"complete": q.done, "completedat": "2026-11-01T00:00:00.000Z",
			"state": map[bool]string{true: "complete", false: "ongoing"}[q.done],
			"reward": map[string]any{"count": q.ep}, "items": items,
			"rewardedmp": 0, "received": isClaimed,
		})
		questInfos = append(questInfos, map[string]any{
			"id": q.id, "type": q.typ, "gp": q.gp, "complete": q.done,
			"completedat": "2026-11-01T00:00:00.000Z",
			"progress": map[string]any{"grade": 1, "current": q.cur, "total": q.target,
				"todo": q.target, "detail": map[string]any{"current": q.cur, "total": q.target}},
			"rewards": map[string]any{"available": q.done && !isClaimed, "grade": 1},
		})
	}
	return questsList, questInfos
}
