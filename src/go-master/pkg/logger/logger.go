// Package logger provides centralized logging for the VeloxEditing system.
package logger

import (
	"errors"
	"os"
	"strings"
	"sync"
	"syscall"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var (
	instance *zap.Logger
	once     sync.Once
	mu       sync.RWMutex
)

// Init initializes the logger singleton with the given configuration.
// This should be called early in main() after config is loaded.
// If Init is never called, Get() will create a default logger.
func Init(level string, format string) {
	mu.Lock()
	defer mu.Unlock()

	lvl := parseLevel(level)
	instance = New(
		WithLevel(lvl),
		WithEncoding(format),
		WithForceSync(parseBoolEnv(os.Getenv("VELOX_LOG_FORCE_SYNC"))),
	)
}

// parseLevel converts a string log level to zapcore.Level
func parseLevel(level string) zapcore.Level {
	switch strings.ToLower(level) {
	case "debug":
		return zapcore.DebugLevel
	case "info":
		return zapcore.InfoLevel
	case "warn", "warning":
		return zapcore.WarnLevel
	case "error":
		return zapcore.ErrorLevel
	default:
		return zapcore.InfoLevel
	}
}

// Get returns the singleton logger instance.
// If Init() was not called, it creates a default info-level json logger.
func Get() *zap.Logger {
	once.Do(func() {
		mu.RLock()
		if instance != nil {
			mu.RUnlock()
			return
		}
		mu.RUnlock()
		// Fallback: create a default logger if Init() was never called
		instance = New(WithLevel(zapcore.InfoLevel), WithEncoding("json"))
	})
	return instance
}

// New creates a new logger with custom options
func New(opts ...Option) *zap.Logger {
	cfg := &config{
		level:     zapcore.InfoLevel,
		encoding:  "json",
		output:    os.Stdout,
		forceSync: false,
	}

	for _, opt := range opts {
		opt(cfg)
	}

	ec := zapcore.EncoderConfig{
		TimeKey:        "timestamp",
		LevelKey:       "level",
		NameKey:        "logger",
		CallerKey:      "caller",
		FunctionKey:    zapcore.OmitKey,
		MessageKey:     "msg",
		StacktraceKey:  "stacktrace",
		LineEnding:     zapcore.DefaultLineEnding,
		EncodeLevel:    zapcore.LowercaseLevelEncoder,
		EncodeTime:     zapcore.ISO8601TimeEncoder,
		EncodeDuration: zapcore.SecondsDurationEncoder,
		EncodeCaller:   zapcore.ShortCallerEncoder,
	}

	var encoder zapcore.Encoder
	if cfg.encoding == "console" {
		ec.EncodeLevel = zapcore.CapitalColorLevelEncoder
		ec.ConsoleSeparator = " | "
		encoder = zapcore.NewConsoleEncoder(ec)
	} else {
		encoder = zapcore.NewJSONEncoder(ec)
	}

	ws := zapcore.AddSync(cfg.output)
	if cfg.forceSync {
		ws = forceSyncWriteSyncer{WriteSyncer: ws}
	}

	core := zapcore.NewCore(
		encoder,
		ws,
		cfg.level,
	)

	logger := zap.New(core, zap.AddCaller(), zap.AddStacktrace(zapcore.ErrorLevel))
	return logger
}

type config struct {
	level     zapcore.Level
	encoding  string
	output    *os.File
	forceSync bool
}

// Option is a functional option for logger configuration
type Option func(*config)

// WithLevel sets the log level
func WithLevel(level zapcore.Level) Option {
	return func(c *config) {
		c.level = level
	}
}

// WithEncoding sets the encoding (json or console)
func WithEncoding(encoding string) Option {
	return func(c *config) {
		c.encoding = encoding
	}
}

// WithOutput sets the output file
func WithOutput(output *os.File) Option {
	return func(c *config) {
		c.output = output
	}
}

// WithForceSync makes the logger flush after every write when the sink supports it.
func WithForceSync(force bool) Option {
	return func(c *config) {
		c.forceSync = force
	}
}

// Sync flushes any buffered log entries
func Sync() error {
	if instance != nil {
		return instance.Sync()
	}
	return nil
}

type forceSyncWriteSyncer struct {
	zapcore.WriteSyncer
}

func (s forceSyncWriteSyncer) Write(p []byte) (int, error) {
	n, err := s.WriteSyncer.Write(p)
	if err != nil {
		return n, err
	}
	if syncErr := s.WriteSyncer.Sync(); syncErr != nil && !ignoreSyncError(syncErr) {
		return n, syncErr
	}
	return n, nil
}

func ignoreSyncError(err error) bool {
	return errors.Is(err, syscall.EINVAL) || errors.Is(err, syscall.ENOTTY)
}

func parseBoolEnv(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// Named returns a named logger
func Named(name string) *zap.Logger {
	return Get().Named(name)
}

// With creates a child logger with fields
func With(fields ...zap.Field) *zap.Logger {
	return Get().With(fields...)
}

// Debug logs a debug message
func Debug(msg string, fields ...zap.Field) {
	Get().Debug(msg, fields...)
}

// Info logs an info message
func Info(msg string, fields ...zap.Field) {
	Get().Info(msg, fields...)
}

// Warn logs a warning message
func Warn(msg string, fields ...zap.Field) {
	Get().Warn(msg, fields...)
}

// Error logs an error message
func Error(msg string, fields ...zap.Field) {
	Get().Error(msg, fields...)
}

// Fatal logs a fatal message and exits
func Fatal(msg string, fields ...zap.Field) {
	Get().Fatal(msg, fields...)
}
