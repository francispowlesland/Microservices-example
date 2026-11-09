package pagination

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	DefaultPageSize = 50
	MaxPageSize     = 200
)

// PageSize clamps requested page size to DefaultPageSize (50) and MaxPageSize (200).
// Zero or negative values return DefaultPageSize.
func PageSize(requested int32) int {
	if requested <= 0 {
		return DefaultPageSize
	}
	if requested > MaxPageSize {
		return MaxPageSize
	}
	return int(requested)
}

// Encode encodes createdAt and id into an opaque base64 page token.
func Encode(createdAt time.Time, id uuid.UUID) string {
	raw := fmt.Sprintf("%s,%s", createdAt.UTC().Format(time.RFC3339Nano), id.String())
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// Decode decodes an opaque base64 page token into createdAt and id.
// An empty token represents the first page and returns zero time, uuid.Nil, and nil error.
func Decode(token string) (time.Time, uuid.UUID, error) {
	if token == "" {
		return time.Time{}, uuid.Nil, nil
	}

	var data []byte
	var err error
	// Support standard and URL-safe base64, with or without padding
	data, err = base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		data, err = base64.URLEncoding.DecodeString(token)
		if err != nil {
			data, err = base64.StdEncoding.DecodeString(token)
			if err != nil {
				return time.Time{}, uuid.Nil, errors.New("pagination: invalid base64 token")
			}
		}
	}

	parts := strings.SplitN(string(data), ",", 2)
	if len(parts) != 2 {
		return time.Time{}, uuid.Nil, errors.New("pagination: invalid token format")
	}

	t, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		t, err = time.Parse(time.RFC3339, parts[0])
		if err != nil {
			return time.Time{}, uuid.Nil, fmt.Errorf("pagination: invalid timestamp in token: %w", err)
		}
	}

	id, err := uuid.Parse(parts[1])
	if err != nil {
		return time.Time{}, uuid.Nil, fmt.Errorf("pagination: invalid uuid in token: %w", err)
	}

	return t, id, nil
}
