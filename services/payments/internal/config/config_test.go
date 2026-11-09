package config

import "testing"

func TestLoad(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		t.Setenv("DATABASE_URL", "postgres://x")
		t.Setenv("JWT_PUBLIC_KEY", "pem")
		t.Setenv("GRPC_ADDR", "")
		t.Setenv("APP_ENV", "")
		t.Setenv("PAYMENT_MAX_CENTS", "")
		c, err := Load()
		if err != nil {
			t.Fatal(err)
		}
		if c.GRPCAddr != ":50053" || c.AppEnv != "development" || c.MaxCents != 1_000_000 {
			t.Fatalf("unexpected defaults: %+v", c)
		}
	})
	t.Run("overrides", func(t *testing.T) {
		t.Setenv("DATABASE_URL", "postgres://x")
		t.Setenv("JWT_PUBLIC_KEY", "pem")
		t.Setenv("PAYMENT_MAX_CENTS", "500")
		c, err := Load()
		if err != nil || c.MaxCents != 500 {
			t.Fatalf("got %+v, %v", c, err)
		}
	})
	for name, env := range map[string]map[string]string{
		"missing database url": {"DATABASE_URL": "", "JWT_PUBLIC_KEY": "pem"},
		"missing public key":   {"DATABASE_URL": "x", "JWT_PUBLIC_KEY": ""},
		"bad max":              {"DATABASE_URL": "x", "JWT_PUBLIC_KEY": "pem", "PAYMENT_MAX_CENTS": "abc"},
		"non-positive max":     {"DATABASE_URL": "x", "JWT_PUBLIC_KEY": "pem", "PAYMENT_MAX_CENTS": "0"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("PAYMENT_MAX_CENTS", "")
			for k, v := range env {
				t.Setenv(k, v)
			}
			if _, err := Load(); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}
