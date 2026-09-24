// Package ws implements a small WebSocket hub that lets Flutter clients
// subscribe to live updates for one order (status changes and, once a
// driver is assigned, its location) without polling. The gateway's own
// Kafka consumer feeds Broadcast; this package has no Kafka knowledge of
// its own.
package ws

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"

	"github.com/coder/websocket"

	"github.com/rainercoetzer/delivery-platform/pkg/auth"
)

// Hub fans out messages to every client subscribed to a room. A room is an
// orderId: every client tracking that order's delivery joins the same room
// and receives the same broadcasts.
type Hub struct {
	mu    sync.RWMutex
	rooms map[string]map[*client]struct{}
	log   *slog.Logger

	// OriginPatterns are hosts allowed to open a socket (coder/websocket
	// matches them against the Origin header's host, not the full URL).
	// Same-origin requests are always allowed.
	OriginPatterns []string
	// Authorize reports whether the bearer of token may watch orderID,
	// returning an HTTP status: 200 to allow, anything else to refuse with
	// that status. Required: a hub without it refuses every connection.
	Authorize func(ctx context.Context, token, orderID string) int
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

// ServeHTTP authenticates and authorizes the caller, then upgrades the
// connection and joins it to the room named by the "orderId" query
// parameter. Before this, anyone with an order id could stream the
// driver's live GPS (audit P0-7).
//
// The token comes from ?token= because browsers can't set headers on a
// WebSocket handshake; an Authorization header works too. Trap: never log
// r.URL.RawQuery here or in middleware, it contains the token.
// (httpx.RequestLogger logs only the path.)
func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	room := r.URL.Query().Get("orderId")
	if room == "" {
		writeErr(w, http.StatusBadRequest, "bad_request", "orderId query parameter is required")
		return
	}
	token := r.URL.Query().Get("token")
	if token == "" {
		token, _ = auth.BearerToken(r.Header.Get("Authorization"))
	}
	if token == "" {
		writeErr(w, http.StatusUnauthorized, "unauthorized", "token query parameter is required")
		return
	}
	if h.Authorize == nil {
		writeErr(w, http.StatusInternalServerError, "internal_error", "websocket authorization not configured")
		return
	}
	switch status := h.Authorize(r.Context(), token, room); status {
	case http.StatusOK:
	case http.StatusUnauthorized:
		writeErr(w, status, "unauthorized", "invalid or expired token")
		return
	case http.StatusForbidden, http.StatusNotFound:
		writeErr(w, http.StatusForbidden, "forbidden", "you can't watch this order")
		return
	default:
		writeErr(w, http.StatusBadGateway, "upstream_unavailable", "could not check access to this order")
		return
	}

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: h.OriginPatterns})
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

func writeErr(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": code, "message": msg})
}
