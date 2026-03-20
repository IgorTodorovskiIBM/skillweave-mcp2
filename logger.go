package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"runtime"
	"sync"
	"time"
)

// LogLevel represents the severity of a log message.
type LogLevel int

const (
	LevelDebug LogLevel = iota
	LevelInfo
	LevelWarn
	LevelError
)

func (l LogLevel) String() string {
	switch l {
	case LevelDebug:
		return "DEBUG"
	case LevelInfo:
		return "INFO"
	case LevelWarn:
		return "WARN"
	case LevelError:
		return "ERROR"
	default:
		return "UNKNOWN"
	}
}

// Logger provides structured logging with context support.
type Logger struct {
	mu       sync.Mutex
	out      io.Writer
	level    LogLevel
	fields   map[string]interface{}
	jsonMode bool
}

// LogEntry represents a single log entry.
type LogEntry struct {
	Timestamp string                 `json:"timestamp"`
	Level     string                 `json:"level"`
	Message   string                 `json:"message"`
	Fields    map[string]interface{} `json:"fields,omitempty"`
	Caller    string                 `json:"caller,omitempty"`
	Error     string                 `json:"error,omitempty"`
}

var (
	globalLogger *Logger
	once         sync.Once
)

// InitLogger initializes the global logger.
func InitLogger(level LogLevel, jsonMode bool) {
	once.Do(func() {
		globalLogger = NewLogger(os.Stderr, level, jsonMode)
	})
}

// GetLogger returns the global logger instance.
func GetLogger() *Logger {
	if globalLogger == nil {
		InitLogger(LevelInfo, false)
	}
	return globalLogger
}

// NewLogger creates a new logger instance.
func NewLogger(out io.Writer, level LogLevel, jsonMode bool) *Logger {
	return &Logger{
		out:      out,
		level:    level,
		fields:   make(map[string]interface{}),
		jsonMode: jsonMode,
	}
}

// WithField returns a new logger with an additional field.
func (l *Logger) WithField(key string, value interface{}) *Logger {
	l.mu.Lock()
	defer l.mu.Unlock()

	fields := make(map[string]interface{}, len(l.fields)+1)
	for k, v := range l.fields {
		fields[k] = v
	}
	fields[key] = value

	return &Logger{
		out:      l.out,
		level:    l.level,
		fields:   fields,
		jsonMode: l.jsonMode,
	}
}

// WithFields returns a new logger with multiple additional fields.
func (l *Logger) WithFields(fields map[string]interface{}) *Logger {
	l.mu.Lock()
	defer l.mu.Unlock()

	newFields := make(map[string]interface{}, len(l.fields)+len(fields))
	for k, v := range l.fields {
		newFields[k] = v
	}
	for k, v := range fields {
		newFields[k] = v
	}

	return &Logger{
		out:      l.out,
		level:    l.level,
		fields:   newFields,
		jsonMode: l.jsonMode,
	}
}

// WithError returns a new logger with an error field.
func (l *Logger) WithError(err error) *Logger {
	if err == nil {
		return l
	}
	return l.WithField("error", err.Error())
}

// SetLevel sets the minimum log level.
func (l *Logger) SetLevel(level LogLevel) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.level = level
}

// Debug logs a debug message.
func (l *Logger) Debug(msg string) {
	l.log(LevelDebug, msg, nil)
}

// Debugf logs a formatted debug message.
func (l *Logger) Debugf(format string, args ...interface{}) {
	l.log(LevelDebug, fmt.Sprintf(format, args...), nil)
}

// Info logs an info message.
func (l *Logger) Info(msg string) {
	l.log(LevelInfo, msg, nil)
}

// Infof logs a formatted info message.
func (l *Logger) Infof(format string, args ...interface{}) {
	l.log(LevelInfo, fmt.Sprintf(format, args...), nil)
}

// Warn logs a warning message.
func (l *Logger) Warn(msg string) {
	l.log(LevelWarn, msg, nil)
}

// Warnf logs a formatted warning message.
func (l *Logger) Warnf(format string, args ...interface{}) {
	l.log(LevelWarn, fmt.Sprintf(format, args...), nil)
}

// Error logs an error message.
func (l *Logger) Error(msg string) {
	l.log(LevelError, msg, nil)
}

// Errorf logs a formatted error message.
func (l *Logger) Errorf(format string, args ...interface{}) {
	l.log(LevelError, fmt.Sprintf(format, args...), nil)
}

// log writes a log entry.
func (l *Logger) log(level LogLevel, msg string, err error) {
	if level < l.level {
		return
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	entry := LogEntry{
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Level:     level.String(),
		Message:   msg,
		Fields:    l.fields,
	}

	// Add caller information for errors and warnings
	if level >= LevelWarn {
		if _, file, line, ok := runtime.Caller(2); ok {
			entry.Caller = fmt.Sprintf("%s:%d", file, line)
		}
	}

	if err != nil {
		entry.Error = err.Error()
	}

	if l.jsonMode {
		data, _ := json.Marshal(entry)
		fmt.Fprintf(l.out, "%s\n", data)
	} else {
		l.formatText(entry)
	}
}

// formatText formats a log entry as human-readable text.
func (l *Logger) formatText(entry LogEntry) {
	fmt.Fprintf(l.out, "[%s] %s: %s",
		entry.Timestamp,
		entry.Level,
		entry.Message)

	if len(entry.Fields) > 0 {
		fmt.Fprint(l.out, " |")
		for k, v := range entry.Fields {
			fmt.Fprintf(l.out, " %s=%v", k, v)
		}
	}

	if entry.Error != "" {
		fmt.Fprintf(l.out, " | error=%s", entry.Error)
	}

	if entry.Caller != "" {
		fmt.Fprintf(l.out, " | caller=%s", entry.Caller)
	}

	fmt.Fprintln(l.out)
}

// --- Context-aware logging ---

type loggerKey struct{}

// ContextWithLogger returns a new context with the logger attached.
func ContextWithLogger(ctx context.Context, logger *Logger) context.Context {
	return context.WithValue(ctx, loggerKey{}, logger)
}

// LoggerFromContext extracts the logger from the context, or returns the global logger.
func LoggerFromContext(ctx context.Context) *Logger {
	if logger, ok := ctx.Value(loggerKey{}).(*Logger); ok {
		return logger
	}
	return GetLogger()
}

// --- Error wrapping utilities ---

// ErrorWithContext wraps an error with additional context.
type ErrorWithContext struct {
	Err     error
	Context map[string]interface{}
	Op      string // Operation that failed
}

func (e *ErrorWithContext) Error() string {
	if e.Op != "" {
		return fmt.Sprintf("%s: %v", e.Op, e.Err)
	}
	return e.Err.Error()
}

func (e *ErrorWithContext) Unwrap() error {
	return e.Err
}

// WrapError wraps an error with operation context.
func WrapError(op string, err error) error {
	if err == nil {
		return nil
	}
	return &ErrorWithContext{
		Err: err,
		Op:  op,
	}
}

// WrapErrorWithFields wraps an error with operation context and additional fields.
func WrapErrorWithFields(op string, err error, fields map[string]interface{}) error {
	if err == nil {
		return nil
	}
	return &ErrorWithContext{
		Err:     err,
		Op:      op,
		Context: fields,
	}
}

// LogError logs an error with full context.
func LogError(logger *Logger, err error) {
	if err == nil {
		return
	}

	if ewc, ok := err.(*ErrorWithContext); ok {
		l := logger
		if ewc.Op != "" {
			l = l.WithField("operation", ewc.Op)
		}
		if len(ewc.Context) > 0 {
			l = l.WithFields(ewc.Context)
		}
		l.WithError(ewc.Err).Error(ewc.Error())
	} else {
		logger.WithError(err).Error(err.Error())
	}
}

// --- Convenience functions for global logger ---

// Debug logs a debug message using the global logger.
func Debug(msg string) {
	GetLogger().Debug(msg)
}

// Debugf logs a formatted debug message using the global logger.
func Debugf(format string, args ...interface{}) {
	GetLogger().Debugf(format, args...)
}

// Info logs an info message using the global logger.
func Info(msg string) {
	GetLogger().Info(msg)
}

// Infof logs a formatted info message using the global logger.
func Infof(format string, args ...interface{}) {
	GetLogger().Infof(format, args...)
}

// Warn logs a warning message using the global logger.
func Warn(msg string) {
	GetLogger().Warn(msg)
}

// Warnf logs a formatted warning message using the global logger.
func Warnf(format string, args ...interface{}) {
	GetLogger().Warnf(format, args...)
}

// Error logs an error message using the global logger.
func Error(msg string) {
	GetLogger().Error(msg)
}

// Errorf logs a formatted error message using the global logger.
func Errorf(format string, args ...interface{}) {
	GetLogger().Errorf(format, args...)
}
