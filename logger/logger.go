package logger

import (
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var _ Logger = (*DefaultLogger)(nil)

// DefaultLogger wraps zap.SugaredLogger to implement Logger.
type DefaultLogger struct {
	logger *zap.SugaredLogger
}

// New creates a new DefaultLogger with the given configuration.
func New(cfg Config) (*DefaultLogger, error) {
	var level zapcore.Level
	if err := level.UnmarshalText([]byte(cfg.Level)); err != nil {
		level = zapcore.InfoLevel
	}

	zapCfg := zap.NewProductionConfig()
	zapCfg.Level = zap.NewAtomicLevelAt(level)
	if len(cfg.OutputPaths) > 0 {
		zapCfg.OutputPaths = cfg.OutputPaths
	}

	zapLogger, err := zapCfg.Build()
	if err != nil {
		return nil, err
	}

	return &DefaultLogger{logger: zapLogger.Sugar()}, nil
}

func (l *DefaultLogger) InfoW(msg string, keysAndValues ...any) {
	l.logger.Infow(msg, keysAndValues...)
}

func (l *DefaultLogger) WarnW(msg string, keysAndValues ...any) {
	l.logger.Warnw(msg, keysAndValues...)
}

func (l *DefaultLogger) ErrorW(msg string, keysAndValues ...any) {
	l.logger.Errorw(msg, keysAndValues...)
}

func (l *DefaultLogger) Sync() error {
	return l.logger.Sync()
}
