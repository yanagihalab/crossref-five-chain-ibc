package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTimingRecorderExcludesErrorsAndUsesMonotonicDuration(t *testing.T) {
	file := filepath.Join(t.TempDir(), "timings.jsonl")
	r, err := newTimingRecorder(file, "test-run", "chain-a")
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	r.record("proof.verify", "", 7, start, errors.New("secret proof"))
	if err := r.close(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(file)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("timing file must have mode 0600: %v", err)
	}
	bz, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(bz), "secret proof") {
		t.Fatal("timing record contains an error message")
	}
	var row map[string]any
	if err := json.Unmarshal(bz, &row); err != nil {
		t.Fatal(err)
	}
	if row["outcome"] != "error" || row["operation"] != "proof.verify" || row["runId"] != "test-run" || row["round"] != float64(7) || row["durationMs"].(float64) < 0 {
		t.Fatalf("unexpected timing record: %v", row)
	}
}
