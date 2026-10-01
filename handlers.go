package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readpref"
	"golang.org/x/crypto/bcrypt"
)

const (
	databaseName       = "chat_app"
	usersCollection    = "users"
	messagesCollection = "messages"
	requestBodyLimit   = 16 << 10
	databaseTimeout    = 5 * time.Second
)

type chatServer struct {
	client   *mongo.Client
	users    *mongo.Collection
	messages *mongo.Collection
	secret   []byte
	logger   *log.Logger
	hub      *chatHub

	indexMutex   sync.Mutex
	indexesReady bool
}

func newChatServer(client *mongo.Client, secret []byte, logger *log.Logger) *chatServer {
	database := client.Database(databaseName)
	return &chatServer{
		client:   client,
		users:    database.Collection(usersCollection),
		messages: database.Collection(messagesCollection),
		secret:   secret,
		logger:   logger,
		hub:      newChatHub(),
	}
}

func (s *chatServer) registerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("POST /register", s.register)
	mux.HandleFunc("POST /login", s.login)
	mux.HandleFunc("GET /messages", s.getMessages)
	mux.HandleFunc("GET /ws", s.websocket)
}

func (s *chatServer) ensureDatabase(ctx context.Context) error {
	if err := s.client.Ping(ctx, readpref.Primary()); err != nil {
		return err
	}

	s.indexMutex.Lock()
	defer s.indexMutex.Unlock()
	if s.indexesReady {
		return nil
	}

	_, err := s.users.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "username", Value: 1}},
		Options: options.Index().SetUnique(true).SetName("unique_username"),
	})
	if err != nil {
		return err
	}
	_, err = s.messages.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "created_at", Value: -1}},
		Options: options.Index().SetName("latest_messages"),
	})
	if err != nil {
		return err
	}

	s.indexesReady = true
	return nil
}

func (s *chatServer) health(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), databaseTimeout)
	defer cancel()
	if err := s.ensureDatabase(ctx); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "degraded"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *chatServer) register(w http.ResponseWriter, r *http.Request) {
	var request RegisterRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "Проверьте формат запроса")
		return
	}

	username, err := normalizeUsername(request.Username)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := validatePassword(request.Password, true); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), databaseTimeout)
	defer cancel()
	if err := s.ensureDatabase(ctx); err != nil {
		s.logger.Print("MongoDB недоступна во время регистрации")
		writeError(w, http.StatusServiceUnavailable, "База данных временно недоступна")
		return
	}

	var existing User
	err = s.users.FindOne(ctx, bson.M{"username": username}).Decode(&existing)
	if err == nil {
		writeError(w, http.StatusConflict, "Это имя пользователя уже занято")
		return
	}
	if !errors.Is(err, mongo.ErrNoDocuments) {
		s.logger.Print("ошибка поиска пользователя при регистрации")
		writeError(w, http.StatusServiceUnavailable, "Не удалось проверить имя пользователя")
		return
	}

	passwordHash, err := bcrypt.GenerateFromPassword([]byte(request.Password), bcrypt.DefaultCost)
	if err != nil {
		s.logger.Print("не удалось захешировать пароль")
		writeError(w, http.StatusInternalServerError, "Не удалось создать аккаунт")
		return
	}
	user := User{
		Username:     username,
		PasswordHash: string(passwordHash),
		CreatedAt:    time.Now().UTC(),
	}
	if _, err := s.users.InsertOne(ctx, user); err != nil {
		if mongo.IsDuplicateKeyError(err) {
			writeError(w, http.StatusConflict, "Это имя пользователя уже занято")
			return
		}
		s.logger.Print("ошибка сохранения пользователя")
		writeError(w, http.StatusServiceUnavailable, "Не удалось сохранить пользователя")
		return
	}

	writeJSON(w, http.StatusCreated, map[string]string{"message": "Аккаунт создан. Теперь войдите в чат."})
}

func (s *chatServer) login(w http.ResponseWriter, r *http.Request) {
	var request LoginRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "Проверьте формат запроса")
		return
	}
	username, err := normalizeUsername(request.Username)
	if err != nil || validatePassword(request.Password, false) != nil {
		writeError(w, http.StatusUnauthorized, "Неверное имя пользователя или пароль")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), databaseTimeout)
	defer cancel()
	if err := s.ensureDatabase(ctx); err != nil {
		s.logger.Print("MongoDB недоступна во время входа")
		writeError(w, http.StatusServiceUnavailable, "База данных временно недоступна")
		return
	}

	var user User
	if err := s.users.FindOne(ctx, bson.M{"username": username}).Decode(&user); err != nil {
		if !errors.Is(err, mongo.ErrNoDocuments) {
			s.logger.Print("ошибка поиска пользователя при входе")
			writeError(w, http.StatusServiceUnavailable, "Не удалось проверить данные для входа")
			return
		}
		writeError(w, http.StatusUnauthorized, "Неверное имя пользователя или пароль")
		return
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(request.Password)); err != nil {
		writeError(w, http.StatusUnauthorized, "Неверное имя пользователя или пароль")
		return
	}

	token, err := issueToken(user, s.secret, time.Now())
	if err != nil {
		s.logger.Print("не удалось создать JWT")
		writeError(w, http.StatusInternalServerError, "Не удалось выполнить вход")
		return
	}
	writeJSON(w, http.StatusOK, AuthResponse{Token: token, Username: user.Username})
}

func (s *chatServer) getMessages(w http.ResponseWriter, r *http.Request) {
	user, ok := s.authenticateHTTP(w, r)
	if !ok {
		return
	}
	_ = user // История общая для чата; JWT всё равно обязателен.

	ctx, cancel := context.WithTimeout(r.Context(), databaseTimeout)
	defer cancel()
	if err := s.ensureDatabase(ctx); err != nil {
		s.logger.Print("MongoDB недоступна при чтении истории")
		writeError(w, http.StatusServiceUnavailable, "База данных временно недоступна")
		return
	}

	cursor, err := s.messages.Find(
		ctx,
		bson.M{},
		options.Find().SetSort(bson.D{{Key: "created_at", Value: -1}}).SetLimit(100),
	)
	if err != nil {
		s.logger.Print("ошибка чтения истории сообщений")
		writeError(w, http.StatusServiceUnavailable, "Не удалось загрузить сообщения")
		return
	}
	defer cursor.Close(ctx)

	messages := make([]Message, 0)
	if err := cursor.All(ctx, &messages); err != nil {
		s.logger.Print("ошибка декодирования истории сообщений")
		writeError(w, http.StatusServiceUnavailable, "Не удалось загрузить сообщения")
		return
	}
	for left, right := 0, len(messages)-1; left < right; left, right = left+1, right-1 {
		messages[left], messages[right] = messages[right], messages[left]
	}
	writeJSON(w, http.StatusOK, messages)
}

func (s *chatServer) authenticateHTTP(w http.ResponseWriter, r *http.Request) (authenticatedUser, bool) {
	parts := strings.Fields(r.Header.Get("Authorization"))
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		writeError(w, http.StatusUnauthorized, "Требуется JWT в заголовке Authorization")
		return authenticatedUser{}, false
	}
	user, err := parseToken(parts[1], s.secret, time.Now())
	if err != nil {
		writeError(w, http.StatusUnauthorized, "JWT недействителен или срок его действия истёк")
		return authenticatedUser{}, false
	}
	return user, true
}

func decodeJSON(w http.ResponseWriter, r *http.Request, destination any) error {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, requestBodyLimit))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("request must contain one JSON object")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, ErrorResponse{Error: message})
}
