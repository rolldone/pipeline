package logging

import (
	"encoding/json"
	"io"
	"os"
	"sync"
	"time"
)

type Level int

const (
	LevelDebug Level = iota
	LevelInfo
	LevelWarn
	LevelError
)

var levelNames = map[Level]string{
	LevelDebug: "debug",
	LevelInfo:  "info",
	LevelWarn:  "warn",
	LevelError: "error",
}

type Logger struct {
	mu    sync.Mutex
	out   io.Writer
	level Level
	base  map[string]interface{}
}

var defaultLogger *Logger

func Init(w io.Writer, lvl Level, baseFields map[string]interface{}) {
	if w == nil {
		w = os.Stderr
	}
	defaultLogger = &Logger{
		out:   w,
		level: lvl,
		base:  copyMap(baseFields),
	}
}

func copyMap(m map[string]interface{}) map[string]interface{} {
	if m == nil {
		return nil
	}
	nm := make(map[string]interface{}, len(m))
	for k, v := range m {
		nm[k] = v
	}
	return nm
}

// WithFields returns a logger-like helper that merges fields for a single call.
func WithFields(fields map[string]interface{}) *Logger {
	if defaultLogger == nil {
		Init(nil, LevelInfo, nil)
	}
	l := &Logger{
		out:   defaultLogger.out,
		level: defaultLogger.level,
		base:  copyMap(defaultLogger.base),
	}
	if fields != nil {
		if l.base == nil {
			l.base = make(map[string]interface{}, len(fields))
		}
		for k, v := range fields {
			l.base[k] = v
		}
	}
	return l
}

func (l *Logger) log(lvl Level, msg string, extra map[string]interface{}) {
	if defaultLogger != nil && lvl < defaultLogger.level {
		return
	}
	entry := make(map[string]interface{}, 4+len(l.base)+len(extra))
	entry["ts"] = time.Now().Format(time.RFC3339Nano)
	entry["lvl"] = levelNames[lvl]
	entry["msg"] = msg
	for k, v := range l.base {
		entry[k] = v
	}
	for k, v := range extra {
		entry[k] = v
	}
	b, err := json.Marshal(entry)
	if err != nil {
		l.mu.Lock()
		defer l.mu.Unlock()
		l.out.Write([]byte(time.Now().Format(time.RFC3339Nano) + " " + levelNames[lvl] + " " + msg + "\n"))
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.out.Write(append(b, '\n'))
}

func (l *Logger) Debug(msg string, extra map[string]interface{}) { l.log(LevelDebug, msg, extra) }
func (l *Logger) Info(msg string, extra map[string]interface{})  { l.log(LevelInfo, msg, extra) }
func (l *Logger) Warn(msg string, extra map[string]interface{})  { l.log(LevelWarn, msg, extra) }
func (l *Logger) Error(msg string, extra map[string]interface{}) { l.log(LevelError, msg, extra) }

// Top-level convenience wrappers
func Debug(msg string, extra map[string]interface{}) { WithFields(nil).Debug(msg, extra) }
func Info(msg string, extra map[string]interface{})  { WithFields(nil).Info(msg, extra) }
func Warn(msg string, extra map[string]interface{})  { WithFields(nil).Warn(msg, extra) }
func Error(msg string, extra map[string]interface{}) { WithFields(nil).Error(msg, extra) }

func SetLevel(lvl Level) {
	if defaultLogger == nil {
		Init(nil, lvl, nil)
		return
	}
	defaultLogger.mu.Lock()
	defaultLogger.level = lvl
	defaultLogger.mu.Unlock()
}
