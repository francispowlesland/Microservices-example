// Package config loads the orders service configuration from the environment.
package config

import (
	"github.com/francispowlesland/microservices-app-go/internal/platform/config"
)

// DefaultAddr is the orders gRPC listen address.
const DefaultAddr = ":50052"

// Config is the typed orders service configuration.
type Config struct {
	GRPCAddr     string
	DatabaseURL  string
	AppEnv       string
	JWTPublicKey string
	// InventoryAddr is the inventory gRPC address orders reserves stock from.
	InventoryAddr string
	// ServicePrivateKey signs the service tokens inventory accepts for ReserveStock/ReleaseStock.
	ServicePrivateKey string
}

// Load reads configuration from environment variables.
func Load() (Config, error) {
	dbURL, err := config.Required("DATABASE_URL")
	if err != nil {
		return Config{}, err
	}
	pub, err := config.Required("JWT_PUBLIC_KEY")
	if err != nil {
		return Config{}, err
	}
	svcKey, err := config.Required("SERVICE_PRIVATE_KEY")
	if err != nil {
		return Config{}, err
	}
	return Config{
		GRPCAddr:          config.String("GRPC_ADDR", DefaultAddr),
		DatabaseURL:       dbURL,
		AppEnv:            config.String("APP_ENV", "development"),
		JWTPublicKey:      pub,
		InventoryAddr:     config.String("INVENTORY_ADDR", "localhost:50054"),
		ServicePrivateKey: svcKey,
	}, nil
}
