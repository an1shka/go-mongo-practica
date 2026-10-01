package main

import (
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	websocketWriteWait = 10 * time.Second
	websocketPongWait  = 60 * time.Second
	websocketPingEvery = (websocketPongWait * 9) / 10
	clientQueueSize    = 32
)

type chatHub struct {
	mu      sync.Mutex
	clients map[*chatClient]struct{}
}

type chatClient struct {
	conn      *websocket.Conn
	send      chan any
	hub       *chatHub
	closeOnce sync.Once
}

func newChatHub() *chatHub {
	return &chatHub{clients: make(map[*chatClient]struct{})}
}

func (h *chatHub) add(conn *websocket.Conn) *chatClient {
	client := &chatClient{
		conn: conn,
		send: make(chan any, clientQueueSize),
		hub:  h,
	}
	h.mu.Lock()
	h.clients[client] = struct{}{}
	h.mu.Unlock()
	return client
}

func (h *chatHub) remove(client *chatClient) {
	h.mu.Lock()
	if _, exists := h.clients[client]; exists {
		delete(h.clients, client)
		close(client.send)
	}
	h.mu.Unlock()
}

func (h *chatHub) broadcast(message Message) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for client := range h.clients {
		select {
		case client.send <- message:
		default:
			// Медленный клиент отключается, чтобы не задерживать остальных.
			delete(h.clients, client)
			close(client.send)
			_ = client.conn.Close()
		}
	}
}

func (client *chatClient) close() {
	client.closeOnce.Do(func() {
		client.hub.remove(client)
		_ = client.conn.Close()
	})
}

func (client *chatClient) writePump() {
	ticker := time.NewTicker(websocketPingEvery)
	defer func() {
		ticker.Stop()
		client.close()
	}()

	for {
		select {
		case message, open := <-client.send:
			_ = client.conn.SetWriteDeadline(time.Now().Add(websocketWriteWait))
			if !open {
				_ = client.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if err := client.conn.WriteJSON(message); err != nil {
				return
			}
		case <-ticker.C:
			_ = client.conn.SetWriteDeadline(time.Now().Add(websocketWriteWait))
			if err := client.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

func (client *chatClient) notifyError(message string) {
	select {
	case client.send <- ErrorResponse{Error: message}:
	default:
	}
}
