package main

import (
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestNormalizeUsername(t *testing.T) {
	for _, valid := range []string{"alice_01", "чат.студент", "Go-learner"} {
		if _, err := normalizeUsername(valid); err != nil {
			t.Errorf("normalizeUsername(%q) unexpected error: %v", valid, err)
		}
	}
	for _, invalid := range []string{"ab", "name with spaces", "name/with/slash"} {
		if _, err := normalizeUsername(invalid); err == nil {
			t.Errorf("normalizeUsername(%q) expected an error", invalid)
		}
	}
}

func TestValidatePasswordAndMessage(t *testing.T) {
	if err := validatePassword("12345678", true); err != nil {
		t.Fatalf("valid password rejected: %v", err)
	}
	if err := validatePassword("short", true); err == nil {
		t.Fatal("short registration password was accepted")
	}
	if err := validatePassword(strings.Repeat("x", 73), false); err == nil {
		t.Fatal("password longer than bcrypt's limit was accepted")
	}
	if text, err := validateMessageText("  Привет  "); err != nil || text != "Привет" {
		t.Fatalf("message was not trimmed: %q, %v", text, err)
	}
	if _, err := validateMessageText("   "); err == nil {
		t.Fatal("empty message was accepted")
	}
}

func TestJWTClaimsAndExpiry(t *testing.T) {
	now := time.Date(2026, time.October, 1, 12, 0, 0, 0, time.UTC)
	user := User{ID: bson.NewObjectID(), Username: "learner"}
	secret := []byte(strings.Repeat("s", 32))

	token, err := issueToken(user, secret, now)
	if err != nil {
		t.Fatalf("issueToken failed: %v", err)
	}
	got, err := parseToken(token, secret, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("parseToken failed: %v", err)
	}
	if got.ID != user.ID || got.Username != user.Username {
		t.Fatalf("unexpected claims: %#v", got)
	}
	if _, err := parseToken(token, []byte(strings.Repeat("x", 32)), now); err == nil {
		t.Fatal("token signed with a different key was accepted")
	}
	if _, err := parseToken(token, secret, now.Add(13*time.Hour)); err == nil {
		t.Fatal("expired token was accepted")
	}
}
