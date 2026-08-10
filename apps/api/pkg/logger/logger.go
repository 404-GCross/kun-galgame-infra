package logger

import (
	"log"
	"os"
)

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

func New() *Logger {
	return &Logger{
		info:  log.New(os.Stdout, "[INFO] ", log.Ldate|log.Ltime|log.Lshortfile),
		warn:  log.New(os.Stdout, "[WARN] ", log.Ldate|log.Ltime|log.Lshortfile),
		error: log.New(os.Stderr, "[ERROR] ", log.Ldate|log.Ltime|log.Lshortfile),
		debug: log.New(os.Stdout, "[DEBUG] ", log.Ldate|log.Ltime|log.Lshortfile),
	}
}

func (l *Logger) Info(format string, v ...any) {
	l.info.Printf(format, v...)
}

func (l *Logger) Warn(format string, v ...any) {
	l.warn.Printf(format, v...)
}

func (l *Logger) Error(format string, v ...any) {
	l.error.Printf(format, v...)
}

func (l *Logger) Debug(format string, v ...any) {
	l.debug.Printf(format, v...)
}

func (l *Logger) Fatal(format string, v ...any) {
	l.error.Fatalf(format, v...)
}

func Init(env string) {
	defaultLogger = New()
}

func Info(format string, v ...any) {
	defaultLogger.Info(format, v...)
}

func Warn(format string, v ...any) {
	defaultLogger.Warn(format, v...)
}

func Error(format string, v ...any) {
	defaultLogger.Error(format, v...)
}

func Debug(format string, v ...any) {
	defaultLogger.Debug(format, v...)
}

func Fatal(format string, v ...any) {
	defaultLogger.Fatal(format, v...)
}
