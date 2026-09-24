// Package ws implements a small WebSocket hub that lets Flutter clients
// subscribe to live updates for one order (status changes and, once a
// driver is assigned, its location) without polling. The gateway's own
// Kafka consumer feeds Broadcast; this package has no Kafka knowledge of
// its own.
package ws

import (
	"context"
	"log/slog"
	"net/http"
	"sync"

	"github.com/coder/websocket"
)

// Hub fans out messages to every client subscribed to a room. A room is an
// orderId: every client tracking that order's delivery joins the same room
// and receives the same broadcasts.
type Hub struct {
	mu    sync.RWMutex
	rooms map[string]map[*client]struct{}
	log   *slog.Logger
}

type client struct {
	conn *websocket.Conn
	send chan []byte
}

func NewHub(log *slog.Logger) *Hub {
	return &Hub{rooms: map[string]map[*client]struct{}{}, log: log}
}

// Broadcast sends msg to every client currently subscribed to room.
func (h *Hub) Broadcast(room string, msg []byte) {
	h.mu.RLock()
	clients := h.rooms[room]
	targets := make([]*client, 0, len(clients))
	for c := range clients {
		targets = append(targets, c)
	}
	h.mu.RUnlock()

	for _, c := range targets {
		select {
		case c.send <- msg:
		default:
			h.log.Warn("dropping message to slow websocket client", "room", room)
		}
	}
}

// ServeHTTP upgrades the connection and joins it to the room named by the
// "orderId" query parameter, per docs/API-CONTRACT.md.
func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	room := r.URL.Query().Get("orderId")
	if room == "" {
		http.Error(w, `{"error":"bad_request","message":"orderId query parameter is required"}`, http.StatusBadRequest)
		return
	}

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: []string{"*"}})
	if err != nil {
		h.log.Error("websocket accept", "error", err)
		return
	}

	c := &client{conn: conn, send: make(chan []byte, 16)}
	h.join(room, c)
	defer h.leave(room, c)

	ctx := r.Context()
	go h.writeLoop(ctx, conn, c)
	h.readLoop(ctx, conn) // blocks until the client disconnects or sends a close frame
}

func (h *Hub) join(room string, c *client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.rooms[room] == nil {
		h.rooms[room] = map[*client]struct{}{}
	}
	h.rooms[room][c] = struct{}{}
}

func (h *Hub) leave(room string, c *client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.rooms[room], c)
	if len(h.rooms[room]) == 0 {
		delete(h.rooms, room)
	}
	close(c.send)
}

func (h *Hub) writeLoop(ctx context.Context, conn *websocket.Conn, c *client) {
	for msg := range c.send {
		if err := conn.Write(ctx, websocket.MessageText, msg); err != nil {
			return
		}
	}
}

// readLoop only exists to detect disconnects (a client-initiated close or a
// dropped connection) — this hub is push-only, so any inbound message is
// discarded.
func (h *Hub) readLoop(ctx context.Context, conn *websocket.Conn) {
	for {
		if _, _, err := conn.Read(ctx); err != nil {
			return
		}
	}
}
