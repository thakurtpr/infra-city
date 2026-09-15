package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
	"github.com/infracity/infracity/pkg/model"
	"github.com/rs/zerolog/log"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(_ *http.Request) bool { return true },
}

// ingest receives AgentReport batches. Auth: Bearer <cluster-token> when configured.
func (s *Server) ingest(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	defer func() { s.ingestLatency.Observe(time.Since(start).Seconds()) }()

	if s.authToken != "" {
		if r.Header.Get("Authorization") != "Bearer "+s.authToken {
			writeJSON(w, 401, map[string]string{"error": "unauthorized"})
			return
		}
	}
	var rep model.AgentReport
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<20)).Decode(&rep); err != nil {
		writeJSON(w, 400, map[string]string{"error": "invalid report: " + err.Error()})
		return
	}
	if rep.ClusterID == "" {
		writeJSON(w, 400, map[string]string{"error": "clusterId required"})
		return
	}
	created, updated := 0, 0
	for _, n := range rep.Nodes {
		if n.Cluster == "" {
			n.Cluster = rep.ClusterID
		}
		if s.graph.UpsertNode(n) {
			created++
			s.recordChange("added", "Discovered "+n.Type+" "+n.Name, n.ID)
			s.emit(model.Event{Type: model.EvResourceCreated, Resource: &n, Timestamp: time.Now()})
		} else {
			updated++
		}
	}
	for _, e := range rep.Edges {
		s.graph.UpsertEdge(e)
		s.emit(model.Event{Type: model.EvNetworkFlow, Edge: &e, Timestamp: time.Now()})
	}
	for _, ev := range rep.Events {
		if ev.Timestamp.IsZero() {
			ev.Timestamp = time.Now()
		}
		s.emit(ev)
	}
	s.ingestTotal.Add(1)
	writeJSON(w, 202, map[string]any{"accepted": true, "created": created, "updated": updated})
}

func (s *Server) emit(ev model.Event) {
	b, err := json.Marshal(ev)
	if err != nil {
		s.eventsDropped.Add(1)
		return
	}
	s.hub.Publish(b)
}

func (s *Server) recordChange(kind, summary, nodeID string) {
	s.changesMu.Lock()
	defer s.changesMu.Unlock()
	s.changes = append(s.changes, model.Change{
		ID:        nodeID + "@" + time.Now().UTC().Format(time.RFC3339Nano),
		Timestamp: time.Now().UnixNano(),
		Kind:      kind,
		Summary:   summary,
		NodeID:    nodeID,
	})
	if len(s.changes) > 1000 {
		s.changes = s.changes[len(s.changes)-1000:]
	}
}

// wsEvents streams live events. Query ?cluster= to scope later; currently global.
func (s *Server) wsEvents(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Warn().Err(err).Msg("ws upgrade failed")
		return
	}
	ch := s.hub.Add(conn)
	defer func() {
		s.hub.Remove(conn)
		_ = conn.Close()
	}()
	// hello frame so UI can measure latency
	_ = conn.WriteJSON(map[string]any{"type": "hello", "ts": time.Now().UnixNano()})
	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()
	go func() {
		for range ping.C {
			_ = conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second))
		}
	}()
	for msg := range ch {
		_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
		if err := conn.WriteMessage(websocket.TextMessage, msg); err != nil {
			return
		}
	}
}
