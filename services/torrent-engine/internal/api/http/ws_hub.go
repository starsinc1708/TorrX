package apihttp

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
	"torrentstream/internal/domain"
)

type wsMessage struct {
	Type string      `json:"type"`
	Data interface{} `json:"data"`
}

type wsClient struct {
	hub  *wsHub
	conn *websocket.Conn
	send chan []byte
}

type wsHub struct {
	clients    map[*wsClient]bool
	broadcast  chan []byte
	register   chan *wsClient
	unregister chan *wsClient
	done       chan struct{}
	logger     *slog.Logger
}

func newWSHub(logger *slog.Logger) *wsHub {
	return &wsHub{
		clients:    make(map[*wsClient]bool),
		broadcast:  make(chan []byte, 64),
		register:   make(chan *wsClient),
		unregister: make(chan *wsClient),
		done:       make(chan struct{}),
		logger:     logger,
	}
}

func (h *wsHub) run() {
	for {
		select {
		case <-h.done:
			for client := range h.clients {
				_ = client.conn.WriteControl(
					websocket.CloseMessage,
					websocket.FormatCloseMessage(websocket.CloseGoingAway, "server shutting down"),
					time.Now().Add(2*time.Second),
				)
				close(client.send)
				delete(h.clients, client)
			}
			h.logger.Debug("ws hub stopped, all clients disconnected")
			return
		case client := <-h.register:
			h.clients[client] = true
			h.logger.Debug("ws client connected", slog.Int("total", len(h.clients)))
		case client := <-h.unregister:
			if _, ok := h.clients[client]; ok {
				delete(h.clients, client)
				close(client.send)
				h.logger.Debug("ws client disconnected", slog.Int("total", len(h.clients)))
			}
		case msg := <-h.broadcast:
			for client := range h.clients {
				select {
				case client.send <- msg:
				default:
					close(client.send)
					delete(h.clients, client)
				}
			}
		}
	}
}

// Close signals the hub to stop and disconnect all clients.
func (h *wsHub) Close() {
	close(h.done)
}

func (h *wsHub) clientCount() int {
	return len(h.clients)
}

// BroadcastStates sends the full state list to all connected clients.
func (h *wsHub) BroadcastStates(states []domain.SessionState) {
	if len(h.clients) == 0 {
		return
	}
	msg := wsMessage{Type: "states", Data: states}
	data, err := json.Marshal(msg)
	if err != nil {
		h.logger.Error("ws marshal failed", slog.String("error", err.Error()))
		return
	}
	select {
	case h.broadcast <- data:
	default:
		// Broadcast channel full, skip this update.
	}
}

// Broadcast sends a typed JSON message to all connected WebSocket clients.
func (h *wsHub) Broadcast(msgType string, data interface{}) {
	if len(h.clients) == 0 {
		return
	}
	msg := wsMessage{Type: msgType, Data: data}
	payload, err := json.Marshal(msg)
	if err != nil {
		h.logger.Error("ws marshal failed", slog.String("error", err.Error()))
		return
	}
	select {
	case h.broadcast <- payload:
	default:
	}
}

var wsUpgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin:     func(r *http.Request) bool { return true },
}

func (c *wsClient) writePump() {
	ticker := time.NewTicker(30 * time.Second)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()
	for {
		select {
		case msg, ok := <-c.send:
			_ = c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if !ok {
				_ = c.conn.WriteMessage(websocket.CloseMessage, nil)
				return
			}
			if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		case <-ticker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

func (c *wsClient) readPump() {
	defer func() {
		c.hub.unregister <- c
		c.conn.Close()
	}()
	c.conn.SetReadLimit(512)
	_ = c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	c.conn.SetPongHandler(func(string) error {
		_ = c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})
	for {
		_, _, err := c.conn.ReadMessage()
		if err != nil {
			break
		}
	}
}
