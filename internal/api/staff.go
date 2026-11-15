package api

// Staff endpoints, ported from staff_service.py.

import (
	mrand "math/rand"
	"strconv"

	"github.com/gin-gonic/gin"

	"fo3server/internal/game"
)

func staffFail(c *gin.Context, reason string) {
	writeJSON(c, map[string]any{"result": false, "reason": reason})
}

// staffTeam ports team_state(): default team + normalize (mutates).
func (s *Server) staffTeam(st game.State) (int, map[string]any) {
	slot := game.DefaultSlot(st)
	team := game.TeamOf(st, slot)
	if team == nil {
		team = map[string]any{"players": []any{}, "squad": map[string]any{"positions": []any{}, "kickers": []any{}}}
	}
	s.d.Roster.NormalizeStaffTeam(team)
	return slot, team
}

func (s *Server) staffReply(c *gin.Context, st game.State, user *game.User, slot int, extra map[string]any) {
	out := map[string]any{"result": true}
	for k, v := range extra {
		out[k] = v
	}
	out["resources"] = s.d.Roster.CoachMeResources(st, slot, user)
	writeJSON(c, out)
}

func findStaff(team map[string]any, staffid string) map[string]any {
	for _, sv := range game.List(team["staffs"]) {
		if s := game.Map(sv); s != nil && game.Str(s["_id"]) == staffid {
			return s
		}
	}
	return nil
}

func (s *Server) staffRecruitList(c *gin.Context) {
	out := []any{}
	for _, p := range game.StaffPacks() {
		row := map[string]any{}
		for k, v := range p {
			if k != "grades" {
				row[k] = v
			}
		}
		out = append(out, row)
	}
	writeJSON(c, out)
}

func (s *Server) staffRecruit(c *gin.Context) {
	st, user, ok := s.loadState(c)
	if !ok {
		return
	}
	recruitid, _ := strconv.Atoi(c.Param("recruitid"))
	var pack map[string]any
	for _, p := range game.StaffPacks() {
		if game.Int(p["recruitid"]) == recruitid {
			pack = p
			break
		}
	}
	if pack == nil {
		staffFail(c, "INVALID_STAFF_PACK")
		return
	}
	var req map[string]any
	_ = c.ShouldBindJSON(&req)
	feetype := game.Str(req["feetype"])
	if feetype == "" {
		feetype = "ep"
	}
	if feetype != "ep" || req["eventtype"] != nil {
		staffFail(c, "INVALID_STAFF_PAYMENT")
		return
	}
	slot, team := s.staffTeam(st)
	if len(game.List(team["staffs"])) >= game.Int(team["maxstaff"]) {
		staffFail(c, "STAFF_LIMIT_EXCEEDED")
		return
	}
	coach := game.CoachBySlot(st, slot)
	ep := int64(game.StartEP)
	if coach != nil && coach["ep"] != nil {
		ep = game.Int64(coach["ep"])
	}
	if ep < int64(game.Int(pack["ep"])) {
		staffFail(c, "STAFF_EP_NOTENOUGH")
		return
	}
	types := s.d.Roster.StaffTypes()
	var category *game.StaffType
	for i := range types {
		if types[i].Type == game.Str(pack["position"]) {
			category = &types[i]
			break
		}
	}
	if category == nil {
		category = &types[mrand.Intn(len(types))]
	}
	portraits := []int{}
	for _, pid := range category.Portraits {
		if s.d.Roster.StaffPortraitExists(pid) {
			portraits = append(portraits, pid)
		}
	}
	if len(portraits) == 0 {
		staffFail(c, "STAFF_PORTRAITS_MISSING")
		return
	}
	grades := pack["grades"].([]int)
	grade := grades[mrand.Intn(len(grades))]
	stfid := portraits[mrand.Intn(len(portraits))]
	info := s.d.Roster.StaffInfoOf(stfid)
	staff := map[string]any{
		"_id": game.NewIID(), "stfid": stfid, "commonname": info.Name,
		"position": category.Type, "grade": grade, "exp": 0, "gradeupto": 1000,
		"life": game.RecontractStaffMaxLife, "status": "idle", "effect": game.RecruitEffects(info, grade),
		"study": nil, "material": false, "contractfee": 0, "new": true,
	}
	team["staffs"] = append(game.List(team["staffs"]), staff)
	if coach != nil {
		coach["ep"] = ep - int64(game.Int(pack["ep"]))
	}
	_ = game.SaveState(c.Request.Context(), s.d.Store, user.ID, st)
	s.staffReply(c, st, user, slot, map[string]any{"staff": staff})
}

func (s *Server) staffAppoint(c *gin.Context) {
	st, user, ok := s.loadState(c)
	if !ok {
		return
	}
	staffid := c.Param("staffid")
	var req map[string]any
	_ = c.ShouldBindJSON(&req)
	idxF, idxIsNum := req["slot"].(float64)
	roleF, roleIsNum := req["staffrole"].(float64)
	if !roleIsNum {
		roleF = 1
		roleIsNum = true
	}
	if !idxIsNum || idxF != float64(int(idxF)) || int(idxF) < 1 || int(idxF) > 7 ||
		!roleIsNum || roleF != 1 {
		staffFail(c, "INVALID_STAFF_SLOT")
		return
	}
	idx := int(idxF)
	slot, team := s.staffTeam(st)
	staff := findStaff(team, staffid)
	if staff == nil || game.Int(staff["life"]) <= 0 || game.Str(staff["status"]) == "studying" {
		staffFail(c, "INVALID_STAFF")
		return
	}
	var target map[string]any
	for _, rv := range game.List(team["staffroles"]) {
		if r := game.Map(rv); r != nil && game.Int(r["roleidx"]) == 1 && game.Int(r["slotidx"]) == idx {
			target = r
			break
		}
	}
	if target == nil || target["open"] != true {
		staffFail(c, "INVALID_STAFF_SLOT")
		return
	}
	if game.Str(staff["position"]) != s.d.Roster.SlotType(idx) {
		staffFail(c, "STAFF_POSITION_MISMATCH")
		return
	}
	previousID := game.Str(target["staffid"])
	for _, rv := range game.List(team["staffroles"]) {
		if r := game.Map(rv); r != nil && game.Str(r["staffid"]) == staffid {
			r["staffid"] = nil
		}
	}
	target["staffid"] = staffid
	active := map[string]bool{}
	for _, rv := range game.List(team["staffroles"]) {
		if r := game.Map(rv); r != nil && r["open"] == true && game.Str(r["staffid"]) != "" {
			active[game.Str(r["staffid"])] = true
		}
	}
	for _, sv := range game.List(team["staffs"]) {
		if p := game.Map(sv); p != nil {
			if active[game.Str(p["_id"])] {
				p["status"] = "appointed"
			} else {
				p["status"] = "idle"
			}
		}
	}
	_ = game.SaveState(c.Request.Context(), s.d.Store, user.ID, st)
	var previous map[string]any
	if previousID != "" {
		previous = findStaff(team, previousID)
	}
	s.staffReply(c, st, user, slot, map[string]any{
		"appointed": map[string]any{"appointedstaff": staff, "prevstaff": previous}})
}

func (s *Server) staffUnappoint(c *gin.Context) {
	st, user, ok := s.loadState(c)
	if !ok {
		return
	}
	staffid := c.Param("staffid")
	slot, team := s.staffTeam(st)
	staff := findStaff(team, staffid)
	if staff == nil {
		staffFail(c, "INVALID_STAFF")
		return
	}
	for _, rv := range game.List(team["staffroles"]) {
		if r := game.Map(rv); r != nil && game.Str(r["staffid"]) == staffid {
			r["staffid"] = nil
		}
	}
	staff["status"] = "idle"
	_ = game.SaveState(c.Request.Context(), s.d.Store, user.ID, st)
	s.staffReply(c, st, user, slot, nil)
}

func (s *Server) staffEnhance(c *gin.Context) {
	st, user, ok := s.loadState(c)
	if !ok {
		return
	}
	staffid := c.Param("staffid")
	var req map[string]any
	if err := c.ShouldBindJSON(&req); err != nil {
		staffFail(c, "INVALID_STAFF_MATERIAL")
		return
	}
	materialIDs := []string{}
	seen := map[string]bool{}
	valid := true
	for _, v := range game.List(req["materials"]) {
		id, isStr := v.(string)
		if !isStr || seen[id] || id == staffid {
			valid = false
			break
		}
		seen[id] = true
		materialIDs = append(materialIDs, id)
	}
	if !valid || len(materialIDs) == 0 {
		staffFail(c, "INVALID_STAFF_MATERIAL")
		return
	}
	slot, team := s.staffTeam(st)
	target := findStaff(team, staffid)
	if target == nil || game.Str(target["status"]) == "studying" || target["material"] == true {
		staffFail(c, "INVALID_STAFF")
		return
	}
	materials := []map[string]any{}
	for _, id := range materialIDs {
		m := findStaff(team, id)
		if m == nil || game.Str(m["status"]) != "idle" || m["material"] == true {
			staffFail(c, "INVALID_STAFF_MATERIAL")
			return
		}
		materials = append(materials, m)
	}
	expGain := 0
	for _, m := range materials {
		grade := game.Int(m["grade"])
		if grade < 0 {
			grade = 0
		}
		mult := 1
		if game.Str(m["position"]) == game.Str(target["position"]) {
			if m["material"] == true {
				mult = 3
			}
		} else if m["material"] == true {
			mult = 2
		}
		expGain += grade * 100 * mult
	}
	target["exp"] = game.Int(target["exp"]) + expGain
	if target["exp"] == nil {
		target["exp"] = expGain
	}
	if game.Int(target["gradeupto"]) < 1 {
		target["gradeupto"] = 1000
	}
	for game.Int(target["grade"]) < 10 && game.Int(target["exp"]) >= game.Int(target["gradeupto"]) {
		target["exp"] = game.Int(target["exp"]) - game.Int(target["gradeupto"])
		target["grade"] = game.Int(target["grade"]) + 1
	}
	consumed := seen
	for _, rv := range game.List(team["staffroles"]) {
		if r := game.Map(rv); r != nil && consumed[game.Str(r["staffid"])] {
			r["staffid"] = nil
		}
	}
	remaining := []any{}
	for _, sv := range game.List(team["staffs"]) {
		if p := game.Map(sv); p != nil && !consumed[game.Str(p["_id"])] {
			remaining = append(remaining, p)
		}
	}
	team["staffs"] = remaining
	_ = game.SaveState(c.Request.Context(), s.d.Store, user.ID, st)
	mats := []any{}
	for _, id := range materialIDs {
		mats = append(mats, id)
	}
	s.staffReply(c, st, user, slot, map[string]any{
		"predictStaff": target, "jackpot": nil, "consumedStaffIds": mats, "exp": expGain})
}

func (s *Server) staffStudyStart(c *gin.Context) {
	st, user, ok := s.loadState(c)
	if !ok {
		return
	}
	lid := c.Param("lid")
	rule, exists := game.StudyRules()[lid]
	if !exists {
		staffFail(c, "ITEM_USE_ERROR")
		return
	}
	var req map[string]any
	_ = c.ShouldBindJSON(&req)
	ids := game.List(req["staffs"])
	if len(ids) != 1 {
		staffFail(c, "INVALID_STAFF")
		return
	}
	staffid, isStr := ids[0].(string)
	if !isStr {
		staffFail(c, "INVALID_STAFF")
		return
	}
	slot, team := s.staffTeam(st)
	for _, sv := range game.List(team["staffs"]) {
		if p := game.Map(sv); p != nil && game.Str(p["status"]) == "studying" &&
			game.Str(game.Map(p["study"])["lid"]) == lid {
			staffFail(c, "ITEM_USE_ERROR")
			return
		}
	}
	staff := findStaff(team, staffid)
	if staff == nil || game.Str(staff["status"]) != "idle" || staff["material"] == true ||
		game.Int(staff["life"]) <= 0 {
		staffFail(c, "INVALID_STAFF")
		return
	}
	if game.Int(staff["grade"]) < 1 || !staffPositionAllowed(game.Str(staff["position"])) {
		staffFail(c, "INVALID_STAFF")
		return
	}
	boost := s.d.Roster.StudyBoost(staff, rule)
	if boost == nil {
		staffFail(c, "ITEM_USE_ERROR")
		return
	}
	history := game.Map(game.Map(staff["study"])["history"])
	if history == nil {
		history = map[string]any{}
	}
	count := len(game.List(history[lid]))
	fee := rule.Fee << min(count, 16)
	if fee > game.StudyMaxFee {
		fee = game.StudyMaxFee
	}
	coach := game.CoachBySlot(st, slot)
	ep := int64(game.StartEP)
	if coach != nil && coach["ep"] != nil {
		ep = game.Int64(coach["ep"])
	}
	if ep < int64(fee) {
		staffFail(c, "STAFF_EP_NOTENOUGH")
		return
	}
	if coach != nil {
		coach["ep"] = ep - int64(fee)
	}
	staff["status"] = "studying"
	staff["study"] = map[string]any{"lid": lid, "mcdate": game.Int(team["staffsmc"]),
		"history": history, "boost": boost}
	_ = game.SaveState(c.Request.Context(), s.d.Store, user.ID, st)
	s.staffReply(c, st, user, slot, map[string]any{
		"staffs": map[string]any{staffid: staff}})
}

func staffPositionAllowed(pos string) bool {
	switch pos {
	case "FRONT", "PREMIUM", "MEDICAL", "FW", "MF", "DF", "GK":
		return true
	}
	return false
}

func (s *Server) staffStudyComplete(c *gin.Context) {
	st, user, ok := s.loadState(c)
	if !ok {
		return
	}
	lid := c.Param("lid")
	rule, exists := game.StudyRules()[lid]
	if !exists {
		staffFail(c, "ITEM_USE_ERROR")
		return
	}
	slot, team := s.staffTeam(st)
	var staff map[string]any
	for _, sv := range game.List(team["staffs"]) {
		if p := game.Map(sv); p != nil && game.Map(p["study"]) != nil &&
			game.Str(game.Map(p["study"])["lid"]) == lid {
			staff = p
			break
		}
	}
	if staff == nil {
		staffFail(c, "INVALID_STAFF")
		return
	}
	study := game.Map(staff["study"])
	boost := game.Map(study["boost"])
	if boost == nil {
		boost = s.d.Roster.StudyBoost(staff, rule)
		if boost == nil {
			staffFail(c, "INVALID_STAFF")
			return
		}
		study["boost"] = boost
	}
	var effect map[string]any
	for _, ev := range game.List(staff["effect"]) {
		if e := game.Map(ev); e != nil && game.Int(e["effectidx"]) == game.Int(boost["effectidx"]) && e["open"] == true {
			effect = e
			break
		}
	}
	if effect == nil {
		staffFail(c, "INVALID_STAFF")
		return
	}
	if game.Str(boost["boosttype"]) == "change" {
		effect["effect"] = boost["effect"]
	} else {
		inc := game.Int(boost["inc"])
		if boost["inc"] == nil {
			inc = 1
		}
		effect["amount"] = min(5, game.Int(effect["amount"])+inc)
	}
	history := game.Map(study["history"])
	if history == nil {
		history = map[string]any{}
		study["history"] = history
	}
	history[lid] = append(game.List(history[lid]), boost)
	staff["status"] = "idle"
	study["lid"] = nil
	_ = game.SaveState(c.Request.Context(), s.d.Store, user.ID, st)
	s.staffReply(c, st, user, slot, map[string]any{
		"staffs": map[string]any{game.Str(staff["_id"]): staff}})
}
