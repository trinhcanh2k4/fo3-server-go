package api

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"fo3server/internal/game"
)

// Admin editors batch 2 (2026-11-13): craft.json editor, quest /
// achievement catalogue editors, one-click backup restore.

// GET /admin/api/config/craft — effective craft config.
func (s *Server) adminCraftGet(c *gin.Context) {
	path := filepath.Join(s.d.GamedataDir, "craft.json")
	_, err := os.Stat(path)
	cfg := s.d.Roster.CraftCfg()
	writeJSON(c, map[string]any{"result": true, "custom": err == nil, "config": cfg})
}

// PUT /admin/api/config/craft {players, count, pieces, fee, grade}.
func (s *Server) adminCraftPut(c *gin.Context) {
	var req map[string]any
	_ = c.ShouldBindJSON(&req)
	players := []int{}
	for _, v := range game.List(req["players"]) {
		players = append(players, game.Int(v))
	}
	count := game.Int(req["count"])
	pieces := game.Int(req["pieces"])
	fee := game.Float(req["fee"])
	grade := game.Int(req["grade"])
	if count < 1 || count > 200 || pieces < 1 || pieces > 200 || fee < 0 || fee > 1 || grade < 0 || grade > 10 {
		writeJSON(c, map[string]any{"result": false, "reason": "BAD_CONFIG"})
		return
	}
	doc := map[string]any{"players": players, "count": count, "pieces": pieces, "fee": fee, "grade": grade}
	raw, _ := json.Marshal(doc)
	path := filepath.Join(s.d.GamedataDir, "craft.json")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		writeJSON(c, map[string]any{"result": false, "reason": "WRITE_FAILED"})
		return
	}
	s.audit(c, "config.craft", "", doc)
	writeJSON(c, map[string]any{"result": true, "config": doc})
}

// GET /admin/api/config/quests — effective quest configuration.
func (s *Server) adminQuestsGet(c *gin.Context) {
	path := filepath.Join(s.d.GamedataDir, "quest_config.json")
	_, err := os.Stat(path)
	writeJSON(c, map[string]any{
		"result": true, "custom": err == nil,
		"pool":       game.QuestPool(s.d.GamedataDir),
		"milestones": game.GPMilestones(s.d.GamedataDir),
		"rewards":    game.QuestRewardOverrides(s.d.GamedataDir),
	})
}

var questMetrics = map[string]bool{
	"match": true, "win": true, "goal": true,
	"rank": true, "rankwin": true, "upgrade": true,
}

// PUT /admin/api/config/quests {pool, milestones, rewards} — an
// empty object clears the override file (back to defaults).
func (s *Server) adminQuestsPut(c *gin.Context) {
	var req map[string]any
	_ = c.ShouldBindJSON(&req)
	path := filepath.Join(s.d.GamedataDir, "quest_config.json")
	if len(req) == 0 {
		_ = os.Remove(path)
		s.audit(c, "config.quests", "", map[string]any{"cleared": true})
		writeJSON(c, map[string]any{"result": true, "custom": false})
		return
	}
	rawPool, _ := json.Marshal(req["pool"])
	var pool []game.QuestPoolDef
	if len(game.List(req["pool"])) > 0 {
		if json.Unmarshal(rawPool, &pool) != nil || len(pool) > 20 {
			writeJSON(c, map[string]any{"result": false, "reason": "BAD_POOL"})
			return
		}
		for _, p := range pool {
			if !questMetrics[p.Metric] || p.Target < 1 || p.GP < 0 {
				writeJSON(c, map[string]any{"result": false, "reason": "BAD_POOL"})
				return
			}
		}
	}
	rawMs, _ := json.Marshal(req["milestones"])
	var milestones []game.GPMilestone
	if len(game.List(req["milestones"])) > 0 {
		if json.Unmarshal(rawMs, &milestones) != nil || len(milestones) > 10 {
			writeJSON(c, map[string]any{"result": false, "reason": "BAD_MILESTONES"})
			return
		}
		for _, m := range milestones {
			if m.GP < 1 || m.EP < 0 || m.Pack < 0 {
				writeJSON(c, map[string]any{"result": false, "reason": "BAD_MILESTONES"})
				return
			}
		}
	}
	raw, _ := json.Marshal(req)
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		writeJSON(c, map[string]any{"result": false, "reason": "WRITE_FAILED"})
		return
	}
	s.audit(c, "config.quests", "", map[string]any{"pool": len(pool), "milestones": len(milestones)})
	writeJSON(c, map[string]any{"result": true, "custom": true})
}

// GET /admin/api/config/achievements — catalogue ids with authored +
// effective (override-aware) reward entries.
func (s *Server) adminAchievementsGet(c *gin.Context) {
	t := game.Achieve(s.d.GamedataDir)
	path := filepath.Join(s.d.GamedataDir, "achievement_rewards.json")
	_, err := os.Stat(path)
	entries := []any{}
	for _, e := range t.List {
		id := game.Str(e["id"])
		authored := []any{}
		if lst := game.List(e["rewards"]); len(lst) > 0 {
			authored = game.List(game.Map(lst[0])["reward"])
		}
		effective := authored
		if ov, ok := t.RewardOverride(id); ok {
			effective = []any{}
			for _, x := range ov {
				effective = append(effective, x)
			}
		}
		entries = append(entries, map[string]any{
			"id": id, "name": game.Str(e["name"]),
			"authored": authored, "effective": effective,
		})
	}
	writeJSON(c, map[string]any{"result": true, "custom": err == nil, "entries": entries})
}

// PUT /admin/api/config/achievements {overrides: {id: [{itemid, count}]}}.
func (s *Server) adminAchievementsPut(c *gin.Context) {
	var req map[string]any
	_ = c.ShouldBindJSON(&req)
	path := filepath.Join(s.d.GamedataDir, "achievement_rewards.json")
	ov := game.Map(req["overrides"])
	if len(ov) == 0 {
		_ = os.Remove(path)
		s.audit(c, "config.achievements", "", map[string]any{"cleared": true})
		writeJSON(c, map[string]any{"result": true, "custom": false})
		return
	}
	for id, raw := range ov {
		for _, e := range game.List(raw) {
			em := game.Map(e)
			if game.Int(em["itemid"]) < 1 || game.Int(em["count"]) < 1 {
				writeJSON(c, map[string]any{"result": false, "reason": "BAD_REWARD", "id": id})
				return
			}
		}
	}
	raw, _ := json.Marshal(ov)
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		writeJSON(c, map[string]any{"result": false, "reason": "WRITE_FAILED"})
		return
	}
	s.audit(c, "config.achievements", "", map[string]any{"ids": len(ov)})
	writeJSON(c, map[string]any{"result": true, "custom": true})
}

// POST /admin/api/ops/restore {name} — restore a backup from the
// backups directory. A pre-restore backup of the CURRENT database
// is taken first and its name returned.
func (s *Server) adminOpsRestore(c *gin.Context) {
	var req map[string]any
	_ = c.ShouldBindJSON(&req)
	name := game.Str(req["name"])
	if name == "" || name != filepath.Base(name) || !strings.HasSuffix(name, ".sql.gz") {
		writeJSON(c, map[string]any{"result": false, "reason": "BAD_NAME"})
		return
	}
	full := filepath.Join(backupDir, name)
	if _, err := os.Stat(full); err != nil {
		writeJSON(c, map[string]any{"result": false, "reason": "NOT_FOUND"})
		return
	}
	pre := "fo3-pre-restore-" + time.Now().Format("20060102-150405") + ".sql.gz"
	if reason := s.createBackupFile(pre); reason != "" {
		writeJSON(c, map[string]any{"result": false, "reason": "PRE_BACKUP_" + reason})
		return
	}
	user, pass, db, ok := s.dbCreds()
	if !ok {
		writeJSON(c, map[string]any{"result": false, "reason": "DSN_PARSE"})
		return
	}
	gz := exec.Command("gzip", "-dc", full)
	mysql := exec.Command("mysql", "-u", user, db)
	mysql.Env = append(os.Environ(), "MYSQL_PWD="+pass)
	mysql.Stdin, _ = gz.StdoutPipe()
	var stderr strings.Builder
	mysql.Stderr = &stderr
	if err := mysql.Start(); err != nil {
		writeJSON(c, map[string]any{"result": false, "reason": "MYSQL_START"})
		return
	}
	if err := gz.Run(); err != nil {
		writeJSON(c, map[string]any{"result": false, "reason": "GUNZIP_FAILED"})
		return
	}
	if err := mysql.Wait(); err != nil {
		writeJSON(c, map[string]any{"result": false, "reason": "RESTORE_FAILED", "detail": stderr.String()})
		return
	}
	s.audit(c, "ops.restore", name, map[string]any{"pre_restore_backup": pre})
	writeJSON(c, map[string]any{"result": true, "name": name, "pre_restore_backup": pre})
}
