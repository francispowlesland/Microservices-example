// Package config loads typed service configuration from the environment.
package config

import (
	"github.com/francispowlesland/microservices-app-go/internal/platform/config"
)

type Config struct {
	GRPCAddr    string
	DatabaseURL string
	AppEnv      string
	JWTPublic   string
	// ServicePublic verifies service-to-service tokens (orders reserving stock).
	ServicePublic string
}

func Load() (Config, error) {
	dbURL, err := config.Required("DATABASE_URL")
	if err != nil {
		return Config{}, err
	}
	pub, err := config.Required("JWT_PUBLIC_KEY")
	if err != nil {
		return Config{}, err
	}
	svcPub, err := config.Required("SERVICE_PUBLIC_KEY")
	if err != nil {
		return Config{}, err
	}
	return Config{
		GRPCAddr:      config.String("GRPC_ADDR", ":50054"),
		DatabaseURL:   dbURL,
		AppEnv:        config.String("APP_ENV", "development"),
		JWTPublic:     pub,
		ServicePublic: svcPub,
	}, nil
}
