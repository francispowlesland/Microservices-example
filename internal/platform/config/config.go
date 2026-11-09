package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// String returns the environment variable for key or defaultVal if unset or empty.
func String(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}

// Required returns the environment variable for key or an error if unset or empty.
func Required(key string) (string, error) {
	if v := os.Getenv(key); v != "" {
		return v, nil
	}
	return "", fmt.Errorf("missing required environment variable %q", key)
}

// Duration returns the parsed time.Duration from the environment variable for key,
// or defaultVal if unset or empty.
func Duration(key string, defaultVal time.Duration) (time.Duration, error) {
	v := os.Getenv(key)
	if v == "" {
		return defaultVal, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("invalid duration for environment variable %q (%q): %w", key, v, err)
	}
	return d, nil
}

// Int returns the parsed int from the environment variable for key,
// or defaultVal if unset or empty.
func Int(key string, defaultVal int) (int, error) {
	v := os.Getenv(key)
	if v == "" {
		return defaultVal, nil
	}
	i, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("invalid int for environment variable %q (%q): %w", key, v, err)
	}
	return i, nil
}
