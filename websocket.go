package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gorilla/websocket"
	"go.mongodb.org/mongo-driver/v2/bson"
)

const websocketMaxMessageBytes = 64 << 10

var websocketUpgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		if origin == "" {
			return true // CLI-клиенты могут не передавать Origin.
		}
		parsed, err := url.Parse(origin)
		return err == nil && strings.EqualFold(parsed.Host, r.Host)
	},
}

func (s *chatServer) websocket(w http.ResponseWriter, r *http.Request) {
	user, err := s.authenticateWebSocket(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "Для подключения к чату нужен действующий JWT")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), databaseTimeout)
	err = s.ensureDatabase(ctx)
	cancel()
	if err != nil {
		s.logger.Print("MongoDB недоступна при подключении WebSocket")
		writeError(w, http.StatusServiceUnavailable, "База данных временно недоступна")
		return
	}

	conn, err := websocketUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	client := s.hub.add(conn)
	go client.writePump()
	s.readMessages(client, user)
}

func (s *chatServer) authenticateWebSocket(r *http.Request) (authenticatedUser, error) {
	rawToken := ""
	if authorization := strings.Fields(r.Header.Get("Authorization")); len(authorization) == 2 &&
		strings.EqualFold(authorization[0], "Bearer") {
		rawToken = authorization[1]
	} else if r.Header.Get("Authorization") == "" {
		// Встроенный браузерный WebSocket-клиент не позволяет задать заголовки.
		rawToken = r.URL.Query().Get("token")
	}
	if strings.TrimSpace(rawToken) == "" {
		return authenticatedUser{}, http.ErrNoCookie
	}
	return parseToken(rawToken, s.secret, time.Now())
}

func (s *chatServer) readMessages(client *chatClient, user authenticatedUser) {
	defer client.close()
	client.conn.SetReadLimit(websocketMaxMessageBytes)
	_ = client.conn.SetReadDeadline(time.Now().Add(websocketPongWait))
	client.conn.SetPongHandler(func(string) error {
		return client.conn.SetReadDeadline(time.Now().Add(websocketPongWait))
	})

	for {
		messageType, payload, err := client.conn.ReadMessage()
		if err != nil {
			return
		}
		if messageType != websocket.TextMessage {
			client.notifyError("Отправляйте сообщения в формате JSON")
			continue
		}

		var incoming WSMessage
		decoder := json.NewDecoder(strings.NewReader(string(payload)))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&incoming); err != nil || !errors.Is(decoder.Decode(&struct{}{}), io.EOF) {
			client.notifyError("Сообщение должно быть JSON-объектом с полем text")
			continue
		}
		text := strings.TrimSpace(incoming.Text)
		if text == "" {
			client.notifyError("Сообщение не может быть пустым")
			continue
		}
		if utf8.RuneCountInString(text) > maxMessageSize {
			client.notifyError("Сообщение не должно превышать 2000 символов")
			continue
		}

		ctx, cancel := context.WithTimeout(context.Background(), databaseTimeout)
		if err := s.ensureDatabase(ctx); err != nil {
			cancel()
			s.logger.Print("MongoDB недоступна при сохранении сообщения")
			client.notifyError("Не удалось сохранить сообщение: база данных недоступна")
			continue
		}
		message := Message{
			UserID:    user.ID,
			Username:  user.Username,
			Text:      text,
			CreatedAt: time.Now().UTC(),
		}
		result, err := s.messages.InsertOne(ctx, message)
		cancel()
		if err != nil {
			s.logger.Print("ошибка сохранения сообщения")
			client.notifyError("Не удалось сохранить сообщение")
			continue
		}
		if id, ok := result.InsertedID.(bson.ObjectID); ok {
			message.ID = id
		}
		s.hub.broadcast(message)
	}
}
