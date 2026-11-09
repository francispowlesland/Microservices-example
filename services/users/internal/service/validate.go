package service

import (
	"errors"
	"fmt"
	"net/mail"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	minPasswordBytes = 12
	maxPasswordBytes = 128
	maxEmailLen      = 254
	maxNameRunes     = 100
)

var usernameRE = regexp.MustCompile(`^[a-z0-9_]{3,32}$`)

// normaliseEmail trims, lower-cases and validates an address.
func normaliseEmail(s string) (string, error) {
	e := strings.ToLower(strings.TrimSpace(s))
	if e == "" || len(e) > maxEmailLen {
		return "", errors.New("email is invalid")
	}
	a, err := mail.ParseAddress(e)
	if err != nil || a.Address != e || a.Name != "" {
		return "", errors.New("email is invalid")
	}
	return e, nil
}

func normaliseUsername(s string) (string, error) {
	u := strings.ToLower(strings.TrimSpace(s))
	if !usernameRE.MatchString(u) {
		return "", errors.New("username must be 3 to 32 characters of a-z, 0-9 or _")
	}
	return u, nil
}

func normaliseName(s string) (string, error) {
	n := strings.TrimSpace(s)
	if n == "" || utf8.RuneCountInString(n) > maxNameRunes {
		return "", fmt.Errorf("name must be 1 to %d characters", maxNameRunes)
	}
	for _, r := range n {
		if unicode.IsControl(r) {
			return "", errors.New("name contains control characters")
		}
	}
	return n, nil
}

// checkPassword enforces the policy: 12 to 128 bytes, and not equal to the
// email or username. Nothing else.
func checkPassword(pw, email, username string) error {
	if len(pw) < minPasswordBytes || len(pw) > maxPasswordBytes {
		return fmt.Errorf("password must be %d to %d bytes", minPasswordBytes, maxPasswordBytes)
	}
	l := strings.ToLower(pw)
	if l == strings.ToLower(email) || l == strings.ToLower(username) {
		return errors.New("password must not equal the email or username")
	}
	return nil
}
