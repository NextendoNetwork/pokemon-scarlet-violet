package main

import (
	"context"
	"io"
	"log"
	"os"
	"path/filepath"
	"testing"

	"google.golang.org/protobuf/types/known/emptypb"
)

func TestRoutineLoggingDisabledByDefault(t *testing.T) {
	t.Setenv("VIOLET_DEBUG", "")
	previousWriter, previousFlags := log.Writer(), log.Flags()
	t.Cleanup(func() {
		log.SetOutput(previousWriter)
		log.SetFlags(previousFlags)
	})
	configureVioletLogging()
	if log.Writer() != io.Discard {
		t.Fatal("routine server logs should be discarded by default")
	}
	t.Setenv("VIOLET_DEBUG", "1")
	configureVioletLogging()
	if log.Writer() != os.Stderr {
		t.Fatal("explicit debug flag should restore server logs")
	}
}

func TestProbeDisabledByDefault(t *testing.T) {
	t.Setenv("VIOLET_DEBUG", "")
	dir := filepath.Join(t.TempDir(), "probe")
	r := &probeRecorder{dir: dir, eventsPath: filepath.Join(dir, "npln_events.jsonl")}
	r.record(probeEvent{Kind: "test"})
	r.capturePayload(probeEvent{Kind: "test"}, []byte("sensitive"), nil)
	r.captureMessage(context.Background(), "in", "/test.Method", &emptypb.Empty{})
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("debug disabled: probe directory exists or stat failed: %v", err)
	}
}

func TestProbeRequiresExplicitDebugFlag(t *testing.T) {
	t.Setenv("VIOLET_DEBUG", "true")
	if violetDebugEnabled() {
		t.Fatal("only VIOLET_DEBUG=1 should enable raw captures")
	}
	t.Setenv("VIOLET_DEBUG", "1")
	dir := filepath.Join(t.TempDir(), "probe")
	r := &probeRecorder{dir: dir, eventsPath: filepath.Join(dir, "npln_events.jsonl")}
	r.captureMessage(context.Background(), "in", "/test.Method", &emptypb.Empty{})
	if _, err := os.Stat(filepath.Join(dir, "npln_events.jsonl")); err != nil {
		t.Fatalf("debug enabled: expected probe event: %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(dir, "payloads"))
	if err != nil || len(entries) == 0 {
		t.Fatalf("debug enabled: expected payload capture, entries=%d err=%v", len(entries), err)
	}
}
