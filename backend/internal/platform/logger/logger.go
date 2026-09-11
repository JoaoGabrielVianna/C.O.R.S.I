// Package logger builds the application's structured logger.
package logger

import (
	"log/slog"
	"os"
	"strings"
)

type Config struct {
	Level  string `env:"LOG_LEVEL" envDefault:"info"`
	Format string `env:"LOG_FORMAT" envDefault:"json"`
}

func New(cfg Config) *slog.Logger {
	var lvl slog.Level
	switch strings.ToLower(cfg.Level) {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}

	opts := &slog.HandlerOptions{Level: lvl}

	var h slog.Handler
	if strings.EqualFold(cfg.Format, "text") {
		h = slog.NewTextHandler(os.Stdout, opts)
	} else {
		h = slog.NewJSONHandler(os.Stdout, opts)
	}
	return slog.New(h)
}
