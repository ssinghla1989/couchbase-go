package config

import (
	"os"
	"time"

	"github.com/caarlos0/env/v10"
	"github.com/go-playground/validator/v10"
	"github.com/joho/godotenv"
)

// Config holds application configuration parsed from environment variables.
type Config struct {
	// Application
	AppEnv                  string        `env:"APP_ENV" envDefault:"e0" validate:"oneof=e0 e1 e2 e3"`
	LogLevel                string        `env:"LOG_LEVEL" envDefault:"info" validate:"oneof=debug info warn error dpanic panic fatal"`
	ServerPort              int           `env:"SERVER_PORT" envDefault:"8080" validate:"min=1,max=65535"`
	RequestTimeout          time.Duration `env:"REQUEST_TIMEOUT" envDefault:"30s"`
	ServerReadTimeout       time.Duration `env:"SERVER_READ_TIMEOUT" envDefault:"15s"`
	ServerWriteTimeout      time.Duration `env:"SERVER_WRITE_TIMEOUT" envDefault:"15s"`
	ServerIdleTimeout       time.Duration `env:"SERVER_IDLE_TIMEOUT" envDefault:"60s"`
	ServerReadHeaderTimeout time.Duration `env:"SERVER_READ_HEADER_TIMEOUT" envDefault:"5s"`
	ShutdownTimeout         time.Duration `env:"SHUTDOWN_TIMEOUT" envDefault:"20s"`

	// CORS (comma-separated origins, * allowed for all)
	CORSAllowedOrigins string `env:"CORS_ALLOWED_ORIGINS" envDefault:"*"`

	// Couchbase (single-cluster)
	Couchbase CouchbaseConfig

	// Bulk operation controls
	BulkMaxWorkers     int           `env:"BULK_MAX_WORKERS" envDefault:"8" validate:"min=1,max=256"`
	BulkGetMaxIDs      int           `env:"BULK_GET_MAX_IDS" envDefault:"1000" validate:"min=1,max=5000"`
	BulkUpsertMaxItems int           `env:"BULK_UPSERT_MAX_ITEMS" envDefault:"500" validate:"min=1,max=5000"`
	BulkHandlerTimeout time.Duration `env:"BULK_HANDLER_TIMEOUT" envDefault:"5s"`

	// Concurrency control
	RequireCASOnDelete bool `env:"REQUIRE_CAS_ON_DELETE" envDefault:"false"`
}

// CouchbaseConfig defines connection parameters for a single Couchbase cluster.
type CouchbaseConfig struct {
	ConnStr       string `env:"CB_CONN_STR,required" validate:"required"`
	Username      string `env:"CB_USERNAME,required" validate:"required"`
	Password      string `env:"CB_PASSWORD,required" validate:"required"`
	Bucket        string `env:"CB_BUCKET" envDefault:"default"`
	KVDialTimeout int    `env:"CB_KV_DIAL_TIMEOUT_MS" envDefault:"5000"`
	KVOpsTimeout  int    `env:"CB_KV_OP_TIMEOUT_MS" envDefault:"2500"`
}

// Load parses environment variables into Config and validates fields.
func Load() (*Config, error) {
	// Determine environment and load layered env files (.env then .env.<APP_ENV>)
	appEnv := os.Getenv("APP_ENV")
	if appEnv == "" {
		appEnv = "e0"
	}

	_ = godotenv.Load(".env")
	_ = godotenv.Load(".env." + appEnv)

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
