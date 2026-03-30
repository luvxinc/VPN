package config_test

import (
	"os"
	"testing"

	"github.com/luvxinc/vpn/server/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoad_FromEnvVar(t *testing.T) {
	f, err := os.CreateTemp("", "weiai-cfg-*.yaml")
	require.NoError(t, err)
	defer os.Remove(f.Name())

	f.WriteString(`
database:
  url: "postgresql://localhost/testdb"
  pool_size: 5
redis:
  url: "redis://localhost:6379/1"
server:
  ip: "1.2.3.4"
  port: 443
  public_key: "pubkey"
  private_key: "privkey"
  short_id: "aabbccdd"
  server_name: "example.com"
auth:
  jwt_secret: "supersecretkey_atleast32chars!!!"
  jwt_expiry_minutes: 15
  refresh_expiry_hours: 24
admin:
  allowed_lan_prefixes: ["127."]
  username: "admin"
  password_hash: "$2b$12$abc"
certs:
  cert_path: "certs/server.crt"
  key_path: "certs/server.key"
sing_box:
  config_path: "sing-box-server.json"
  binary_path: "/usr/local/bin/sing-box"
  clash_api_url: "http://127.0.0.1:9090"
geoip:
  db_path: "GeoLite2-City.mmdb"
log:
  retention_days: 90
  max_domains_per_user_per_day: 500
client:
  min_version: "1.0.0"
  download_url: "https://example.com/download"
  client_zip_path: "../client/dist/app.zip"
`)
	f.Close()

	os.Setenv("WEIAI_CONFIG", f.Name())
	defer os.Unsetenv("WEIAI_CONFIG")

	cfg, err := config.Load()
	require.NoError(t, err)

	assert.Equal(t, "postgresql://localhost/testdb", cfg.Database.URL)
	assert.Equal(t, 5, cfg.Database.PoolSize)
	assert.Equal(t, "redis://localhost:6379/1", cfg.Redis.URL)
	assert.Equal(t, "1.2.3.4", cfg.Server.IP)
	assert.Equal(t, 443, cfg.Server.Port)
	assert.Equal(t, "supersecretkey_atleast32chars!!!", cfg.Auth.JWTSecret)
	assert.Equal(t, 15, cfg.Auth.JWTExpiryMinutes)
	assert.Equal(t, "1.0.0", cfg.Client.MinVersion)
	assert.Equal(t, []string{"127."}, cfg.Admin.AllowedLANPrefixes)
}

func TestLoad_FallbackDefaults(t *testing.T) {
	f, err := os.CreateTemp("", "weiai-cfg-minimal-*.yaml")
	require.NoError(t, err)
	defer os.Remove(f.Name())

	// Minimal config — should trigger all defaults
	f.WriteString(`
database:
  url: "postgresql://localhost/testdb"
redis:
  url: "redis://localhost:6379/0"
auth:
  jwt_secret: "some_secret_key_that_is_32chars!!"
admin:
  username: "admin"
  password_hash: "$2b$12$abc"
`)
	f.Close()

	os.Setenv("WEIAI_CONFIG", f.Name())
	defer os.Unsetenv("WEIAI_CONFIG")

	cfg, err := config.Load()
	require.NoError(t, err)

	assert.Equal(t, 10, cfg.Database.PoolSize)
	assert.Equal(t, 15, cfg.Auth.JWTExpiryMinutes)
	assert.Equal(t, 24, cfg.Auth.RefreshExpiryHours)
	assert.Equal(t, 90, cfg.Log.RetentionDays)
	assert.Equal(t, 500, cfg.Log.MaxDomainsPerUserPerDay)
	assert.Equal(t, "1.0.0", cfg.Client.MinVersion)
}

func TestLoad_MissingFile(t *testing.T) {
	os.Setenv("WEIAI_CONFIG", "/nonexistent/path/config.yaml")
	defer os.Unsetenv("WEIAI_CONFIG")

	_, err := config.Load()
	assert.Error(t, err)
}

func TestMustLoad_Panics(t *testing.T) {
	os.Setenv("WEIAI_CONFIG", "/nonexistent/path/config.yaml")
	defer os.Unsetenv("WEIAI_CONFIG")

	assert.Panics(t, func() { config.MustLoad() })
}
