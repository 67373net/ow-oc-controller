package connlog

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/67373net/ow-oc-controller/internal/clash"
	"github.com/67373net/ow-oc-controller/internal/logger"
)

// Record represents a single connection log entry
type Record struct {
	ID        string `json:"id"`
	Time      string `json:"time"`       // "YYMMDD-HHMMSS"
	Timestamp int64  `json:"timestamp"`  // Unix timestamp
	SourceIP  string `json:"source_ip"`  // Client LAN IP
	Host      string `json:"host"`       // Destination host or IP:port
	Network   string `json:"network"`    // tcp / udp
	Type      string `json:"type"`       // HTTPS, HTTP, etc.
	Chains    string `json:"chains"`     // Proxy node name (e.g. "香港 01")
	Rule      string `json:"rule"`       // Match rule
	Upload    int64  `json:"upload"`     // Bytes uploaded
	Download  int64  `json:"download"`   // Bytes downloaded
}

// Manager handles connection logging, storage, retention, and querying
type Manager struct {
	dataDir       string
	retentionDays int
	log           *logger.MemoryLogger
	clashClient   *clash.Client
	mu            sync.RWMutex
	seenIDs       map[string]int64 // id -> last seen timestamp (to deduplicate)
	seenMu        sync.Mutex
	stopChan      chan struct{}
}

// NewManager creates a new Connection Log Manager
func NewManager(dataDir string, retentionDays int, log *logger.MemoryLogger, clashClient *clash.Client) *Manager {
	if retentionDays <= 0 {
		retentionDays = 8
	}
	connDir := filepath.Join(dataDir, "connections")
	_ = os.MkdirAll(connDir, 0755)

	m := &Manager{
		dataDir:       connDir,
		retentionDays: retentionDays,
		log:           log,
		clashClient:   clashClient,
		seenIDs:       make(map[string]int64),
		stopChan:      make(chan struct{}),
	}

	return m
}

// Start launches the background connection collector and retention cleaner
func (m *Manager) Start() {
	// 1. Retention cleanup routine (every 2 hours)
	go func() {
		m.cleanExpiredLogs()
		ticker := time.NewTicker(2 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-m.stopChan:
				return
			case <-ticker.C:
				m.cleanExpiredLogs()
			}
		}
	}()

	// 2. Connection collector routine (every 4 seconds)
	go func() {
		ticker := time.NewTicker(4 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-m.stopChan:
				return
			case <-ticker.C:
				m.collectConnections()
			}
		}
	}()
}

// Stop terminates background routines
func (m *Manager) Stop() {
	close(m.stopChan)
}

// SetRetentionDays updates retention policy
func (m *Manager) SetRetentionDays(days int) {
	if days <= 0 {
		days = 8
	}
	m.mu.Lock()
	m.retentionDays = days
	m.mu.Unlock()
	go m.cleanExpiredLogs()
}

// GetRetentionDays returns current retention days
func (m *Manager) GetRetentionDays() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.retentionDays
}

// ClashConnectionItem matches Clash core REST API /connections output item
type clashConnectionItem struct {
	ID       string `json:"id"`
	Metadata struct {
		Network         string `json:"network"`
		Type            string `json:"type"`
		SourceIP        string `json:"sourceIP"`
		DestinationIP   string `json:"destinationIP"`
		SourcePort      string `json:"sourcePort"`
		DestinationPort string `json:"destinationPort"`
		Host            string `json:"host"`
	} `json:"metadata"`
	Upload   int64    `json:"upload"`
	Download int64    `json:"download"`
	Start    string   `json:"start"`
	Chains   []string `json:"chains"`
	Rule     string   `json:"rule"`
}

type clashConnectionsResponse struct {
	Connections []clashConnectionItem `json:"connections"`
}

func (m *Manager) collectConnections() {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	resp, err := m.clashClient.GetConnections(ctx)
	if err != nil || resp == nil || len(resp.Connections) == 0 {
		return
	}

	now := time.Now()
	nowUnix := now.Unix()
	loc := now.Location()
	timeStr := now.In(loc).Format("060102-150405")

	var newRecords []Record

	m.seenMu.Lock()
	// Clean stale seenIDs older than 1 hour
	if len(m.seenIDs) > 50000 {
		for id, ts := range m.seenIDs {
			if nowUnix-ts > 3600 {
				delete(m.seenIDs, id)
			}
		}
	}

	for _, c := range resp.Connections {
		if c.ID == "" {
			continue
		}
		// If we've already logged this connection with same ID, skip unless high bytes update
		if _, exists := m.seenIDs[c.ID]; exists {
			continue
		}
		m.seenIDs[c.ID] = nowUnix

		host := c.Metadata.Host
		if host == "" {
			host = c.Metadata.DestinationIP
		}
		if c.Metadata.DestinationPort != "" && !strings.Contains(host, ":") {
			host = fmt.Sprintf("%s:%s", host, c.Metadata.DestinationPort)
		}

		chainStr := "DIRECT"
		if len(c.Chains) > 0 {
			chainStr = c.Chains[len(c.Chains)-1]
		}

		newRecords = append(newRecords, Record{
			ID:        c.ID,
			Time:      timeStr,
			Timestamp: nowUnix,
			SourceIP:  c.Metadata.SourceIP,
			Host:      host,
			Network:   strings.ToUpper(c.Metadata.Network),
			Type:      c.Metadata.Type,
			Chains:    chainStr,
			Rule:      c.Rule,
			Upload:    c.Upload,
			Download:  c.Download,
		})
	}
	m.seenMu.Unlock()

	if len(newRecords) > 0 {
		m.appendRecords(newRecords)
	}
}

func (m *Manager) appendRecords(records []Record) {
	m.mu.Lock()
	defer m.mu.Unlock()

	dateStr := time.Now().Format("2006-01-02")
	filePath := filepath.Join(m.dataDir, fmt.Sprintf("%s.jsonl", dateStr))

	f, err := os.OpenFile(filePath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()

	w := bufio.NewWriter(f)
	for _, r := range records {
		data, err := json.Marshal(r)
		if err == nil {
			w.Write(data)
			w.WriteByte('\n')
		}
	}
	_ = w.Flush()
}

func (m *Manager) cleanExpiredLogs() {
	m.mu.Lock()
	defer m.mu.Unlock()

	entries, err := os.ReadDir(m.dataDir)
	if err != nil {
		return
	}

	cutoff := time.Now().AddDate(0, 0, -m.retentionDays)

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}

		base := strings.TrimSuffix(entry.Name(), ".jsonl")
		fileDate, err := time.Parse("2006-01-02", base)
		if err != nil {
			continue
		}

		if fileDate.Before(cutoff) {
			filePath := filepath.Join(m.dataDir, entry.Name())
			_ = os.Remove(filePath)
			if m.log != nil {
				m.log.Info("CONNLOG", fmt.Sprintf("已自动清理超过 %d 天的过期连接日志: %s", m.retentionDays, entry.Name()))
			}
		}
	}
}

// QueryResult represents paginated connection log result
type QueryResult struct {
	Records       []Record `json:"records"`
	Total         int      `json:"total"`
	Page          int      `json:"page"`
	Limit         int      `json:"limit"`
	TotalPages    int      `json:"total_pages"`
	RetentionDays int      `json:"retention_days"`
}

// Query searches and paginates connection logs in reverse chronological order
func (m *Manager) Query(page, limit int, query string) QueryResult {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if page <= 0 {
		page = 1
	}
	if limit <= 0 {
		limit = 200
	} else if limit > 10000 {
		limit = 10000
	}

	query = strings.ToLower(strings.TrimSpace(query))

	// Find log files in reverse date order
	entries, err := os.ReadDir(m.dataDir)
	if err != nil {
		return QueryResult{Records: []Record{}, Total: 0, Page: page, Limit: limit, TotalPages: 0, RetentionDays: m.retentionDays}
	}

	var files []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".jsonl") {
			files = append(files, filepath.Join(m.dataDir, entry.Name()))
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(files)))

	var allMatches []Record

	for _, fPath := range files {
		f, err := os.Open(fPath)
		if err != nil {
			continue
		}

		var fileRecords []Record
		scanner := bufio.NewScanner(f)
		buf := make([]byte, 0, 64*1024)
		scanner.Buffer(buf, 1024*1024)

		for scanner.Scan() {
			line := scanner.Bytes()
			if len(line) == 0 {
				continue
			}
			var rec Record
			if err := json.Unmarshal(line, &rec); err != nil {
				continue
			}

			if query != "" {
				matches := strings.Contains(strings.ToLower(rec.Host), query) ||
					strings.Contains(strings.ToLower(rec.SourceIP), query) ||
					strings.Contains(strings.ToLower(rec.Chains), query) ||
					strings.Contains(strings.ToLower(rec.Rule), query) ||
					strings.Contains(strings.ToLower(rec.Type), query)
				if !matches {
					continue
				}
			}
			fileRecords = append(fileRecords, rec)
		}
		_ = f.Close()

		// Reverse records in today's file so newest is first
		for i := len(fileRecords) - 1; i >= 0; i-- {
			allMatches = append(allMatches, fileRecords[i])
			// Cap total loaded to prevent memory bloat
			if len(allMatches) >= 10000 {
				break
			}
		}
		if len(allMatches) >= 10000 {
			break
		}
	}

	total := len(allMatches)
	totalPages := (total + limit - 1) / limit
	if totalPages == 0 {
		totalPages = 1
	}

	start := (page - 1) * limit
	if start > total {
		start = total
	}
	end := start + limit
	if end > total {
		end = total
	}

	paged := allMatches[start:end]
	if paged == nil {
		paged = []Record{}
	}

	return QueryResult{
		Records:       paged,
		Total:         total,
		Page:          page,
		Limit:         limit,
		TotalPages:    totalPages,
		RetentionDays: m.retentionDays,
	}
}

// ClearAll removes all connection log files
func (m *Manager) ClearAll() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	entries, err := os.ReadDir(m.dataDir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".jsonl") {
			_ = os.Remove(filepath.Join(m.dataDir, entry.Name()))
		}
	}
	return nil
}
