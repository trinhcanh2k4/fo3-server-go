// Command relay is a faithful Go port of relay_session.py, the
// FO3 session relay v5.18 (UDP 28000) — a real protocol
// participant, not a forwarder.
//
// The Python file is the source of truth; this port mirrors its
// behaviour function by function, including its quirks (see the
// per-function notes). Protocol summary, from the Python header:
//
//	packet = u16 checksum | u16 key | obfuscated payload (LE)
//	checksum: h = 0x17; for b in packet[2:]: h = (h*0x103 + b) & 0xffff
//	decrypt: chained XOR/subtract with dl = (k>>8)+(k&0xff), then
//	         de-rotate by k; encrypt is the inverse.
//	Plaintext header (24 B): [4] i8 src slot, [5] i8 dst slot
//	(the relay is slot 0x14), [6:10] u32 matchid, [0xa:0xe] u32
//	reliable seq (type 1) / sub-message count (type 2),
//	[0xe:0x12] u32 cumulative ack, [0x12:0x16] u32 token,
//	[0x16:0x18] u16 type.
//
// Environment overrides (defaults are the production values):
//
//	FO3_RELAY_PORT  UDP port to bind (default 28000)
//	FO3_RELAY_LOG   log file path (default /root/fo3-capture.log)
//	FO3_RELAY_AUX   "0" disables the log-only listeners on
//	                UDP 28001 / TCP 28000 / TCP 28001 (they exist
//	                in the Python relay too; disable them when
//	                test-running beside the production relay,
//	                which already holds those sockets)
package main

import (
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/rand"
	"net"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const relaySlot = 0x14

var (
	logPath    = envOr("FO3_RELAY_LOG", "/root/fo3-capture.log")
	listenPort = envIntOr("FO3_RELAY_PORT", 28000)
	auxEnabled = os.Getenv("FO3_RELAY_AUX") != "0"

	internalKey string

	goForfeitURL  = "http://127.0.0.1:8080/internal/relay/forfeit"
	goJoinauthURL = "http://127.0.0.1:8080/internal/relay/joinauth"
)

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func envIntOr(k string, def int) int {
	if v := os.Getenv(k); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

// ----------------------------------------------------------------
// Logging (Python: log() — GMT timestamp with ms, "RELAY4" tag,
// append to LOG_PATH under a lock).

var logMu sync.Mutex

func logLine(msg string) {
	t := time.Now().UTC()
	ts := fmt.Sprintf("%s.%03d", t.Format("2006-01-02 15:04:05"),
		t.Nanosecond()/int(time.Millisecond))
	logMu.Lock()
	defer logMu.Unlock()
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s RELAY4 %s\n", ts, msg)
}

// addrStr renders a UDP address the way Python prints the
// (ip, port) tuple: ('127.0.0.1', 54321).
func addrStr(a *net.UDPAddr) string {
	return fmt.Sprintf("('%s', %d)", a.IP.String(), a.Port)
}

func pyBool(b bool) string {
	if b {
		return "True"
	}
	return "False"
}

// hex06 renders Python's {:#06x} format (0x0001, 0x0105, 0x4001).
func hex06(v uint32) string { return fmt.Sprintf("0x%04x", v) }

// intList renders a sorted int set the way Python prints a
// sorted list: [0, 1].
func intList(xs []int) string {
	parts := make([]string, len(xs))
	for i, x := range xs {
		parts[i] = strconv.Itoa(x)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

func nowSec() float64 { return float64(time.Now().UnixNano()) / 1e9 }

// ----------------------------------------------------------------
// Wire crypto (Python: checksum / decrypt / encrypt / header /
// parse — verified byte-for-byte against captures 2026-11-13).

func checksum(b []byte) uint16 {
	h := 0x17
	for _, x := range b {
		h = (h*0x103 + int(x)) & 0xFFFF
	}
	return uint16(h)
}

func decrypt(p []byte) []byte {
	k := int(binary.LittleEndian.Uint16(p[2:4]))
	n := len(p) - 4
	dl := ((k >> 8) + (k & 0xFF)) & 0xFF
	r := make([]byte, 0, n)
	prev := int(p[3])
	for _, x := range p[4:] {
		r = append(r, byte((int(x)^prev-dl)&0xFF))
		prev = int(x)
	}
	pay := make([]byte, n)
	for i := 0; i < n; i++ {
		pay[(k+i)%n] = r[i]
	}
	out := make([]byte, 0, len(p))
	out = append(out, p[:4]...)
	out = append(out, pay...)
	return out
}

func encrypt(plain []byte) []byte {
	k := rand.Intn(0xFFFF) + 1
	pay := plain[4:]
	n := len(pay)
	dl := ((k >> 8) + (k & 0xFF)) & 0xFF
	rot := make([]byte, n)
	for i := 0; i < n; i++ {
		rot[i] = pay[(k+i)%n]
	}
	prev := k >> 8
	c := make([]byte, n)
	for i, x := range rot {
		prev = ((int(x)+dl)&0xFF) ^ prev
		c[i] = byte(prev)
	}
	rest := make([]byte, 0, n+2)
	rest = binary.LittleEndian.AppendUint16(rest, uint16(k))
	rest = append(rest, c...)
	out := binary.LittleEndian.AppendUint16(nil, checksum(rest))
	out = append(out, rest...)
	return out
}

// header builds the 24-byte plaintext header (4 zero bytes, then
// struct "<bbIIIIH": src, dst, mid, rseq, ack, token, typ). The
// parameter order matches the Python def header(src, dst, mid,
// ack, typ, rseq, token) so call sites translate 1:1.
func header(src, dst int, mid, ack uint32, typ uint16, rseq, token uint32) []byte {
	b := make([]byte, 24)
	b[4] = byte(src)
	b[5] = byte(dst)
	binary.LittleEndian.PutUint32(b[6:10], mid)
	binary.LittleEndian.PutUint32(b[10:14], rseq)
	binary.LittleEndian.PutUint32(b[14:18], ack)
	binary.LittleEndian.PutUint32(b[18:22], token)
	binary.LittleEndian.PutUint16(b[22:24], typ)
	return b
}

type packet struct {
	src, dst          int
	mid, rseq, ack    uint32
	token             uint32
	typ               uint16
	body              []byte
}

func parse(plain []byte) packet {
	return packet{
		src:   int(int8(plain[4])),
		dst:   int(int8(plain[5])),
		mid:   binary.LittleEndian.Uint32(plain[6:10]),
		rseq:  binary.LittleEndian.Uint32(plain[10:14]),
		ack:   binary.LittleEndian.Uint32(plain[14:18]),
		token: binary.LittleEndian.Uint32(plain[18:22]),
		typ:   binary.LittleEndian.Uint16(plain[22:24]),
		body:  plain[24:],
	}
}

// ----------------------------------------------------------------
// Client: reliable stream state of one client (emu-faithful).

type streamMsg struct {
	id   uint16
	body []byte
}

type client struct {
	addr    *net.UDPAddr
	key     string // addr.String(), map key
	ack     uint32
	got     map[uint32][]byte
	stream  []byte
	seq     uint32
	out     map[uint32][]byte
	mid     uint32
	hasMid  bool
	slot    int
	hasSlot bool
	joined  bool
	token   uint32
	last    float64
	last105 []byte // latest short 0x105 frame (ghost template)
	bigBlob []byte // latest big 0x105 blob (start stats/AI blob)
	maxSent uint32
}

func newClient(addr *net.UDPAddr) *client {
	return &client{
		addr: addr,
		key:  addr.String(),
		got:  map[uint32][]byte{},
		out:  map[uint32][]byte{},
	}
}

// take consumes one decoded packet: drops acked out-messages,
// reassembles the reliable stream in order, and returns the
// complete {u16 len, u16 msgid, body} messages it yields.
func (c *client) take(m packet) []streamMsg {
	for q := range c.out {
		if q <= m.ack {
			delete(c.out, q)
		}
	}
	type sub struct {
		q     uint32
		chunk []byte
	}
	var subs []sub
	if m.typ == 1 {
		subs = append(subs, sub{m.rseq, m.body})
	} else if (m.typ == 2 || m.typ == 0x4001 || m.typ == 0x4004) &&
		len(m.body) > 0 && m.body[0] == 0 {
		p := 1
		for i := 0; i < int(m.rseq); i++ {
			if p+6 > len(m.body) {
				break
			}
			q := binary.LittleEndian.Uint32(m.body[p : p+4])
			n := int(binary.LittleEndian.Uint16(m.body[p+4 : p+6]))
			end := p + 6 + n
			if end > len(m.body) {
				end = len(m.body)
			}
			subs = append(subs, sub{q, m.body[p+6 : end]})
			p += 6 + n
		}
	}
	for _, s := range subs {
		if s.q > c.ack {
			if _, ok := c.got[s.q]; !ok {
				c.got[s.q] = s.chunk
			}
		}
	}
	for {
		chunk, ok := c.got[c.ack+1]
		if !ok {
			break
		}
		c.ack++
		c.stream = append(c.stream, chunk...)
		delete(c.got, c.ack)
	}
	var msgs []streamMsg
	for len(c.stream) >= 4 {
		n := int(binary.LittleEndian.Uint16(c.stream[0:2]))
		if len(c.stream) < 4+n {
			break
		}
		msgid := binary.LittleEndian.Uint16(c.stream[2:4])
		body := append([]byte(nil), c.stream[4:4+n]...)
		msgs = append(msgs, streamMsg{msgid, body})
		c.stream = c.stream[4+n:]
	}
	return msgs
}

// queue appends one message to the reliable out-stream.
func (c *client) queue(msgid uint16, body []byte) {
	c.seq++
	msg := binary.LittleEndian.AppendUint16(nil, uint16(len(body)))
	msg = binary.LittleEndian.AppendUint16(msg, msgid)
	msg = append(msg, body...)
	c.out[c.seq] = msg
}

func (c *client) slotOr0() int {
	if c.hasSlot {
		return c.slot
	}
	return 0
}

func (c *client) midOr0() uint32 {
	if c.hasMid {
		return c.mid
	}
	return 0
}

// ----------------------------------------------------------------
// Relay state. The Python relay is effectively single-threaded
// for all of this (one packet loop + ticks on the same thread);
// this port keeps the same model — every access below happens on
// the main loop goroutine.

type matchKey struct {
	mid  uint32
	slot int
}

type matchState struct {
	ready         map[int]bool
	started       bool
	sentAt        float64
	sends         int
	notifyAfter   float64
	resolved      bool
	resolvedSlots map[int]bool
}

type held300 struct {
	order  []int
	bodies map[int][]byte
}

type ghostState struct {
	slot          int
	blobMode      bool
	template      []byte
	counter       uint32
	winnerCounter uint32
}

type authEntry struct {
	ok     bool
	expiry float64
}

type relay struct {
	conn *net.UDPConn

	clients     map[string]*client
	clientOrder []string
	byMatch     map[matchKey]string
	matchStart  map[uint32]*matchState
	pending200  map[uint32]float64
	matchSval   map[uint32]uint32
	pending300  map[uint32]*held300
	blob2seen   map[uint32]map[int]bool
	pending200b map[uint32]float64
	ghosts      map[uint32]*ghostState
	matchUsers  map[uint32]int
	announced   map[uint32]bool
	muted       map[string]float64
	pingCounts  map[string]int

	authCache  map[string]authEntry
	authChecks []float64
}

func newRelay() *relay {
	return &relay{
		clients:     map[string]*client{},
		byMatch:     map[matchKey]string{},
		matchStart:  map[uint32]*matchState{},
		pending200:  map[uint32]float64{},
		matchSval:   map[uint32]uint32{},
		pending300:  map[uint32]*held300{},
		blob2seen:   map[uint32]map[int]bool{},
		pending200b: map[uint32]float64{},
		ghosts:      map[uint32]*ghostState{},
		matchUsers:  map[uint32]int{},
		announced:   map[uint32]bool{},
		muted:       map[string]float64{},
		pingCounts:  map[string]int{},
		authCache:   map[string]authEntry{},
	}
}

// ----------------------------------------------------------------
// B1 joinauth (v5.18): ask Go whether (mid, slot) may be claimed
// from ip. Verdicts cached per (mid, slot, ip) — positives for
// the join window, negatives briefly. Fail-closed.

type joinauthReq struct {
	Mid  string `json:"mid"`
	Slot int    `json:"slot"`
	IP   string `json:"ip"`
}

func (r *relay) joinauth(mid uint32, slot int, ip string) bool {
	key := fmt.Sprintf("%d|%d|%s", mid, slot, ip)
	now := nowSec()
	if hit, ok := r.authCache[key]; ok && hit.expiry > now {
		return hit.ok
	}
	for len(r.authChecks) > 0 && r.authChecks[0] < now-10 {
		r.authChecks = r.authChecks[1:]
	}
	if len(r.authChecks) >= 20 {
		logLine(fmt.Sprintf("JOIN DENIED mid %d slot %d ip %s (joinauth flood cap)", mid, slot, ip))
		r.authCache[key] = authEntry{false, now + 30}
		return false
	}
	r.authChecks = append(r.authChecks, now)
	ok := false
	data, _ := json.Marshal(joinauthReq{Mid: strconv.FormatUint(uint64(mid), 10), Slot: slot, IP: ip})
	req, err := http.NewRequest(http.MethodPost, goJoinauthURL, strings.NewReader(string(data)))
	if err == nil {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Internal-Key", internalKey)
		hc := &http.Client{Timeout: 2 * time.Second}
		var resp *http.Response
		resp, err = hc.Do(req)
		if err == nil {
			defer resp.Body.Close()
			if resp.StatusCode != 200 {
				err = fmt.Errorf("http status %s", resp.Status)
			} else {
				var body struct {
					OK bool `json:"ok"`
				}
				if derr := json.NewDecoder(resp.Body).Decode(&body); derr == nil {
					ok = body.OK
				}
			}
		}
	}
	if err != nil {
		logLine(fmt.Sprintf("joinauth mid %d slot %d ip %s failed: %v -> deny", mid, slot, ip, err))
	}
	if !ok {
		logLine(fmt.Sprintf("JOIN DENIED mid %d slot %d ip %s (joinauth)", mid, slot, ip))
	}
	ttl := 30.0
	if ok {
		ttl = 600
	}
	r.authCache[key] = authEntry{ok, now + ttl}
	return ok
}

// membersOf returns all joined clients of a match, ordered by
// slot (v5.17: matches seat 2..6 engines, not just two).
func (r *relay) membersOf(mid uint32) []*client {
	var out []*client
	for k, addrKey := range r.byMatch {
		if k.mid == mid {
			if cc := r.clients[addrKey]; cc != nil && cc.joined {
				out = append(out, cc)
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].slotOr0() < out[j].slotOr0()
	})
	return out
}

// expectedUsers is how many engines the match should seat (from
// the JOIN bodies); 2 when unknown.
func (r *relay) expectedUsers(mid uint32) int {
	if n, ok := r.matchUsers[mid]; ok && n != 0 {
		return n
	}
	return 2
}

// flushOut (re)sends everything the client has not acked yet.
func (r *relay) flushOut(c *client) {
	seqs := make([]int, 0, len(c.out))
	for q := range c.out {
		seqs = append(seqs, int(q))
	}
	sort.Ints(seqs)
	for _, qi := range seqs {
		q := uint32(qi)
		msg := c.out[q]
		plain := append(header(relaySlot, c.slotOr0(), c.midOr0(), c.ack, 1, q, 0), msg...)
		// (header args: src, dst, mid, ack, typ=1, rseq=q, token=0)
		if _, err := r.conn.WriteToUDP(encrypt(plain), c.addr); err != nil {
			logLine(fmt.Sprintf("flush_out to %s seq %d failed: %v", addrStr(c.addr), q, err))
			continue
		}
		if q > c.maxSent {
			c.maxSent = q
			logLine(fmt.Sprintf("-> %s seq %d msg %s (%dB, first send)",
				addrStr(c.addr), q, hex.EncodeToString(msg[2:4]), len(msg)))
		}
	}
}

// ----------------------------------------------------------------
// handlePacket processes one decoded packet (Python:
// handle_packet). All state access is on the main loop goroutine,
// mirroring the Python relay's single-threaded packet loop.

func (r *relay) handlePacket(data []byte, addr *net.UDPAddr) {
	key := addr.String()
	if exp, ok := r.muted[key]; ok && exp > nowSec() {
		return // v5.14: forfeited side / released survivor: total silence
	}
	m := parse(decrypt(data))
	c, ok := r.clients[key]
	if !ok {
		c = newClient(addr)
		r.clients[key] = c
		r.clientOrder = append(r.clientOrder, key)
	}
	c.last = nowSec()
	if m.typ == 0x4005 {
		n := r.pingCounts[key] + 1
		r.pingCounts[key] = n
		if n == 1 || n%15 == 0 {
			logLine(fmt.Sprintf("%s ping x%d (mid %d, slot %d, stream ack %d, joined %s)",
				addrStr(addr), n, m.mid, m.src, m.ack, pyBool(c.joined)))
		}
	}
	if m.typ != 0x4005 && m.typ != 0x4006 {
		logLine(fmt.Sprintf("%s <- src %d dst %d mid %d rseq %d ack %d token %d type %s body %s",
			addrStr(addr), m.src, m.dst, m.mid, m.rseq, m.ack, m.token,
			hex06(uint32(m.typ)), hex.EncodeToString(m.body)))
	}
	for _, msg := range c.take(m) {
		msgid, body := msg.id, msg.body
		logLine(fmt.Sprintf("%s <- msg %s %s", addrStr(addr),
			hex06(uint32(msgid)), hex.EncodeToString(body)))
		if msgid == 0x105 && len(body) >= 7 && len(body) <= 64 {
			c.last105 = body
		}
		if msgid == 0x105 && len(body) > 100 {
			c.bigBlob = body
		}
		switch {
		case msgid == 0x100: // JOIN {u32 matchid, u8 users, u8 slot, u8 flag}
			var jmid uint32
			var users, jslot int
			if len(body) >= 7 {
				jmid = binary.LittleEndian.Uint32(body[0:4])
				users = int(body[4])
				jslot = int(body[5])
			} else {
				jmid, jslot = m.mid, m.src
			}
			// B1 (v5.18): the first JOIN of a (match, slot) must
			// come from the IP the match recorded for that slot's
			// coach; Go arbitrates via joinauth. A denied JOIN is
			// dropped silently — no token, no inited.
			denied := (!c.joined || !c.hasMid || c.mid != jmid ||
				!c.hasSlot || c.slot != jslot) &&
				!r.joinauth(jmid, jslot, addr.IP.String())
			if denied {
				continue
			}
			c.mid, c.hasMid = jmid, true
			c.slot, c.hasSlot = jslot, true
			c.joined = true
			r.byMatch[matchKey{jmid, jslot}] = key
			if len(body) >= 7 && users != 0 {
				if cur, ok := r.matchUsers[jmid]; !ok || users > cur {
					r.matchUsers[jmid] = users
				}
			}
			// Token = slot + 1, unique per session user (two
			// engines sharing token 1 trapped the start
			// serializer on both engines, dumps 2026-11-13).
			c.token = uint32(jslot + 1)
			c.queue(0x100, binary.LittleEndian.AppendUint32(nil, c.token))
			// 0x102: one session value per match (unix seconds
			// at first JOIN), identical for all engines.
			sv, ok := r.matchSval[jmid]
			if !ok {
				sv = uint32(time.Now().Unix())
				r.matchSval[jmid] = sv
			}
			c.queue(0x102, binary.LittleEndian.AppendUint32(nil, sv))
			c.queue(0x101, nil) // inited
			logLine(fmt.Sprintf("%s JOIN mid %d slot %d -> token %d + inited queued",
				addrStr(addr), jmid, jslot, c.token))
			mem := r.membersOf(jmid)
			if len(mem) >= 2 {
				logLine(fmt.Sprintf("match %d: %d/%d engines joined (latest %s slot %d)",
					jmid, len(mem), r.expectedUsers(jmid), addrStr(addr), jslot))
			}
			if len(mem) >= r.expectedUsers(jmid) && !r.announced[jmid] {
				if _, pending := r.pending200[jmid]; !pending {
					r.pending200[jmid] = nowSec() + 0.5
				}
			}
		case msgid == 0x400:
			// "Loading finished" — addressed to the SESSION
			// SERVER, never forwarded (the client stream handler
			// has no 0x400 case; forwarding it trapped both
			// engines). Announce this user's readiness to ALL
			// engines as 0x303 {slot}; when all are ready, send
			// 0x302 ("start now") to all. 0x202 is NEVER sent.
			st, ok := r.matchStart[c.midOr0()]
			if !ok {
				st = &matchState{ready: map[int]bool{}}
				r.matchStart[c.midOr0()] = st
			}
			st.ready[c.slot] = true
			mem := r.membersOf(c.midOr0())
			for _, cc := range mem {
				cc.queue(0x303, []byte{byte(c.slot)})
				r.flushOut(cc)
			}
			logLine(fmt.Sprintf("match %d: 0x303 ready-announce slot %d sent", c.midOr0(), c.slot))
			readySlots := make([]int, 0, len(st.ready))
			for s := range st.ready {
				readySlots = append(readySlots, s)
			}
			sort.Ints(readySlots)
			logLine(fmt.Sprintf("%s 0x400 ready (slot %d); match %d ready slots %s",
				addrStr(addr), c.slot, c.midOr0(), intList(readySlots)))
			if !st.started && c.hasMid && len(mem) >= r.expectedUsers(c.mid) {
				all := true
				for _, cc := range mem {
					if !st.ready[cc.slot] {
						all = false
						break
					}
				}
				if all {
					st.started = true
					st.sentAt = nowSec()
					st.sends = 1
					for _, cc := range mem {
						cc.queue(0x302, nil)
						r.flushOut(cc)
					}
					logLine(fmt.Sprintf("match %d: all ready -> 0x302 sent to all", c.mid))
				}
			}
		case msgid == 0x300:
			// RENDEZVOUS BARRIER (v5.8): hold each 0x300 until
			// ALL seated engines have checked in, then release:
			// each engine receives every OTHER engine's 0x300.
			held, ok := r.pending300[c.midOr0()]
			if !ok {
				held = &held300{bodies: map[int][]byte{}}
				r.pending300[c.midOr0()] = held
			}
			if _, seen := held.bodies[c.slot]; !seen {
				held.order = append(held.order, c.slot)
			}
			held.bodies[c.slot] = body
			heldSlots := make([]int, 0, len(held.bodies))
			for s := range held.bodies {
				heldSlots = append(heldSlots, s)
			}
			sort.Ints(heldSlots)
			logLine(fmt.Sprintf("%s 0x300 held at barrier (match %d, slots %s)",
				addrStr(addr), c.midOr0(), intList(heldSlots)))
			mem := r.membersOf(c.midOr0())
			if c.hasMid && len(mem) >= r.expectedUsers(c.mid) {
				all := true
				for _, cc := range mem {
					if _, ok := held.bodies[cc.slot]; !ok {
						all = false
						break
					}
				}
				if all {
					for _, cc := range mem {
						for _, slot := range held.order {
							if slot != cc.slot {
								cc.queue(0x300, held.bodies[slot])
							}
						}
						r.flushOut(cc)
					}
					delete(r.pending300, c.mid)
					logLine(fmt.Sprintf("match %d: 0x300 barrier released to all %d engines",
						c.mid, len(mem)))
				}
			}
		default:
			var mem []*client
			if c.hasMid {
				mem = r.membersOf(c.mid)
			}
			if len(mem) >= 2 {
				for _, cc := range mem {
					if cc != c {
						cc.queue(msgid, body)
						r.flushOut(cc)
					}
				}
				logLine(fmt.Sprintf("route msg %s %s -> %d peers (%dB)",
					hex06(uint32(msgid)), addrStr(addr), len(mem)-1, len(body)))
				// v5.13 ECHO: also deliver the message back to
				// its SENDER — the session synch module feeds
				// its lockstep ring ONLY from inbound
				// redistribution, so without the echo both
				// engines starve waiting for their own inputs.
				c.queue(msgid, body)
				logLine(fmt.Sprintf("echo msg %s -> sender %s (%dB)",
					hex06(uint32(msgid)), addrStr(addr), len(body)))
				r.flushOut(c)
				// v5.15 GHOST: track the survivor's step
				// counter and top up fabricated ghost frames.
				if g, ok := r.ghosts[c.mid]; ok && msgid == 0x105 &&
					c.slot != g.slot && len(body) >= 7 {
					if wc := binary.LittleEndian.Uint32(body[3:7]); wc > g.winnerCounter {
						g.winnerCounter = wc
					}
					r.pumpGhost(c.mid)
				}
				// v5.12: watch for each engine's second start
				// blob; when all have crossed, arm the
				// post-start 0x200 re-announce.
				if msgid == 0x105 && len(body) > 100 && body[0] == 0x02 &&
					binary.LittleEndian.Uint32(body[3:7]) == 1 {
					seen, ok := r.blob2seen[c.mid]
					if !ok {
						seen = map[int]bool{}
						r.blob2seen[c.mid] = seen
					}
					seen[c.slot] = true
					if len(seen) >= r.expectedUsers(c.mid) {
						if _, armed := r.pending200b[c.mid]; !armed {
							r.pending200b[c.mid] = nowSec() + 1.0
							logLine(fmt.Sprintf("match %d: all start blobs crossed; 0x200 re-announce armed", c.mid))
						}
					}
				}
			} else {
				midStr := "None"
				if c.hasMid {
					midStr = strconv.FormatUint(uint64(c.mid), 10)
				}
				logLine(fmt.Sprintf("no peer for msg %s from %s (mid %s)",
					hex06(uint32(msgid)), addrStr(addr), midStr))
			}
		}
	}
	if m.typ == 0x4005 { // ping -> pong, same body
		plain := append(header(relaySlot, m.src, m.mid, c.ack, 0x4006, 0, 0), m.body...)
		_, _ = r.conn.WriteToUDP(encrypt(plain), addr)
	}
	if m.typ == 0x4001 { // wants an ack -> 0x4002, empty body
		_, _ = r.conn.WriteToUDP(encrypt(header(relaySlot, m.src, m.mid, c.ack, 0x4002, 0, 0)), addr)
	}
	r.flushOut(c)
	// GC clients silent for 5 minutes.
	now := nowSec()
	for _, akey := range append([]string(nil), r.clientOrder...) {
		cc, ok := r.clients[akey]
		if !ok || now-cc.last <= 300 {
			continue
		}
		delete(r.clients, akey)
		for i, k := range r.clientOrder {
			if k == akey {
				r.clientOrder = append(r.clientOrder[:i], r.clientOrder[i+1:]...)
				break
			}
		}
		if cc.hasMid {
			delete(r.byMatch, matchKey{cc.mid, cc.slot})
			still := false
			for k := range r.byMatch {
				if k.mid == cc.mid {
					still = true
					break
				}
			}
			if !still {
				delete(r.matchStart, cc.mid)
				delete(r.matchUsers, cc.mid)
				delete(r.announced, cc.mid)
				delete(r.matchSval, cc.mid)
				delete(r.ghosts, cc.mid)
				delete(r.blob2seen, cc.mid)
				delete(r.pending200b, cc.mid)
				delete(r.pending300, cc.mid)
			}
		}
		logLine(fmt.Sprintf("GC client %s", addrStr(cc.addr)))
	}
}

// ----------------------------------------------------------------
// Ticks (Python: tick_announce / tick_forfeit / tick_resend),
// run on socket timeouts and after every packet.

// tickAnnounce fires the 0x200 user-announce matrix ~0.5s after
// all engines joined (v5.5), and the post-start re-announce
// (v5.12). Case 0x200 tells an engine "user S is in the
// session"; every engine must register every OTHER user, so the
// announce is a matrix (the 1v1 case is the 2x2 subset).
func (r *relay) tickAnnounce() {
	now := nowSec()
	for mid, due := range r.pending200 {
		if now < due {
			continue
		}
		delete(r.pending200, mid)
		mem := r.membersOf(mid)
		if len(mem) < 2 {
			continue
		}
		r.announced[mid] = true
		r.announceMatrix(mem)
		logLine(fmt.Sprintf("match %d: 0x200 user-announce matrix sent (%d engines)", mid, len(mem)))
	}
	for mid, due := range r.pending200b {
		if now < due {
			continue
		}
		delete(r.pending200b, mid)
		mem := r.membersOf(mid)
		if len(mem) < 2 {
			continue
		}
		r.announceMatrix(mem)
		logLine(fmt.Sprintf("match %d: 0x200 RE-announce matrix sent (post-start)", mid))
	}
}

func (r *relay) announceMatrix(mem []*client) {
	for _, cc := range mem {
		for _, other := range mem {
			if other.slot != cc.slot {
				cc.queue(0x200, []byte{byte(other.slot)})
			}
		}
		r.flushOut(cc)
	}
}

type forfeitReq struct {
	Mid       string `json:"mid"`
	LoserSlot int    `json:"loser_slot"`
	Ghost     bool   `json:"ghost"`
}

func (r *relay) notifyForfeit(mid uint32, loserSlot int, canGhost bool) bool {
	data, _ := json.Marshal(forfeitReq{
		Mid:       strconv.FormatUint(uint64(mid), 10),
		LoserSlot: loserSlot,
		Ghost:     canGhost,
	})
	req, err := http.NewRequest(http.MethodPost, goForfeitURL, strings.NewReader(string(data)))
	if err != nil {
		logLine(fmt.Sprintf("forfeit notify failed for match %d: %v", mid, err))
		return false
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-Key", internalKey)
	hc := &http.Client{Timeout: 5 * time.Second}
	resp, err := hc.Do(req)
	if err != nil {
		logLine(fmt.Sprintf("forfeit notify failed for match %d: %v", mid, err))
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		logLine(fmt.Sprintf("forfeit notify failed for match %d: http status %s", mid, resp.Status))
		return false
	}
	return true
}

// ghostAhead bounds how far fabricated frames may lead the
// survivor's own step counter (v5.15).
const ghostAhead = 15

// pumpGhost queues fabricated ghost frames on the survivor's
// stream, paced off the survivor's own frames and bounded by
// ghostAhead so the ghost never overruns the synch ring buffer.
func (r *relay) pumpGhost(mid uint32) {
	g, ok := r.ghosts[mid]
	if !ok || g.blobMode {
		return
	}
	n := int(g.winnerCounter) + ghostAhead - int(g.counter)
	if n > ghostAhead {
		n = ghostAhead
	}
	if n <= 0 {
		return
	}
	for k, addrKey := range r.byMatch {
		if k.mid != mid || k.slot == g.slot {
			continue
		}
		wc := r.clients[addrKey]
		if wc == nil {
			return
		}
		for i := 0; i < n; i++ {
			g.counter++
			frame := append([]byte(nil), g.template...)
			binary.LittleEndian.PutUint32(frame[3:7], g.counter)
			wc.queue(0x105, frame)
		}
		r.flushOut(wc)
		return
	}
}

func winnerCounter(c *client, dflt uint32) uint32 {
	if c.last105 != nil && len(c.last105) >= 7 {
		return binary.LittleEndian.Uint32(c.last105[3:7])
	}
	return dflt
}

// tickForfeit implements v5.14 quit detection + ghost takeover:
// a started match where one engine is totally silent for 10s
// while another was seen within 5s means that player quit. Go
// settles the quitter as a 0-5 forfeit loss. In 2-player matches
// the relay synthesizes the quitter's end blob from their start
// blob (their engine hands the team to its local AI), or, with
// only a frame template, ghosts their frames; with no template
// both addresses are muted so the survivor's client is released
// by its own session watchdog. In multi matches Go settles only
// the quitter and the survivors get the end blob; no ghosting.
func (r *relay) tickForfeit() {
	now := nowSec()
	for akey, exp := range r.muted {
		if now >= exp {
			delete(r.muted, akey)
		}
	}
	for mid := range r.ghosts {
		r.pumpGhost(mid)
	}
	if internalKey == "" {
		return
	}
	for mid, ms := range r.matchStart {
		if !ms.started {
			continue
		}
		if now < ms.notifyAfter {
			continue
		}
		members := r.membersOf(mid)
		if len(members) < 2 {
			continue
		}
		multi := len(members) > 2
		if !multi && ms.resolved {
			continue
		}
		if ms.resolvedSlots == nil {
			ms.resolvedSlots = map[int]bool{}
		}
		var silent, active []*client
		for _, c := range members {
			if now-c.last >= 10 && !ms.resolvedSlots[c.slot] {
				silent = append(silent, c)
			}
			if now-c.last <= 5 {
				active = append(active, c)
			}
		}
		if len(silent) == 0 || len(active) == 0 {
			continue
		}
		loser, winner := silent[0], active[0]
		canGhost := loser.last105 != nil
		logLine(fmt.Sprintf("match %d: quit detected - slot %d silent %.0fs, slot %d active; ghost=%s multi=%s",
			mid, loser.slot, now-loser.last, winner.slot,
			pyBool(canGhost), pyBool(multi)))
		ghostArg := canGhost
		if multi {
			ghostArg = true
		}
		if !r.notifyForfeit(mid, loser.slot, ghostArg) {
			ms.notifyAfter = now + 5
			continue
		}
		ms.resolvedSlots[loser.slot] = true
		if multi {
			if loser.bigBlob != nil && canGhost {
				nxt := binary.LittleEndian.Uint32(loser.last105[3:7]) + 1
				blob := append([]byte(nil), loser.bigBlob...)
				binary.LittleEndian.PutUint32(blob[3:7], nxt)
				for _, cc := range members {
					if cc != loser {
						cc.queue(0x105, blob)
						r.flushOut(cc)
					}
				}
				logLine(fmt.Sprintf("match %d: end-blob for slot %d sent to the survivors", mid, loser.slot))
			}
			continue
		}
		ms.resolved = true
		if canGhost && loser.bigBlob != nil {
			// v5.16 END-BLOB: synthesize the quitter's final
			// stats blob from their own start blob, step
			// counter patched to the next step.
			nxt := binary.LittleEndian.Uint32(loser.last105[3:7]) + 1
			blob := append([]byte(nil), loser.bigBlob...)
			binary.LittleEndian.PutUint32(blob[3:7], nxt)
			winner.queue(0x105, blob)
			r.flushOut(winner)
			r.ghosts[mid] = &ghostState{
				slot:          loser.slot,
				blobMode:      true,
				winnerCounter: winnerCounter(winner, 0),
			}
			logLine(fmt.Sprintf("match %d: end-blob sent to slot %d (counter %d)", mid, winner.slot, nxt))
		} else if canGhost {
			start := binary.LittleEndian.Uint32(loser.last105[3:7])
			r.ghosts[mid] = &ghostState{
				slot:          loser.slot,
				template:      loser.last105,
				counter:       start,
				winnerCounter: winnerCounter(winner, start),
			}
			logLine(fmt.Sprintf("match %d: ghosting slot %d from counter %d", mid, loser.slot, start))
		} else {
			r.muted[loser.key] = now + 180
			r.muted[winner.key] = now + 180
			logLine(fmt.Sprintf("match %d: no ghost template; both muted, survivor released by watchdog", mid))
		}
	}
}

// tickResend re-sends 0x302 once, ~2s after the first send
// (kept from v5.1). 0x106 is NEVER sent: it is a
// session-finalising transition, not a start signal (the
// 0x106 experiments are closed, v5.2/v5.3.1).
func (r *relay) tickResend() {
	now := nowSec()
	for mid, st := range r.matchStart {
		if !st.started || st.sends >= 2 {
			continue
		}
		if now-st.sentAt < 2.0 {
			continue
		}
		st.sends++
		for k, addrKey := range r.byMatch {
			if k.mid != mid {
				continue
			}
			if cc := r.clients[addrKey]; cc != nil && cc.joined {
				cc.queue(0x302, nil)
				r.flushOut(cc)
			}
		}
		logLine(fmt.Sprintf("match %d: 0x302 re-sent (attempt %d)", mid, st.sends))
	}
}

func (r *relay) ticks() {
	defer func() {
		if e := recover(); e != nil {
			logLine(fmt.Sprintf("tick error: %v", e))
		}
	}()
	r.tickResend()
	r.tickAnnounce()
	r.tickForfeit()
}

// ----------------------------------------------------------------
// Main UDP loop (Python: udp_relay) + the log-only listeners.

func (r *relay) run() {
	conn, err := net.ListenUDP("udp", &net.UDPAddr{Port: listenPort})
	if err != nil {
		fmt.Fprintf(os.Stderr, "bind UDP/%d failed: %v\n", listenPort, err)
		os.Exit(1)
	}
	r.conn = conn
	logLine(fmt.Sprintf("UDP/%d listening (session relay v5.18)", listenPort))
	buf := make([]byte, 65536)
	for {
		_ = conn.SetReadDeadline(time.Now().Add(400 * time.Millisecond))
		n, addr, err := conn.ReadFromUDP(buf)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				r.ticks()
				continue
			}
			continue // OSError in Python: continue
		}
		data := append([]byte(nil), buf[:n]...)
		if len(data) < 24 || checksum(data[2:]) != binary.LittleEndian.Uint16(data[:2]) {
			logLine(fmt.Sprintf("%s bad packet %s", addrStr(addr), hex.EncodeToString(data)))
			continue
		}
		func() {
			defer func() {
				if e := recover(); e != nil {
					// One bad packet must not kill the relay.
					logLine(fmt.Sprintf("handler error for %s: %v", addrStr(addr), e))
				}
			}()
			r.handlePacket(data, addr)
		}()
		// The delayed sends must not depend on socket timeouts:
		// during a live match the packet stream is dense enough
		// that a 0.4s timeout may never fire. Tick on every
		// packet too.
		r.ticks()
	}
}

func udpLogOnly(port int) {
	conn, err := net.ListenUDP("udp", &net.UDPAddr{Port: port})
	if err != nil {
		fmt.Fprintf(os.Stderr, "udp_log_only bind %d failed: %v\n", port, err)
		return
	}
	buf := make([]byte, 4096)
	for {
		n, addr, err := conn.ReadFromUDP(buf)
		if err != nil {
			continue
		}
		parts := make([]string, n)
		for i := 0; i < n; i++ {
			parts[i] = fmt.Sprintf("%02x", buf[i])
		}
		logLine(fmt.Sprintf("UDP/%d %s:%d %dB: %s", port, addr.IP.String(), addr.Port, n, strings.Join(parts, " ")))
	}
}

func tcpLogOnly(port int) {
	ln, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", port))
	if err != nil {
		fmt.Fprintf(os.Stderr, "tcp_log_only bind %d failed: %v\n", port, err)
		return
	}
	for {
		conn, err := ln.Accept()
		if err != nil {
			continue
		}
		go func(conn net.Conn, addr net.Addr) {
			defer conn.Close()
			if ta, ok := addr.(*net.TCPAddr); ok {
				logLine(fmt.Sprintf("TCP/%d CONNECT from %s:%d", port, ta.IP.String(), ta.Port))
			}
			_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
			tmp := make([]byte, 4096)
			for {
				if _, err := conn.Read(tmp); err != nil {
					return
				}
			}
		}(conn, conn.RemoteAddr())
	}
}

func main() {
	if raw, err := os.ReadFile("/etc/fo3/internal.key"); err == nil {
		internalKey = strings.TrimSpace(string(raw))
	}
	r := newRelay()
	if auxEnabled {
		go udpLogOnly(28001)
		go tcpLogOnly(28000)
		go tcpLogOnly(28001)
	}
	r.run()
}
