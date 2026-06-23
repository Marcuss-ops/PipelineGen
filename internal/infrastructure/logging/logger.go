// Package logger provides centralized logging for the PipelineGen system.
//
// Worker safety (June 2026): the singleton uses sync.Once for initialisation
// and an atomic.Pointer for the instance so 100+ concurrent workers can call
// Get(), Named(), With(), Debug(), Info(), etc. without any data race.
// zap.Logger itself is already concurrency-safe.
package logger

import (
	"os"
	"strings"
	"sync"
	"sync/atomic"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var (
	instance atomic.Pointer[zap.Logger]
	initOnce sync.Once
)

// Init initializes the logger singleton with the given configuration.
// Safe to call from any goroutine; guaranteed to run at most once.
// Subsequent calls are no-ops and return immediately.
// zap.ReplaceGlobals is called so zap.L() returns the configured logger.
func Init(level string, format string) {
	initOnce.Do(func() {
		lvl := parseLevel(level)
		inst := New(
			WithLevel(lvl),
			WithEncoding(format),
		)
		instance.Store(inst)
		zap.ReplaceGlobals(inst)
	})
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
// If Init() was not called, it creates a default info-level JSON logger
// via an atomic compare-and-swap so only one goroutine ever creates it.
func Get() *zap.Logger {
	if inst := instance.Load(); inst != nil {
		return inst
	}
	// Lazy default: only one goroutine succeeds.
	defaultLogger := New(WithLevel(zapcore.InfoLevel), WithEncoding("json"))
	if instance.CompareAndSwap(nil, defaultLogger) {
		return defaultLogger
	}
	// Another goroutine won the CAS; use its logger.
	return instance.Load()
}

// New creates a new logger with custom options
func New(opts ...Option) *zap.Logger {
	cfg := &config{
		level:    zapcore.InfoLevel,
		encoding: "json",
		output:   os.Stdout,
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

	core := zapcore.NewCore(
		encoder,
		ws,
		cfg.level,
	)

	logger := zap.New(core, zap.AddCaller(), zap.AddStacktrace(zapcore.ErrorLevel))
	return logger
}

type config struct {
	level    zapcore.Level
	encoding string
	output   *os.File
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

// Sync flushes any buffered log entries from the singleton.
func Sync() error {
	if inst := instance.Load(); inst != nil {
		return inst.Sync()
	}
	return nil
}

// Named returns a named logger from the singleton.
func Named(name string) *zap.Logger {
	return Get().Named(name)
}

// With creates a child logger with fields from the singleton.
func With(fields ...zap.Field) *zap.Logger {
	return Get().With(fields...)
}

// Debug logs a debug message via the singleton.
func Debug(msg string, fields ...zap.Field) {
	Get().Debug(msg, fields...)
}

// Info logs an info message via the singleton.
func Info(msg string, fields ...zap.Field) {
	Get().Info(msg, fields...)
}

// Warn logs a warning message via the singleton.
func Warn(msg string, fields ...zap.Field) {
	Get().Warn(msg, fields...)
}

// Error logs an error message via the singleton.
func Error(msg string, fields ...zap.Field) {
	Get().Error(msg, fields...)
}

// Fatal logs a fatal message and exits via the singleton.
func Fatal(msg string, fields ...zap.Field) {
	Get().Fatal(msg, fields...)
}
