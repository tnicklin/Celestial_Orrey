package logger

// Logger defines the logging interface used throughout the application.
type Logger interface {
	InfoW(msg string, keysAndValues ...any)
	WarnW(msg string, keysAndValues ...any)
	ErrorW(msg string, keysAndValues ...any)
	Sync() error
}
