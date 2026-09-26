package server

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type countedLogReader struct {
	io.ReaderAt
	bytes int
}

func (r *countedLogReader) ReadAt(p []byte, off int64) (int, error) {
	n, err := r.ReaderAt.ReadAt(p, off)
	r.bytes += n
	return n, err
}

func TestReadLogTail(t *testing.T) {
	for _, tc := range []struct {
		name, text string
		offset     int64
		count      int
		want       []string
	}{
		{"empty", "", 0, 3, []string{}},
		{"trailing newline", "a\nb\nc\n", 0, 2, []string{"b", "c"}},
		{"no trailing newline", "a\nb\nc", 0, 2, []string{"b", "c"}},
		{"blank lines", "a\n\nb\n\n", 0, 3, []string{"", "b", ""}},
		{"crlf", "a\r\nb\r\n", 0, 2, []string{"a", "b"}},
		{"offset", "old\nnew\nlast", 4, 10, []string{"new", "last"}},
		{"zero count", "a\n", 0, 0, []string{}},
		{"long line", "old\n" + strings.Repeat("x", 100000) + "\nlast\n", 0, 2, []string{strings.Repeat("x", 100000), "last"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := readLogTail(strings.NewReader(tc.text), tc.offset, int64(len(tc.text)), tc.count)
			if err != nil || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("lines=%d want=%d err=%v", len(got), len(tc.want), err)
			}
		})
	}
}

func TestLogTailReadsBoundedBytes(t *testing.T) {
	for _, count := range []int{80, 1000} {
		previousBytes := 0
		for _, lines := range []int{10000, 1000000} {
			text := strings.Repeat("2026-09-26 INFO fixed-size fixture\n", lines)
			r := &countedLogReader{ReaderAt: strings.NewReader(text)}
			got, err := readLogTail(r, 0, int64(len(text)), count)
			if err != nil || len(got) != count {
				t.Fatal(len(got), err)
			}
			if previousBytes != 0 && r.bytes != previousBytes {
				t.Fatalf("read grew with file: %d -> %d", previousBytes, r.bytes)
			}
			previousBytes = r.bytes
			t.Logf("file=%d bytes tail=%d lines read=%d bytes", len(text), count, r.bytes)
		}
	}
}

func TestTailFileAfterTruncationAndReplacement(t *testing.T) {
	path := filepath.Join(t.TempDir(), "logs")
	if err := os.WriteFile(path, []byte("old-long-content\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(path, 0); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("new\n"), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := tailFileSince(path, 100, 10)
	if err != nil || !reflect.DeepEqual(got, []string{"new"}) {
		t.Fatal(got, err)
	}
	replacement := path + ".new"
	if err := os.WriteFile(replacement, []byte("replaced\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, path); err != nil {
		t.Fatal(err)
	}
	got, err = tailFile(path, 10)
	if err != nil || !reflect.DeepEqual(got, []string{"replaced"}) {
		t.Fatal(got, err)
	}
	_, err = readLogTail(strings.NewReader("short"), 0, 100, 10)
	if err == nil {
		t.Fatal("concurrent truncation must not return mixed snapshot")
	}
}

func BenchmarkLogTail(b *testing.B) {
	text := bytes.Repeat([]byte("2026-09-26 INFO fixture\n"), 100000)
	for _, count := range []int{80, 1000} {
		b.Run(fmt.Sprintf("reverse/%d", count), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if _, err := readLogTail(bytes.NewReader(text), 0, int64(len(text)), count); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run(fmt.Sprintf("previous/%d", count), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				scanner := bufio.NewScanner(bytes.NewReader(text))
				rows := make([]string, 0, count)
				for scanner.Scan() {
					line := scanner.Text()
					if len(rows) == count {
						copy(rows, rows[1:])
						rows[count-1] = line
					} else {
						rows = append(rows, line)
					}
				}
				if err := scanner.Err(); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
