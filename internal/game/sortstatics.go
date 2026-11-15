package game

// Computed /static/db/SORT* payloads (hc club_index + ep_sort*).
// Python's fx by-id variants are broken upstream (club_index()[1]
// is a tuple, .get raises, the except branch always wins), so in
// production SORTLEAGUE/<nation> always answers [13 16 19 31 53]
// and SORTTEAM/<league> always answers EURO_CLUBS — the api layer
// reproduces those constants; the no-arg variants below are real.

import (
	"sort"
	"strings"
	"sync"
)

type clubLeagueTeams struct {
	League int
	Teams  []int
}

type clubNationEntry struct {
	Nation  int
	Leagues []clubLeagueTeams
}

var (
	clubIndexOnce  sync.Once
	clubIndexCache []clubNationEntry
)

// ClubIndex ports hc.club_index (cached for the process lifetime,
// like Python's _CLUBS).
func (r *Roster) ClubIndex() []clubNationEntry {
	clubIndexOnce.Do(func() { clubIndexCache = r.buildClubIndex() })
	return clubIndexCache
}

func (r *Roster) buildClubIndex() []clubNationEntry {
	counts := map[int][2]int{} // teamid -> (players, hasGoalie)
	if rows, err := r.db.Query(`SELECT p.teamid, COUNT(DISTINCT p.playerid),
		MAX(p.preferredposition1 = 0) FROM players p
		JOIN permid m ON m.spid = p.spid AND m.licensed = 1
		WHERE p.spid / 1000000 = ? GROUP BY p.teamid`, genCurrentSeason); err == nil {
		defer rows.Close()
		for rows.Next() {
			var tid, n, gk int
			if rows.Scan(&tid, &n, &gk) == nil {
				counts[tid] = [2]int{n, gk}
			}
		}
	}
	leagues := map[int][]int{}
	for tid, c := range counts {
		lid, ok := r.clubLinks[tid]
		if c[0] < 18 || c[1] == 0 || !ok || lid == 76 || lid == 78 || lid == 2028 {
			continue
		}
		if _, ok := r.clubLeagues[lid]; !ok {
			continue
		}
		leagues[lid] = append(leagues[lid], tid)
	}
	nations := map[int][]clubLeagueTeams{}
	for lid, teams := range leagues {
		sort.Slice(teams, func(i, j int) bool {
			a := strings.ToLower(Str(r.clubTeams[teams[i]]["teamname"]))
			b := strings.ToLower(Str(r.clubTeams[teams[j]]["teamname"]))
			if a != b {
				return a < b
			}
			return teams[i] < teams[j]
		})
		nation := Int(r.clubLeagues[lid]["countryid"])
		nations[nation] = append(nations[nation], clubLeagueTeams{lid, teams})
	}
	// International: playable national teams with enough licensed
	// players of the current season and a goalie among them.
	natCounts := map[int][2]int{}
	if rows, err := r.db.Query(`SELECT p.nationality, COUNT(DISTINCT p.playerid),
		MAX(p.preferredposition1 = 0) FROM players p
		JOIN permid m ON m.spid = p.spid AND m.licensed = 1
		WHERE p.spid / 1000000 = ? GROUP BY p.nationality`, genCurrentSeason); err == nil {
		defer rows.Close()
		for rows.Next() {
			var nat, n, gk int
			if rows.Scan(&nat, &n, &gk) == nil {
				natCounts[nat] = [2]int{n, gk}
			}
		}
	}
	internationals := []int{}
	for _, tid := range r.nationalOrder {
		c, ok := natCounts[r.nationalTeams[tid]]
		if ok && c[0] >= 18 && c[1] != 0 {
			internationals = append(internationals, tid)
		}
	}
	sort.SliceStable(internationals, func(i, j int) bool {
		return strings.ToLower(Str(r.clubTeams[internationals[i]]["teamname"])) <
			strings.ToLower(Str(r.clubTeams[internationals[j]]["teamname"]))
	})
	if len(internationals) > 0 {
		nations[75] = []clubLeagueTeams{{78, internationals}}
	}
	nationIDs := make([]int, 0, len(nations))
	for n := range nations {
		nationIDs = append(nationIDs, n)
	}
	sort.Slice(nationIDs, func(i, j int) bool {
		return Str(r.clubNations[nationIDs[i]]["nationname"]) <
			Str(r.clubNations[nationIDs[j]]["nationname"])
	})
	out := make([]clubNationEntry, 0, len(nationIDs))
	for _, n := range nationIDs {
		ls := nations[n]
		sort.Slice(ls, func(i, j int) bool {
			li, lj := Int(r.clubLeagues[ls[i].League]["level"]), Int(r.clubLeagues[ls[j].League]["level"])
			if li != lj {
				return li < lj
			}
			return ls[i].League < ls[j].League
		})
		out = append(out, clubNationEntry{n, ls})
	}
	return out
}

// SortNation ports ep_sortnation (International first).
func (r *Roster) SortNation() []int {
	idx := r.ClubIndex()
	out := []int{}
	has75 := false
	for _, e := range idx {
		if e.Nation == 75 {
			has75 = true
		}
	}
	if has75 {
		out = append(out, 75)
	}
	for _, e := range idx {
		if e.Nation != 75 {
			out = append(out, e.Nation)
		}
	}
	return out
}

// SortLeague ports ep_sortleague: {nationid: [leagueid]}.
func (r *Roster) SortLeague() map[string][]int {
	out := map[string][]int{}
	for _, e := range r.ClubIndex() {
		ids := []int{}
		for _, l := range e.Leagues {
			ids = append(ids, l.League)
		}
		out[itoa(e.Nation)] = ids
	}
	return out
}

// SortTeam ports ep_sortteam: {leagueid: [teamid]}.
func (r *Roster) SortTeam() map[string][]int {
	out := map[string][]int{}
	for _, e := range r.ClubIndex() {
		for _, l := range e.Leagues {
			out[itoa(l.League)] = l.Teams
		}
	}
	return out
}

// AITeamID resolves a room AI index (the bot pick the client
// sends via POST /room/<id>/AI: {nation, league, team} stepper
// positions) to a team id, walking the same SORT* lists the
// client resolves against. Returns 0 when any index is out of
// range.
func (r *Roster) AITeamID(index map[string]any) int {
	nations := r.SortNation()
	ni := Int(index["nation"])
	if ni < 0 || ni >= len(nations) {
		return 0
	}
	leagues := r.SortLeague()[itoa(nations[ni])]
	li := Int(index["league"])
	if li < 0 || li >= len(leagues) {
		return 0
	}
	teams := r.SortTeam()[itoa(leagues[li])]
	ti := Int(index["team"])
	if ti < 0 || ti >= len(teams) {
		return 0
	}
	return teams[ti]
}

// SortWorldCupTeam ports ep_sortworldcupteam:
// {confederation: [national teamids sorted by team name]}.
func (r *Roster) SortWorldCupTeam() map[string][]int {
	out := map[string][]int{}
	for _, n := range r.clubNations {
		teamid := Int(n["teamid"])
		conf := Int(n["confederation"])
		if teamid == 0 || conf == 0 {
			continue
		}
		if _, ok := r.nationalTeams[teamid]; !ok {
			continue
		}
		key := itoa(conf)
		out[key] = append(out[key], teamid)
	}
	for k := range out {
		ids := out[k]
		sort.Slice(ids, func(i, j int) bool {
			a := strings.ToLower(Str(r.clubTeams[ids[i]]["teamname"]))
			b := strings.ToLower(Str(r.clubTeams[ids[j]]["teamname"]))
			if a != b {
				return a < b
			}
			return ids[i] < ids[j]
		})
		out[k] = ids
	}
	return out
}
