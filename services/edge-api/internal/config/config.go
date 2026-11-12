// Package config loads the edge configuration from environment variables
// (see /.env.example). There are no secret defaults: anything sensitive must
// be supplied by the operator.
package config

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	SiteID   string
	SiteName string

	HTTPAddr     string
	StaticDir    string // optional override of embedded static assets
	CookieSecure bool   // MUST be true behind TLS

	DBPath string

	MQTTURL      string
	MQTTUser     string
	MQTTPassword string

	// AllowSimulatedData: accept data_source=SIMULATOR. Off in production;
	// simulator rows are always stored/labelled as SIMULATOR regardless.
	AllowSimulatedData bool

	// Timing policy (spec: heartbeat 5 s, stale after ~15 s, <=3 retries)
	StaleAfter      time.Duration
	CommandRetry    time.Duration
	CommandMaxTries int
	CommandTTL      time.Duration

	// Resource policy
	MemSoftLimitMiB int
	RetentionDays   int
	MinFreeBytes    uint64
	RecordQueueSize int

	// Cloud sync is OFF by default; the outbox is only written when linked.
	CloudSyncEnabled   bool
	CloudURL           string // https://... (plain http only for loopback or explicit insecure flag)
	CloudToken         string // per-site edge credential; supply via secrets, never Git
	CloudAllowInsecure bool   // lab only
	CloudSyncInterval  time.Duration
	// RemotePrograms: accept programme REQUESTS that originated in the cloud.
	// Default OFF: a site must opt in locally. Even when on, the Pi revalidates
	// and the ESP32 confirms; manual pump tests are never accepted remotely.
	RemotePrograms bool

	BootstrapAdminUser     string
	BootstrapAdminPassword string
	ProvisionFile          string // dev only: devices.json from aerolab-seed

	LogLevel string
}

func env(k, def string) string {
	if v, ok := os.LookupEnv(k); ok && v != "" {
		return v
	}
	return def
}

func envInt(k string, def int) (int, error) {
	v := os.Getenv(k)
	if v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", k, err)
	}
	return n, nil
}

func envBool(k string, def bool) bool {
	switch strings.ToLower(os.Getenv(k)) {
	case "1", "true", "yes":
		return true
	case "0", "false", "no":
		return false
	}
	return def
}

func envDur(k string, def time.Duration) (time.Duration, error) {
	v := os.Getenv(k)
	if v == "" {
		return def, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", k, err)
	}
	return d, nil
}

// Load reads and validates the environment.
func Load() (*Config, error) {
	var err error
	c := &Config{
		SiteID: env("AEROLAB_SITE_ID", ""), SiteName: env("AEROLAB_SITE_NAME", "AeroLab Site"),
		HTTPAddr: env("AEROLAB_HTTP_ADDR", "127.0.0.1:8080"), StaticDir: env("AEROLAB_STATIC_DIR", ""),
		CookieSecure: envBool("AEROLAB_COOKIE_SECURE", false),
		DBPath:       env("AEROLAB_DB_PATH", "/var/lib/aerolab/aerolab.db"),
		MQTTURL:      env("AEROLAB_MQTT_URL", "tcp://127.0.0.1:1883"), MQTTUser: env("AEROLAB_MQTT_USER", ""), MQTTPassword: env("AEROLAB_MQTT_PASSWORD", ""),
		AllowSimulatedData: envBool("AEROLAB_ALLOW_SIMULATED_DATA", false),
		CloudSyncEnabled:   envBool("AEROLAB_CLOUD_SYNC_ENABLED", false),
		BootstrapAdminUser: env("AEROLAB_BOOTSTRAP_ADMIN_USER", ""), BootstrapAdminPassword: env("AEROLAB_BOOTSTRAP_ADMIN_PASSWORD", ""),
		ProvisionFile: env("AEROLAB_PROVISION_FILE", ""), LogLevel: env("AEROLAB_LOG_LEVEL", "info"),
	}
	c.CloudURL, c.CloudToken = env("AEROLAB_CLOUD_URL", ""), env("AEROLAB_CLOUD_TOKEN", "")
	c.CloudAllowInsecure = envBool("AEROLAB_CLOUD_ALLOW_INSECURE", false)
	c.RemotePrograms = envBool("AEROLAB_REMOTE_PROGRAMS", false)
	if c.CloudSyncInterval, err = envDur("AEROLAB_CLOUD_SYNC_INTERVAL", 5*time.Second); err != nil {
		return nil, err
	}
	if c.StaleAfter, err = envDur("AEROLAB_STALE_AFTER", 15*time.Second); err != nil {
		return nil, err
	}
	if c.CommandRetry, err = envDur("AEROLAB_COMMAND_RETRY", 5*time.Second); err != nil {
		return nil, err
	}
	if c.CommandTTL, err = envDur("AEROLAB_COMMAND_TTL", 120*time.Second); err != nil {
		return nil, err
	}
	if c.CommandMaxTries, err = envInt("AEROLAB_COMMAND_MAX_TRIES", 3); err != nil {
		return nil, err
	}
	if c.MemSoftLimitMiB, err = envInt("AEROLAB_MEM_SOFT_LIMIT_MIB", 192); err != nil {
		return nil, err
	}
	if c.RetentionDays, err = envInt("AEROLAB_RETENTION_DAYS", 30); err != nil {
		return nil, err
	}
	if c.RecordQueueSize, err = envInt("AEROLAB_RECORD_QUEUE", 2048); err != nil {
		return nil, err
	}
	minFree, err := envInt("AEROLAB_MIN_FREE_MIB", 64)
	if err != nil {
		return nil, err
	}
	c.MinFreeBytes = uint64(minFree) << 20
	return c, c.Validate()
}

func (c *Config) Validate() error {
	if c.SiteID == "" {
		return fmt.Errorf("AEROLAB_SITE_ID is required")
	}
	if c.CommandMaxTries < 1 || c.CommandMaxTries > 3 {
		return fmt.Errorf("AEROLAB_COMMAND_MAX_TRIES must be 1..3 (spec: at most 3 application-level retries)")
	}
	if c.CommandTTL < 5*time.Second || c.CommandTTL > 5*time.Minute {
		return fmt.Errorf("AEROLAB_COMMAND_TTL must be 5s..5m")
	}
	if c.StaleAfter < time.Second {
		return fmt.Errorf("AEROLAB_STALE_AFTER too small")
	}
	if c.RetentionDays < 1 {
		return fmt.Errorf("AEROLAB_RETENTION_DAYS must be >= 1")
	}
	if c.CloudSyncEnabled {
		if c.CloudURL == "" || c.CloudToken == "" {
			return fmt.Errorf("cloud sync enabled but AEROLAB_CLOUD_URL / AEROLAB_CLOUD_TOKEN missing")
		}
		u, err := url.Parse(c.CloudURL)
		if err != nil || u.Host == "" {
			return fmt.Errorf("AEROLAB_CLOUD_URL invalid")
		}
		host := u.Hostname()
		loop := host == "localhost" || host == "127.0.0.1" || host == "::1"
		if u.Scheme != "https" && !(u.Scheme == "http" && (loop || c.CloudAllowInsecure)) {
			return fmt.Errorf("AEROLAB_CLOUD_URL must be https (http only for loopback or AEROLAB_CLOUD_ALLOW_INSECURE=true)")
		}
		if c.CloudSyncInterval < time.Second {
			return fmt.Errorf("AEROLAB_CLOUD_SYNC_INTERVAL must be >= 1s")
		}
	}
	if c.RecordQueueSize < 16 || c.RecordQueueSize > 65536 {
		return fmt.Errorf("AEROLAB_RECORD_QUEUE must be 16..65536")
	}
	return nil
}
