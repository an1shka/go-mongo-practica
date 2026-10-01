package main

import (
	"context"
	"embed"
	"errors"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

//go:embed web/*
var webFiles embed.FS

func main() {
	logger := log.New(os.Stdout, "mini-chat: ", log.LstdFlags|log.LUTC)
	secret, err := jwtSecretFromEnvironment()
	if err != nil {
		logger.Fatal(err)
	}

	mongoURI := strings.TrimSpace(os.Getenv("MONGODB_URI"))
	if mongoURI == "" {
		// Сохраняем URI из исходной заготовки урока для локального MongoDB.
		mongoURI = "mongodb://localhost:27017"
	}

	client, err := mongo.Connect(options.Client().ApplyURI(mongoURI))
	if err != nil {
		logger.Fatal("не удалось прочитать настройки MongoDB")
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := client.Disconnect(ctx); err != nil {
			logger.Print("не удалось корректно закрыть соединение с MongoDB")
		}
	}()

	service := newChatServer(client, secret, logger)
	startupContext, cancelStartup := context.WithTimeout(context.Background(), 4*time.Second)
	if err := service.ensureDatabase(startupContext); err != nil {
		logger.Print("MongoDB пока недоступна; API запустится в ограниченном режиме и сообщит об ошибке запросам")
	}
	cancelStartup()

	staticFiles, err := fs.Sub(webFiles, "web")
	if err != nil {
		logger.Fatal("не удалось открыть файлы интерфейса")
	}

	mux := http.NewServeMux()
	service.registerRoutes(mux)
	mux.Handle("GET /", http.FileServer(http.FS(staticFiles)))

	port := strings.TrimSpace(os.Getenv("PORT"))
	if port == "" {
		port = "8080"
	}
	server := &http.Server{
		Addr:              ":" + port,
		Handler:           securityHeaders(mux),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       75 * time.Second,
	}

	shutdownContext, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		logger.Printf("сервер запущен на порту %s", port)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Fatalf("ошибка запуска HTTP-сервера: %v", err)
		}
	}()

	<-shutdownContext.Done()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		logger.Print("не удалось корректно остановить HTTP-сервер")
	}
}

func jwtSecretFromEnvironment() ([]byte, error) {
	secret := strings.TrimSpace(os.Getenv("JWT_SECRET"))
	if secret == "" {
		// В Replit можно использовать уже настроенный секрет проекта.
		secret = strings.TrimSpace(os.Getenv("SESSION_SECRET"))
	}
	if len(secret) < 32 {
		return nil, errors.New("задайте JWT_SECRET (не менее 32 байт) или SESSION_SECRET в окружении")
	}
	return []byte(secret), nil
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}
