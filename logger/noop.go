package logger

import "go.uber.org/zap"

// NewNop creates a no-op logger that discards all output.
func NewNop() *DefaultLogger {
	return &DefaultLogger{logger: zap.NewNop().Sugar()}
}
