package game

// Club (Team) domain, ported from fo3_ext.py section 8. Relational
// rows (clubs / club_members / club_requests / club_posts) live in
// MariaDB; the mutable club document and every post document are
// AES-256-GCM encrypted, per the owner's data-encryption requirement.
// Activity points stay zero by design (the Python decision), so club
// level is always 1.

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"fo3server/internal/store"
)

const (
	ClubFee            = 10000
	ClubExpandFee      = 5000
	ClubLimit          = 30
	ClubExpand         = 10
	ClubLimitMax       = 60
	ClubDissolutionMin = 60
	ClubResetMin       = 17 * 60
)

var ClubLevels = []int{0, 1500, 5000, 11000, 20000, 32000, 48000, 68000, 92000, 120000}

var ClubRewards = []struct {
	Points int
	Type   string
	Value  float64
}{{300, "MATCH_PLUS_COACHEXP", 0.05}, {800, "MATCH_PLUS_GAMEMONEY", 0.05},
	{1500, "MATCH_PLUS_PLAYEREXP_ALL", 0.05}, {2500, "MATCH_PLUS_GAMEMONEY", 0.1}}

var ClubColors = []int{1203168, 13382451, 3381555, 16750848, 10027161, 39372, 16737843, 6697932, 13421619, 3355647}

var ClubFacilityTypes = []string{"bank", "clinic", "clubAgency", "clubDormitory",
	"staffDormitory", "fanSupporters", "playerTrainer", "clubTrainer"}

// FacilityEntry is fx._facility_entry (every donation synonym the
// ClubFacilityProxy might read, all zero: nothing built or donated).
func FacilityEntry(t string) map[string]any {
	return map[string]any{
		"type": t, "id": t, "level": 0, "grade": 0, "donation": 0,
		"donationamount": 0, "amount": 0, "todaydonation": 0, "totaldonation": 0,
		"todayDonation": 0, "totalDonation": 0,
	}
}

// FacilitiesMap is fx._facilities_map.
func FacilitiesMap() map[string]any {
	out := map[string]any{}
	for _, t := range ClubFacilityTypes {
		out[t] = FacilityEntry(t)
	}
	return out
}

// ClubDoc is the encrypted club document.
type ClubDoc struct {
	Description string  `json:"description"`
	DescAt      float64 `json:"descat"`
	News        string  `json:"news"`
	NewsBy      string  `json:"newsby"`
	NewsAt      float64 `json:"newsat"`
	AllowJoin   bool    `json:"allowjoin"`
	SkipJudge   bool    `json:"skipjudgement"`
	Color       int     `json:"color"`
	Limit       int     `json:"limit"`
	DeleteAt    float64 `json:"deleteat"`
	Created     float64 `json:"created"`
	// Records is the club record room's data (Phòng thành tích):
	// monthly per-member statistics, opponent records, recent
	// matches and club-level totals, keyed as the /club/record
	// handlers serve them. Any club may carry it; nil means the
	// club has no recorded matches yet (the room renders its
	// empty state). Seeded with fake data for the GMTeam club
	// on 2026-11-14 (owner request, debug purposes) via
	// cmd/seedclubrecords.
	Records map[string]any `json:"records,omitempty"`
}

// Club is one clubs row.
type Club struct {
	ID       string
	Name     string
	MasterID string
	Doc      ClubDoc
}

func sealDoc(st *store.Store, v any) (string, error) {
	plain, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return st.Cipher.Encrypt(plain)
}

func openDoc(st *store.Store, enc string, out any) error {
	plain, err := st.Cipher.Decrypt(enc)
	if err != nil {
		return err
	}
	return json.Unmarshal(plain, out)
}

// ClubGet loads a club by id, applying fx's lazy dissolution: a club
// whose deleteat has passed is deleted with all its rows.
func ClubGet(ctx context.Context, st *store.Store, clubid string) (*Club, error) {
	var name, master, enc string
	err := st.DB.QueryRowContext(ctx,
		"SELECT name, master_id, doc FROM clubs WHERE clubid = ?", clubid).Scan(&name, &master, &enc)
	if err != nil {
		return nil, nil
	}
	c := &Club{ID: clubid, Name: name, MasterID: master}
	if err := openDoc(st, enc, &c.Doc); err != nil {
		return nil, err
	}
	if c.Doc.DeleteAt > 0 && float64(time.Now().Unix()) >= c.Doc.DeleteAt {
		ClubDeleteAll(ctx, st, clubid)
		return nil, nil
	}
	return c, nil
}

// ClubSaveDoc re-encrypts and stores the club document.
func ClubSaveDoc(ctx context.Context, st *store.Store, c *Club) error {
	enc, err := sealDoc(st, c.Doc)
	if err != nil {
		return err
	}
	_, err = st.DB.ExecContext(ctx, "UPDATE clubs SET doc = ? WHERE clubid = ?", enc, c.ID)
	return err
}

// ClubDeleteAll removes a club and every dependent row.
func ClubDeleteAll(ctx context.Context, st *store.Store, clubid string) {
	for _, t := range []string{"clubs", "club_members", "club_requests", "club_posts"} {
		_, _ = st.DB.ExecContext(ctx, "DELETE FROM "+t+" WHERE clubid = ?", clubid)
	}
}

// ClubOfCoach returns the club a coach belongs to (or nil).
func ClubOfCoach(ctx context.Context, st *store.Store, cid string) (*Club, error) {
	var clubid string
	err := st.DB.QueryRowContext(ctx,
		"SELECT clubid FROM club_members WHERE coach_id = ?", cid).Scan(&clubid)
	if err != nil {
		return nil, nil
	}
	return ClubGet(ctx, st, clubid)
}

// ClubCreate inserts a new club with its master as first member.
func ClubCreate(ctx context.Context, st *store.Store, name, desc, masterCID, masterName string) (*Club, error) {
	now := float64(time.Now().Unix())
	c := &Club{ID: NewIID(), Name: name, MasterID: masterCID, Doc: ClubDoc{
		Description: desc, DescAt: now, NewsBy: masterName, NewsAt: now,
		AllowJoin: true, SkipJudge: false, Color: ClubColors[0], Limit: ClubLimit,
		Created: now,
	}}
	enc, err := sealDoc(st, c.Doc)
	if err != nil {
		return nil, err
	}
	if _, err := st.DB.ExecContext(ctx,
		"INSERT INTO clubs (clubid, name, master_id, doc) VALUES (?, ?, ?, ?)",
		c.ID, name, masterCID, enc); err != nil {
		return nil, err
	}
	_, err = st.DB.ExecContext(ctx,
		"INSERT INTO club_members (clubid, coach_id, grade, joined_at) VALUES (?, ?, 10, ?)",
		c.ID, masterCID, now)
	return c, err
}

// ClubNameTaken reports a case-insensitive name clash.
func ClubNameTaken(ctx context.Context, st *store.Store, name string) bool {
	var one int
	err := st.DB.QueryRowContext(ctx,
		"SELECT 1 FROM clubs WHERE LOWER(name) = LOWER(?)", name).Scan(&one)
	return err == nil
}

// ClubNameOK is fx._club_name_ok.
func ClubNameOK(name string) bool {
	if len([]byte(name)) < 4 || len([]byte(name)) > 20 {
		return false
	}
	if strings.TrimSpace(name) != name {
		return false
	}
	return !strings.ContainsAny(name, "<>&\"'\\/")
}

// ClubMember is one resolved member row of a club view.
type ClubMember struct {
	ID       string
	Name     string
	Level    int
	Elo      int
	Username string
	TeamID   int
	Grade    int
	Joined   float64
}

// ClubView is fx._club_view: the club plus members resolved against
// the real accounts.
type ClubView struct {
	Club    *Club
	Members []ClubMember
}

// CoachIndex resolves coach ids against every account (name, level,
// elo, team) -- fx._find_coach_across_users over the whole user base.
func CoachIndex(ctx context.Context, st *store.Store, r *Roster) map[string]*ClubMember {
	out := map[string]*ClubMember{}
	users, _ := AllUsers(ctx, st)
	for _, u := range users {
		ust, err := LoadState(ctx, st, u.ID)
		if err != nil || ust == nil {
			continue
		}
		for _, coach := range Coaches(ust) {
			cid := Str(coach["_id"])
			if cid == "" {
				continue
			}
			elo := LadderStartElo
			if rk := Map(ust["ranked"]); rk != nil && rk["elo"] != nil {
				elo = Int(rk["elo"])
			}
			out[cid] = &ClubMember{
				ID: cid, Name: Str(coach["name"]), Username: u.Username,
				Level:  Int(CoachLevelinfo(coach)["level"]),
				Elo:    elo,
				TeamID: Int(coach["teamid"]),
			}
		}
	}
	return out
}

// ClubViewOf builds the view for a club.
func ClubViewOf(ctx context.Context, st *store.Store, c *Club, index map[string]*ClubMember) (*ClubView, error) {
	rows, err := st.DB.QueryContext(ctx,
		"SELECT coach_id, grade, joined_at FROM club_members WHERE clubid = ? ORDER BY grade DESC, joined_at ASC", c.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	v := &ClubView{Club: c}
	for rows.Next() {
		var cid string
		var grade int
		var joined float64
		if rows.Scan(&cid, &grade, &joined) != nil {
			continue
		}
		m := ClubMember{ID: cid, Name: cid, Level: 1, Elo: LadderStartElo, Grade: grade, Joined: joined}
		if f := index[cid]; f != nil {
			m.Name, m.Level, m.Elo, m.Username, m.TeamID = f.Name, f.Level, f.Elo, f.Username, f.TeamID
			if m.Name == "" {
				m.Name = cid
			}
		}
		v.Members = append(v.Members, m)
	}
	return v, rows.Err()
}

// ClubMyGrade is fx._club_my_grade.
func (v *ClubView) ClubMyGrade(cid string) int {
	for _, m := range v.Members {
		if m.ID == cid {
			return m.Grade
		}
	}
	return 0
}

// ClubHasRequest reports a pending join request.
func ClubHasRequest(ctx context.Context, st *store.Store, clubid, cid string) bool {
	var one int
	err := st.DB.QueryRowContext(ctx,
		"SELECT 1 FROM club_requests WHERE clubid = ? AND coach_id = ?", clubid, cid).Scan(&one)
	return err == nil
}

// ClubLevel is fx._club_level for a points total.
func ClubLevel(total int) (level, lo, hi int) {
	level, lo = 1, ClubLevels[0]
	for i, t := range ClubLevels {
		if total >= t {
			level, lo = i + 1, t
		}
	}
	hi = ClubLevels[len(ClubLevels)-1]
	if level < len(ClubLevels) {
		hi = ClubLevels[level]
	}
	return
}

// ClubBody is fx._club_body.
func ClubBody(v *ClubView, myCID string, mine bool, requested bool) map[string]any {
	c := v.Club
	level, lo, hi := ClubLevel(0)
	master := ClubMember{Name: "", Level: 1}
	foundMaster := false
	for _, m := range v.Members {
		if m.ID == c.MasterID {
			master, foundMaster = m, true
			break
		}
	}
	if !foundMaster && len(v.Members) > 0 {
		master = v.Members[0]
	}
	grade := -2
	if myCID != "" {
		if g := v.ClubMyGrade(myCID); g != 0 {
			grade = g
		} else if requested {
			grade = 0
		}
	}
	end := WeekEndTs()
	nowICT := time.Now().In(time.FixedZone("ICT", 7*3600))
	monthInt := nowICT.Year()*100 + int(nowICT.Month())
	body := map[string]any{
		"_id": c.ID, "name": c.Name, "createdat": W3C(c.Doc.Created),
		"state": "active",
		"description": map[string]any{"message": c.Doc.Description, "changedat": W3C(c.Doc.DescAt)},
		"news":        map[string]any{"message": c.Doc.News, "coachname": c.Doc.NewsBy, "changedat": W3C(c.Doc.NewsAt)},
		"allowjoin":    c.Doc.AllowJoin, "skipjudgement": c.Doc.SkipJudge, "color": c.Doc.Color,
		"masterid": c.MasterID, "mastername": master.Name, "masterlevel": master.Level,
		"membercount": len(v.Members),
		"limits":      map[string]any{"members": c.Doc.Limit, "submasters": 2},
		"levelinfo": map[string]any{
			"level": level, "from": lo, "to": hi,
			"actpoints": map[string]any{
				"total": 0, "weekly": 0, "today": 0, "weeklycached": 0,
				"limits":        map[string]any{"weekly": 15000, "today": 3000},
				"resetat":       ClubResetMin,
				"weeklybegindate": W3C(end - 7*86400), "weeklyenddate": W3C(end - 1),
			},
		},
		"rewards": clubRewardsJSON(),
		"statistics": map[string]any{
			"friendly": map[string]any{"wins": 0, "total": 0, "forfeitwins": 0,
				"recent": map[string]any{"month": monthInt, "wins": 0, "forfeitwins": 0}},
			"ranked": map[string]any{
				"recent":     map[string]any{"wins": 0, "losses": 0, "total": 0, "wprating": 0, "sid": 0, "highrecord": map[string]any{"wp": 0}},
				"highrecord": map[string]any{"wp": 0, "rank": 0},
				"last":       map[string]any{"rank": 0, "wprating": 0},
			},
		},
		"member":     map[string]any{"grade": grade},
		"facilities": FacilitiesMap(),
	}
	// Overlay the club's stored record totals (record room data)
	// onto the statistics block the record pages read through
	// ClubProxy.clubInfo (members header + season tab).
	if rec := c.Doc.Records; rec != nil {
		if stats := Map(body["statistics"]); stats != nil {
			if fr := Map(stats["friendly"]); fr != nil {
				fr["wins"] = Int(rec["friendly_wins"])
				fr["total"] = Int(rec["friendly_total"])
				fr["forfeitwins"] = Int(rec["friendly_forfeitwins"])
				if rc := Map(fr["recent"]); rc != nil {
					rc["wins"] = Int(rec["friendly_wins"])
					rc["forfeitwins"] = Int(rec["friendly_forfeitwins"])
				}
			}
			if rk := Map(stats["ranked"]); rk != nil {
				if rc := Map(rk["recent"]); rc != nil {
					rc["wins"] = Int(rec["ranked_wins"])
					rc["losses"] = Int(rec["ranked_losses"])
					rc["total"] = Int(rec["ranked_total"])
					rc["wprating"] = Int(rec["ranked_wprating"])
					if hr := Map(rc["highrecord"]); hr != nil {
						hr["wp"] = Int(rec["ranked_wp"])
					}
				}
				if hr := Map(rk["highrecord"]); hr != nil {
					hr["wp"] = Int(rec["ranked_wp"])
				}
			}
		}
	}
	if c.Doc.DeleteAt > 0 {
		body["state"] = "inactive"
		body["willbedeletedat"] = W3C(c.Doc.DeleteAt)
	}
	if mine && grade >= 1 {
		rewards := []any{}
		for i := 0; i < 7; i++ {
			rewards = append(rewards, map[string]any{"goalpoints": 30,
				"reward": map[string]any{"name": "EP", "count": 1000}})
		}
		body["member"] = map[string]any{
			"grade": grade, "limits": map[string]any{"today": 100},
			"actpoints": map[string]any{"today": 0, "weekly": 0, "total": 0, "activities": []any{}},
			"rewards":   rewards,
			"reward": map[string]any{
				"todaypoint": 0,
				"prev":       map[string]any{"step": 0, "goalpoints": 0},
				"next":       map[string]any{"step": 1, "goalpoints": 30, "reward": map[string]any{"clubep": 0, "expandme": 0}},
			},
			"statistics": map[string]any{"ranked": map[string]any{
				"recent": map[string]any{"wins": 0, "losses": 0, "sid": 0}}},
		}
	}
	return body
}

func clubRewardsJSON() []any {
	out := []any{}
	for _, r := range ClubRewards {
		out = append(out, map[string]any{"points": r.Points,
			"boost": map[string]any{"type": r.Type, "value": r.Value}})
	}
	return out
}

// ClubMemberRow is fx._club_member_row. online is decided by the
// caller (presence lives with the messenger; during the hybrid phase
// only the requester themself is known-online).
func ClubMemberRow(m ClubMember, online bool) map[string]any {
	divName, _ := DivisionForElo(m.Elo)
	div := "DIVISION_" + divName
	seen := m.Joined
	if online {
		seen = float64(time.Now().Unix())
	}
	return map[string]any{
		"_id": m.ID, "coachid": m.ID, "name": m.Name, "level": m.Level,
		"division": div, "currdivision": div,
		"actpoints":  map[string]any{"weekly": 0, "total": 0},
		"joinedat":   W3C(m.Joined),
		"connectedat": W3C(seen),
		"donates":    nil, "grade": m.Grade, "online": online,
	}
}

// ClubPost is one board post (document stored encrypted).
type ClubPost struct {
	ID       string  `json:"-"`
	CoachID  string  `json:"coach_id"`
	CoachName string `json:"coach_name"`
	Message  string  `json:"message"`
	At       float64 `json:"at"`
}

// ClubPosts lists a club's board, newest first (fx: LIMIT 50).
func ClubPosts(ctx context.Context, st *store.Store, clubid string) ([]ClubPost, error) {
	rows, err := st.DB.QueryContext(ctx,
		"SELECT post_id, doc FROM club_posts WHERE clubid = ? ORDER BY at_ts DESC LIMIT 50", clubid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ClubPost
	for rows.Next() {
		var pid, enc string
		if rows.Scan(&pid, &enc) != nil {
			continue
		}
		var p ClubPost
		if openDoc(st, enc, &p) == nil {
			p.ID = pid
			out = append(out, p)
		}
	}
	return out, rows.Err()
}

// ClubAddPost inserts a post and trims the board to the newest 30
// (fx's retention rule).
func ClubAddPost(ctx context.Context, st *store.Store, clubid string, p ClubPost) error {
	enc, err := sealDoc(st, p)
	if err != nil {
		return err
	}
	if _, err := st.DB.ExecContext(ctx,
		"INSERT INTO club_posts (clubid, post_id, doc, at_ts) VALUES (?, ?, ?, ?)",
		clubid, p.ID, enc, p.At); err != nil {
		return err
	}
	_, err = st.DB.ExecContext(ctx,
		`DELETE FROM club_posts WHERE clubid = ? AND post_id NOT IN
		 (SELECT post_id FROM (SELECT post_id FROM club_posts WHERE clubid = ? ORDER BY at_ts DESC LIMIT 30) keep)`,
		clubid, clubid)
	return err
}

// ClubAllViews loads every club's view (find / leaderboard).
func ClubAllViews(ctx context.Context, st *store.Store, index map[string]*ClubMember) ([]*ClubView, error) {
	rows, err := st.DB.QueryContext(ctx, "SELECT clubid FROM clubs ORDER BY created_at")
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	var out []*ClubView
	for _, id := range ids {
		c, err := ClubGet(ctx, st, id)
		if err != nil || c == nil {
			continue
		}
		v, err := ClubViewOf(ctx, st, c, index)
		if err == nil {
			out = append(out, v)
		}
	}
	return out, nil
}

// SortClubViewsByMembers sorts views for the club leaderboard.
func SortClubViewsByMembers(views []*ClubView) {
	sort.SliceStable(views, func(i, j int) bool {
		if len(views[i].Members) != len(views[j].Members) {
			return len(views[i].Members) > len(views[j].Members)
		}
		return strings.ToLower(views[i].Club.Name) < strings.ToLower(views[j].Club.Name)
	})
}
