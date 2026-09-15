// Package ws implements the WebSocket fan-out hub for live telemetry.
//
// One hub, many subscribers. Ingest/API publish model.Event values;
// the hub serializes and broadcasts with backpressure protection
// (slow clients get dropped, counted in self-metrics — never block ingestion).
package ws

import (
	"sync"
	"sync/atomic"

	"github.com/gorilla/websocket"
	"github.com/infracity/infracity/pkg/model"
	"github.com/rs/zerolog/log"
)

type Hub struct {
	mu        sync.RWMutex
	clients   map[*websocket.Conn]chan []byte
	broadcast chan []byte

	connections atomic.Int64
	dropped     atomic.Int64
}

func NewHub() *Hub {
	h := &Hub{
		clients:   make(map[*websocket.Conn]chan []byte),
		broadcast: make(chan []byte, 4096),
	}
	go h.run()
	return h
}

func (h *Hub) run() {
	for msg := range h.broadcast {
		h.mu.RLock()
		for conn, ch := range h.clients {
			select {
			case ch <- msg:
			default:
				// slow client: drop message, count it
				h.dropped.Add(1)
				_ = conn
			}
		}
		h.mu.RUnlock()
	}
}

// Publish marshalled payload to all subscribers (non-blocking).
func (h *Hub) Publish(payload []byte) {
	select {
	case h.broadcast <- payload:
	default:
		h.dropped.Add(1)
	}
}

// PublishEvent is a convenience wrapper (caller marshals to keep hub lean).
func (h *Hub) Add(conn *websocket.Conn) chan []byte {
	h.mu.Lock()
	defer h.mu.Unlock()
	ch := make(chan []byte, 256)
	h.clients[conn] = ch
	h.connections.Add(1)
	return ch
}

func (h *Hub) Remove(conn *websocket.Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if ch, ok := h.clients[conn]; ok {
		delete(h.clients, conn)
		close(ch)
		h.connections.Add(-1)
		log.Debug().Msg("ws client disconnected")
	}
}

func (h *Hub) Stats() (connections int64, dropped int64) {
	return h.connections.Load(), h.dropped.Load()
}

var _ = model.EvMetricUpdate
