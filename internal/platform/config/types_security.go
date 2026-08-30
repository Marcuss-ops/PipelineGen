package config

type SecurityConfig struct {
	AdminToken                 string   `yaml:"admin_token" env:"VELOX_ADMIN_TOKEN" default:""`
	WorkerToken                string   `yaml:"worker_token" env:"VELOX_WORKER_TOKEN" default:""`
	WebhookSecret              string   `yaml:"webhook_secret" env:"VELOX_WEBHOOK_SECRET" default:""`
	EnableAuth                 bool     `yaml:"enable_auth" env:"VELOX_ENABLE_AUTH" default:"true"`
	EnableM2M                  bool     `yaml:"enable_m2m" env:"VELOX_ENABLE_M2M" default:"false"`
	CORSOrigins                []string `yaml:"cors_origins" env:"VELOX_CORS_ORIGINS" default:"[]"`
	RateLimitEnabled           bool     `yaml:"rate_limit_enabled" env:"VELOX_RATE_LIMIT_ENABLED" default:"true"`
	RateLimitRequests          int      `yaml:"rate_limit_requests" env:"VELOX_RATE_LIMIT_REQUESTS" default:"100"`
	AllowedDownloadHosts       []string `yaml:"allowed_download_hosts" env:"VELOX_ALLOWED_DOWNLOAD_HOSTS" default:"[]"`
	DeliveryHMACSecret         string   `yaml:"delivery_hmac_secret" env:"VELOX_DELIVERY_HMAC_SECRET" default:""`
	DeliveryHMACSecretPrevious string   `yaml:"delivery_hmac_secret_previous" env:"VELOX_DELIVERY_HMAC_SECRET_PREVIOUS" default:""`
	DeliveryReplayWindowSec    int      `yaml:"delivery_replay_window_seconds" env:"VELOX_DELIVERY_REPLAY_WINDOW_SECONDS" default:"300"`
	DeliveryInsecureDev        bool     `yaml:"delivery_insecure_dev" env:"VELOX_ALLOW_INSECURE_DEV" default:"false"`
}
