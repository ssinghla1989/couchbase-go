package config

import (
	"time"

	"github.com/caarlos0/env/v10"
	"github.com/go-playground/validator/v10"
)

// Config holds application configuration parsed from environment variables.
type Config struct {
	// Application
	AppEnv   string `env:"APP_ENV" envDefault:"prod" validate:"oneof=dev prod"`
	LogLevel string `env:"LOG_LEVEL" envDefault:"info" validate:"oneof=debug info warn error dpanic panic fatal"`

	// Server
	ServerPort         int           `env:"SERVER_PORT" envDefault:"8080" validate:"min=1,max=65535"`
	ServerReadTimeout  time.Duration `env:"SERVER_READ_TIMEOUT" envDefault:"15s"`
	ServerWriteTimeout time.Duration `env:"SERVER_WRITE_TIMEOUT" envDefault:"15s"`
	ServerIdleTimeout  time.Duration `env:"SERVER_IDLE_TIMEOUT" envDefault:"60s"`
	ShutdownTimeout    time.Duration `env:"SHUTDOWN_TIMEOUT" envDefault:"20s"`
	RequestTimeout     time.Duration `env:"REQUEST_TIMEOUT" envDefault:"30s"`

	// CORS (comma-separated origins, * allowed for all)
	CORSAllowedOrigins string `env:"CORS_ALLOWED_ORIGINS" envDefault:"*"`

	// Couchbase
	CBConnStr  string        `env:"CB_CONN_STR" envDefault:"couchbase://localhost" validate:"required"`
	CBUsername string        `env:"CB_USERNAME" validate:"required"`
	CBPassword string        `env:"CB_PASSWORD" validate:"required"`
	CBBucket   string        `env:"CB_BUCKET" envDefault:"default"`
	CBTimeout  time.Duration `env:"CB_TIMEOUT" envDefault:"10s"`
}

// Load parses environment variables into Config and validates fields.
func Load() (*Config, error) {
	cfg := &Config{}
	if err := env.Parse(cfg); err != nil {
		return nil, err
	}

	validate := validator.New(validator.WithRequiredStructEnabled())
	if err := validate.Struct(cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}
