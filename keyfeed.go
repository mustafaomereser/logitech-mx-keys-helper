//go:build windows

package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// keyFeed streams raw key events from the MX keyboard to the GUI, which lights
// the pressed keys on the keyboard drawing. Events carry the scan code (the
// physical key, independent of the keyboard layout); 0xE000 marks E0 keys.
// Nothing is collected when no GUI is listening.

type keyEvent struct {
	SC   uint16 `json:"sc"`
	Down bool   `json:"d"`
}

var keyFeed = struct {
	sync.Mutex
	subs map[chan keyEvent]bool
}{subs: map[chan keyEvent]bool{}}

func keyFeedActive() bool {
	keyFeed.Lock()
	defer keyFeed.Unlock()
	return len(keyFeed.subs) > 0
}

// publishKey is called from the UI thread (WM_INPUT); it never blocks.
func publishKey(ev keyEvent) {
	keyFeed.Lock()
	for ch := range keyFeed.subs {
		select {
		case ch <- ev:
		default: // a slow listener just misses a frame
		}
	}
	keyFeed.Unlock()
}

func subscribeKeys() (chan keyEvent, func()) {
	ch := make(chan keyEvent, 64)
	keyFeed.Lock()
	keyFeed.subs[ch] = true
	keyFeed.Unlock()
	return ch, func() {
		keyFeed.Lock()
		delete(keyFeed.subs, ch)
		keyFeed.Unlock()
	}
}

// apiKeyStream serves the feed as Server-Sent Events.
func apiKeyStream(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		httpErr(w, "akış desteklenmiyor")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	ch, done := subscribeKeys()
	defer done()
	fmt.Fprint(w, ": ok\n\n")
	fl.Flush()
	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case ev := <-ch:
			b, _ := json.Marshal(ev)
			fmt.Fprintf(w, "data: %s\n\n", b)
			fl.Flush()
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
			fl.Flush()
		}
	}
}
