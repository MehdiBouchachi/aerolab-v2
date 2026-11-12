// Package config loads cloud configuration from the environment.
package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

type Config struct {
	HTTPAddr     string
	DatabaseURL  string
	CookieSecure bool
	StaticDir    string // built apps/web for the cloud origin
	// StaleAfter: a site that has not synced for this long is shown as stale/offline.
	StaleAfter time.Duration
	// IntentTTL: how long a remote programme request may wait for the Pi.
	IntentTTL time.Duration
	LogLevel  string
}

func Load() (*Config, error) {
	c := &Config{HTTPAddr: get("AEROLAB_CLOUD_HTTP_ADDR", "127.0.0.1:8090"), DatabaseURL: os.Getenv("AEROLAB_CLOUD_DATABASE_URL"),
		CookieSecure: get("AEROLAB_CLOUD_COOKIE_SECURE", "true") != "false", StaticDir: os.Getenv("AEROLAB_CLOUD_STATIC_DIR"),
		StaleAfter: 90 * time.Second, IntentTTL: 2 * time.Minute, LogLevel: get("AEROLAB_LOG_LEVEL", "info")}
	var err error
	if v := os.Getenv("AEROLAB_CLOUD_STALE_AFTER"); v != "" {
		if c.StaleAfter, err = time.ParseDuration(v); err != nil {
			return nil, fmt.Errorf("AEROLAB_CLOUD_STALE_AFTER: %w", err)
		}
	}
	if v := os.Getenv("AEROLAB_CLOUD_INTENT_TTL"); v != "" {
		if c.IntentTTL, err = time.ParseDuration(v); err != nil {
			return nil, fmt.Errorf("AEROLAB_CLOUD_INTENT_TTL: %w", err)
		}
	}
	_ = strconv.Itoa
	if c.DatabaseURL == "" {
		return nil, fmt.Errorf("AEROLAB_CLOUD_DATABASE_URL is required")
	}
	if c.IntentTTL < 10*time.Second || c.IntentTTL > 10*time.Minute {
		return nil, fmt.Errorf("AEROLAB_CLOUD_INTENT_TTL must be 10s..10m (remote changes must be short-lived)")
	}
	return c, nil
}

func get(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
