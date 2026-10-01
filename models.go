package main

import (
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// User хранит только bcrypt-хеш пароля. Сам пароль никогда не попадает в MongoDB.
type User struct {
	ID           bson.ObjectID `bson:"_id,omitempty" json:"id,omitempty"`
	Username     string        `bson:"username" json:"username"`
	PasswordHash string        `bson:"password_hash" json:"-"`
	CreatedAt    time.Time     `bson:"created_at" json:"created_at"`
}

type RegisterRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type AuthResponse struct {
	Token    string `json:"token"`
	Username string `json:"username"`
}

// Message содержит обязательные поля практики и имя автора для отображения в чате.
type Message struct {
	ID        bson.ObjectID `bson:"_id,omitempty" json:"id"`
	UserID    bson.ObjectID `bson:"user_id" json:"user_id"`
	Username  string        `bson:"username" json:"username"`
	Text      string        `bson:"text" json:"text"`
	CreatedAt time.Time     `bson:"created_at" json:"created_at"`
}

type WSMessage struct {
	Text string `json:"text"`
}

type ErrorResponse struct {
	Error string `json:"error"`
}
