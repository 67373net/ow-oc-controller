package logger

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

type Level string

const (
	LevelInfo    Level = "INFO"
	LevelSuccess Level = "SUCCESS"
	LevelWarn    Level = "WARN"
	LevelError   Level = "ERROR"
)

type LogEntry struct {
	ID      int64  `json:"id"`
	Time    string `json:"time"` // YYMMDD-HHmmss
	Level   Level  `json:"level"`
	Source  string `json:"source"`
	Message string `json:"message"`
	Detail  string `json:"detail,omitempty"`
}

type MemoryLogger struct {
	mu          sync.RWMutex
	entries     []LogEntry
	maxSize     int
	nextID      int64
	persistPath string
	file        *os.File
}

var defaultLogger = NewMemoryLogger(1000)

func NewMemoryLogger(maxSize int) *MemoryLogger {
	if maxSize <= 0 {
		maxSize = 1000
	}
	return &MemoryLogger{
		entries: make([]LogEntry, 0, maxSize),
		maxSize: maxSize,
	}
}

func Default() *MemoryLogger {
	return defaultLogger
}

// InitPersistence loads existing logs from disk and sets up append-only log file
func (l *MemoryLogger) InitPersistence(filePath string) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.persistPath = filePath
	if err := os.MkdirAll(filepath.Dir(filePath), 0755); err != nil {
		return err
	}

	// 1. Read existing lines
	if f, err := os.Open(filePath); err == nil {
		scanner := bufio.NewScanner(f)
		buf := make([]byte, 0, 64*1024)
		scanner.Buffer(buf, 1024*1024)
		var existing []LogEntry
		var maxID int64
		for scanner.Scan() {
			line := scanner.Bytes()
			if len(line) == 0 {
				continue
			}
			var entry LogEntry
			if err := json.Unmarshal(line, &entry); err == nil {
				existing = append(existing, entry)
				if entry.ID > maxID {
					maxID = entry.ID
				}
			}
		}
		_ = f.Close()

		if len(existing) > l.maxSize {
			existing = existing[len(existing)-l.maxSize:]
		}
		l.entries = existing
		l.nextID = maxID
	}

	// 2. Open in append mode
	f, err := os.OpenFile(filePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return err
	}
	l.file = f
	return nil
}

func (l *MemoryLogger) Log(level Level, source, msg, detail string) {
	now := time.Now()
	if tz := os.Getenv("TZ"); tz != "" {
		if loc, err := time.LoadLocation(tz); err == nil && loc != nil {
			now = now.In(loc)
		}
	}

	// Format: YYMMDD-HHmmss
	timeStr := fmt.Sprintf("%02d%02d%02d-%02d%02d%02d",
		now.Year()%100, now.Month(), now.Day(),
		now.Hour(), now.Minute(), now.Second(),
	)

	id := atomic.AddInt64(&l.nextID, 1)

	entry := LogEntry{
		ID:      id,
		Time:    timeStr,
		Level:   level,
		Source:  source,
		Message: msg,
		Detail:  detail,
	}

	l.mu.Lock()
	if len(l.entries) >= l.maxSize {
		l.entries = l.entries[1:]
	}
	l.entries = append(l.entries, entry)

	// Persist to file
	if l.file != nil {
		data, err := json.Marshal(entry)
		if err == nil {
			data = append(data, '\n')
			_, _ = l.file.Write(data)
		}
	}
	l.mu.Unlock()

	// Also output to stdout for docker logs
	if detail != "" {
		log.Printf("[%s] [%s] [%s] %s | Detail: %s", timeStr, level, source, msg, detail)
	} else {
		log.Printf("[%s] [%s] [%s] %s", timeStr, level, source, msg)
	}
}

func (l *MemoryLogger) Info(source, msg string, detail ...string) {
	d := ""
	if len(detail) > 0 {
		d = detail[0]
	}
	l.Log(LevelInfo, source, msg, d)
}

func (l *MemoryLogger) Success(source, msg string, detail ...string) {
	d := ""
	if len(detail) > 0 {
		d = detail[0]
	}
	l.Log(LevelSuccess, source, msg, d)
}

func (l *MemoryLogger) Warn(source, msg string, detail ...string) {
	d := ""
	if len(detail) > 0 {
		d = detail[0]
	}
	l.Log(LevelWarn, source, msg, d)
}

func (l *MemoryLogger) Error(source, msg string, detail ...string) {
	d := ""
	if len(detail) > 0 {
		d = detail[0]
	}
	l.Log(LevelError, source, msg, d)
}

func (l *MemoryLogger) GetEntries(limit int) []LogEntry {
	l.mu.RLock()
	defer l.mu.RUnlock()

	total := len(l.entries)
	if limit <= 0 || limit > total {
		limit = total
	}

	result := make([]LogEntry, limit)
	copy(result, l.entries[total-limit:])
	return result
}

func (l *MemoryLogger) Clear() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries = make([]LogEntry, 0, l.maxSize)
	if l.file != nil {
		_ = l.file.Close()
		l.file = nil
	}
	if l.persistPath != "" {
		_ = os.WriteFile(l.persistPath, []byte{}, 0644)
		if f, err := os.OpenFile(l.persistPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644); err == nil {
			l.file = f
		}
	}
}

func (l *MemoryLogger) Close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file != nil {
		_ = l.file.Close()
		l.file = nil
	}
}
