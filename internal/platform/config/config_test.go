package config_test

import (
	"testing"
	"time"

	"github.com/francispowlesland/microservices-app-go/internal/platform/config"
)

func TestString(t *testing.T) {
	t.Setenv("TEST_KEY", "value")
	if got := config.String("TEST_KEY", "default"); got != "value" {
		t.Fatalf("expected value, got %s", got)
	}
	if got := config.String("UNSET_KEY", "default"); got != "default" {
		t.Fatalf("expected default, got %s", got)
	}
}

func TestRequired(t *testing.T) {
	t.Setenv("TEST_REQ", "val")
	got, err := config.Required("TEST_REQ")
	if err != nil || got != "val" {
		t.Fatalf("expected val with nil err, got %q, %v", got, err)
	}

	_, err = config.Required("UNSET_REQ")
	if err == nil {
		t.Fatal("expected error for unset key")
	}
}

func TestDuration(t *testing.T) {
	t.Setenv("TEST_DUR", "5s")
	d, err := config.Duration("TEST_DUR", time.Second)
	if err != nil || d != 5*time.Second {
		t.Fatalf("expected 5s, got %v, %v", d, err)
	}

	d, err = config.Duration("UNSET_DUR", time.Second)
	if err != nil || d != time.Second {
		t.Fatalf("expected 1s, got %v, %v", d, err)
	}

	t.Setenv("INVALID_DUR", "invalid")
	_, err = config.Duration("INVALID_DUR", time.Second)
	if err == nil {
		t.Fatal("expected error for invalid duration")
	}
}

func TestInt(t *testing.T) {
	t.Setenv("TEST_INT", "42")
	i, err := config.Int("TEST_INT", 10)
	if err != nil || i != 42 {
		t.Fatalf("expected 42, got %v, %v", i, err)
	}

	i, err = config.Int("UNSET_INT", 10)
	if err != nil || i != 10 {
		t.Fatalf("expected 10, got %v, %v", i, err)
	}

	t.Setenv("INVALID_INT", "notanint")
	_, err = config.Int("INVALID_INT", 10)
	if err == nil {
		t.Fatal("expected error for invalid int")
	}
}
