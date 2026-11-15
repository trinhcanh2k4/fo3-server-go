package game

// Staff recruitment/assignment, ported from staff_service.py:
// packs (five-star bundles only), recruit, appoint/unappoint,
// enhance (EXP by consuming idle staff) and study (boost change /
// improve at 6 locations). Staff live in team["staffs"], the 7 role
// slots in team["staffroles"].

import (
	"os"
	"path/filepath"
	mrand "math/rand"
	"sort"
	"strconv"
	"strings"
)

// StaffInfo is one staff_catalog.json entry.
type StaffInfo struct {
	Name        string
	Position    string
	Material    bool
	BoostGroups [][]string
}

var staffTypeLabels = [][2]string{
	{"FRONT", "Technical"}, {"PREMIUM", "Head Coach"}, {"MEDICAL", "Medical"},
	{"FW", "FW"}, {"MF", "MF"}, {"DF", "DF"}, {"GK", "GK"},
}

// StatKeys maps catalog boost labels to client stat keys.
var StatKeys = map[string]string{
	"Acceleration": "acceleration", "Aggresion": "aggression", "Agility": "agility",
	"Balance": "balance", "Ball Control": "ballcontrol", "Cross": "crossing",
	"Dribble": "dribbling", "Finishing": "finishing", "Free Kick": "freekickaccuracy",
	"GK Diving": "gkdiving", "GK Goal Kick": "gkkicking", "GK Handling": "gkhandling",
	"GK Positioning": "gkpositioning", "GK Reaction": "gkreflexes",
	"Heading": "headingaccuracy", "Interception": "interceptions", "Jump": "jumping",
	"Long Pass": "longpassing", "Long Shot": "longshots", "Marking": "marking",
	"Overall Rating": "ovr", "Penalty Kick": "penalties", "Positioning": "positioning",
	"Reaction": "reactions", "Shoot Curve": "curve", "Shoot Power": "shotpower",
	"Short Pass": "shortpassing", "Sliding Tackle": "slidingtackle",
	"Sprint Speed": "sprintspeed", "Stamina": "stamina", "Strength": "strength",
	"Tackle": "standingtackle", "Vision": "vision", "Volley": "volleys",
}

// StaffType is one TYPES row (position, label, portrait pool).
type StaffType struct {
	Type      string
	Label     string
	Portraits []int
}

// StaffTypes builds the TYPES table from the catalog.
func (r *Roster) StaffTypes() []StaffType {
	out := []StaffType{}
	for _, tl := range staffTypeLabels {
		t := StaffType{Type: tl[0], Label: tl[1]}
		for _, pid := range r.staffOrder {
			info := r.staffCatalog[pid]
			if info != nil && info.Position == tl[0] && !info.Material {
				t.Portraits = append(t.Portraits, pid)
			}
		}
		out = append(out, t)
	}
	return out
}

// SlotType returns SLOT_TYPES[idx] for idx 1..7.
func (r *Roster) SlotType(idx int) string {
	types := r.StaffTypes()
	if idx >= 1 && idx <= len(types) {
		return types[idx-1].Type
	}
	return ""
}

// StaffPacks ports the filtered PACKS (grades == (10,) only).
func StaffPacks() []map[string]any {
	base := []map[string]any{
		{"recruitid": 1, "icon": "broall", "recruittype": "normal", "staffpack_name": "Offline Staff - Basic",
			"staffpack_desc": "Recruit one staff member (1-2 stars). Free.", "ep": 0, "item": 0, "grades": []int{2, 4}},
		{"recruitid": 2, "icon": "silall", "recruittype": "normal", "staffpack_name": "Offline Staff - Advanced",
			"staffpack_desc": "Recruit one staff member (3-4 stars). 50,000 EP.", "ep": 50000, "item": 0, "grades": []int{6, 8}},
		{"recruitid": 3, "icon": "goldall", "recruittype": "normal", "staffpack_name": "All Staff - Elite",
			"staffpack_desc": "Recruit one staff member (5 stars). 150,000 EP.", "ep": 150000, "item": 0, "grades": []int{10}},
	}
	all := append([]map[string]any{}, base...)
	tiers := []string{"Basic", "Advanced", "Elite"}
	for i, tl := range staffTypeLabels {
		for tier, b := range base {
			icon := Str(b["icon"])
			if tier == 0 && (tl[0] == "FW" || tl[0] == "MF" || tl[0] == "DF" || tl[0] == "GK") {
				icon = "bro" + lowerASCII(tl[0])
			}
			p := map[string]any{}
			for k, v := range b {
				p[k] = v
			}
			p["recruitid"] = 100 + (i+1)*10 + (tier + 1)
			p["position"] = tl[0]
			p["icon"] = icon
			p["staffpack_name"] = tl[1] + " - " + tiers[tier]
			all = append(all, p)
		}
	}
	out := []map[string]any{}
	for _, p := range all {
		grades := p["grades"].([]int)
		if len(grades) == 1 && grades[0] == 10 {
			p["icon"] = "staffpack/" + Str(p["icon"])
			out = append(out, p)
		}
	}
	return out
}

func lowerASCII(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		}
	}
	return string(b)
}

// RecruitEffects ports staff_service.recruit_effects (random pick).
func RecruitEffects(info *StaffInfo, grade int) []any {
	out := []any{}
	n := len(info.BoostGroups)
	if n > 3 {
		n = 3
	}
	if n > grade {
		n = grade
	}
	if n < 0 {
		n = 0
	}
	for gi := 0; gi < n; gi++ {
		choices := []string{}
		for _, label := range info.BoostGroups[gi] {
			if k, ok := StatKeys[label]; ok {
				choices = append(choices, k)
			}
		}
		if len(choices) == 0 {
			continue
		}
		amount := (grade + 1) / 2
		if amount < 1 {
			amount = 1
		}
		out = append(out, map[string]any{"effectidx": gi + 1,
			"effect": choices[mrand.Intn(len(choices))], "amount": amount,
			"grade": 1, "open": true})
	}
	return out
}

// NormalizeStaffTeam ports staff_service.normalize_team.
func (r *Roster) NormalizeStaffTeam(team map[string]any) {
	staffs := []map[string]any{}
	for _, sv := range List(team["staffs"]) {
		if s := Map(sv); s != nil {
			staffs = append(staffs, s)
		}
	}
	validTypes := map[string]bool{}
	for _, tl := range staffTypeLabels {
		validTypes[tl[0]] = true
	}
	for _, s := range staffs {
		stfid := fmt_Sprint(s["stfid"])
		if info := r.staffCatalog[atoiSafe(stfid)]; info != nil {
			s["commonname"] = info.Name
			s["position"] = info.Position
			s["material"] = info.Material
		} else if !validTypes[Str(s["position"])] {
			s["position"] = "FRONT"
		}
		if stfid == "9502" || stfid == "9505" {
			s["commonname"] = "Staff #" + stfid + " (unverified)"
		}
		// Legacy staff granted under the old 100-match cap clamp
		// down to the current 40-match cap on first touch.
		if _, present := s["life"]; present && Int(s["life"]) > RecontractStaffMaxLife {
			s["life"] = RecontractStaffMaxLife
		}
	}
	byID := map[string]map[string]any{}
	for _, s := range staffs {
		byID[Str(s["_id"])] = s
	}
	roles := []any{}
	for i := 1; i <= 7; i++ {
		roles = append(roles, map[string]any{"roleidx": 1, "slotidx": i, "open": true, "staffid": nil})
	}
	for _, rv := range List(team["staffroles"]) {
		prev := Map(rv)
		s := byID[Str(prev["staffid"])]
		if s == nil {
			continue
		}
		for _, roleV := range roles {
			role := Map(roleV)
			if r.SlotType(Int(role["slotidx"])) == Str(s["position"]) {
				if role["staffid"] == nil {
					role["staffid"] = s["_id"]
				}
				break
			}
		}
	}
	active := map[string]bool{}
	for _, roleV := range roles {
		if id := Str(Map(roleV)["staffid"]); id != "" {
			active[id] = true
		}
	}
	staffList := []any{}
	for _, s := range staffs {
		if Str(s["status"]) != "studying" {
			if active[Str(s["_id"])] {
				s["status"] = "appointed"
			} else {
				s["status"] = "idle"
			}
		}
		if Map(s["study"]) == nil {
			s["study"] = map[string]any{"history": map[string]any{}}
		}
		staffList = append(staffList, s)
	}
	team["staffs"] = staffList
	team["staffroles"] = roles
	team["staffroleidx"] = 1
	if Int(team["maxstaff"]) < 30 {
		team["maxstaff"] = 30
	}
}

// StaffPortraitExists mirrors the Python os.path.isfile check.
func (r *Roster) StaffPortraitExists(pid int) bool {
	if r.assetsDir == "" {
		return true
	}
	_, err := os.Stat(filepath.Join(r.assetsDir, "externalAssets", "staffs", strconv.Itoa(pid)+".png"))
	return err == nil
}

// StudyRule is one STUDY location.
type StudyRule struct {
	Location    string
	Fee         int
	EffectIdx   int
	BoostType   string
	EffectString string
}

// StudyRules ports the STUDY table (locations 1..6).
func StudyRules() map[string]StudyRule {
	out := map[string]StudyRule{}
	positions := []string{}
	for _, tl := range staffTypeLabels {
		positions = append(positions, tl[0])
	}
	_ = positions
	for i := 1; i <= 6; i++ {
		fee := 2000
		es := "Improve one bonus by +1 (maximum +5)."
		bt := "enhance"
		if i <= 3 {
			fee = 1000
			es = "Change one bonus within its original option group."
			bt = "change"
		}
		out[strconv.Itoa(i)] = StudyRule{
			Location: "LOCATION_NAME_0" + strconv.Itoa(i), Fee: fee,
			EffectIdx: (i-1)%3 + 1, BoostType: bt, EffectString: es}
	}
	return out
}

const StudyMaxFee = 100000

// StudyBoost ports staff_service.study_boost.
func (r *Roster) StudyBoost(staff map[string]any, rule StudyRule) map[string]any {
	info := r.staffCatalog[atoiSafe(fmt_Sprint(staff["stfid"]))]
	types := []string{rule.BoostType}
	if rule.BoostType == "enhance" {
		types = append(types, "change")
	} else {
		types = append(types, "enhance")
	}
	findEffect := func(idx int) map[string]any {
		for _, ev := range List(staff["effect"]) {
			if e := Map(ev); e != nil && Int(e["effectidx"]) == idx && e["open"] == true {
				return e
			}
		}
		return nil
	}
	for _, bt := range types {
		if bt == "enhance" {
			indices := []int{rule.EffectIdx}
			for i := 1; i <= 3; i++ {
				if i != rule.EffectIdx {
					indices = append(indices, i)
				}
			}
			for _, idx := range indices {
				if cur := findEffect(idx); cur != nil && Int(cur["amount"]) < 5 {
					return map[string]any{"effectidx": idx, "boosttype": "enhance",
						"effect": cur["effect"], "inc": 1}
				}
			}
			continue
		}
		if info == nil {
			continue
		}
		indices := []int{rule.EffectIdx}
		for i := 1; i <= 3; i++ {
			if i != rule.EffectIdx {
				indices = append(indices, i)
			}
		}
		for _, idx := range indices {
			cur := findEffect(idx)
			if cur == nil || idx > len(info.BoostGroups) {
				continue
			}
			choices := []string{}
			for _, label := range info.BoostGroups[idx-1] {
				if k, ok := StatKeys[label]; ok && k != Str(cur["effect"]) {
					choices = append(choices, k)
				}
			}
			if len(choices) > 0 {
				return map[string]any{"effectidx": idx, "boosttype": "change",
					"effect": choices[mrand.Intn(len(choices))], "inc": 0}
			}
		}
	}
	return nil
}

// SortedStaffIDs is a tiny helper for deterministic tests.
func SortedStaffIDs(team map[string]any) []string {
	ids := []string{}
	for _, sv := range List(team["staffs"]) {
		ids = append(ids, Str(Map(sv)["_id"]))
	}
	sort.Strings(ids)
	return ids
}

// StaffInfoOf returns the catalog entry for a portrait id.
func (r *Roster) StaffInfoOf(stfid int) *StaffInfo { return r.staffCatalog[stfid] }

// StaffSearch finds trainable staff (HLV huấn luyện) by name for
// the admin grant section; material entries are excluded (they
// are enhance fodder, not grantable staff).
func (r *Roster) StaffSearch(q string, limit int) []map[string]any {
	q = strings.ToLower(strings.TrimSpace(q))
	labels := map[string]string{}
	for _, tl := range staffTypeLabels {
		labels[tl[0]] = tl[1]
	}
	out := []map[string]any{}
	for _, pid := range r.staffOrder {
		info := r.staffCatalog[pid]
		if info == nil || info.Material {
			continue
		}
		if q == "" || strings.Contains(strings.ToLower(info.Name), q) {
			out = append(out, map[string]any{
				"stfid": pid, "name": info.Name,
				"position": info.Position, "label": labels[info.Position]})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i]["name"].(string) < out[j]["name"].(string)
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}
