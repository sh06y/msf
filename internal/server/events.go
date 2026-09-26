package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

func (a *App) registerEvents(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/events", a.handleEvents)
	mux.HandleFunc("GET /api/v1/events/monitor", a.handleEvents)
	mux.HandleFunc("GET /api/v1/events/proxy", a.handleProxyEvents)
	mux.HandleFunc("GET /api/v1/events/mosdns", a.handleMosDNSEvents)
	mux.HandleFunc("GET /api/v1/events/mihomo", a.handleMihomoEvents)
	mux.HandleFunc("GET /api/v1/events/logs/{service}", a.handleLogEvents)
}

func (a *App) handleEvents(w http.ResponseWriter, r *http.Request) {
	a.sseLoopNamed(w, r, time.Second, "monitor", func() any {
		return a.monitorPayload()
	})
}

func (a *App) handleProxyEvents(w http.ResponseWriter, r *http.Request) {
	a.sseLoopNamed(w, r, time.Second, "proxy", func() any {
		data := a.proxySnapshot()
		data["time"] = time.Now()
		return data
	})
}

func (a *App) handleMosDNSEvents(w http.ResponseWriter, r *http.Request) {
	a.sseLoopNamed(w, r, 2*time.Second, "mosdns", func() any {
		data := a.mosDNSSnapshot(2000)
		data["time"] = time.Now()
		data["audit_ranks"] = map[string]any{"domain": data["top_domains"], "client": data["top_clients"], "domain_set": data["top_rules"]}
		return data
	})
}

func (a *App) handleMihomoEvents(w http.ResponseWriter, r *http.Request) {
	a.sseLoopNamed(w, r, 2*time.Second, "mihomo", func() any {
		data := a.mihomoSnapshot()
		data["time"] = time.Now()
		return data
	})
}

func (a *App) handleLogEvents(w http.ResponseWriter, r *http.Request) {
	service := normalizeServiceName(r.PathValue("service"))
	if service == "" {
		service = "msf"
	}
	limit := queryInt(r, "lines", 40)
	if limit <= 0 {
		limit = 40
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, _ := w.(http.Flusher)
	enc := json.NewEncoder(w)
	readRows := func() ([]map[string]any, logSnapshotCursor) {
		rows, cursor := a.readLogSnapshot(service, limit)
		entries := make([]map[string]any, 0, len(rows))
		for _, row := range rows {
			if strings.TrimSpace(row["line"]) == "" {
				continue
			}
			entry := structuredLogLines([]string{row["line"]})[0]
			entry["source"] = row["source"]
			entry["path"] = row["path"]
			entries = append(entries, entry)
		}
		return filterStructuredLogs(entries, r), cursor
	}
	sendRows := func(rows []map[string]any, cursor logSnapshotCursor) bool {
		if len(rows) == 0 {
			return true
		}
		fmt.Fprint(w, "event: logs\n")
		fmt.Fprint(w, "data: ")
		if err := enc.Encode(map[string]any{"service": service, "logs": rows, "cursor": cursor}); err != nil {
			return false
		}
		fmt.Fprint(w, "\n")
		if flusher != nil {
			flusher.Flush()
		}
		return true
	}
	rows, cursor := readRows()
	lines := logEventKeys(rows)
	if !sendRows(rows, cursor) {
		return
	}
	ticker := time.NewTicker(1500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			rows, cursor := readRows()
			current := logEventKeys(rows)
			delta := newLogEventLines(lines, current)
			lines = current
			if len(delta) > 0 {
				if !sendRows(rows[len(rows)-len(delta):], cursor) {
					return
				}
				continue
			}
			fmt.Fprint(w, ": heartbeat\n\n")
			if flusher != nil {
				flusher.Flush()
			}
		}
	}
}

func logEventKeys(rows []map[string]any) []string {
	keys := make([]string, len(rows))
	for i, row := range rows {
		keys[i] = fmtAny(row["source"]) + "\x00" + fmtAny(row["raw"])
	}
	return keys
}

func newLogEventLines(previous, current []string) []string {
	if len(current) == 0 {
		return nil
	}
	if len(previous) == 0 {
		return current
	}
	maxOverlap := len(previous)
	if len(current) < maxOverlap {
		maxOverlap = len(current)
	}
	for overlap := maxOverlap; overlap >= 0; overlap-- {
		if stringSlicesEqual(previous[len(previous)-overlap:], current[:overlap]) {
			return current[overlap:]
		}
	}
	return current
}

func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (a *App) sseLoop(w http.ResponseWriter, r *http.Request, interval time.Duration, payload func() any) {
	a.sseLoopNamed(w, r, interval, "", payload)
}

func (a *App) sseLoopNamed(w http.ResponseWriter, r *http.Request, interval time.Duration, event string, payload func() any) {
	a.LogInfo("handler/sse.go:184", "SSE连接", map[string]any{"event": event, "path": r.URL.Path})
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, _ := w.(http.Flusher)
	enc := json.NewEncoder(w)
	send := func(v any) bool {
		if event != "" {
			fmt.Fprintf(w, "event: %s\n", event)
		}
		fmt.Fprint(w, "data: ")
		if err := enc.Encode(v); err != nil {
			return false
		}
		fmt.Fprint(w, "\n")
		if flusher != nil {
			flusher.Flush()
		}
		return true
	}
	if !send(payload()) {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			if !send(payload()) {
				return
			}
		}
	}
}
