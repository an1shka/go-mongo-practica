package main

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/golang-jwt/jwt/v5"
	"go.mongodb.org/mongo-driver/v2/bson"
)

const (
	jwtIssuer       = "go-mongo-chat"
	tokenLifetime   = 12 * time.Hour
	maxUsernameSize = 32
	maxPasswordSize = 72 // bcrypt использует максимум 72 байта
	maxMessageSize  = 2000
)

type authClaims struct {
	Username string `json:"username"`
	jwt.RegisteredClaims
}

type authenticatedUser struct {
	ID       bson.ObjectID
	Username string
}

func normalizeUsername(username string) (string, error) {
	username = strings.TrimSpace(username)
	length := utf8.RuneCountInString(username)
	if length < 3 || length > maxUsernameSize {
		return "", errors.New("имя пользователя должно содержать от 3 до 32 символов")
	}

	for _, char := range username {
		if !unicode.IsLetter(char) && !unicode.IsDigit(char) &&
			char != '_' && char != '-' && char != '.' {
			return "", errors.New("используйте в имени буквы, цифры, точку, дефис или подчёркивание")
		}
	}
	return username, nil
}

func validatePassword(password string, requireMinimum bool) error {
	if len(password) > maxPasswordSize {
		return errors.New("пароль не должен превышать 72 байта")
	}
	if requireMinimum && len(password) < 8 {
		return errors.New("пароль должен содержать не менее 8 байт")
	}
	if password == "" {
		return errors.New("введите пароль")
	}
	return nil
}

func validateMessageText(text string) (string, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", errors.New("сообщение не может быть пустым")
	}
	if utf8.RuneCountInString(text) > maxMessageSize {
		return "", fmt.Errorf("сообщение не должно превышать %d символов", maxMessageSize)
	}
	return text, nil
}

func issueToken(user User, secret []byte, now time.Time) (string, error) {
	claims := authClaims{
		Username: user.Username,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    jwtIssuer,
			Subject:   user.ID.Hex(),
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(tokenLifetime)),
		},
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(secret)
}

func parseToken(rawToken string, secret []byte, now time.Time) (authenticatedUser, error) {
	claims := &authClaims{}
	token, err := jwt.ParseWithClaims(
		rawToken,
		claims,
		func(token *jwt.Token) (any, error) {
			if token.Method != jwt.SigningMethodHS256 {
				return nil, errors.New("unexpected signing method")
			}
			return secret, nil
		},
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer(jwtIssuer),
		jwt.WithTimeFunc(func() time.Time { return now }),
	)
	if err != nil || token == nil || !token.Valid {
		return authenticatedUser{}, errors.New("invalid token")
	}

	id, err := bson.ObjectIDFromHex(claims.Subject)
	if err != nil || strings.TrimSpace(claims.Username) == "" {
		return authenticatedUser{}, errors.New("invalid token claims")
	}
	return authenticatedUser{ID: id, Username: claims.Username}, nil
}
