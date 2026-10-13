package logging

import (
	"bufio"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReadPodLogStreamWritesFinalFragment(t *testing.T) {
	ctx := context.Background()
	writer := &fileWriter{channel: make(chan string, 1), ctx: ctx}
	reader := bufio.NewReader(strings.NewReader("2026-10-09T14:42:39.123456789Z final fragment"))

	lastLogTime, err := readPodLogStream(reader, "test-pod", writer, nil)
	if err != io.EOF {
		t.Fatalf("got error %v, want EOF", err)
	}
	if lastLogTime == nil {
		t.Fatal("expected final fragment timestamp")
	}
	if got := <-writer.channel; got != "[test-pod] 2026-10-09T14:42:39.123456789Z final fragment" {
		t.Fatalf("got %q", got)
	}
}

func TestWriteNodeInfo(t *testing.T) {
	logDir := t.TempDir()
	originalCollector := collector
	collector = &PodLogCollector{logDir: logDir}
	t.Cleanup(func() { collector = originalCollector })

	err := WriteNodeInfo("node-b", "bc", map[string]string{
		"Grandmaster Clock": "node-a",
		"Clock Under Test":  "node-b",
	})
	if err != nil {
		t.Fatal(err)
	}

	content, err := os.ReadFile(filepath.Join(logDir, "NODE_INFO.txt"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(content)
	for _, want := range []string{"Node Under Test: node-b", "Clock Under Test: node-b", "Grandmaster Clock: node-a"} {
		if !strings.Contains(text, want) {
			t.Errorf("NODE_INFO.txt missing %q", want)
		}
	}
}

func TestRetryUntilDone(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	attempts := 0
	retryUntilDone(ctx, 0, func() bool {
		attempts++
		if attempts == 2 {
			cancel()
		}
		return true
	})
	if attempts != 2 {
		t.Fatalf("got %d attempts, want 2", attempts)
	}
}

func TestParseLogTimestamp(t *testing.T) {
	timestamp := parseLogTimestamp("2026-10-09T14:42:39.123456789Z proxy restarted\n")
	if timestamp == nil {
		t.Fatal("expected timestamp")
	}
	want := time.Date(2026, 10, 9, 14, 42, 39, 123456790, time.UTC)
	if !timestamp.Time.Equal(want) {
		t.Fatalf("got %s, want %s", timestamp.Time, want)
	}

	if timestamp := parseLogTimestamp("proxy restarted\n"); timestamp != nil {
		t.Fatalf("unexpected timestamp %s", timestamp.Time)
	}
}
