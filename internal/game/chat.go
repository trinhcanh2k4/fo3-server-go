package game

import (
	"sync"
	"time"
)

// World (global) chat channels, ported from fo3_ext.py's GLOBAL_CHAT
// subsystem (GLOBAL_CHAT_CHANNELS, _ensure_in_channel,
// _chat_coach_summary). Channels are process-global in-memory state
// in both generations: membership and the last 60 messages reset on
// restart. The per-user current channel persists in state
// ("chat_channel").

type ChatChannel struct {
	ID       int
	Size     int
	Members  []any
	Messages []any
	Seq      int
}

var (
	chatMu       sync.Mutex
	chatChannels = map[int]*ChatChannel{}
)

func chatChannelLocked(num int) *ChatChannel {
	if num < 1 {
		num = 1
	}
	if num > 100 {
		num = 100
	}
	ch, ok := chatChannels[num]
	if !ok {
		ch = &ChatChannel{ID: num, Size: 100}
		chatChannels[num] = ch
	}
	return ch
}

// ChatCoachSummary ports _chat_coach_summary: the identity the chat
// system shows for the state's default coach.
func ChatCoachSummary(st State) map[string]any {
	slot := DefaultSlot(st)
	c := CoachBySlot(st, slot)
	if c == nil {
		return map[string]any{"id": CoachIDForUser(0, 0), "name": "Manager", "level": 1, "teamid": 1}
	}
	lvl := Int(CoachLevelinfo(c)["level"])
	if lvl == 0 {
		lvl = 1
	}
	id := Str(c["_id"])
	if id == "" {
		id = CoachIDForUser(0, slot)
	}
	teamid := Int(c["teamid"])
	if teamid == 0 {
		teamid = 1
	}
	name := Str(c["name"])
	if name == "" {
		name = "Manager"
	}
	return map[string]any{"id": id, "name": name, "level": lvl, "teamid": teamid}
}

// ChatEnsureInChannel ports _ensure_in_channel: move the coach into
// channel num (dropping them from every other channel) and seed the
// welcome message on an empty channel.
func ChatEnsureInChannel(num int, coachInfo map[string]any) *ChatChannel {
	chatMu.Lock()
	defer chatMu.Unlock()
	ch := chatChannelLocked(num)
	myID := Str(coachInfo["id"])
	for _, mch := range chatChannels {
		kept := mch.Members[:0]
		for _, m := range mch.Members {
			if Str(Map(m)["id"]) != myID {
				kept = append(kept, m)
			}
		}
		mch.Members = kept
	}
	ch.Members = append(ch.Members, coachInfo)
	if len(ch.Messages) == 0 {
		ch.Seq++
		ch.Messages = append(ch.Messages, map[string]any{
			"type":      "chat",
			"coachid":   "000000000000000000000000",
			"coachname": "HỆ THỐNG",
			"message":   "Chào mừng đến Kênh Chat Thế Giới #" + itoa(ch.ID) + " - Server FO3 Online!",
			"hint":      map[string]any{"linkinfo": []any{}},
			"time":      int(time.Now().Unix()),
		})
	}
	return ch
}

// ChatChannelGet returns the channel (creating it empty if needed)
// without touching membership.
func ChatChannelGet(num int) *ChatChannel {
	chatMu.Lock()
	defer chatMu.Unlock()
	return chatChannelLocked(num)
}

// ChatAppendMessage appends a message VO, keeping the last 60.
func ChatAppendMessage(ch *ChatChannel, vo map[string]any) {
	chatMu.Lock()
	defer chatMu.Unlock()
	ch.Seq++
	ch.Messages = append(ch.Messages, vo)
	if len(ch.Messages) > 60 {
		ch.Messages = ch.Messages[len(ch.Messages)-60:]
	}
}

// ChatLatest returns the newest message VO (or the welcome VO when
// the channel is somehow empty).
func ChatLatest(ch *ChatChannel) map[string]any {
	chatMu.Lock()
	defer chatMu.Unlock()
	if len(ch.Messages) > 0 {
		return Map(ch.Messages[len(ch.Messages)-1])
	}
	return map[string]any{
		"type":      "chat",
		"coachid":   "000000000000000000000000",
		"coachname": "HỆ THỐNG",
		"message":   "Chào mừng đến Kênh Chat Thế Giới #" + itoa(ch.ID) + "!",
		"hint":      map[string]any{"linkinfo": []any{}},
		"time":      int(time.Now().Unix()),
	}
}
