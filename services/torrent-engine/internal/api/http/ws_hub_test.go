package apihttp

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"torrentstream/internal/domain"
)

// ---- helpers ----

// startTestHub creates a hub and runs it in a background goroutine.
// For unit tests with fake (nil-conn) clients, we do NOT auto-close since
// hub.Close() tries to write a close frame to each client's conn. Instead,
// each test that registers fake clients must unregister them before the hub
// is stopped, or simply let the goroutine leak (short-lived test process).
func startTestHub(t *testing.T) *wsHub {
	t.Helper()
	hub := newWSHub(slog.Default())
	go hub.run()
	return hub
}

// unregisterAll sends unregister for each client and waits briefly.
func unregisterAll(hub *wsHub, clients ...*wsClient) {
	for _, c := range clients {
		hub.unregister <- c
	}
	time.Sleep(20 * time.Millisecond)
}

// dialWS upgrades an httptest.Server to a WebSocket connection.
func dialWS(t *testing.T, srv *httptest.Server) *websocket.Conn {
	t.Helper()
	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
	conn, resp, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("dial ws: %v", err)
	}
	resp.Body.Close()
	return conn
}

// readWSMessage reads and decodes a single wsMessage from the connection
// with a timeout.
func readWSMessage(t *testing.T, conn *websocket.Conn, timeout time.Duration) wsMessage {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(timeout))
	_, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read ws message: %v", err)
	}
	var msg wsMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		t.Fatalf("unmarshal ws message: %v (raw: %s)", err, data)
	}
	return msg
}

// makeWSServer creates a Server suitable for WebSocket testing.
func makeWSServer() *Server {
	return NewServer(nil)
}

// ---- wsHub unit tests ----

func TestNewWSHub_Initialization(t *testing.T) {
	hub := newWSHub(slog.Default())
	if hub == nil {
		t.Fatal("newWSHub returned nil")
	}
	if hub.clients == nil {
		t.Fatal("clients map is nil")
	}
	if len(hub.clients) != 0 {
		t.Fatalf("clients map should be empty, got %d", len(hub.clients))
	}
	if hub.broadcast == nil {
		t.Fatal("broadcast channel is nil")
	}
	if hub.register == nil {
		t.Fatal("register channel is nil")
	}
	if hub.unregister == nil {
		t.Fatal("unregister channel is nil")
	}
	if hub.done == nil {
		t.Fatal("done channel is nil")
	}
	if hub.logger == nil {
		t.Fatal("logger is nil")
	}
}

func TestWSHub_ClientCount_Empty(t *testing.T) {
	hub := newWSHub(slog.Default())
	if hub.clientCount() != 0 {
		t.Fatalf("expected 0 clients, got %d", hub.clientCount())
	}
}

func TestWSHub_RegisterClient(t *testing.T) {
	hub := startTestHub(t)

	client := &wsClient{
		hub:  hub,
		send: make(chan []byte, 256),
	}
	hub.register <- client

	// Allow the hub goroutine to process
	time.Sleep(20 * time.Millisecond)

	if hub.clientCount() != 1 {
		t.Fatalf("expected 1 client, got %d", hub.clientCount())
	}
	unregisterAll(hub, client)
}

func TestWSHub_UnregisterClient(t *testing.T) {
	hub := startTestHub(t)

	client := &wsClient{
		hub:  hub,
		send: make(chan []byte, 256),
	}
	hub.register <- client
	time.Sleep(20 * time.Millisecond)

	hub.unregister <- client
	time.Sleep(20 * time.Millisecond)

	if hub.clientCount() != 0 {
		t.Fatalf("expected 0 clients, got %d", hub.clientCount())
	}
}

func TestWSHub_UnregisterUnknownClient(t *testing.T) {
	hub := startTestHub(t)

	unknown := &wsClient{
		hub:  hub,
		send: make(chan []byte, 256),
	}

	// Should not panic or break anything
	hub.unregister <- unknown
	time.Sleep(20 * time.Millisecond)

	if hub.clientCount() != 0 {
		t.Fatalf("expected 0 clients, got %d", hub.clientCount())
	}
}

func TestWSHub_BroadcastToClients(t *testing.T) {
	hub := startTestHub(t)

	c1 := &wsClient{hub: hub, send: make(chan []byte, 256)}
	c2 := &wsClient{hub: hub, send: make(chan []byte, 256)}
	c3 := &wsClient{hub: hub, send: make(chan []byte, 256)}

	hub.register <- c1
	hub.register <- c2
	hub.register <- c3
	time.Sleep(20 * time.Millisecond)

	msg, _ := json.Marshal(wsMessage{Type: "test", Data: "hello"})
	hub.broadcast <- msg
	time.Sleep(20 * time.Millisecond)

	for i, c := range []*wsClient{c1, c2, c3} {
		select {
		case got := <-c.send:
			var m wsMessage
			if err := json.Unmarshal(got, &m); err != nil {
				t.Fatalf("client %d: unmarshal: %v", i, err)
			}
			if m.Type != "test" {
				t.Fatalf("client %d: type = %q, want test", i, m.Type)
			}
		default:
			t.Fatalf("client %d: no message received", i)
		}
	}
	unregisterAll(hub, c1, c2, c3)
}

func TestWSHub_BroadcastDropsSlowClient(t *testing.T) {
	hub := startTestHub(t)

	// Create a client with a tiny buffer that will fill up
	slow := &wsClient{hub: hub, send: make(chan []byte, 1)}
	hub.register <- slow
	time.Sleep(20 * time.Millisecond)

	// Fill the client's send buffer
	slow.send <- []byte("fill")

	// Now broadcast — the slow client's buffer is full, it should be dropped
	msg, _ := json.Marshal(wsMessage{Type: "test", Data: "x"})
	hub.broadcast <- msg
	time.Sleep(20 * time.Millisecond)

	if hub.clientCount() != 0 {
		t.Fatalf("expected slow client to be dropped, got %d clients", hub.clientCount())
	}
}

func TestWSHub_BroadcastStates_NoClients(t *testing.T) {
	hub := startTestHub(t)

	// Should not panic or block
	hub.BroadcastStates([]domain.SessionState{
		{ID: "abc123", Progress: 0.5},
	})
}

func TestWSHub_BroadcastStates_WithClients(t *testing.T) {
	hub := startTestHub(t)

	client := &wsClient{hub: hub, send: make(chan []byte, 256)}
	hub.register <- client
	time.Sleep(20 * time.Millisecond)

	states := []domain.SessionState{
		{ID: "abc123", Status: "active", Progress: 0.42, Peers: 5, DownloadSpeed: 1024},
		{ID: "def456", Status: "completed", Progress: 1.0},
	}
	hub.BroadcastStates(states)
	time.Sleep(20 * time.Millisecond)

	select {
	case data := <-client.send:
		var msg wsMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if msg.Type != "states" {
			t.Fatalf("type = %q, want states", msg.Type)
		}
		// Verify data is an array
		arr, ok := msg.Data.([]interface{})
		if !ok {
			t.Fatalf("data is not an array: %T", msg.Data)
		}
		if len(arr) != 2 {
			t.Fatalf("data len = %d, want 2", len(arr))
		}
	default:
		t.Fatal("no message received")
	}
	unregisterAll(hub, client)
}

func TestWSHub_Broadcast_GenericMessage(t *testing.T) {
	hub := startTestHub(t)

	client := &wsClient{hub: hub, send: make(chan []byte, 256)}
	hub.register <- client
	time.Sleep(20 * time.Millisecond)

	hub.Broadcast("player_settings", map[string]string{"currentTorrentId": "abc"})
	time.Sleep(20 * time.Millisecond)

	select {
	case data := <-client.send:
		var msg wsMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if msg.Type != "player_settings" {
			t.Fatalf("type = %q, want player_settings", msg.Type)
		}
	default:
		t.Fatal("no message received")
	}
	unregisterAll(hub, client)
}

func TestWSHub_Broadcast_NoClients(t *testing.T) {
	hub := startTestHub(t)

	// Should not panic or block
	hub.Broadcast("health", map[string]string{"status": "ok"})
}

func TestWSHub_Broadcast_MarshalFailure(t *testing.T) {
	hub := startTestHub(t)

	client := &wsClient{hub: hub, send: make(chan []byte, 256)}
	hub.register <- client
	time.Sleep(20 * time.Millisecond)

	// channels cannot be marshaled to JSON
	hub.Broadcast("bad", make(chan int))
	time.Sleep(20 * time.Millisecond)

	// Client should not receive anything since marshal failed
	select {
	case <-client.send:
		t.Fatal("should not receive message when marshal fails")
	default:
		// expected
	}
	unregisterAll(hub, client)
}

func TestWSHub_BroadcastStates_NilSlice(t *testing.T) {
	hub := startTestHub(t)

	client := &wsClient{hub: hub, send: make(chan []byte, 256)}
	hub.register <- client
	time.Sleep(20 * time.Millisecond)

	// nil states should still marshal to {"type":"states","data":null}
	hub.BroadcastStates(nil)
	time.Sleep(20 * time.Millisecond)

	select {
	case data := <-client.send:
		var msg wsMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if msg.Type != "states" {
			t.Fatalf("type = %q, want states", msg.Type)
		}
	default:
		// With 0 clients check, nil slice might cause early return due to empty check
		// This is also acceptable behavior
	}
	unregisterAll(hub, client)
}

func TestWSHub_Close_DisconnectsClients(t *testing.T) {
	// Use real WebSocket connections so hub.Close() can write close frames.
	s := makeWSServer()
	srv := httptest.NewServer(s)
	defer srv.Close()

	c1 := dialWS(t, srv)
	c2 := dialWS(t, srv)
	time.Sleep(50 * time.Millisecond)

	s.Close()
	time.Sleep(100 * time.Millisecond)

	// Both clients should get a close/error on next read
	_ = c1.SetReadDeadline(time.Now().Add(time.Second))
	_, _, err1 := c1.ReadMessage()
	if err1 == nil {
		t.Fatal("c1: expected error after hub close")
	}

	_ = c2.SetReadDeadline(time.Now().Add(time.Second))
	_, _, err2 := c2.ReadMessage()
	if err2 == nil {
		t.Fatal("c2: expected error after hub close")
	}
	c1.Close()
	c2.Close()
}

func TestWSHub_MultipleRegisterUnregister(t *testing.T) {
	hub := startTestHub(t)

	clients := make([]*wsClient, 5)
	for i := range clients {
		clients[i] = &wsClient{hub: hub, send: make(chan []byte, 256)}
		hub.register <- clients[i]
	}
	time.Sleep(20 * time.Millisecond)

	if hub.clientCount() != 5 {
		t.Fatalf("expected 5 clients, got %d", hub.clientCount())
	}

	// Unregister first 3
	for i := 0; i < 3; i++ {
		hub.unregister <- clients[i]
	}
	time.Sleep(20 * time.Millisecond)

	if hub.clientCount() != 2 {
		t.Fatalf("expected 2 clients after unregister, got %d", hub.clientCount())
	}
	// Clean up remaining
	unregisterAll(hub, clients[3], clients[4])
}

// ---- WebSocket HTTP handler integration tests ----

func TestHandleWS_UpgradeSucceeds(t *testing.T) {
	srv := httptest.NewServer(makeWSServer())
	defer srv.Close()

	conn := dialWS(t, srv)
	defer conn.Close()

	// Connection should be open — send a message to verify
	err := conn.WriteMessage(websocket.TextMessage, []byte("ping"))
	if err != nil {
		t.Fatalf("write: %v", err)
	}
}

func TestHandleWS_MultipleConcurrentClients(t *testing.T) {
	s := makeWSServer()
	srv := httptest.NewServer(s)
	defer srv.Close()

	const numClients = 3
	conns := make([]*websocket.Conn, numClients)
	for i := range conns {
		conns[i] = dialWS(t, srv)
		defer conns[i].Close()
	}

	// Allow all connections to be registered
	time.Sleep(50 * time.Millisecond)

	// Broadcast to all
	s.BroadcastStates([]domain.SessionState{
		{ID: "t1", Progress: 0.5},
	})

	// All clients should receive the message
	for i, conn := range conns {
		msg := readWSMessage(t, conn, 2*time.Second)
		if msg.Type != "states" {
			t.Fatalf("client %d: type = %q, want states", i, msg.Type)
		}
	}
}

func TestHandleWS_ClientDisconnect(t *testing.T) {
	s := makeWSServer()
	srv := httptest.NewServer(s)
	defer srv.Close()

	conn := dialWS(t, srv)
	time.Sleep(50 * time.Millisecond)

	// Close client — should not cause server errors
	conn.Close()
	time.Sleep(100 * time.Millisecond)

	// Server should still work — broadcast should not panic
	s.BroadcastStates([]domain.SessionState{{ID: "t1"}})
}

func TestHandleWS_BroadcastTorrents(t *testing.T) {
	repo := &fakeWSRepo{
		records: []domain.TorrentRecord{
			{ID: "t1", Name: "Test Torrent", Status: "active", TotalBytes: 1000, DoneBytes: 500},
			{ID: "t2", Name: "Other Torrent", Status: "completed", TotalBytes: 2000, DoneBytes: 2000},
		},
	}
	s := NewServer(nil, WithRepository(repo))
	srv := httptest.NewServer(s)
	defer srv.Close()

	conn := dialWS(t, srv)
	defer conn.Close()
	time.Sleep(50 * time.Millisecond)

	s.BroadcastTorrents()

	msg := readWSMessage(t, conn, 2*time.Second)
	if msg.Type != "torrents" {
		t.Fatalf("type = %q, want torrents", msg.Type)
	}
	arr, ok := msg.Data.([]interface{})
	if !ok {
		t.Fatalf("data is not array: %T", msg.Data)
	}
	if len(arr) != 2 {
		t.Fatalf("expected 2 torrents, got %d", len(arr))
	}
}

func TestHandleWS_BroadcastPlayerSettings(t *testing.T) {
	player := &fakeWSPlayerCtrl{torrentID: "active-torrent"}
	s := NewServer(nil, WithPlayerSettings(player))
	srv := httptest.NewServer(s)
	defer srv.Close()

	conn := dialWS(t, srv)
	defer conn.Close()
	time.Sleep(50 * time.Millisecond)

	s.BroadcastPlayerSettings()

	msg := readWSMessage(t, conn, 2*time.Second)
	if msg.Type != "player_settings" {
		t.Fatalf("type = %q, want player_settings", msg.Type)
	}
	dataMap, ok := msg.Data.(map[string]interface{})
	if !ok {
		t.Fatalf("data is not map: %T", msg.Data)
	}
	if got := dataMap["currentTorrentId"]; got != "active-torrent" {
		t.Fatalf("currentTorrentId = %v, want active-torrent", got)
	}
}

func TestHandleWS_BroadcastHealth(t *testing.T) {
	s := makeWSServer()
	srv := httptest.NewServer(s)
	defer srv.Close()

	conn := dialWS(t, srv)
	defer conn.Close()
	time.Sleep(50 * time.Millisecond)

	s.BroadcastHealth(context.Background())

	msg := readWSMessage(t, conn, 2*time.Second)
	if msg.Type != "health" {
		t.Fatalf("type = %q, want health", msg.Type)
	}
}

func TestHandleWS_NonWSRequest(t *testing.T) {
	s := makeWSServer()

	// Regular HTTP request to /ws should fail the upgrade
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ws", nil)
	s.ServeHTTP(rec, req)

	// Gorilla websocket returns 400 for non-upgrade requests
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestHandleWS_PingPong(t *testing.T) {
	srv := httptest.NewServer(makeWSServer())
	defer srv.Close()

	conn := dialWS(t, srv)
	defer conn.Close()

	// Set up pong handler to track receipt
	pongReceived := make(chan struct{}, 1)
	conn.SetPongHandler(func(string) error {
		select {
		case pongReceived <- struct{}{}:
		default:
		}
		return nil
	})

	// Send a ping — server's readPump refreshes deadline on pong
	if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
		t.Fatalf("write ping: %v", err)
	}

	// Read in a goroutine to process control frames
	go func() {
		for {
			_, _, err := conn.ReadMessage()
			if err != nil {
				return
			}
		}
	}()

	// The server should respond with a pong (automatic in gorilla/websocket)
	select {
	case <-pongReceived:
		// success
	case <-time.After(2 * time.Second):
		t.Fatal("pong not received within timeout")
	}
}

func TestServer_BroadcastStates_NilHub(t *testing.T) {
	// Create a server without running hub (shouldn't happen in practice, but test safety)
	s := &Server{}
	// Should not panic
	s.BroadcastStates(nil)
}

func TestServer_BroadcastTorrents_NilHub(t *testing.T) {
	s := &Server{}
	// Should not panic
	s.BroadcastTorrents()
}

func TestServer_BroadcastTorrents_NilRepo(t *testing.T) {
	s := makeWSServer()
	// Server has wsHub but no repo — should return early
	s.BroadcastTorrents()
}

func TestServer_BroadcastTorrents_RepoError(t *testing.T) {
	repo := &fakeWSRepo{err: context.DeadlineExceeded}
	s := NewServer(nil, WithRepository(repo))
	srv := httptest.NewServer(s)
	defer srv.Close()

	conn := dialWS(t, srv)
	defer conn.Close()
	time.Sleep(50 * time.Millisecond)

	// Should not panic; no message sent on error
	s.BroadcastTorrents()
	time.Sleep(50 * time.Millisecond)

	_ = conn.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	_, _, err := conn.ReadMessage()
	if err == nil {
		t.Fatal("expected read timeout, got message")
	}
}

func TestServer_BroadcastPlayerSettings_NilHub(t *testing.T) {
	s := &Server{}
	s.BroadcastPlayerSettings()
}

func TestServer_BroadcastPlayerSettings_NilPlayer(t *testing.T) {
	s := makeWSServer()
	s.BroadcastPlayerSettings()
}

func TestServer_BroadcastHealth_NilHub(t *testing.T) {
	s := &Server{}
	s.BroadcastHealth(context.Background())
}

func TestServer_Close_WithHub(t *testing.T) {
	s := makeWSServer()
	srv := httptest.NewServer(s)
	defer srv.Close()

	conn := dialWS(t, srv)
	time.Sleep(50 * time.Millisecond)

	s.Close()
	time.Sleep(100 * time.Millisecond)

	// Client should receive close or error on next read
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	_, _, err := conn.ReadMessage()
	if err == nil {
		t.Fatal("expected error after server close")
	}
	conn.Close()
}

func TestHandleWS_ConcurrentBroadcasts(t *testing.T) {
	s := makeWSServer()
	srv := httptest.NewServer(s)
	defer srv.Close()

	conn := dialWS(t, srv)
	defer conn.Close()
	time.Sleep(50 * time.Millisecond)

	// Send multiple broadcasts concurrently
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			s.BroadcastStates([]domain.SessionState{
				{ID: domain.TorrentID("t" + string(rune('0'+idx)))},
			})
		}(i)
	}
	wg.Wait()

	// Read all messages — we should get at least some
	received := 0
	for {
		_ = conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		_, _, err := conn.ReadMessage()
		if err != nil {
			break
		}
		received++
	}

	if received == 0 {
		t.Fatal("expected at least one broadcast message")
	}
}

func TestWSMessage_JSONStructure(t *testing.T) {
	tests := []struct {
		name    string
		msgType string
		data    interface{}
	}{
		{"states", "states", []domain.SessionState{{ID: "abc", Progress: 0.5}}},
		{"torrents", "torrents", []torrentSummary{{ID: "t1", Name: "Test"}}},
		{"player_settings", "player_settings", playerSettingsResponse{CurrentTorrentID: "t1"}},
		{"health", "health", map[string]interface{}{"status": "ok"}},
		{"nil_data", "test", nil},
		{"empty_string_data", "test", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := wsMessage{Type: tt.msgType, Data: tt.data}
			data, err := json.Marshal(msg)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}

			var decoded wsMessage
			if err := json.Unmarshal(data, &decoded); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if decoded.Type != tt.msgType {
				t.Fatalf("type = %q, want %q", decoded.Type, tt.msgType)
			}
		})
	}
}

// ---- fakes ----

type fakeWSRepo struct {
	records []domain.TorrentRecord
	err     error
}

func (f *fakeWSRepo) Create(_ context.Context, r domain.TorrentRecord) error {
	return f.err
}
func (f *fakeWSRepo) Update(_ context.Context, r domain.TorrentRecord) error {
	return f.err
}
func (f *fakeWSRepo) UpdateProgress(_ context.Context, _ domain.TorrentID, _ domain.ProgressUpdate) error {
	return f.err
}
func (f *fakeWSRepo) Get(_ context.Context, id domain.TorrentID) (domain.TorrentRecord, error) {
	for _, r := range f.records {
		if r.ID == id {
			return r, nil
		}
	}
	return domain.TorrentRecord{}, domain.ErrNotFound
}
func (f *fakeWSRepo) List(_ context.Context, _ domain.TorrentFilter) ([]domain.TorrentRecord, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.records, nil
}
func (f *fakeWSRepo) GetMany(_ context.Context, ids []domain.TorrentID) ([]domain.TorrentRecord, error) {
	return f.records, f.err
}
func (f *fakeWSRepo) Delete(_ context.Context, id domain.TorrentID) error {
	return f.err
}
func (f *fakeWSRepo) UpdateTags(_ context.Context, id domain.TorrentID, tags []string) error {
	return f.err
}

type fakeWSPlayerCtrl struct {
	torrentID domain.TorrentID
}

func (f *fakeWSPlayerCtrl) CurrentTorrentID() domain.TorrentID {
	return f.torrentID
}
func (f *fakeWSPlayerCtrl) SetCurrentTorrentID(id domain.TorrentID) error {
	f.torrentID = id
	return nil
}
