package api

// Admin API part 3: staff management and ops (backup, service
// restart) for the web admin.

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"fo3server/internal/game"
)

// --- staff ------------------------------------------------------

func (s *Server) adminStaff(c *gin.Context) {
	st, user, slot, ok := s.loadUserState(c)
	if !ok {
		return
	}
	team := game.TeamOf(st, slot)
	if team == nil {
		writeJSON(c, map[string]any{"result": false, "reason": "NO_TEAM"})
		return
	}
	ctx := c.Request.Context()
	if c.Request.Method == "GET" {
		out := []any{}
		for _, sv := range game.List(team["staffs"]) {
			if sf := game.Map(sv); sf != nil {
				out = append(out, map[string]any{
					"id": game.Str(sf["_id"]), "stfid": sf["stfid"],
					"name": sf["commonname"], "position": sf["position"],
					"grade": sf["grade"], "exp": sf["exp"], "life": sf["life"],
					"status": sf["status"],
				})
			}
		}
		writeJSON(c, map[string]any{"result": true, "staffs": out,
			"roles": team["staffroles"]})
		return
	}
	var req map[string]any
	_ = c.ShouldBindJSON(&req)
	switch c.Param("op") {
	case "grant":
		stfid := game.Int(req["stfid"])
		grade := game.Int(req["grade"])
		if grade < 1 {
			grade = 1
		}
		if grade > 10 {
			grade = 10
		}
		info := s.d.Roster.StaffInfoOf(stfid)
		if info == nil {
			writeJSON(c, map[string]any{"result": false, "reason": "NO_STAFF"})
			return
		}
		staff := map[string]any{
			"_id": game.NewIID(), "stfid": stfid, "commonname": info.Name,
			"position": info.Position, "grade": grade, "exp": 0, "gradeupto": 1000,
			"life": game.RecontractStaffMaxLife, "status": "idle", "effect": game.RecruitEffects(info, grade),
			"study": nil, "material": false, "contractfee": 0, "new": true,
		}
		team["staffs"] = append(game.List(team["staffs"]), staff)
		s.audit(c, "staff.grant", strconv.FormatInt(user.ID, 10),
			map[string]any{"stfid": stfid, "grade": grade})
	case "remove":
		sid := game.Str(req["staffiid"])
		kept := []any{}
		for _, sv := range game.List(team["staffs"]) {
			if sf := game.Map(sv); sf != nil && game.Str(sf["_id"]) == sid {
				continue
			}
			kept = append(kept, sv)
		}
		team["staffs"] = kept
		for _, rv := range game.List(team["staffroles"]) {
			if r := game.Map(rv); r != nil && game.Str(r["staffid"]) == sid {
				r["staffid"] = nil
			}
		}
		s.audit(c, "staff.remove", strconv.FormatInt(user.ID, 10), map[string]any{"staffiid": sid})
	case "life":
		sid := game.Str(req["staffiid"])
		life := game.Int(req["value"])
		if life < 0 {
			life = 0
		}
		if life > game.RecontractStaffMaxLife {
			life = game.RecontractStaffMaxLife
		}
		for _, sv := range game.List(team["staffs"]) {
			if sf := game.Map(sv); sf != nil && game.Str(sf["_id"]) == sid {
				sf["life"] = life
			}
		}
		s.audit(c, "staff.life", strconv.FormatInt(user.ID, 10), map[string]any{"staffiid": sid, "life": life})
	default:
		writeJSON(c, map[string]any{"result": false, "reason": "UNKNOWN_OP"})
		return
	}
	_ = game.SaveState(ctx, s.d.Store, user.ID, st)
	writeJSON(c, map[string]any{"result": true})
}

// --- ops: backup + restart ---------------------------------------

const backupDir = "/root/fo3-backups"

func (s *Server) adminBackups(c *gin.Context) {
	if c.Request.Method == "GET" {
		entries, _ := os.ReadDir(backupDir)
		out := []any{}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql.gz") {
				continue
			}
			if info, err := e.Info(); err == nil {
				out = append(out, map[string]any{"name": e.Name(),
					"size": info.Size(), "at": info.ModTime().Unix()})
			}
		}
		sort.Slice(out, func(i, j int) bool {
			return out[i].(map[string]any)["at"].(int64) > out[j].(map[string]any)["at"].(int64)
		})
		writeJSON(c, map[string]any{"result": true, "backups": out})
		return
	}
	// POST: mysqldump the fo3 database (docs are ciphertext; the
	// dump is only useful together with the master key).
	name := "fo3-" + time.Now().Format("20060102-150405") + ".sql.gz"
	if reason := s.createBackupFile(name); reason != "" {
		writeJSON(c, map[string]any{"result": false, "reason": reason})
		return
	}
	s.audit(c, "ops.backup", name, nil)
	writeJSON(c, map[string]any{"result": true, "name": name})
}

// dbCreds splits the configured DSN into (user, password, dbname).
func (s *Server) dbCreds() (string, string, string, bool) {
	dsn := s.d.Cfg.DBDSN
	user, rest, found := strings.Cut(dsn, ":")
	if !found {
		return "", "", "", false
	}
	pass, _, _ := strings.Cut(rest, "@")
	db := "fo3"
	if i := strings.Index(dsn, ")/"); i >= 0 {
		tail := dsn[i+2:]
		if j := strings.IndexAny(tail, "?"); j >= 0 {
			tail = tail[:j]
		}
		if tail != "" {
			db = tail
		}
	}
	return user, pass, db, true
}

// createBackupFile dumps the fo3 database into backupDir/name and
// returns "" on success or a failure reason.
func (s *Server) createBackupFile(name string) string {
	user, pass, db, ok := s.dbCreds()
	if !ok {
		return "DSN_PARSE"
	}
	_ = os.MkdirAll(backupDir, 0o755)
	full := filepath.Join(backupDir, name)
	dump := exec.Command("mysqldump", "-u", user, db)
	dump.Env = append(os.Environ(), "MYSQL_PWD="+pass)
	gz := exec.Command("gzip", "-c")
	gz.Stdin, _ = dump.StdoutPipe()
	out, err := os.Create(full)
	if err != nil {
		return "CREATE_FAILED"
	}
	defer out.Close()
	gz.Stdout = out
	if err := gz.Start(); err != nil {
		return "GZIP_FAILED"
	}
	if err := dump.Run(); err != nil {
		return "DUMP_FAILED"
	}
	if err := gz.Wait(); err != nil {
		return "GZIP_FAILED"
	}
	return ""
}

var restartable = map[string]bool{
	"fo3-go": true, "fo3": true, "fo3-messenger": true,
}

func (s *Server) adminRestart(c *gin.Context) {
	var req map[string]any
	_ = c.ShouldBindJSON(&req)
	svc := game.Str(req["service"])
	if !restartable[svc] {
		writeJSON(c, map[string]any{"result": false, "reason": "NOT_ALLOWED"})
		return
	}
	// Delayed via systemd-run so this HTTP reply completes even
	// when the service being restarted is fo3-go itself.
	cmd := exec.Command("systemd-run", "--on-active=2", "/bin/systemctl", "restart", svc+".service")
	if err := cmd.Run(); err != nil {
		writeJSON(c, map[string]any{"result": false, "reason": "SCHEDULE_FAILED"})
		return
	}
	s.audit(c, "ops.restart", svc, nil)
	writeJSON(c, map[string]any{"result": true, "scheduled_in_sec": 2})
}
