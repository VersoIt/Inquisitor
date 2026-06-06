package logger

import (
	"io"
	"log/slog"
	"os"
	"strings"
)

func New(level string) *slog.Logger {
	return NewWithWriter(level, os.Stdout)
}

func NewWithWriter(level string, writer io.Writer) *slog.Logger {
	handler := slog.NewJSONHandler(writer, &slog.HandlerOptions{
		Level: parseLevel(level),
	})
	return slog.New(handler)
}

func parseLevel(level string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
