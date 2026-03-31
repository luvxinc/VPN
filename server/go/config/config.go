package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// MinClientVersion is the minimum client version the server will accept.
// Bump this whenever a client update should be enforced.
const MinClientVersion = "1.0.8"

type DatabaseConfig struct {
	URL      string `yaml:"url"`
	PoolSize int    `yaml:"pool_size"`
}

type RedisConfig struct {
	URL string `yaml:"url"`
}

type ServerConfig struct {
	IP               string `yaml:"ip"`
	Port             int    `yaml:"port"`
	AuthPort         int    `yaml:"auth_port"`
	WSPort           int    `yaml:"ws_port"`
	WSFallbackDomain string `yaml:"ws_fallback_domain"`
	PublicKey        string `yaml:"public_key"`
	PrivateKey       string `yaml:"private_key"`
	ShortID          string `yaml:"short_id"`
	ServerName       string `yaml:"server_name"`
}

type AuthConfig struct {
	JWTSecret          string `yaml:"jwt_secret"`
	JWTExpiryMinutes   int    `yaml:"jwt_expiry_minutes"`
	RefreshExpiryHours int    `yaml:"refresh_expiry_hours"`
}

type AdminConfig struct {
	AllowedLANPrefixes []string `yaml:"allowed_lan_prefixes"`
	Username           string   `yaml:"username"`
	PasswordHash       string   `yaml:"password_hash"`
}

type CertsConfig struct {
	CertPath string `yaml:"cert_path"`
	KeyPath  string `yaml:"key_path"`
}

type SingBoxConfig struct {
	ConfigPath  string `yaml:"config_path"`
	BinaryPath  string `yaml:"binary_path"`
	ClashAPIURL string `yaml:"clash_api_url"`
}

type GeoIPConfig struct {
	DBPath string `yaml:"db_path"`
}

type LogConfig struct {
	RetentionDays            int `yaml:"retention_days"`
	MaxDomainsPerUserPerDay  int `yaml:"max_domains_per_user_per_day"`
}

type ClientConfig struct {
	MinVersion    string `yaml:"min_version"`
	DownloadURL   string `yaml:"download_url"`
	ClientZipPath string `yaml:"client_zip_path"`
}

type Config struct {
	Database DatabaseConfig `yaml:"database"`
	Redis    RedisConfig    `yaml:"redis"`
	Server   ServerConfig   `yaml:"server"`
	Auth     AuthConfig     `yaml:"auth"`
	Admin    AdminConfig    `yaml:"admin"`
	Certs    CertsConfig    `yaml:"certs"`
	SingBox  SingBoxConfig  `yaml:"sing_box"`
	GeoIP    GeoIPConfig    `yaml:"geoip"`
	Log      LogConfig      `yaml:"log"`
	Client   ClientConfig   `yaml:"client"`
}

func Load() (*Config, error) {
	path := os.Getenv("WEIAI_CONFIG")
	if path == "" {
		path = "config.yaml"
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config: cannot read %s: %w", path, err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("config: parse error: %w", err)
	}
	if cfg.Database.PoolSize == 0 {
		cfg.Database.PoolSize = 10
	}
	if cfg.Auth.JWTExpiryMinutes == 0 {
		cfg.Auth.JWTExpiryMinutes = 15
	}
	if cfg.Auth.RefreshExpiryHours == 0 {
		cfg.Auth.RefreshExpiryHours = 24
	}
	if cfg.Log.RetentionDays == 0 {
		cfg.Log.RetentionDays = 90
	}
	if cfg.Log.MaxDomainsPerUserPerDay == 0 {
		cfg.Log.MaxDomainsPerUserPerDay = 500
	}
	if cfg.Server.AuthPort == 0 {
		cfg.Server.AuthPort = 443
	}
	if cfg.Server.WSPort == 0 {
		cfg.Server.WSPort = 8888
	}
	if cfg.Client.MinVersion == "" {
		cfg.Client.MinVersion = MinClientVersion
	}
	return &cfg, nil
}

func MustLoad() *Config {
	cfg, err := Load()
	if err != nil {
		panic(err)
	}
	return cfg
}
