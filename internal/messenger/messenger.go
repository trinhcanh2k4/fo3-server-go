// Package messenger is the Go port of messenger.py: the
// Blowfish-CBC framed TCP notification server the Flash client
// connects to (port 28002), the loopback IPC listener the API
// servers push through (28003), and the Redis presence publishing
// (msgonline:<coachid> + heartbeat) the contact list reads.
package messenger

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"log"
	"net"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"golang.org/x/crypto/blowfish"
)

var key = []byte("kueec")

// encode ports messenger.encode: JSON + "\n", PKCS#5 padded,
// Blowfish-CBC with a zero IV, framed <u16 LE len><u16 LE 2>.
func encode(obj any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(obj); err != nil {
		return nil, err
	}
	pt := buf.Bytes() // Encoder already appends "\n"
	pad := 8 - (len(pt) % 8)
	for i := 0; i < pad; i++ {
		pt = append(pt, byte(pad))
	}
	c, err := blowfish.NewCipher(key)
	if err != nil {
		return nil, err
	}
	ct := make([]byte, len(pt))
	var prev [8]byte
	var block [8]byte
	for i := 0; i < len(pt); i += 8 {
		for j := 0; j < 8; j++ {
			block[j] = pt[i+j] ^ prev[j]
		}
		c.Encrypt(ct[i:i+8], block[:])
		copy(prev[:], ct[i:i+8])
	}
	out := make([]byte, 4, 4+len(ct))
	binary.LittleEndian.PutUint16(out[0:], uint16(len(ct)))
	binary.LittleEndian.PutUint16(out[2:], 2)
	return append(out, ct...), nil
}

// decode ports messenger.decode.
func decode(body []byte) (map[string]any, error) {
	c, err := blowfish.NewCipher(key)
	if err != nil {
		return nil, err
	}
	out := make([]byte, len(body))
	var prev [8]byte
	for i := 0; i+8 <= len(body); i += 8 {
		c.Decrypt(out[i:i+8], body[i:i+8])
		for j := 0; j < 8; j++ {
			out[i+j] ^= prev[j]
		}
		copy(prev[:], body[i:i+8])
	}
	if len(out) == 0 {
		return nil, nil
	}
	pad := int(out[len(out)-1])
	if pad < 1 || pad > 8 || pad > len(out) {
		pad = 0
	}
	var msg map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(out[:len(out)-pad]), &msg); err != nil {
		return nil, err
	}
	return msg, nil
}

type Server struct {
	rdb *redis.Client

	mu      sync.Mutex
	clients map[net.Conn]string // conn -> coachid ("" until signon)
}

// New builds a messenger publishing presence to Redis (rdb may be
// nil: presence is then skipped).
func New(rdb *redis.Client) *Server {
	return &Server{rdb: rdb, clients: map[net.Conn]string{}}
}

func (s *Server) presenceOnline(coachid string) {
	if s.rdb != nil && coachid != "" {
		s.rdb.Set(context.Background(), "msgonline:"+coachid, "1", 0)
	}
}

func (s *Server) presenceOffline(coachid string) {
	if s.rdb == nil || coachid == "" {
		return
	}
	s.mu.Lock()
	still := false
	for _, c := range s.clients {
		if c == coachid {
			still = true
			break
		}
	}
	s.mu.Unlock()
	if !still {
		s.rdb.Del(context.Background(), "msgonline:"+coachid)
	}
}

func (s *Server) heartbeat() {
	for {
		if s.rdb != nil {
			ctx := context.Background()
			s.rdb.SetEx(ctx, "msgonline:heartbeat", time.Now().Unix(), 90*time.Second)
			s.mu.Lock()
			seen := map[string]bool{}
			for _, cid := range s.clients {
				if cid != "" {
					seen[cid] = true
				}
			}
			s.mu.Unlock()
			for cid := range seen {
				s.rdb.Set(ctx, "msgonline:"+cid, "1", 0)
			}
		}
		time.Sleep(30 * time.Second)
	}
}

// BroadcastPush ports broadcast_push: targeted when coachid is
// set, else every live connection. Payloads whose push JSON
// exceeds 65,000 bytes are skipped (the client's frame limit).
func (s *Server) BroadcastPush(uri string, body any, coachid string) {
	raw, err := json.Marshal(map[string]any{"cmd": "push", "uri": uri, "body": body})
	if err != nil {
		return
	}
	if len(raw) > 65000 {
		log.Printf("messenger: broadcast_push skipped: payload too large (%d bytes) for %s", len(raw), uri)
		return
	}
	pkt, err := encode(map[string]any{"cmd": "push", "uri": uri, "body": body})
	if err != nil {
		return
	}
	s.mu.Lock()
	var targets []net.Conn
	for conn, cid := range s.clients {
		if coachid == "" || cid == coachid {
			targets = append(targets, conn)
		}
	}
	s.mu.Unlock()
	if coachid != "" && len(targets) == 0 {
		log.Printf("messenger: broadcast_push: no live connection for coach %s (%s)", coachid, uri)
		return
	}
	var dead []net.Conn
	for _, conn := range targets {
		if _, err := conn.Write(pkt); err != nil {
			dead = append(dead, conn)
		}
	}
	for _, conn := range dead {
		s.drop(conn)
	}
}

func (s *Server) drop(conn net.Conn) {
	s.mu.Lock()
	cid := s.clients[conn]
	delete(s.clients, conn)
	s.mu.Unlock()
	_ = conn.Close()
	if cid != "" {
		s.presenceOffline(cid)
	}
}

func replies(msg map[string]any) []map[string]any {
	switch msg["cmd"] {
	case "auth":
		return []map[string]any{{"cmd": "auth", "result": true}}
	case "ping":
		return []map[string]any{{"cmd": "ping"}}
	case "signon":
		return []map[string]any{{"cmd": "sign", "result": true}}
	}
	return nil
}

func (s *Server) handleClient(conn net.Conn) {
	s.mu.Lock()
	s.clients[conn] = ""
	s.mu.Unlock()
	defer s.drop(conn)
	buf := make([]byte, 0, 65536)
	tmp := make([]byte, 65536)
	for {
		n, err := conn.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
			for len(buf) >= 4 {
				ln := int(binary.LittleEndian.Uint16(buf[0:]))
				ty := binary.LittleEndian.Uint16(buf[2:])
				if len(buf) < 4+ln {
					break
				}
				body := buf[4 : 4+ln]
				buf = buf[4+ln:]
				if ty != 2 {
					continue
				}
				msg, derr := decode(body)
				if derr != nil || msg == nil {
					continue
				}
				if msg["cmd"] == "signon" {
					if cid, ok := msg["coachid"].(string); ok && cid != "" {
						s.mu.Lock()
						s.clients[conn] = cid
						s.mu.Unlock()
						s.presenceOnline(cid)
					}
				}
				for _, out := range replies(msg) {
					pkt, err := encode(out)
					if err != nil {
						continue
					}
					if _, err := conn.Write(pkt); err != nil {
						return
					}
				}
			}
		}
		if err != nil {
			return
		}
	}
}

func (s *Server) handleIPC(conn net.Conn) {
	defer conn.Close()
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil && len(line) == 0 {
		return
	}
	var data map[string]any
	if json.Unmarshal(line, &data) != nil {
		return
	}
	uri, _ := data["uri"].(string)
	if uri == "" {
		uri = "/chat/my/channel/messages/latest"
	}
	coachid, _ := data["coachid"].(string)
	s.BroadcastPush(uri, data["body"], coachid)
	_, _ = conn.Write([]byte("{\"ok\":true}\n"))
}

// ListenAndServe starts the client listener, the IPC listener
// and the presence heartbeat. A failed bind is logged and that
// listener skipped (the Python messenger behaves the same), so
// the API server never dies over a port clash.
func (s *Server) ListenAndServe(addr, ipcAddr string) {
	go s.heartbeat()
	if ln, err := net.Listen("tcp", addr); err != nil {
		log.Printf("messenger: listen %s failed: %v", addr, err)
	} else {
		go func() {
			for {
				conn, err := ln.Accept()
				if err != nil {
					return
				}
				go s.handleClient(conn)
			}
		}()
		log.Printf("messenger on %s", addr)
	}
	if ln, err := net.Listen("tcp", ipcAddr); err != nil {
		log.Printf("messenger: IPC listen %s failed: %v", ipcAddr, err)
	} else {
		go func() {
			for {
				conn, err := ln.Accept()
				if err != nil {
					return
				}
				go s.handleIPC(conn)
			}
		}()
		log.Printf("messenger IPC on %s", ipcAddr)
	}
}
