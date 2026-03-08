// Package logging provides a shared structured logger for the RAG Gateway.
//
// All services should call New() once at startup and pass the resulting logger
// through constructors. Logs are emitted as JSON lines to stdout, making them
// compatible with Docker log collectors and structured log aggregators.
//
// Standard fields included in every log line (via zap.NewProduction):
//
//	ts        – Unix timestamp (seconds)
//	level     – debug / info / warn / error / fatal
//	caller    – file:line of the log call
//	msg       – human-readable message
//	<extras>  – any additional zap.Field values passed at the call site
package logging

import (
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// New returns a production zap logger that writes JSON to stdout.
// It panics if the logger cannot be constructed (should never happen).
func New() *zap.Logger {
	cfg := zap.NewProductionConfig()
	cfg.EncoderConfig.TimeKey = "ts"
	cfg.EncoderConfig.LevelKey = "level"
	cfg.EncoderConfig.MessageKey = "msg"
	cfg.EncoderConfig.CallerKey = "caller"
	cfg.EncoderConfig.EncodeLevel = zapcore.LowercaseLevelEncoder
	cfg.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder

	logger, err := cfg.Build()
	if err != nil {
		panic("failed to construct zap logger: " + err.Error())
	}
	return logger
}
