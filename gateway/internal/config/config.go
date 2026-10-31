// Package config loads and validates the gateway configuration.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const DefaultUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36"

// RateLimit configures the per-IP token bucket. RPS <= 0 disables limiting.
type RateLimit struct {
	RPS   float64 `json:"rps"`
	Burst int     `json:"burst"`
}

// Config is the on-disk gateway configuration. All durations are seconds and
// all size limits are MiB unless stated otherwise.
type Config struct {
	Listen        string            `json:"listen"`
	WebDir        string            `json:"webDir"`
	WebDir2       string            `json:"webDir2"`
	DataDir       string            `json:"dataDir"`
	Secret        string            `json:"secret"`
	Password      string            `json:"password"`
	AllowHosts    []string          `json:"allowHosts"`
	AllowAll      bool              `json:"allowAll"`
	HostOverrides map[string]string `json:"hostOverrides"`
	OutboundProxy string            `json:"outboundProxy"`

	CacheDir   string `json:"cacheDir"`
	CacheTTL   int    `json:"cacheTTL"`
	CacheMaxMB int    `json:"cacheMaxMB"`

	DownloadsDir string `json:"downloadsDir"`

	MaxBodyMB  int `json:"maxBodyMB"`
	MaxRespMB  int `json:"maxRespMB"`
	TimeoutSec int `json:"timeoutSec"`

	RateLimit RateLimit `json:"rateLimit"`

	CookieSecure   bool `json:"cookieSecure"`
	TrustProxy     bool `json:"trustProxy"`
	MetaSetCookies bool `json:"metaSetCookies"`
	ImgTokenTTL    int  `json:"imgTokenTTL"`

	UserAgent      string                       `json:"userAgent"`
	HeaderProfiles map[string]map[string]string `json:"headerProfiles"`
}

// Default returns the built-in configuration.
func Default() Config {
	return Config{
		Listen:      "127.0.0.1:18081",
		WebDir:      "./web",
		DataDir:     "./data",
		AllowHosts:  []string{},
		CacheTTL:    86400,
		CacheMaxMB:  512,
		MaxBodyMB:   8,
		MaxRespMB:   200,
		TimeoutSec:  120,
		RateLimit:   RateLimit{RPS: 30, Burst: 60},
		ImgTokenTTL: 3600,
		UserAgent:   DefaultUserAgent,
	}
}

// Load reads path if it exists, applies environment overrides and validates
// the result. A missing config file is not an error: defaults and environment
// variables are used.
func Load(path string) (*Config, error) {
	cfg := Default()

	if path != "" {
		b, err := os.ReadFile(path)
		switch {
		case err == nil:
			dec := json.NewDecoder(bytes.NewReader(b))
			dec.DisallowUnknownFields()
			if err := dec.Decode(&cfg); err != nil {
				return nil, fmt.Errorf("parse config %s: %w", path, err)
			}
		case errors.Is(err, os.ErrNotExist):
			// Fall through to defaults.
		default:
			return nil, fmt.Errorf("read config %s: %w", path, err)
		}
	}

	if err := applyEnv(&cfg); err != nil {
		return nil, err
	}
	cfg.normalize()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (c *Config) normalize() {
	c.Listen = strings.TrimSpace(c.Listen)
	c.WebDir = strings.TrimSpace(c.WebDir)
	c.DataDir = strings.TrimSpace(c.DataDir)
	if c.DataDir == "" {
		c.DataDir = "./data"
	}
	if c.WebDir == "" {
		c.WebDir = "./web"
	}
	c.WebDir2 = strings.TrimSpace(c.WebDir2)
	if c.CacheDir == "" {
		c.CacheDir = filepath.Join(c.DataDir, "imgcache")
	}
	if c.DownloadsDir == "" {
		c.DownloadsDir = filepath.Join(c.DataDir, "downloads")
	}
	if c.CacheTTL < 0 {
		c.CacheTTL = 0
	}
	if c.CacheMaxMB < 0 {
		c.CacheMaxMB = 0
	}
	if c.ImgTokenTTL <= 0 {
		c.ImgTokenTTL = 3600
	}
	if c.RateLimit.RPS > 0 && c.RateLimit.Burst <= 0 {
		c.RateLimit.Burst = int(c.RateLimit.RPS * 2)
		if c.RateLimit.Burst < 1 {
			c.RateLimit.Burst = 1
		}
	}

	hosts := make([]string, 0, len(c.AllowHosts))
	for _, h := range c.AllowHosts {
		h = strings.ToLower(strings.TrimSpace(strings.TrimSuffix(h, ".")))
		if h != "" {
			hosts = append(hosts, h)
		}
	}
	c.AllowHosts = hosts

	if len(c.HostOverrides) > 0 {
		m := make(map[string]string, len(c.HostOverrides))
		for k, v := range c.HostOverrides {
			m[strings.ToLower(strings.TrimSpace(strings.TrimSuffix(k, ".")))] = strings.TrimSpace(v)
		}
		c.HostOverrides = m
	}
}

// Validate checks configuration invariants.
func (c *Config) Validate() error {
	if c.Listen == "" {
		return errors.New("listen must not be empty")
	}
	if c.MaxBodyMB <= 0 || c.MaxRespMB <= 0 {
		return errors.New("maxBodyMB and maxRespMB must be positive")
	}
	if c.TimeoutSec <= 0 {
		return errors.New("timeoutSec must be positive")
	}
	if c.RateLimit.RPS < 0 || c.RateLimit.Burst < 0 {
		return errors.New("rateLimit values must not be negative")
	}
	for _, h := range c.AllowHosts {
		if h == "*" {
			return errors.New(`allowHosts cannot contain "*"; use allowAll instead`)
		}
		if strings.ContainsAny(h, "/: ") {
			return fmt.Errorf("allowHosts entry %q is not a hostname", h)
		}
	}
	for host, ip := range c.HostOverrides {
		if host == "" {
			return errors.New("hostOverrides contains an empty host")
		}
		if ip == "" {
			return fmt.Errorf("hostOverrides[%s] has an empty IP", host)
		}
	}
	return nil
}

func applyEnv(c *Config) error {
	if v := os.Getenv("PICA_LISTEN"); v != "" {
		c.Listen = v
	}
	if v := os.Getenv("PICA_WEB_DIR"); v != "" {
		c.WebDir = v
	}
	if v := os.Getenv("PICA_WEB_DIR2"); v != "" {
		c.WebDir2 = v
	}
	if v := os.Getenv("PICA_DATA_DIR"); v != "" {
		c.DataDir = v
	}
	if v := os.Getenv("PICA_SECRET"); v != "" {
		c.Secret = v
	}
	if v := os.Getenv("PICA_PASSWORD"); v != "" {
		c.Password = v
	}
	if v := os.Getenv("PICA_ALLOW_HOSTS"); v != "" {
		c.AllowHosts = splitList(v)
	}
	if v := os.Getenv("PICA_ALLOW_ALL"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return fmt.Errorf("PICA_ALLOW_ALL: %w", err)
		}
		c.AllowAll = b
	}
	if v := os.Getenv("PICA_OUTBOUND_PROXY"); v != "" {
		c.OutboundProxy = v
	}
	if v := os.Getenv("PICA_CACHE_DIR"); v != "" {
		c.CacheDir = v
	}
	if v := os.Getenv("PICA_DOWNLOADS_DIR"); v != "" {
		c.DownloadsDir = v
	}
	if err := envInt("PICA_CACHE_TTL", &c.CacheTTL); err != nil {
		return err
	}
	if err := envInt("PICA_CACHE_MAX_MB", &c.CacheMaxMB); err != nil {
		return err
	}
	if err := envInt("PICA_MAX_BODY_MB", &c.MaxBodyMB); err != nil {
		return err
	}
	if err := envInt("PICA_MAX_RESP_MB", &c.MaxRespMB); err != nil {
		return err
	}
	if err := envInt("PICA_TIMEOUT_SEC", &c.TimeoutSec); err != nil {
		return err
	}
	if err := envInt("PICA_IMG_TOKEN_TTL", &c.ImgTokenTTL); err != nil {
		return err
	}
	if v := os.Getenv("PICA_RATE_RPS"); v != "" {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return fmt.Errorf("PICA_RATE_RPS: %w", err)
		}
		c.RateLimit.RPS = f
	}
	if err := envInt("PICA_RATE_BURST", &c.RateLimit.Burst); err != nil {
		return err
	}
	if v := os.Getenv("PICA_COOKIE_SECURE"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return fmt.Errorf("PICA_COOKIE_SECURE: %w", err)
		}
		c.CookieSecure = b
	}
	if v := os.Getenv("PICA_TRUST_PROXY"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return fmt.Errorf("PICA_TRUST_PROXY: %w", err)
		}
		c.TrustProxy = b
	}
	if v := os.Getenv("PICA_META_SET_COOKIES"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return fmt.Errorf("PICA_META_SET_COOKIES: %w", err)
		}
		c.MetaSetCookies = b
	}
	if v := os.Getenv("PICA_USER_AGENT"); v != "" {
		c.UserAgent = v
	}
	return nil
}

func envInt(name string, dst *int) error {
	v := os.Getenv(name)
	if v == "" {
		return nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	*dst = n
	return nil
}

func splitList(v string) []string {
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// BodyLimit returns the maximum request body size in bytes.
func (c *Config) BodyLimit() int64 { return int64(c.MaxBodyMB) << 20 }

// RespLimit returns the maximum upstream response size in bytes.
func (c *Config) RespLimit() int64 { return int64(c.MaxRespMB) << 20 }

// Timeout returns the upstream request timeout.
func (c *Config) Timeout() time.Duration {
	return time.Duration(c.TimeoutSec) * time.Second
}

// CacheTTLDuration returns the image cache TTL.
func (c *Config) CacheTTLDuration() time.Duration {
	return time.Duration(c.CacheTTL) * time.Second
}

// CacheMaxBytes returns the image cache size limit in bytes.
func (c *Config) CacheMaxBytes() int64 { return int64(c.CacheMaxMB) << 20 }

// ImgTokenTTLDuration returns the signed image URL lifetime.
func (c *Config) ImgTokenTTLDuration() time.Duration {
	return time.Duration(c.ImgTokenTTL) * time.Second
}

// WebDir2Resolved returns the Vue webui root. An empty webDir2 falls back to
// <webDir>/../webui/dist; a missing directory is fine, /v2 simply 404s then.
func (c *Config) WebDir2Resolved() string {
	if c.WebDir2 != "" {
		return c.WebDir2
	}
	return filepath.Join(c.WebDir, "..", "webui", "dist")
}
