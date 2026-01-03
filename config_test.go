package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestGetUpstreamTimeout(t *testing.T) {
	tests := []struct {
		name     string
		timeout  string
		expected time.Duration
	}{
		{"valid duration", "60s", 60 * time.Second},
		{"valid minutes", "2m", 2 * time.Minute},
		{"empty string defaults to 30s", "", 30 * time.Second},
		{"invalid string defaults to 30s", "invalid", 30 * time.Second},
		{"zero duration", "0s", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := &Config{
				Upstream: Upstream{Timeout: tt.timeout},
			}
			got := config.GetUpstreamTimeout()
			if got != tt.expected {
				t.Errorf("GetUpstreamTimeout() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestIsMethodAllowed(t *testing.T) {
	tests := []struct {
		name           string
		allowedMethods []string
		method         string
		expected       bool
	}{
		{"method in list", []string{"getblock", "getinfo"}, "getblock", true},
		{"method not in list", []string{"getblock", "getinfo"}, "sendtoaddress", false},
		{"empty list", []string{}, "getblock", false},
		{"empty method", []string{"getblock"}, "", false},
		{"case sensitive - exact match", []string{"getBlock"}, "getBlock", true},
		{"case sensitive - no match", []string{"getBlock"}, "getblock", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := &Config{AllowedMethods: tt.allowedMethods}
			got := config.IsMethodAllowed(tt.method)
			if got != tt.expected {
				t.Errorf("IsMethodAllowed(%q) = %v, want %v", tt.method, got, tt.expected)
			}
		})
	}
}

func TestGetZMQTopic(t *testing.T) {
	tests := []struct {
		name     string
		topic    string
		expected string
	}{
		{"empty defaults to hashblock", "", "hashblock"},
		{"custom topic", "rawtx", "rawtx"},
		{"whitespace topic", "  ", "  "},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := &Config{ZMQ: ZMQConfig{Topic: tt.topic}}
			got := config.GetZMQTopic()
			if got != tt.expected {
				t.Errorf("GetZMQTopic() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestConfigValidation(t *testing.T) {
	tests := []struct {
		name      string
		config    Config
		wantError bool
		errorMsg  string
	}{
		{
			name: "valid minimal config",
			config: Config{
				Listen:         ":8080",
				Upstream:       Upstream{URL: "http://localhost:8232"},
				AllowedMethods: []string{"getinfo"},
			},
			wantError: false,
		},
		{
			name: "missing listen address",
			config: Config{
				Upstream:       Upstream{URL: "http://localhost:8232"},
				AllowedMethods: []string{"getinfo"},
			},
			wantError: true,
			errorMsg:  "listen address is required",
		},
		{
			name: "missing upstream URL",
			config: Config{
				Listen:         ":8080",
				AllowedMethods: []string{"getinfo"},
			},
			wantError: true,
			errorMsg:  "upstream URL is required",
		},
		{
			name: "empty allowed methods",
			config: Config{
				Listen:   ":8080",
				Upstream: Upstream{URL: "http://localhost:8232"},
			},
			wantError: true,
			errorMsg:  "at least one allowed method is required",
		},
		{
			name: "proxy auth enabled without username",
			config: Config{
				Listen:         ":8080",
				Upstream:       Upstream{URL: "http://localhost:8232"},
				AllowedMethods: []string{"getinfo"},
				ProxyAuth:      ProxyAuth{Enabled: true, Password: "pass"},
			},
			wantError: true,
			errorMsg:  "proxy_auth username and password are required when enabled",
		},
		{
			name: "proxy auth enabled without password",
			config: Config{
				Listen:         ":8080",
				Upstream:       Upstream{URL: "http://localhost:8232"},
				AllowedMethods: []string{"getinfo"},
				ProxyAuth:      ProxyAuth{Enabled: true, Username: "user"},
			},
			wantError: true,
			errorMsg:  "proxy_auth username and password are required when enabled",
		},
		{
			name: "proxy auth enabled with credentials",
			config: Config{
				Listen:         ":8080",
				Upstream:       Upstream{URL: "http://localhost:8232"},
				AllowedMethods: []string{"getinfo"},
				ProxyAuth:      ProxyAuth{Enabled: true, Username: "user", Password: "pass"},
			},
			wantError: false,
		},
		{
			name: "ZMQ enabled without upstream URL",
			config: Config{
				Listen:         ":8080",
				Upstream:       Upstream{URL: "http://localhost:8232"},
				AllowedMethods: []string{"getinfo"},
				ZMQ:            ZMQConfig{Enabled: true, Listen: "tcp://0.0.0.0:28333"},
			},
			wantError: true,
			errorMsg:  "zmq upstream_url is required when zmq is enabled",
		},
		{
			name: "ZMQ enabled without listen",
			config: Config{
				Listen:         ":8080",
				Upstream:       Upstream{URL: "http://localhost:8232"},
				AllowedMethods: []string{"getinfo"},
				ZMQ:            ZMQConfig{Enabled: true, UpstreamURL: "tcp://127.0.0.1:28332"},
			},
			wantError: true,
			errorMsg:  "zmq listen address is required when zmq is enabled",
		},
		{
			name: "ZMQ enabled with all required fields",
			config: Config{
				Listen:         ":8080",
				Upstream:       Upstream{URL: "http://localhost:8232"},
				AllowedMethods: []string{"getinfo"},
				ZMQ: ZMQConfig{
					Enabled:     true,
					UpstreamURL: "tcp://127.0.0.1:28332",
					Listen:      "tcp://0.0.0.0:28333",
				},
			},
			wantError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.validate()
			if tt.wantError {
				if err == nil {
					t.Errorf("validate() expected error containing %q, got nil", tt.errorMsg)
				} else if tt.errorMsg != "" && err.Error() != tt.errorMsg {
					t.Errorf("validate() error = %q, want %q", err.Error(), tt.errorMsg)
				}
			} else {
				if err != nil {
					t.Errorf("validate() unexpected error: %v", err)
				}
			}
		})
	}
}

func TestLoadConfig(t *testing.T) {
	t.Run("valid config file", func(t *testing.T) {
		content := `
listen = "0.0.0.0:8080"
allowed_methods = ["getinfo", "getblock"]

[upstream]
url = "http://localhost:8232"
timeout = "45s"
`
		path := writeTempConfig(t, content)
		config, err := LoadConfig(path)
		if err != nil {
			t.Fatalf("LoadConfig() error = %v", err)
		}
		if config.Listen != "0.0.0.0:8080" {
			t.Errorf("Listen = %q, want %q", config.Listen, "0.0.0.0:8080")
		}
		if config.Upstream.URL != "http://localhost:8232" {
			t.Errorf("Upstream.URL = %q, want %q", config.Upstream.URL, "http://localhost:8232")
		}
		if len(config.AllowedMethods) != 2 {
			t.Errorf("AllowedMethods len = %d, want 2", len(config.AllowedMethods))
		}
	})

	t.Run("missing file", func(t *testing.T) {
		_, err := LoadConfig("/nonexistent/path/config.toml")
		if err == nil {
			t.Error("LoadConfig() expected error for missing file, got nil")
		}
	})

	t.Run("invalid TOML", func(t *testing.T) {
		content := `
listen = "unclosed string
`
		path := writeTempConfig(t, content)
		_, err := LoadConfig(path)
		if err == nil {
			t.Error("LoadConfig() expected error for invalid TOML, got nil")
		}
	})

	t.Run("validation error", func(t *testing.T) {
		content := `
listen = ""
allowed_methods = ["getinfo"]

[upstream]
url = "http://localhost:8232"
`
		path := writeTempConfig(t, content)
		_, err := LoadConfig(path)
		if err == nil {
			t.Error("LoadConfig() expected validation error, got nil")
		}
	})
}

// writeTempConfig writes content to a temporary file and returns its path
func writeTempConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write temp config: %v", err)
	}
	return path
}
