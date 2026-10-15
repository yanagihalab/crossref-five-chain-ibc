package main

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"time"
)

var timingIdentity = regexp.MustCompile(`^[A-Za-z0-9-]{1,80}$`)

type timingRecorder struct {
	file     *os.File
	runID    string
	domain   string
	disabled bool
}

func newTimingRecorder(file, runID, domain string) (*timingRecorder, error) {
	if file == "" {
		return nil, nil
	}
	if !timingIdentity.MatchString(runID) || !timingIdentity.MatchString(domain) {
		return nil, fmt.Errorf("timing requires a safe run ID and domain")
	}
	if st, err := os.Lstat(file); err == nil && (!st.Mode().IsRegular() || st.Mode().Perm() != 0600) {
		return nil, fmt.Errorf("timing file must be regular with mode 0600")
	} else if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	f, err := os.OpenFile(file, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	return &timingRecorder{file: f, runID: runID, domain: domain}, nil
}

func (r *timingRecorder) record(operation, endpoint string, round uint64, start time.Time, result error) {
	if r == nil || r.disabled {
		return
	}
	finished := time.Now()
	row := map[string]any{
		"schemaVersion": 1, "runId": r.runID, "component": "history-signer", "domain": r.domain,
		"operation": operation, "startedAt": start.UTC().Format(time.RFC3339Nano),
		"finishedAt": finished.UTC().Format(time.RFC3339Nano), "durationMs": float64(finished.Sub(start).Nanoseconds()) / 1e6,
		"outcome": "ok", "clock": "process-monotonic",
	}
	if result != nil {
		row["outcome"] = "error"
	}
	if endpoint != "" {
		row["endpoint"] = endpoint
	}
	if round > 0 {
		row["round"] = round
	}
	bz, err := json.Marshal(row)
	if err == nil {
		var n int
		n, err = r.file.Write(append(bz, '\n'))
		if n != len(bz)+1 {
			err = fmt.Errorf("short timing write")
		}
	}
	if err != nil {
		r.disabled = true
		fmt.Fprintln(os.Stderr, "history timing records could not be written")
	}
}

func (r *timingRecorder) close() error {
	if r == nil {
		return nil
	}
	return r.file.Close()
}

func (o options) recordTiming(operation, endpoint string, round uint64, start time.Time, result error) {
	o.timings.record(operation, endpoint, round, start, result)
}
