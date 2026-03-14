package logger

import (
	"log"
	"os"
)

// Logger is a simple wrapper around the standard logger
type Logger struct {
	info  *log.Logger
	warn  *log.Logger
	error *log.Logger
	debug *log.Logger
}

var defaultLogger *Logger

func init() {
	defaultLogger = New()
}

// New creates a new Logger instance
func New() *Logger {
	return &Logger{
		info:  log.New(os.Stdout, "[INFO] ", log.Ldate|log.Ltime|log.Lshortfile),
		warn:  log.New(os.Stdout, "[WARN] ", log.Ldate|log.Ltime|log.Lshortfile),
		error: log.New(os.Stderr, "[ERROR] ", log.Ldate|log.Ltime|log.Lshortfile),
		debug: log.New(os.Stdout, "[DEBUG] ", log.Ldate|log.Ltime|log.Lshortfile),
	}
}

// Info logs an info message
func (l *Logger) Info(format string, v ...any) {
	l.info.Printf(format, v...)
}

// Warn logs a warning message
func (l *Logger) Warn(format string, v ...any) {
	l.warn.Printf(format, v...)
}

// Error logs an error message
func (l *Logger) Error(format string, v ...any) {
	l.error.Printf(format, v...)
}

// Debug logs a debug message
func (l *Logger) Debug(format string, v ...any) {
	l.debug.Printf(format, v...)
}

// Fatal logs an error message and exits
func (l *Logger) Fatal(format string, v ...any) {
	l.error.Fatalf(format, v...)
}

// Init initializes the logger based on environment
func Init(env string) {
	// For now, just use the default logger
	// In production, could configure structured logging
	defaultLogger = New()
}

// Package-level functions using default logger

// Info logs an info message
func Info(format string, v ...any) {
	defaultLogger.Info(format, v...)
}

// Warn logs a warning message
func Warn(format string, v ...any) {
	defaultLogger.Warn(format, v...)
}

// Error logs an error message
func Error(format string, v ...any) {
	defaultLogger.Error(format, v...)
}

// Debug logs a debug message
func Debug(format string, v ...any) {
	defaultLogger.Debug(format, v...)
}

// Fatal logs an error message and exits
func Fatal(format string, v ...any) {
	defaultLogger.Fatal(format, v...)
}
