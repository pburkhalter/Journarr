package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// Broker is a minimal SSE fan-out: every subscriber gets every published
// event. Slow subscribers drop messages instead of blocking publishers —
// clients are expected to refetch on receipt, so a lost patch heals itself
// on the next event or reconnect.
type Broker struct {
	mu     sync.Mutex
	subs   map[chan []byte]struct{}
	done   chan struct{}
	closed bool
}

func NewBroker() *Broker {
	return &Broker{subs: map[chan []byte]struct{}{}, done: make(chan struct{})}
}

// Shutdown releases every connected SSE handler so http.Server.Shutdown can
// complete — without it, one connected dashboard makes every stop take the
// full shutdown timeout.
func (b *Broker) Shutdown() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.closed {
		b.closed = true
		close(b.done)
	}
}

func (b *Broker) Publish(event string, data any) {
	payload, err := json.Marshal(data)
	if err != nil {
		return
	}
	frame := []byte(fmt.Sprintf("event: %s\ndata: %s\n\n", event, payload))
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.subs {
		select {
		case ch <- frame:
		default: // subscriber too slow, drop
		}
	}
}

func (b *Broker) subscribe() chan []byte {
	ch := make(chan []byte, 32)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()
	return ch
}

func (b *Broker) unsubscribe(ch chan []byte) {
	b.mu.Lock()
	delete(b.subs, ch)
	b.mu.Unlock()
}

func (b *Broker) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()

	ch := b.subscribe()
	defer b.unsubscribe(ch)

	heartbeat := time.NewTicker(25 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-b.done:
			return
		case msg := <-ch:
			if _, err := w.Write(msg); err != nil {
				return
			}
			flusher.Flush()
		case <-heartbeat.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}
