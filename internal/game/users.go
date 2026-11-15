package game

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"strings"
	"time"

	"fo3server/internal/store"
)

// User is one row of the users table.
type User struct {
	ID         int64
	Username   string
	CoachName  string
	Elo        int
	PassHash   string // Argon2id PHC, may be empty before upgrade
	LegacyHash string // Python-era SHA-256 hex, cleared after upgrade
	Disabled   bool
}

// UserByUsername loads a user by name.
func UserByUsername(ctx context.Context, st *store.Store, username string) (*User, error) {
	return scanUser(st.DB.QueryRowContext(ctx,
		`SELECT id, username, coach_name, elo, pass_hash, IFNULL(legacy_hash,''), disabled
		 FROM users WHERE username = ?`, username))
}

// UserByID loads a user by id.
func UserByID(ctx context.Context, st *store.Store, id int64) (*User, error) {
	return scanUser(st.DB.QueryRowContext(ctx,
		`SELECT id, username, coach_name, elo, pass_hash, IFNULL(legacy_hash,''), disabled
		 FROM users WHERE id = ?`, id))
}

func scanUser(row *sql.Row) (*User, error) {
	u := &User{}
	err := row.Scan(&u.ID, &u.Username, &u.CoachName, &u.Elo, &u.PassHash, &u.LegacyHash, &u.Disabled)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return u, nil
}

func tokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// IssueToken mints a new session token for the user (the launcher flow):
// the raw token goes to the client; only its SHA-256 is stored.
func IssueToken(ctx context.Context, st *store.Store, u *User, ip string) (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	token := hex.EncodeToString(b)
	_, err := st.DB.ExecContext(ctx,
		"UPDATE users SET token_hash = ?, last_ip = ? WHERE id = ?", tokenHash(token), ip, u.ID)
	if err != nil {
		return "", err
	}
	_ = st.Redis.Set(ctx, "ipuser:"+ip, u.Username, 24*time.Hour).Err()
	return token, nil
}

// VerifyPassword checks a login password: Argon2id when present,
// otherwise the Python-era SHA-256, upgrading the row on success.
func VerifyPassword(ctx context.Context, st *store.Store, u *User, pw string) bool {
	if u.PassHash != "" {
		return store.CheckPassword(pw, u.PassHash)
	}
	if u.LegacyHash == "" {
		return false
	}
	sum := sha256.Sum256([]byte(pw))
	if hex.EncodeToString(sum[:]) != u.LegacyHash {
		return false
	}
	if phc, err := store.HashPassword(pw); err == nil {
		_, _ = st.DB.ExecContext(ctx,
			"UPDATE users SET pass_hash = ?, legacy_hash = NULL WHERE id = ?", phc, u.ID)
		u.PassHash = phc
		u.LegacyHash = ""
	}
	return true
}

// CreateUser registers a new account (Argon2id from day one).
func CreateUser(ctx context.Context, st *store.Store, username, pw string) (*User, error) {
	phc, err := store.HashPassword(pw)
	if err != nil {
		return nil, err
	}
	res, err := st.DB.ExecContext(ctx,
		"INSERT INTO users (username, pass_hash, coach_name, elo) VALUES (?, ?, '', 1000)",
		username, phc)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return &User{ID: id, Username: username, Elo: 1000, PassHash: phc}, nil
}

// DualWriteSQLiteToken mirrors a freshly issued token + client IP into
// the legacy Python SQLite users table. During the shadow phase the
// Python messenger (TCP 28002) and proxied Python handlers still read
// that database; without the mirror they would not know the session.
// Best-effort: a failure is logged by the caller, never fatal.
func DualWriteSQLiteToken(path string, uid int64, token, ip string) error {
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		return err
	}
	defer db.Close()
	_, err = db.Exec("UPDATE users SET token = ?, last_ip = ? WHERE id = ?", token, ip, uid)
	return err
}

// ResolveUser ports fx._resolve_request_user: token -> client IP ->
// last active user -> admin. Redis caches the token and IP
// mappings; MariaDB is the durable store.
//
// The fx-era legacy token form "FO3-<username>-<uid>" is honored
// ONLY when allowLegacy is set. The API layer sets it exclusively
// for direct loopback calls carrying no proxy headers (the
// internal match-settlement calls in forfeit.go); anything that
// arrived through the public reverse proxy must present a
// server-issued token — a legacy token is trivially forgeable from
// a username and a small sequential uid. The second return value
// reports how the user was resolved ("token", "ip", or "" when the
// request stays anonymous — the global fallbacks were removed in
// the A2b tightening, 2026-11-15).
func ResolveUser(ctx context.Context, st *store.Store, token, clientIP string, allowLegacy bool) (*User, string) {
	if token != "" {
		if name, err := st.Redis.Get(ctx, "token:"+tokenHash(token)).Result(); err == nil && name != "" {
			if u, _ := UserByUsername(ctx, st, name); u != nil {
				return u, "token"
			}
		}
		var uname string
		legacy := false
		err := st.DB.QueryRowContext(ctx,
			"SELECT username FROM users WHERE token_hash = ?", tokenHash(token)).Scan(&uname)
		if err != nil && allowLegacy && strings.HasPrefix(token, "FO3-") {
			parts := strings.Split(token, "-")
			if len(parts) >= 2 {
				uname = parts[1]
				legacy = true
			}
		}
		if uname != "" {
			if u, _ := UserByUsername(ctx, st, uname); u != nil {
				if !legacy {
					// Never cache a legacy token: the cache is
					// consulted before the gate, so caching one
					// would re-open the public path for 24h.
					_ = st.Redis.Set(ctx, "token:"+tokenHash(token), u.Username, 24*time.Hour).Err()
				}
				_ = st.Redis.Set(ctx, "ipuser:"+clientIP, u.Username, 24*time.Hour).Err()
				return u, "token"
			}
		}
	}
	if clientIP != "" {
		if name, err := st.Redis.Get(ctx, "ipuser:"+clientIP).Result(); err == nil && name != "" {
			if u, _ := UserByUsername(ctx, st, name); u != nil {
				return u, "ip"
			}
		}
		var uname string
		if err := st.DB.QueryRowContext(ctx,
			"SELECT username FROM users WHERE last_ip = ? ORDER BY id DESC LIMIT 1", clientIP).Scan(&uname); err == nil && uname != "" {
			if u, _ := UserByUsername(ctx, st, uname); u != nil {
				return u, "ip"
			}
		}
	}
	// A2b tightening (2026-11-15, user's call): the fx-era global
	// fallbacks are GONE. A request that presents no valid token and
	// comes from an IP with no recorded login resolves to NOBODY —
	// it must never be attributed to the most recently active user,
	// to "admin", or to the first account in the DB (which silently
	// made every orphan request act as that account once strangers
	// could register). Callers already handle a nil user.
	return nil, ""
}

// NoteActive records the last-active user (Python: gamedata/last_active.json).
func NoteActive(ctx context.Context, st *store.Store, u *User) {
	_ = st.Redis.Set(ctx, "lastactive", u.Username, 7*24*time.Hour).Err()
	_ = st.Redis.Set(ctx, "seen:"+u.Username, time.Now().Unix(), 24*time.Hour).Err()
}

// IsCoachOnline reports a coach's presence. When the messenger is
// publishing (fresh heartbeat), its live connection set is
// authoritative -- a coach is online exactly while their client is
// connected to the messenger. Otherwise it falls back to the
// last-seen window (fx._is_online).
func IsCoachOnline(ctx context.Context, st *store.Store, coachID, username string) bool {
	if hb, err := st.Redis.Get(ctx, "msgonline:heartbeat").Int64(); err == nil &&
		time.Now().Unix()-hb < 120 {
		n, _ := st.Redis.Exists(ctx, "msgonline:"+coachID).Result()
		return n > 0
	}
	return IsOnline(ctx, st, username)
}

// IsOnline reports whether the account produced traffic in the last
// 150 seconds (fx._is_online).
func IsOnline(ctx context.Context, st *store.Store, username string) bool {
	if username == "" {
		return false
	}
	v, err := st.Redis.Get(ctx, "seen:"+username).Int64()
	return err == nil && time.Now().Unix()-v < 150
}

// AllUsers lists every account (id, username, coach name, elo).
// UserLastIP returns the account's last seen public IP (recorded
// at token use), or "" — used to fill real peer addresses into
// human-vs-human match sides instead of the loopback placeholder.
func UserLastIP(ctx context.Context, st *store.Store, uid int64) string {
	var ip sql.NullString
	err := st.DB.QueryRowContext(ctx,
		"SELECT last_ip FROM users WHERE id = ?", uid).Scan(&ip)
	if err != nil || !ip.Valid {
		return ""
	}
	return ip.String
}

func AllUsers(ctx context.Context, st *store.Store) ([]*User, error) {
	rows, err := st.DB.QueryContext(ctx, "SELECT id, username, coach_name, elo FROM users ORDER BY elo DESC, id ASC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*User
	for rows.Next() {
		u := &User{}
		var name sql.NullString
		if err := rows.Scan(&u.ID, &u.Username, &name, &u.Elo); err == nil {
			u.CoachName = name.String
			out = append(out, u)
		}
	}
	return out, rows.Err()
}

// AllElos returns every account's (1v1 elo, manager elo) from their
// stored ranked records -- the board-position input of
// fx._coach_ladder_statistics.
func AllElos(ctx context.Context, st *store.Store) [][2]int {
	users, err := AllUsers(ctx, st)
	if err != nil {
		return nil
	}
	out := make([][2]int, 0, len(users))
	for _, u := range users {
		s, err := LoadState(ctx, st, u.ID)
		if err != nil || s == nil {
			out = append(out, [2]int{LadderStartElo, LadderStartElo})
			continue
		}
		out = append(out, [2]int{eloOfRk(RankedState(s, "ranked")), eloOfRk(RankedState(s, "ranked_mgr"))})
	}
	return out
}

// AcceptedFriendCoachIDs returns the coach ids of my accepted friends
// from the relational friends table.
func AcceptedFriendCoachIDs(ctx context.Context, st *store.Store, myCoachID string) (map[string]bool, error) {
	rows, err := st.DB.QueryContext(ctx,
		"SELECT friend_coach_id FROM friends WHERE coach_id = ? AND status = 'accepted'", myCoachID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			out[id] = true
		}
	}
	return out, rows.Err()
}
