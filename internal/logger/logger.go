package logger

import (
	"strings"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// Logger wraps zap.Logger and SugaredLogger for convenience
type Logger struct {
	Base  *zap.Logger
	Sugar *zap.SugaredLogger
}

// New creates a new zap logger based on appEnv and logLevel.
// appEnv: e0 (local) uses development config, others use production.
func New(appEnv, logLevel string) (*Logger, error) {
	var cfg zap.Config
	if strings.EqualFold(appEnv, "e0") || strings.EqualFold(appEnv, "dev") {
		c := zap.NewDevelopmentConfig()
		cfg = c
	} else {
		c := zap.NewProductionConfig()
		cfg = c
	}

	if logLevel != "" {
		level := zapcore.InfoLevel
		switch strings.ToLower(logLevel) {
		case "debug":
			level = zapcore.DebugLevel
		case "info":
			level = zapcore.InfoLevel
		case "warn":
			level = zapcore.WarnLevel
		case "error":
			level = zapcore.ErrorLevel
		case "dpanic":
			level = zapcore.DPanicLevel
		case "panic":
			level = zapcore.PanicLevel
		case "fatal":
			level = zapcore.FatalLevel
		}
		cfg.Level = zap.NewAtomicLevelAt(level)
	}

	base, err := cfg.Build()
	if err != nil {
		return nil, err
	}

	return &Logger{Base: base, Sugar: base.Sugar()}, nil
}

// Sync flushes any buffered log entries.
func (l *Logger) Sync() {
	if l == nil || l.Base == nil {
		return
	}
	_ = l.Base.Sync()
}
