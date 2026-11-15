package api

// Club (Team) endpoints, ported from fo3_ext.py section 8.

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"fo3server/internal/game"
)

func myCoachID(st game.State) (string, string) {
	slot := game.DefaultSlot(st)
	coach := game.CoachBySlot(st, slot)
	if coach == nil {
		return "", ""
	}
	return game.Str(coach["_id"]), game.Str(coach["name"])
}

func (s *Server) clubViewMine(c *gin.Context, st game.State) (*game.ClubView, string, error) {
	myCID, _ := myCoachID(st)
	if myCID == "" {
		return nil, "", nil
	}
	club, err := game.ClubOfCoach(c.Request.Context(), s.d.Store, myCID)
	if err != nil || club == nil {
		return nil, myCID, err
	}
	idx := game.CoachIndex(c.Request.Context(), s.d.Store, s.d.Roster)
	v, err := game.ClubViewOf(c.Request.Context(), s.d.Store, club, idx)
	return v, myCID, err
}

// facilitiesResource is the /facilities CachedResource for the
// coach's current club ({} when clubless). ClubFacilityProxy reads
// this CACHED resource in updateDonationAmount when the club
// resource changes; if the cache is still cold (club just created,
// or the sign-on batch never carried it) the cached body is null
// and the proxy throws #1009 — the "loi giao dien" popup right
// after club creation (telemetry 2026-11-12 19:12 + 21:29 UTC,
// each within a second of the club resource changing, while later
// fetches with a warm cache stayed clean). Warming the cache in
// the sign-on batch and putting the resource BEFORE /club/me in
// club replies removes the cold read.
func (s *Server) facilitiesResource(c *gin.Context, myCID string) map[string]any {
	club, _ := game.ClubOfCoach(c.Request.Context(), s.d.Store, myCID)
	var body any = map[string]any{}
	if club != nil {
		body = game.FacilitiesMap()
	}
	return map[string]any{"uri": "/facilities", "body": body}
}

func (s *Server) clubResources(c *gin.Context, st game.State, user *game.User, myCID string) []any {
	v, _, _ := s.clubViewMine(c, st)
	var body any = map[string]any{}
	if v != nil {
		body = game.ClubBody(v, myCID, true, false)
	}
	out := []any{s.facilitiesResource(c, myCID),
		map[string]any{"uri": "/club/me", "body": body}}
	slot := game.DefaultSlot(st)
	out = append(out, s.d.Roster.CoachMeResources(st, slot, user)...)
	return out
}

func (s *Server) clubReply(c *gin.Context, st game.State, user *game.User, ok bool, reason string, extra map[string]any) {
	out := map[string]any{"result": ok}
	if !ok && reason != "" {
		out["reason"] = reason
	}
	for k, v := range extra {
		out[k] = v
	}
	if ok {
		myCID, _ := myCoachID(st)
		out["resources"] = s.clubResources(c, st, user, myCID)
	}
	writeJSON(c, out)
}

func (s *Server) clubFeeCreation(c *gin.Context) {
	writeJSON(c, map[string]any{"fee": game.CfgI("club_fee", game.ClubFee)})
}

func (s *Server) clubFeeExpansion(c *gin.Context) {
	n, _ := strconv.Atoi(c.Param("n"))
	newLimit := n + game.ClubExpand
	if newLimit > game.ClubLimitMax {
		newLimit = game.ClubLimitMax
	}
	writeJSON(c, map[string]any{"fee": game.CfgI("club_expand_fee", game.ClubExpandFee), "newLimit": newLimit})
}

func (s *Server) clubCreate(c *gin.Context) {
	st, user, ok := s.loadState(c)
	if !ok {
		return
	}
	var req struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	_ = c.ShouldBindJSON(&req)
	name := req.Name
	desc := req.Description
	if len([]rune(desc)) > 200 {
		desc = string([]rune(desc)[:200])
	}
	ctx := c.Request.Context()
	myCID, myName := myCoachID(st)
	if club, _ := game.ClubOfCoach(ctx, s.d.Store, myCID); club != nil {
		s.clubReply(c, st, user, false, "already have a club", nil)
		return
	}
	if !game.ClubNameOK(name) {
		s.clubReply(c, st, user, false, "invalid name", nil)
		return
	}
	if game.ClubNameTaken(ctx, s.d.Store, name) {
		s.clubReply(c, st, user, false, "name is already used", nil)
		return
	}
	slot := game.DefaultSlot(st)
	coach := game.CoachBySlot(st, slot)
	ep := 0
	if coach != nil {
		ep = game.Int(coach["ep"])
	}
	if ep < game.CfgI("club_fee", game.ClubFee) {
		s.clubReply(c, st, user, false, "not enough money", nil)
		return
	}
	coach["ep"] = ep - game.CfgI("club_fee", game.ClubFee)
	if err := game.SaveState(ctx, s.d.Store, user.ID, st); err != nil {
		s.clubReply(c, st, user, false, "invalid request", nil)
		return
	}
	club, err := game.ClubCreate(ctx, s.d.Store, name, desc, myCID, myName)
	if err != nil {
		s.clubReply(c, st, user, false, "invalid request", nil)
		return
	}
	// Founding a club also voids the founder's pending
	// applications at other clubs.
	_, _ = s.d.Store.DB.ExecContext(ctx,
		"DELETE FROM club_requests WHERE coach_id = ?", myCID)
	s.clubReply(c, st, user, true, "", map[string]any{"clubid": club.ID, "name": name})
}

func (s *Server) clubMe(c *gin.Context) {
	st, _, ok := s.loadState(c)
	if !ok {
		return
	}
	v, myCID, _ := s.clubViewMine(c, st)
	if v == nil {
		writeJSON(c, map[string]any{})
		return
	}
	writeJSON(c, game.ClubBody(v, myCID, true, false))
}

// clubMePost: the client schedules club dissolution as
// POST /club/me?_method=DELETE (fx convention); anything else on
// POST /club/me keeps the fallback behaviour.
func (s *Server) clubMePost(c *gin.Context) {
	if c.Query("_method") == "DELETE" {
		s.clubMeDelete(c)
		return
	}
	s.fallback(c)
}

func (s *Server) clubMeDelete(c *gin.Context) {
	st, user, ok := s.loadState(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	myCID, _ := myCoachID(st)
	club, _ := game.ClubOfCoach(ctx, s.d.Store, myCID)
	if club == nil || club.MasterID != myCID {
		s.clubReply(c, st, user, false, "invalid request", nil)
		return
	}
	club.Doc.DeleteAt = float64(time.Now().Unix()) + game.ClubDissolutionMin*60
	_ = game.ClubSaveDoc(ctx, s.d.Store, club)
	s.clubReply(c, st, user, true, "", nil)
}

func (s *Server) clubMeUndelete(c *gin.Context) {
	st, user, ok := s.loadState(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	myCID, _ := myCoachID(st)
	club, _ := game.ClubOfCoach(ctx, s.d.Store, myCID)
	if club == nil || club.MasterID != myCID {
		s.clubReply(c, st, user, false, "invalid request", nil)
		return
	}
	club.Doc.DeleteAt = 0
	_ = game.ClubSaveDoc(ctx, s.d.Store, club)
	s.clubReply(c, st, user, true, "", nil)
}

func (s *Server) clubFind(c *gin.Context) {
	st, _, ok := s.loadState(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	myCID, _ := myCoachID(st)
	idx := game.CoachIndex(ctx, s.d.Store, s.d.Roster)
	views, _ := game.ClubAllViews(ctx, s.d.Store, idx)
	kw := strings.ToLower(c.Query("keyword"))
	lo, _ := strconv.Atoi(c.Query("membernumfrom"))
	hi, _ := strconv.Atoi(c.Query("membernumto"))
	// A coach who already belongs to a club can never be shown
	// as "applied" anywhere: join requests filed before joining
	// are stale (fx never cleaned them up cross-club), and the
	// client renders member.grade 0 as "đã nộp đơn" (owner
	// report 2026-11-15: an application to Aton from 2026-11-12
	// still showed after he joined GMTeam).
	myClub, _ := game.ClubOfCoach(ctx, s.d.Store, myCID)
	rows := []map[string]any{}
	for _, v := range views {
		req := false
		if myClub == nil {
			req = game.ClubHasRequest(ctx, s.d.Store, v.Club.ID, myCID)
		}
		rows = append(rows, game.ClubBody(v, myCID, false, req))
	}
	filtered := rows[:0]
	for _, r := range rows {
		if !strings.Contains(strings.ToLower(game.Str(r["name"])), kw) {
			continue
		}
		if c.Query("allowjoin") == "true" && r["allowjoin"] != true {
			continue
		}
		if c.Query("skipjudgement") == "true" && r["skipjudgement"] != true {
			continue
		}
		mc := game.Int(r["membercount"])
		if mc < lo || (hi > 0 && mc > hi) {
			continue
		}
		filtered = append(filtered, r)
	}
	sortKey := c.DefaultQuery("sort", "")
	reverse := c.DefaultQuery("order", "-1") == "-1"
	keyFn := func(r map[string]any) float64 {
		switch sortKey {
		case "name":
			return 0
		case "member":
			return float64(game.Int(r["membercount"]))
		case "weeklypoints":
			return float64(game.Int(game.Map(game.Map(r["levelinfo"])["actpoints"])["weeklycached"]))
		default: // level
			return float64(game.Int(game.Map(r["levelinfo"])["level"]))
		}
	}
	for i := 0; i < len(filtered); i++ {
		for j := i + 1; j < len(filtered); j++ {
			a, b := filtered[i], filtered[j]
			swap := false
			if sortKey == "name" {
				an, bn := strings.ToLower(game.Str(a["name"])), strings.ToLower(game.Str(b["name"]))
				swap = (an > bn) != reverse && an != bn
			} else {
				swap = (keyFn(a) < keyFn(b)) != reverse && keyFn(a) != keyFn(b)
			}
			if swap {
				filtered[i], filtered[j] = b, a
			}
		}
	}
	out := []any{}
	for _, r := range filtered {
		out = append(out, r)
	}
	writeJSON(c, map[string]any{"clubs": out, "count": len(out)})
}

func (s *Server) clubGet(c *gin.Context) {
	st, _, ok := s.loadState(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	myCID, _ := myCoachID(st)
	club, _ := game.ClubGet(ctx, s.d.Store, c.Param("id"))
	if club == nil {
		writeJSON(c, map[string]any{"result": false, "reason": "deleted club"})
		return
	}
	idx := game.CoachIndex(ctx, s.d.Store, s.d.Roster)
	v, _ := game.ClubViewOf(ctx, s.d.Store, club, idx)
	mine := false
	myClub, _ := game.ClubOfCoach(ctx, s.d.Store, myCID)
	if myClub != nil && myClub.ID == club.ID {
		mine = true
	}
	// Same stale-request guard as clubFind: no "applied" flag
	// for a coach who already has a club.
	req := false
	if myClub == nil {
		req = game.ClubHasRequest(ctx, s.d.Store, club.ID, myCID)
	}
	writeJSON(c, game.ClubBody(v, myCID, mine, req))
}

func (s *Server) clubJoin(c *gin.Context) {
	st, user, ok := s.loadState(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	myCID, _ := myCoachID(st)
	cid := c.Param("id")
	if c.Request.Method == "DELETE" || c.Query("_method") == "DELETE" {
		_, _ = s.d.Store.DB.ExecContext(ctx,
			"DELETE FROM club_requests WHERE clubid = ? AND coach_id = ?", cid, myCID)
		writeJSON(c, map[string]any{"result": true})
		return
	}
	if club, _ := game.ClubOfCoach(ctx, s.d.Store, myCID); club != nil {
		s.clubReply(c, st, user, false, "already have a club", nil)
		return
	}
	club, _ := game.ClubGet(ctx, s.d.Store, cid)
	if club == nil || !club.Doc.AllowJoin {
		s.clubReply(c, st, user, false, "invalid request", nil)
		return
	}
	var n int
	_ = s.d.Store.DB.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM club_members WHERE clubid = ?", club.ID).Scan(&n)
	if n >= club.Doc.Limit {
		s.clubReply(c, st, user, false, "invalid request", nil)
		return
	}
	now := float64(time.Now().Unix())
	if club.Doc.SkipJudge {
		_, _ = s.d.Store.DB.ExecContext(ctx,
			"INSERT INTO club_members (clubid, coach_id, grade, joined_at) VALUES (?, ?, 1, ?) ON DUPLICATE KEY UPDATE grade = 1, joined_at = VALUES(joined_at)",
			club.ID, myCID, now)
		// Becoming a member voids every pending application the
		// coach has anywhere, not just this club's (stale rows
		// otherwise keep other clubs showing "đã nộp đơn").
		_, _ = s.d.Store.DB.ExecContext(ctx,
			"DELETE FROM club_requests WHERE coach_id = ?", myCID)
	} else {
		_, _ = s.d.Store.DB.ExecContext(ctx,
			"INSERT INTO club_requests (clubid, coach_id, requested_at) VALUES (?, ?, ?) ON DUPLICATE KEY UPDATE requested_at = VALUES(requested_at)",
			club.ID, myCID, now)
	}
	s.clubReply(c, st, user, true, "", nil)
}

func (s *Server) clubLeave(c *gin.Context) {
	st, user, ok := s.loadState(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	myCID, _ := myCoachID(st)
	club, _ := game.ClubOfCoach(ctx, s.d.Store, myCID)
	if club == nil || club.MasterID == myCID {
		s.clubReply(c, st, user, false, "invalid request", nil)
		return
	}
	_, _ = s.d.Store.DB.ExecContext(ctx,
		"DELETE FROM club_members WHERE clubid = ? AND coach_id = ?", club.ID, myCID)
	s.clubReply(c, st, user, true, "", nil)
}

func (s *Server) clubMembers(c *gin.Context) {
	st, _, ok := s.loadState(c)
	if !ok {
		return
	}
	v, myCID, _ := s.clubViewMine(c, st)
	out := []any{}
	if v != nil {
		for _, m := range v.Members {
			out = append(out, game.ClubMemberRow(m, m.ID == myCID))
		}
	}
	writeJSON(c, out)
}

func (s *Server) clubMemberGrade(c *gin.Context) {
	st, user, ok := s.loadState(c)
	if !ok {
		return
	}
	var req struct {
		Grade *int `json:"grade"`
	}
	_ = c.ShouldBindJSON(&req)
	ctx := c.Request.Context()
	myCID, _ := myCoachID(st)
	mid := c.Param("mid")
	club, _ := game.ClubOfCoach(ctx, s.d.Store, myCID)
	if club == nil || club.MasterID != myCID || mid == myCID || req.Grade == nil ||
		(*req.Grade != 8 && *req.Grade != 6 && *req.Grade != 1) {
		s.clubReply(c, st, user, false, "invalid request", nil)
		return
	}
	res, _ := s.d.Store.DB.ExecContext(ctx,
		"UPDATE club_members SET grade = ? WHERE clubid = ? AND coach_id = ?", *req.Grade, club.ID, mid)
	if n, _ := res.RowsAffected(); n == 0 {
		s.clubReply(c, st, user, false, "invalid request", nil)
		return
	}
	s.clubReply(c, st, user, true, "", nil)
}

func (s *Server) clubExclude(c *gin.Context) {
	st, user, ok := s.loadState(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	myCID, _ := myCoachID(st)
	mid := c.Param("mid")
	club, _ := game.ClubOfCoach(ctx, s.d.Store, myCID)
	if club == nil || club.MasterID != myCID || mid == myCID {
		s.clubReply(c, st, user, false, "invalid request", nil)
		return
	}
	res, _ := s.d.Store.DB.ExecContext(ctx,
		"DELETE FROM club_members WHERE clubid = ? AND coach_id = ?", club.ID, mid)
	if n, _ := res.RowsAffected(); n == 0 {
		s.clubReply(c, st, user, false, "invalid request", nil)
		return
	}
	// Tell the excluded coach immediately: without a push
	// their client keeps the cached /club/me and goes on
	// showing the club (and refusing their own leave, since
	// server-side they are already out) until relog — user
	// report 2026-11-15. The empty body is exactly what
	// /club/me returns for a clubless coach, a shape the
	// client's ClubProxy change path already handles at
	// every clubless boot.
	game.MessengerPush("/club/me", map[string]any{}, mid)
	s.pushNotes(ctx, mid)
	s.clubReply(c, st, user, true, "", nil)
}

func (s *Server) clubJoinRequests(c *gin.Context) {
	st, _, ok := s.loadState(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	v, myCID, _ := s.clubViewMine(c, st)
	out := []any{}
	if v == nil || v.ClubMyGrade(myCID) < 6 {
		writeJSON(c, out)
		return
	}
	idx := game.CoachIndex(ctx, s.d.Store, s.d.Roster)
	rows, err := s.d.Store.DB.QueryContext(ctx,
		"SELECT coach_id, requested_at FROM club_requests WHERE clubid = ? ORDER BY requested_at", v.Club.ID)
	if err != nil {
		writeJSON(c, out)
		return
	}
	defer rows.Close()
	users, _ := game.AllUsers(ctx, s.d.Store)
	byName := map[string]*game.User{}
	for _, u := range users {
		byName[u.Username] = u
	}
	for rows.Next() {
		var cid string
		var at float64
		if rows.Scan(&cid, &at) != nil {
			continue
		}
		f := idx[cid]
		name, level, teamid, elo := cid, 1, 1, game.LadderStartElo
		ovr := map[string]any{"FW": 0, "MF": 0, "DF": 0}
		if f != nil {
			name, level, teamid, elo = f.Name, f.Level, f.TeamID, f.Elo
			if teamid == 0 {
				teamid = 1
			}
			if u := byName[f.Username]; u != nil {
				if ust, err := game.LoadState(ctx, s.d.Store, u.ID); err == nil {
					if t := game.TeamOf(ust, game.DefaultSlot(ust)); t != nil {
						sum := s.d.Roster.TeamSummaryOf(t, teamid)
						if o := game.Map(sum["ovr"]); o != nil {
							ovr = map[string]any{"FW": game.Int(o["FW"]), "MF": game.Int(o["MF"]), "DF": game.Int(o["DF"])}
						}
					}
				}
			}
		}
		divName, _ := game.DivisionForElo(elo)
		out = append(out, map[string]any{
			"id": cid, "coachid": cid, "name": name, "level": level,
			"division": "DIVISION_" + divName, "ovr": ovr,
			"requestedat": game.W3C(at), "teamid": teamid,
		})
	}
	writeJSON(c, out)
}

func (s *Server) clubJoinDecide(c *gin.Context) {
	st, user, ok := s.loadState(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	myCID, _ := myCoachID(st)
	rid := c.Param("rid")
	club, _ := game.ClubOfCoach(ctx, s.d.Store, myCID)
	if club == nil {
		s.clubReply(c, st, user, false, "invalid request", nil)
		return
	}
	idx := game.CoachIndex(ctx, s.d.Store, s.d.Roster)
	v, _ := game.ClubViewOf(ctx, s.d.Store, club, idx)
	if v == nil || v.ClubMyGrade(myCID) < 6 {
		s.clubReply(c, st, user, false, "invalid request", nil)
		return
	}
	if !game.ClubHasRequest(ctx, s.d.Store, club.ID, rid) {
		s.clubReply(c, st, user, false, "invalid request", nil)
		return
	}
	if c.Request.Method == "DELETE" || c.Query("_method") == "DELETE" {
		_, _ = s.d.Store.DB.ExecContext(ctx,
			"DELETE FROM club_requests WHERE clubid = ? AND coach_id = ?", club.ID, rid)
		s.clubReply(c, st, user, true, "", nil)
		return
	}
	var n int
	_ = s.d.Store.DB.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM club_members WHERE clubid = ?", club.ID).Scan(&n)
	if n >= club.Doc.Limit {
		s.clubReply(c, st, user, false, "club is full", nil)
		return
	}
	if other, _ := game.ClubOfCoach(ctx, s.d.Store, rid); other != nil {
		_, _ = s.d.Store.DB.ExecContext(ctx,
			"DELETE FROM club_requests WHERE clubid = ? AND coach_id = ?", club.ID, rid)
		s.clubReply(c, st, user, false, "already have a club", nil)
		return
	}
	_, _ = s.d.Store.DB.ExecContext(ctx,
		"INSERT INTO club_members (clubid, coach_id, grade, joined_at) VALUES (?, ?, 1, ?)",
		club.ID, rid, float64(time.Now().Unix()))
	// Accepted anywhere = applications everywhere are void.
	_, _ = s.d.Store.DB.ExecContext(ctx,
		"DELETE FROM club_requests WHERE coach_id = ?", rid)
	// Same staleness class as the exclude push: an applicant
	// who is online when accepted would not see their club
	// until relog without a /club/me push.
	if v2, _ := game.ClubViewOf(ctx, s.d.Store, club, idx); v2 != nil {
		game.MessengerPush("/club/me", game.ClubBody(v2, rid, true, false), rid)
	}
	s.clubReply(c, st, user, true, "", nil)
}

func (s *Server) clubSettings(c *gin.Context) {
	st, user, ok := s.loadState(c)
	if !ok {
		return
	}
	var req struct {
		Description *string `json:"description"`
		Message     *string `json:"message"`
		Color       *int    `json:"color"`
	}
	_ = c.ShouldBindJSON(&req)
	ctx := c.Request.Context()
	myCID, myName := myCoachID(st)
	what := c.Param("what")
	value := c.Param("value")
	club, _ := game.ClubOfCoach(ctx, s.d.Store, myCID)
	if club == nil {
		s.clubReply(c, st, user, false, "invalid request", nil)
		return
	}
	idx := game.CoachIndex(ctx, s.d.Store, s.d.Roster)
	v, _ := game.ClubViewOf(ctx, s.d.Store, club, idx)
	grade := 0
	if v != nil {
		grade = v.ClubMyGrade(myCID)
	}
	now := float64(time.Now().Unix())
	trim200 := func(s string) string {
		r := []rune(s)
		if len(r) > 200 {
			return string(r[:200])
		}
		return s
	}
	switch {
	case what == "description" && grade == 10 && req.Description != nil:
		club.Doc.Description = trim200(*req.Description)
		club.Doc.DescAt = now
	case what == "news" && grade >= 6 && req.Message != nil:
		club.Doc.News = trim200(*req.Message)
		club.Doc.NewsBy = myName
		club.Doc.NewsAt = now
	// allowjoin/skipjudgement accept the manager tier (grade >= 6,
	// same as news and join decisions), not just the master: the
	// client's management page enables both toggles for any member
	// while the club is active (it grade-gates only master actions
	// like close-club at grade > 9), so the fx master-only gate made
	// every grade-8 manager's toggle fail with "invalid request"
	// (user report 2026-11-15: GM, grade 8 in GMTeam).
	case what == "allowjoin" && grade >= 6:
		club.Doc.AllowJoin = value == "true"
	case what == "skipjudgement" && grade >= 6:
		club.Doc.SkipJudge = value == "true"
	case what == "color" && grade == 10 && req.Color != nil:
		club.Doc.Color = *req.Color
	default:
		s.clubReply(c, st, user, false, "invalid request", nil)
		return
	}
	_ = game.ClubSaveDoc(ctx, s.d.Store, club)
	s.clubReply(c, st, user, true, "", nil)
}

func (s *Server) clubExpand(c *gin.Context) {
	st, user, ok := s.loadState(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	myCID, _ := myCoachID(st)
	club, _ := game.ClubOfCoach(ctx, s.d.Store, myCID)
	if club == nil || club.MasterID != myCID {
		s.clubReply(c, st, user, false, "invalid request", nil)
		return
	}
	if club.Doc.Limit >= game.ClubLimitMax {
		// Already at the member cap: succeed as a no-op instead of
		// erroring (the client's button lags the real limit by one
		// step, so the last click always landed here).
		s.clubReply(c, st, user, true, "", nil)
		return
	}
	slot := game.DefaultSlot(st)
	coach := game.CoachBySlot(st, slot)
	ep := 0
	if coach != nil {
		ep = game.Int(coach["ep"])
	}
	if ep < game.CfgI("club_expand_fee", game.ClubExpandFee) {
		s.clubReply(c, st, user, false, "not enough money", nil)
		return
	}
	coach["ep"] = ep - game.CfgI("club_expand_fee", game.ClubExpandFee)
	if err := game.SaveState(ctx, s.d.Store, user.ID, st); err != nil {
		s.clubReply(c, st, user, false, "invalid request", nil)
		return
	}
	club.Doc.Limit += game.ClubExpand
	if club.Doc.Limit > game.ClubLimitMax {
		club.Doc.Limit = game.ClubLimitMax
	}
	_ = game.ClubSaveDoc(ctx, s.d.Store, club)
	s.clubReply(c, st, user, true, "", nil)
}

func (s *Server) clubPosts(c *gin.Context) {
	st, _, ok := s.loadState(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	myCID, _ := myCoachID(st)
	club, _ := game.ClubOfCoach(ctx, s.d.Store, myCID)
	out := []any{}
	if club == nil {
		writeJSON(c, out)
		return
	}
	posts, _ := game.ClubPosts(ctx, s.d.Store, club.ID)
	for _, p := range posts {
		out = append(out, map[string]any{
			"_id": p.ID, "name": p.CoachName, "message": p.Message,
			"createdat": game.W3C(p.At),
		})
	}
	writeJSON(c, out)
}

func (s *Server) clubPost(c *gin.Context) {
	st, user, ok := s.loadState(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	myCID, myName := myCoachID(st)
	club, _ := game.ClubOfCoach(ctx, s.d.Store, myCID)
	if club == nil {
		s.clubReply(c, st, user, false, "invalid request", nil)
		return
	}
	pid := c.Param("pid")
	if c.Request.Method == "DELETE" || c.Query("_method") == "DELETE" {
		posts, _ := game.ClubPosts(ctx, s.d.Store, club.ID)
		found := false
		for _, p := range posts {
			if p.ID == pid && (p.CoachID == myCID || club.MasterID == myCID) {
				found = true
			}
		}
		if !found {
			s.clubReply(c, st, user, false, "invalid request", nil)
			return
		}
		_, _ = s.d.Store.DB.ExecContext(ctx,
			"DELETE FROM club_posts WHERE clubid = ? AND post_id = ?", club.ID, pid)
		s.clubReply(c, st, user, true, "", nil)
		return
	}
	var req struct {
		Msg     string `json:"msg"`
		Message string `json:"message"`
	}
	_ = c.ShouldBindJSON(&req)
	text := req.Msg
	if text == "" {
		text = req.Message
	}
	if r := []rune(text); len(r) > 200 {
		text = string(r[:200])
	}
	if text == "" {
		s.clubReply(c, st, user, false, "invalid request", nil)
		return
	}
	_ = game.ClubAddPost(ctx, s.d.Store, club.ID, game.ClubPost{
		ID: game.NewIID(), CoachID: myCID, CoachName: myName,
		Message: text, At: float64(time.Now().Unix()),
	})
	s.clubReply(c, st, user, true, "", nil)
}

func (s *Server) clubMessage(c *gin.Context) {
	// Ports fx.ep_club_message: the line is pushed to EVERY club
	// member's messenger connection (sender included, as the echo)
	// at /club/me/message. The stub version accepted the line and
	// delivered nothing, so team chat looked dead. Without a club
	// (or an empty line) the answer stays result True and nothing
	// is delivered (a False pops an error in the chat tab).
	st, _, ok := s.loadState(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	var req map[string]any
	_ = c.ShouldBindJSON(&req)
	text := game.Str(req["message"])
	if r := []rune(text); len(r) > 300 {
		text = string(r[:300])
	}
	myCID, myName := myCoachID(st)
	club, _ := game.ClubOfCoach(ctx, s.d.Store, myCID)
	if club == nil || text == "" {
		writeJSON(c, map[string]any{"result": true})
		return
	}
	payload := map[string]any{"type": "chat", "clubid": club.ID,
		"coachid": myCID, "coachname": myName, "message": text}
	if ids, err := game.ClubMemberIDs(ctx, s.d.Store, club.ID); err == nil {
		for cid := range ids {
			game.MessengerPush("/club/me/message", payload, cid)
		}
	}
	writeJSON(c, map[string]any{"result": true})
}

func (s *Server) contactClub(c *gin.Context) {
	st, _, ok := s.loadState(c)
	if !ok {
		return
	}
	v, myCID, _ := s.clubViewMine(c, st)
	members := []any{}
	if v != nil {
		for _, m := range v.Members {
			if m.ID != myCID {
				members = append(members, map[string]any{"_id": m.ID, "coachname": m.Name})
			}
		}
	}
	writeJSON(c, map[string]any{"members": members})
}

func (s *Server) facilities(c *gin.Context) {
	st, _, ok := s.loadState(c)
	if !ok {
		return
	}
	myCID, _ := myCoachID(st)
	club, _ := game.ClubOfCoach(c.Request.Context(), s.d.Store, myCID)
	if club == nil {
		writeJSON(c, map[string]any{})
		return
	}
	writeJSON(c, game.FacilitiesMap())
}

func (s *Server) clubFacilities(c *gin.Context) {
	if _, _, ok := s.loadState(c); !ok {
		return
	}
	club, _ := game.ClubGet(c.Request.Context(), s.d.Store, c.Param("id"))
	if club == nil {
		writeJSON(c, map[string]any{})
		return
	}
	writeJSON(c, game.FacilitiesMap())
}

// clubRecordMonth is the record room's month key (YYYYMM, ICT) —
// fx served it as an int and the tab pages parse the month digits
// out of its string form for their titles.
func clubRecordMonth() int {
	n := time.Now().In(time.FixedZone("ICT", 7*3600))
	return n.Year()*100 + int(n.Month())
}

// clubRecord serves month-less shells for every tab: the
// pages' handlers bail on the missing month before touching
// any control, so the room opens stable and error-free but
// empty (the state accepted on 2026-11-13). Full bodies
// (bak-2030..2132) made the members handler throw #1009 on
// first open in a session — the handler runs before the
// page's controls are attached, and neither response timing
// (0-10 s delays), ClubProxy warming (fetches, pushes,
// in-band envelopes) nor payload shaping changed that; the
// user chose the stable empty room on 2026-11-15. The seeded
// Records data stays in the club docs (the club hub totals
// use it); only this room's per-tab endpoints serve shells.
// Revisit if club team matches are ever built — this room
// is their stats home.
func (s *Server) clubRecord(c *gin.Context) {
	switch c.Param("what") {
	case "members":
		writeJSON(c, map[string]any{"members": []any{}})
	case "opponent":
		writeJSON(c, map[string]any{"opponentrecords": []any{}})
	case "recent":
		writeJSON(c, map[string]any{"recentmatches": []any{}})
	case "leaderboard":
		writeJSON(c, map[string]any{"leaderboardlist": []any{}})
	default:
		writeJSON(c, map[string]any{"opponentrecords": []any{}, "recentmatches": []any{}})
	}
}

var emptyPNG = []byte{
	0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x00, 0x00, 0x0d,
	0x49, 0x48, 0x44, 0x52, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
	0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4, 0x89, 0x00, 0x00, 0x00,
	0x0a, 0x49, 0x44, 0x41, 0x54, 0x78, 0x9c, 0x63, 0x00, 0x01, 0x00, 0x00,
	0x05, 0x00, 0x01, 0x0d, 0x0a, 0x2d, 0xb4, 0x00, 0x00, 0x00, 0x00, 0x49,
	0x45, 0x4e, 0x44, 0xae, 0x42, 0x60, 0x82,
}

func (s *Server) clubEmblem(c *gin.Context) {
	cid := c.Param("id")
	size := strings.TrimSuffix(c.Param("file"), ".png")
	idx := 0
	// int(cid, 16) % len(colors), computed incrementally.
	for _, ch := range cid {
		var d int
		switch {
		case ch >= '0' && ch <= '9':
			d = int(ch - '0')
		case ch >= 'a' && ch <= 'f':
			d = int(ch-'a') + 10
		case ch >= 'A' && ch <= 'F':
			d = int(ch-'A') + 10
		default:
			d = 0
		}
		idx = (idx*16 + d) % len(game.ClubColors)
	}
	data, err := os.ReadFile(filepath.Join(s.d.AssetsDir, "club_emblems",
		strconv.Itoa(idx)+"_"+size+".png"))
	if err != nil || data == nil {
		data = emptyPNG
	}
	c.Data(200, "image/png", data)
}
