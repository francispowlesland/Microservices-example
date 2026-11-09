// Package config loads the payments service configuration from the environment.
package config

import (
	"fmt"

	platform "github.com/francispowlesland/microservices-app-go/internal/platform/config"
)

// Config is the typed payments service configuration.
type Config struct {
	GRPCAddr     string
	DatabaseURL  string
	AppEnv       string
	JWTPublicKey string
	// MaxCents is the largest amount the simulated processor accepts.
	MaxCents int64
}

// Load reads the configuration from environment variables.
func Load() (Config, error) {
	dbURL, err := platform.Required("DATABASE_URL")
	if err != nil {
		return Config{}, err
	}
	pubKey, err := platform.Required("JWT_PUBLIC_KEY")
	if err != nil {
		return Config{}, err
	}
	maxCents, err := platform.Int("PAYMENT_MAX_CENTS", 1_000_000)
	if err != nil {
		return Config{}, err
	}
	if maxCents <= 0 {
		return Config{}, fmt.Errorf("PAYMENT_MAX_CENTS must be positive, got %d", maxCents)
	}
	return Config{
		GRPCAddr:     platform.String("GRPC_ADDR", ":50053"),
		DatabaseURL:  dbURL,
		AppEnv:       platform.String("APP_ENV", "development"),
		JWTPublicKey: pubKey,
		MaxCents:     int64(maxCents),
	}, nil
}
