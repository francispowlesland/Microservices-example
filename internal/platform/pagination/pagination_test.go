package pagination_test

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/francispowlesland/microservices-app-go/internal/platform/pagination"
)

func TestPageSize(t *testing.T) {
	tests := []struct {
		name      string
		requested int32
		expected  int
	}{
		{"zero", 0, 50},
		{"negative", -10, 50},
		{"valid small", 10, 10},
		{"valid default", 50, 50},
		{"valid max", 200, 200},
		{"exceeds max", 500, 200},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := pagination.PageSize(tt.requested)
			if got != tt.expected {
				t.Fatalf("PageSize(%d) = %d; want %d", tt.requested, got, tt.expected)
			}
		})
	}
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Microsecond)
	id := uuid.New()

	token := pagination.Encode(now, id)
	if token == "" {
		t.Fatal("expected non-empty token")
	}

	decodedTime, decodedID, err := pagination.Decode(token)
	if err != nil {
		t.Fatalf("unexpected decode error: %v", err)
	}

	if !decodedTime.Equal(now) {
		t.Fatalf("expected time %v, got %v", now, decodedTime)
	}
	if decodedID != id {
		t.Fatalf("expected id %v, got %v", id, decodedID)
	}
}

func TestDecodeEmptyToken(t *testing.T) {
	tZero, idZero, err := pagination.Decode("")
	if err != nil {
		t.Fatalf("unexpected error for empty token: %v", err)
	}
	if !tZero.IsZero() || idZero != uuid.Nil {
		t.Fatalf("expected zero values, got time=%v, id=%v", tZero, idZero)
	}
}

func TestDecodeMalformedToken(t *testing.T) {
	malformed := []string{
		"not-base64-!!!",
		"YWJj",                         // "abc" - no comma
		"MjAyMy0wMS0wMSxub3QtdXVpZA==", // timestamp, not-uuid
		"bm90LXRpbWUsYTFhMmEzYTQtYjFiMi1jMWMzLWQxZDQtZTFlMmUzZTRlNTVl", // not-time, uuid
	}

	for _, token := range malformed {
		_, _, err := pagination.Decode(token)
		if err == nil {
			t.Fatalf("expected error for malformed token %q", token)
		}
	}
}
