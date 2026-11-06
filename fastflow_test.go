package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFastFlowDiscoveryStreamingAndBenchmark(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			fmt.Fprint(w, `{"data":[{"id":"qwen3:4b"}]}`)
			return
		}
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("Unexpected endpoint %s", r.URL.Path)
		}
		var request struct {
			Model     string        `json:"model"`
			Stream    bool          `json:"stream"`
			Messages  []ChatMessage `json:"messages"`
			MaxTokens int           `json:"max_tokens"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		if request.Model != "qwen3:4b" || !request.Stream || len(request.Messages) != 1 {
			t.Errorf("Invalid request: %+v", request)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"thinking\"}}]}\n\n")
		w.(http.Flusher).Flush()
		time.Sleep(20 * time.Millisecond)
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[],\"usage\":{\"completion_tokens\":10}}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	models, err := listFastFlow(context.Background(), server.Client(), server.URL)
	if err != nil || len(models) != 1 || models[0].Name != "fastflowlm::qwen3:4b" {
		t.Fatalf("Discovery: %+v %v", models, err)
	}
	chunks := []ChatEvent{}
	chat := &ChatService{client: server.Client(), flmURL: server.URL, emit: func(event ChatEvent) { chunks = append(chunks, event) }}
	result := chat.stream(context.Background(), "test", "fastflowlm::qwen3:4b", []ChatMessage{{Role: "user", Content: "hello"}})
	if result.Error != "" || result.Tokens != 10 || result.TokensPerSecond <= 0 || result.RateBasis != "estimated" || len(chunks) != 2 || chunks[0].Reasoning != "thinking" || chunks[1].Text != "hello" {
		t.Fatalf("Invalid stream: %+v %+v", result, chunks)
	}
	monitor := &MonitorService{client: server.Client(), flmURL: server.URL, historyPath: filepath.Join(t.TempDir(), "history.json")}
	benchmark, err := monitor.Benchmark("fastflowlm::qwen3:4b", "hello")
	if err != nil || benchmark.Response != "hello" || benchmark.LoadSeconds != -1 || benchmark.RateBasis != "estimated" {
		t.Fatalf("Benchmark: %+v %v", benchmark, err)
	}
}
func TestFastFlowOnlySnapshotWithoutOllama(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			fmt.Fprint(w, `{"data":[{"id":"local:4b"}]}`)
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	monitor := &MonitorService{client: server.Client(), baseURL: server.URL, flmURL: server.URL}
	snapshot := monitor.Snapshot()
	if snapshot.OllamaOnline || !snapshot.InferenceOnline || len(snapshot.Models) != 1 || len(snapshot.Providers) != 2 || !snapshot.Providers[1].Online {
		t.Fatalf("FastFlow-only discovery failed: %+v", snapshot)
	}
}
func TestFastFlowCancellationAndTruncatedStream(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(fmt.Sprint(cancelled), func(t *testing.T) {
			started := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v1/models" {
					fmt.Fprint(w, `{"data":[{"id":"local:4b"}]}`)
					return
				}
				fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n")
				w.(http.Flusher).Flush()
				close(started)
				if cancelled {
					<-r.Context().Done()
				}
			}))
			defer server.Close()
			chat := &ChatService{client: server.Client(), flmURL: server.URL, emit: func(ChatEvent) {}}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if cancelled {
				go func() { <-started; cancel() }()
			}
			result := chat.stream(ctx, "test", "fastflowlm::local:4b", []ChatMessage{{Role: "user", Content: "hi"}})
			if cancelled && !result.Cancelled {
				t.Fatalf("Expected cancellation: %+v", result)
			}
			if !cancelled && !strings.Contains(result.Error, "disconnected") {
				t.Fatalf("Expected truncated-stream error: %+v", result)
			}
		})
	}
}
func TestFastFlowLocalEndpointValidation(t *testing.T) {
	for _, endpoint := range []string{"https://example.com", "http://localhost:1234?key=x", "http://user:secret@localhost:1234", "file:///tmp/model"} {
		if validateLocalURL(endpoint) == nil {
			t.Fatalf("Invalid endpoint accepted: %s", endpoint)
		}
	}
	for _, endpoint := range []string{"http://127.0.0.1:52625", "http://localhost:8000", "http://[::1]:52625"} {
		if err := validateLocalURL(endpoint); err != nil {
			t.Fatal(err)
		}
	}
}

func TestComparisonAcrossOllamaAndFastFlow(t *testing.T) {
	order := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			fmt.Fprint(w, `{"models":[{"name":"ollama:a"}]}`)
		case "/v1/models":
			fmt.Fprint(w, `{"data":[{"id":"flm:b"}]}`)
		case "/api/chat":
			order = append(order, "ollama")
			fmt.Fprint(w, "{\"message\":{\"content\":\"A\"}}\n{\"done\":true,\"eval_count\":10,\"eval_duration\":1000000000}\n")
		case "/v1/chat/completions":
			order = append(order, "flm")
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"B\"}}],\"usage\":{\"completion_tokens\":5}}\n\ndata: [DONE]\n\n")
		default:
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	chat := NewChatService(func(ChatEvent) {})
	chat.client = server.Client()
	chat.baseURL = server.URL
	chat.flmURL = server.URL
	completed := make(chan struct{}, 1)
	service := &ComparisonService{chat: chat, workspace: testWorkspace(t), emit: func(event ComparisonEvent) {
		if event.Complete {
			completed <- struct{}{}
		}
	}}
	if err := service.Start("cross-provider", "hello", []string{"ollama:a", "fastflowlm::flm:b"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-completed:
	case <-time.After(3 * time.Second):
		t.Fatal("Comparison timed out")
	}
	if strings.Join(order, ",") != "ollama,flm" {
		t.Fatalf("Wrong routing order: %+v", order)
	}
	items, err := service.History()
	if err != nil {
		t.Fatal(err)
	}
	record, err := service.Get(items[0].ID)
	if err != nil || record.Results[1].Response != "B" || record.Results[1].RateBasis != "estimated" {
		t.Fatalf("Wrong comparison: %+v %v", record, err)
	}
}
