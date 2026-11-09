package config

import (
	"testing"
	"time"
)

func TestValidate(t *testing.T) {
	ok := Config{AppEnv: "development", Mailer: "log", AccessTTL: time.Minute, RefreshTTL: time.Hour}
	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr bool
	}{
		{"ok", func(*Config) {}, false},
		{"production with log mailer refuses", func(c *Config) { c.AppEnv = "production" }, true},
		{"unknown env", func(c *Config) { c.AppEnv = "staging" }, true},
		{"unknown mailer", func(c *Config) { c.Mailer = "smtp" }, true},
		{"zero ttl", func(c *Config) { c.AccessTTL = 0 }, true},
		{"half bootstrap pair", func(c *Config) { c.BootstrapAdminEmail = "a@b.co" }, true},
		{"full bootstrap pair", func(c *Config) { c.BootstrapAdminEmail = "a@b.co"; c.BootstrapAdminPassword = "x" }, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := ok
			tt.mutate(&c)
			if err := c.Validate(); (err != nil) != tt.wantErr {
				t.Fatalf("Validate() err = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
