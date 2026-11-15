package api

// Other-coach profile cluster, ported from fx: the ranking rows
// (/rank/teamvalue/*) and the CoachUserPopupScreen endpoints
// (/coach/<id>, /coach/<id>/summary, /achievement/<id>,
// /ladder/<id>/info). These did not exist at all in the first Python
// build -- the client answered "HLV ko ton tai" / crashed the popup.

import (
	"sort"
	"strings"

	"github.com/gin-gonic/gin"

	"fo3server/internal/game"
)

// rankEntries is fx._coach_rank_entries: one row per account (its
// first coach), sorted by team value.
func (s *Server) rankEntries(c *gin.Context, myCID string) []map[string]any {
	ctx := c.Request.Context()
	users, err := game.AllUsers(ctx, s.d.Store)
	if err != nil {
		return nil
	}
	entries := []map[string]any{}
	for _, u := range users {
		st, err := game.LoadState(ctx, s.d.Store, u.ID)
		if err != nil || st == nil {
			continue
		}
		coaches := game.Coaches(st)
		if len(coaches) == 0 {
			continue
		}
		coach := coaches[0]
		cid := game.Str(coach["_id"])
		if cid == "" {
			cid = game.CoachIDForUser(u.ID, game.Int(coach["slot"]))
		}
		slot := game.Int(coach["slot"])
		level := 1
		if li := game.Map(coach["levelinfo"]); li != nil && game.Int(li["level"]) > 0 {
			level = game.Int(li["level"])
		}
		me := s.d.Roster.CoachMe(st, slot, u)
		ovr := map[string]any{"FW": 0, "MF": 0, "DF": 0}
		if sum := game.Map(me["summary"]); sum != nil {
			if o := game.Map(sum["ovr"]); o != nil {
				for _, k := range []string{"FW", "MF", "DF"} {
					ovr[k] = game.Int(o[k])
				}
			}
		}
		name := game.Str(coach["name"])
		if name == "" {
			name = u.Username
		}
		val := s.d.Roster.TeamValue(st, slot)
		entries = append(entries, map[string]any{
			"_id": cid, "id": cid, "coachid": cid,
			"coachname": name, "coachName": name, "name": name,
			"teamid":    game.Int(coach["teamid"]),
			"level":     level,
			"levelinfo": map[string]any{"level": level},
			"ovr":       ovr,
			"teamvalue": map[string]any{"val": val, "rank": 0, "fluc": 0, "updown": 0},
			"hiderank":  false, "isMe": cid == myCID,
			"_val": val,
		})
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i]["_val"] != entries[j]["_val"] {
			return entries[i]["_val"].(int) > entries[j]["_val"].(int)
		}
		return entries[i]["name"].(string) < entries[j]["name"].(string)
	})
	for i, e := range entries {
		e["rank"] = i + 1
		e["ranking"] = i + 1
		e["teamvalue"].(map[string]any)["rank"] = i + 1
		delete(e, "_val")
	}
	return entries
}

func (s *Server) rankTeamvalueFriend(c *gin.Context) {
	st, _, ok := s.loadState(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	myCID := s.myCoachID(st)
	keep := map[string]bool{myCID: true}
	friends, _ := game.AcceptedFriendCoachIDs(ctx, s.d.Store, myCID)
	for id := range friends {
		keep[id] = true
	}
	rows := []map[string]any{}
	for _, e := range s.rankEntries(c, myCID) {
		if keep[game.Str(e["coachid"])] {
			rows = append(rows, e)
		}
	}
	for i, e := range rows {
		e["rank"] = i + 1
		e["ranking"] = i + 1
		e["teamvalue"].(map[string]any)["rank"] = i + 1
	}
	writeJSON(c, rows)
}

func (s *Server) rankTeamvalueClub(c *gin.Context) {
	st, _, ok := s.loadState(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	myCID := s.myCoachID(st)
	keep := map[string]bool{myCID: true}
	if club, err := game.ClubOfCoach(ctx, s.d.Store, myCID); err == nil && club != nil {
		if ids, err := game.ClubMemberIDs(ctx, s.d.Store, club.ID); err == nil {
			for id := range ids {
				keep[id] = true
			}
		}
	}
	rows := []map[string]any{}
	for _, e := range s.rankEntries(c, myCID) {
		if keep[game.Str(e["coachid"])] {
			rows = append(rows, e)
		}
	}
	for i, e := range rows {
		e["rank"] = i + 1
		e["ranking"] = i + 1
		e["teamvalue"].(map[string]any)["rank"] = i + 1
	}
	writeJSON(c, rows)
}

func (s *Server) teamvalueHistory(c *gin.Context) {
	st, _, ok := s.loadState(c)
	if !ok {
		return
	}
	val := s.d.Roster.TeamValue(st, game.DefaultSlot(st))
	values := make([]any, 30)
	for i := range values {
		values[i] = val
	}
	writeJSON(c, map[string]any{"values": values})
}

func (s *Server) coachHideRank(c *gin.Context) {
	writeJSON(c, map[string]any{"result": true})
}

// targetCoach resolves a coach id to (user, state, slot).
func (s *Server) targetCoach(c *gin.Context, cid string) (*game.User, game.State, int, bool) {
	ctx := c.Request.Context()
	idx := game.CoachIndex(ctx, s.d.Store, s.d.Roster)
	info := idx[cid]
	if info == nil || info.Username == "" {
		return nil, nil, 0, false
	}
	u, err := game.UserByUsername(ctx, s.d.Store, info.Username)
	if err != nil || u == nil {
		return nil, nil, 0, false
	}
	st, err := game.LoadState(ctx, s.d.Store, u.ID)
	if err != nil || st == nil {
		return nil, nil, 0, false
	}
	return u, st, game.SlotOfCoach(st, cid), true
}

func isCoachID(s string) bool {
	if len(s) != 24 {
		return false
	}
	for _, r := range s {
		if !strings.ContainsRune("0123456789abcdef", r) {
			return false
		}
	}
	return true
}

func (s *Server) achievementByCoach(c *gin.Context) {
	cid := c.Param("cid")
	if cid == "me" || cid == "my" {
		s.achievementMe(c)
		return
	}
	_, st, _, found := s.targetCoach(c, cid)
	if !found {
		writeJSON(c, map[string]any{"result": false, "reason": "not exist coach"})
		return
	}
	writeJSON(c, s.d.Roster.AchievementPayload(st))
}

func (s *Server) ladderByCoach(c *gin.Context) {
	cid := c.Param("cid")
	if cid == "me" || cid == "my" {
		s.ladderMyInfo(c)
		return
	}
	_, st, _, found := s.targetCoach(c, cid)
	if !found {
		writeJSON(c, map[string]any{"result": false, "reason": "not exist coach"})
		return
	}
	allElos := game.AllElos(c.Request.Context(), s.d.Store)
	writeJSON(c, game.LadderStatistics(st, allElos))
}

func (s *Server) coachSummary(c *gin.Context) {
	cid := c.Param("cid")
	if !isCoachID(cid) {
		writeJSON(c, map[string]any{})
		return
	}
	u, st, slot, found := s.targetCoach(c, cid)
	if !found {
		s.unknownCoachSummary(c, cid)
		return
	}
	me := s.d.Roster.CoachMe(st, slot, u)
	ctx := c.Request.Context()
	friends, _ := game.FriendRows(ctx, s.d.Store, cid)
	friendCount := 0
	for _, f := range friends {
		if f.Status == "accepted" || f.Status == "" {
			friendCount++
		}
	}
	stats := game.Map(me["statistics"])
	if stats == nil {
		stats = map[string]any{}
	}
	allElos := game.AllElos(ctx, s.d.Store)
	statsCopy := map[string]any{}
	for k, v := range stats {
		statsCopy[k] = v
	}
	statsCopy["ladder"] = game.LadderStatistics(st, allElos)
	profile := game.Map(me["profile"])
	if profile == nil {
		profile = map[string]any{}
	}
	profileCopy := map[string]any{}
	for k, v := range profile {
		profileCopy[k] = v
	}
	profileCopy["views"] = game.Int(profile["views"])
	if _, has := profileCopy["description"]; !has {
		profileCopy["description"] = ""
	}
	writeJSON(c, map[string]any{
		"coach": map[string]any{
			"_id": cid, "id": cid, "name": me["name"],
			"levelinfo":    me["levelinfo"],
			"teamvalue":    map[string]any{"val": s.d.Roster.TeamValue(st, slot)},
			"currentbadge": me["currentbadge"],
			"playtime":     me["playtime"],
			"profile":      profileCopy,
			"statistics":   statsCopy,
		},
		"friends": friendCount,
	})
}

func (s *Server) unknownCoachSummary(c *gin.Context, cid string) {
	st, _, ok := s.loadState(c)
	if !ok {
		return
	}
	name, _ := game.RecordedIdentity(st, cid)
	if name == "" {
		name = "HLV"
	}
	writeJSON(c, map[string]any{
		"coach": map[string]any{
			"_id": cid, "id": cid, "name": name,
			"levelinfo":    map[string]any{"level": 1},
			"teamvalue":    map[string]any{"val": 0},
			"currentbadge": 1001, "playtime": 0,
			"profile":    map[string]any{"views": 0, "description": ""},
			"statistics": map[string]any{},
		},
		"friends": 0,
	})
}

func (s *Server) coachByID(c *gin.Context) {
	cid := c.Param("cid")
	if !isCoachID(cid) {
		writeJSON(c, map[string]any{})
		return
	}
	u, st, slot, found := s.targetCoach(c, cid)
	if !found {
		mySt, _, ok := s.loadState(c)
		if !ok {
			return
		}
		name, tid := game.RecordedIdentity(mySt, cid)
		if name == "" {
			name = "HLV"
		}
		team := map[string]any{"teamid": tid, "players": []any{},
			"teamvalue": map[string]any{"val": 0}, "formations": []any{},
			"teamsquadlist": map[string]any{"preset": []any{}}}
		if strings.Contains(c.Request.URL.RawQuery, "path=team") {
			writeJSON(c, team)
			return
		}
		// fx ep_coach_by_id not-found shell: the popup's team tab reads
		// .team/.teamid/.levelinfo even for non-account coaches.
		writeJSON(c, map[string]any{"id": cid, "_id": cid, "name": name,
			"teamvalue": map[string]any{"val": 0}, "teamid": tid,
			"levelinfo": map[string]any{"level": 1}, "team": team,
			"kit": map[string]any{}, "summary": map[string]any{},
			"formations": []any{},
			"badges": []any{}, "currentbadge": 1001,
			"teamsquadlist": map[string]any{"preset": []any{}}})
		return
	}
	me := s.d.Roster.CoachMe(st, slot, u)
	team := game.Map(me["team"])
	if team == nil {
		team = map[string]any{}
	}
	teamValue := map[string]any{"val": s.d.Roster.TeamValue(st, slot)}
	teamCopy := map[string]any{}
	for k, v := range team {
		teamCopy[k] = v
	}
	teamCopy["teamvalue"] = teamValue
	if strings.Contains(c.Request.URL.RawQuery, "path=team") {
		writeJSON(c, teamCopy)
		return
	}
	// fx ep_coach_by_id: the CoachUserPopupScreen team tab reads the
	// team from THIS body's .team (plus .teamid/.levelinfo/.kit/
	// .summary). The earlier minimal body ({id, name, teamvalue,
	// formations, teamsquadlist}) left the tab with no squad and
	// "Gia tri doi: 0 EP" (user report 2026-11-13).
	tsl := team["teamsquadlist"]
	if tsl == nil {
		tsl = map[string]any{"preset": []any{}}
	}
	// badges/currentbadge: CoachAchievementBadgePopup (the popup's
	// achievement tab, loaded from CoachAssets) reads them off THIS
	// body in its /coach/<id> fetch step: _myBadges = data.badges,
	// _currentBadge = data.currentbadge. Without them _myBadges is
	// undefined and makeBadgeList's null guard returns null, so the
	// tab renders an empty badge list and its date-sort comparer
	// freezes the client (user report 2026-11-13; the fx body lacks
	// them too, so the tab never worked in this lineage). fx
	// ep_coach_by_id DERIVES the owned list from the target's
	// completed achievement groups (Roster.OwnedBadges); CoachMe's
	// own badges stay [] (hc /coach/me parity).
	writeJSON(c, map[string]any{"id": cid, "_id": cid, "name": me["name"],
		"teamvalue": teamValue, "teamid": me["teamid"],
		"levelinfo": me["levelinfo"], "team": teamCopy,
		"kit": team["kit"], "summary": team["summary"],
		"formations": team["formations"],
		"badges": s.d.Roster.OwnedBadges(st), "currentbadge": me["currentbadge"],
		"teamsquadlist": tsl})
}
