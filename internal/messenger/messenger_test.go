package messenger

import (
	"encoding/hex"
	"testing"
)

// The fixed frame from messenger.py's selftest: the real
// client's auth message must decode identically.
func TestSelftestVector(t *testing.T) {
	raw, _ := hex.DecodeString("8b412a58950cd8431cb7b2e7473b61a8534a576ac50317310d047b5400622951")
	msg, err := decode(raw)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if msg["cmd"] != "auth" {
		t.Fatalf("cmd = %v", msg["cmd"])
	}
	if v, ok := msg["sessionid"]; !ok || v != "" {
		t.Fatalf("sessionid = %v", v)
	}
}

func TestRoundtrip(t *testing.T) {
	in := map[string]any{"cmd": "push", "uri": "/room/1", "body": map[string]any{"x": 1}}
	frame, err := encode(in)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if frame[2] != 2 || frame[3] != 0 {
		t.Fatalf("type bytes = %v", frame[2:4])
	}
	out, err := decode(frame[4:])
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out["cmd"] != "push" || out["uri"] != "/room/1" {
		t.Fatalf("roundtrip = %v", out)
	}
}
