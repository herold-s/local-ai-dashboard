package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseGPUsHandlesMultipleCardsAndUnavailableMetrics(t *testing.T) {
	gpus, err := parseGPUs("GPU-a, NVIDIA GeForce RTX 5080, 2048, 16303, 23, 44, 65.8\nGPU-b, NVIDIA GeForce RTX 3070, 300, 8192, [N/A], 38, [Not Supported]\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(gpus) != 2 || gpus[0].ID != "GPU-a" || *gpus[0].MemoryTotal != 16303 {
		t.Fatalf("Unexpected GPUs: %+v", gpus)
	}
	if gpus[1].Utilization != nil || gpus[1].Power != nil {
		t.Fatal("Unavailable metrics must not be presented as zero")
	}
	if _, err := parseGPUs("invalid,row\n"); err == nil {
		t.Fatal("Expected an error for malformed telemetry")
	}
}

func TestBenchmarkCalculatesRateAndPersistsHistory(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/generate" || r.Method != http.MethodPost {
			t.Errorf("Unexpected request: %s %s", r.Method, r.URL.Path)
		}
		var body struct {
			Stream  bool `json:"stream"`
			Options struct {
				NumPredict int `json:"num_predict"`
			} `json:"options"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body.Stream || body.Options.NumPredict != 256 {
			t.Error("Benchmark must use bounded, non-streaming generation")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"response":"func palindrome() {}","eval_count":100,"eval_duration":2000000000,"total_duration":3500000000,"load_duration":500000000}`))
	}))
	defer server.Close()
	s := &MonitorService{client: server.Client(), baseURL: server.URL, historyPath: filepath.Join(t.TempDir(), "history.json")}
	for i := 0; i < 2; i++ {
		result, err := s.Benchmark("test:7b", "Write a function")
		if err != nil {
			t.Fatal(err)
		}
		if result.TokensPerSecond != 50 || result.TotalSeconds != 3.5 || result.LoadSeconds != .5 || result.SaveWarning != "" {
			t.Fatalf("Unexpected result: %+v", result)
		}
	}
	history, err := s.History()
	if err != nil || len(history) != 2 {
		t.Fatalf("History not persisted: %+v, %v", history, err)
	}
	if _, err := s.Benchmark("test:7b-cloud", "hello"); err == nil {
		t.Fatal("Cloud tags should not be benchmarked")
	}
	if _, err := s.Benchmark("test:7b", ""); err == nil {
		t.Fatal("Empty prompts should be rejected")
	}
}

func TestBenchmarkReportsOllamaErrorAndPreservesCorruptHistory(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"error":"model not found"}`))
	}))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "history.json")
	s := &MonitorService{client: server.Client(), baseURL: server.URL, historyPath: path}
	if _, err := s.Benchmark("missing", "hello"); err == nil || !strings.Contains(err.Error(), "model not found") {
		t.Fatalf("Expected Ollama's error: %v", err)
	}
	if err := os.WriteFile(path, []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.saveResult(BenchmarkResult{}); err == nil {
		t.Fatal("Do not overwrite corrupt history")
	}
	data, _ := os.ReadFile(path)
	if string(data) != "broken" {
		t.Fatal("Existing history was overwritten")
	}
}

func TestLiveSnapshot(t *testing.T) {
	if os.Getenv("DASHBOARD_LIVE_TEST") != "1" {
		t.Skip("Set DASHBOARD_LIVE_TEST=1 to check actual hardware")
	}
	state := NewMonitorService().Snapshot()
	t.Logf("GPUs: %+v; Ollama online: %v; Ollama status: %s", state.GPUs, state.OllamaOnline, state.OllamaError)
	if state.GPUError != "" || len(state.GPUs) == 0 {
		t.Fatalf("Live GPU detection failed: %s", state.GPUError)
	}
}
