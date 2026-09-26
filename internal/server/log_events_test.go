package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type logBatchRecorder struct {
	*httptest.ResponseRecorder
	cancel  context.CancelFunc
	flushes int
}

func (w *logBatchRecorder) Flush() { w.flushes++; w.ResponseRecorder.Flush(); w.cancel() }

func TestLogEventsBatchMatchesSnapshot(t *testing.T) {
	app := newTestApp(t)
	path := filepath.Join(app.DataDir, "logs", "msf.log")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	text := "2026-09-26 10:00:00 INFO needle-one\n2026-09-26 10:00:01 WARN needle-two\n2026-09-26 10:00:02 INFO other\n"
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{"?lines=80&q=needle", "?lines=80&q=needle&level=warn"} {
		req := httptest.NewRequest("GET", "/api/v1/events/logs/msf"+query, nil)
		req.SetPathValue("service", "msf")
		ctx, cancel := context.WithCancel(req.Context())
		w := &logBatchRecorder{ResponseRecorder: httptest.NewRecorder(), cancel: cancel}
		app.handleLogEvents(w, req.WithContext(ctx))
		cancel()
		if w.flushes != 1 || strings.Count(w.Body.String(), "event: logs\n") != 1 {
			t.Fatalf("expected one batch: %s", w.Body.String())
		}
		var stream struct {
			Logs   []map[string]any  `json:"logs"`
			Cursor logSnapshotCursor `json:"cursor"`
		}
		body := strings.TrimSpace(strings.SplitN(w.Body.String(), "data: ", 2)[1])
		if err := json.Unmarshal([]byte(body), &stream); err != nil {
			t.Fatal(err)
		}
		snapshot := httptest.NewRecorder()
		app.handleLogs(snapshot, req)
		var full struct {
			Logs   []map[string]any  `json:"logs"`
			Cursor logSnapshotCursor `json:"cursor"`
		}
		if err := json.Unmarshal(snapshot.Body.Bytes(), &full); err != nil {
			t.Fatal(err)
		}
		if stream.Cursor.Epoch == "" || stream.Cursor.Epoch != full.Cursor.Epoch || stream.Cursor.Revision >= full.Cursor.Revision {
			t.Fatalf("HTTP and SSE must share ordered cursors: %v then %v", stream.Cursor, full.Cursor)
		}
		if !reflect.DeepEqual(stream.Logs, full.Logs) {
			t.Fatalf("SSE rows differ from snapshot")
		}
		for _, entry := range stream.Logs {
			if entry["source"] != "msf" {
				t.Fatal("missing source", entry["source"])
			}
		}
	}
}

func TestLogSnapshotEpochAndOrder(t *testing.T) {
	first, second := newTestApp(t), newTestApp(t)
	_, a := first.readLogSnapshot("mihomo", 80)
	_, b := first.readLogSnapshot("mihomo", 1000)
	_, c := second.readLogSnapshot("mihomo", 80)
	if a.Epoch == "" || a.Epoch != b.Epoch || a.Revision >= b.Revision || a.Epoch == c.Epoch {
		t.Fatalf("invalid observation cursors: %v %v %v", a, b, c)
	}
}
