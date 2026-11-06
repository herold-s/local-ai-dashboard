package main

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type GPU struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	MemoryUsed  *float64 `json:"memoryUsed"`
	MemoryTotal *float64 `json:"memoryTotal"`
	Utilization *float64 `json:"utilization"`
	Temperature *float64 `json:"temperature"`
	Power       *float64 `json:"power"`
}

type Model struct {
	Provider    string `json:"provider,omitempty"`
	ModelID     string `json:"modelID,omitempty"`
	RemoteModel string `json:"remote_model,omitempty"`
	RemoteHost  string `json:"remote_host,omitempty"`
	Name        string `json:"name"`
	Size        int64  `json:"size"`
	SizeVRAM    int64  `json:"size_vram"`
	Details     struct {
		ParameterSize string `json:"parameter_size"`
		Quantization  string `json:"quantization_level"`
	} `json:"details"`
}

type Snapshot struct {
	Providers       []ProviderStatus `json:"providers"`
	InferenceOnline bool             `json:"inferenceOnline"`
	Timestamp       string           `json:"timestamp"`
	GPUs            []GPU            `json:"gpus"`
	GPUError        string           `json:"gpuError"`
	OllamaOnline    bool             `json:"ollamaOnline"`
	OllamaError     string           `json:"ollamaError"`
	Models          []Model          `json:"models"`
	Running         []Model          `json:"running"`
}

type BenchmarkResult struct {
	RateBasis       string  `json:"rateBasis,omitempty"`
	Timestamp       string  `json:"timestamp"`
	Model           string  `json:"model"`
	Tokens          int     `json:"tokens"`
	TokensPerSecond float64 `json:"tokensPerSecond"`
	TotalSeconds    float64 `json:"totalSeconds"`
	LoadSeconds     float64 `json:"loadSeconds"`
	Response        string  `json:"response"`
	SaveWarning     string  `json:"saveWarning,omitempty"`
}

type MonitorService struct {
	flmURL      string
	client      *http.Client
	baseURL     string
	historyPath string
	mu          sync.Mutex
	benchmarkMu sync.Mutex
}

func NewMonitorService() *MonitorService {
	config, err := os.UserConfigDir()
	path := ""
	if err == nil {
		path = filepath.Join(config, "LocalAIDashboard", "benchmarks.json")
	}
	return &MonitorService{client: &http.Client{}, baseURL: "http://127.0.0.1:11434", historyPath: path}
}

func (s *MonitorService) Snapshot() Snapshot {
	result := Snapshot{Timestamp: time.Now().Format(time.RFC3339), GPUs: []GPU{}, Models: []Model{}, Running: []Model{}}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "nvidia-smi", "--query-gpu=uuid,name,memory.used,memory.total,utilization.gpu,temperature.gpu,power.draw", "--format=csv,noheader,nounits")
		hideWindow(cmd)
		out, err := cmd.Output()
		if err != nil {
			result.GPUError = "GPU telemetry unavailable. Check nvidia-smi and your NVIDIA driver."
			return
		}
		gpus, err := parseGPUs(string(out))
		if err != nil {
			result.GPUError = err.Error()
			return
		}
		result.GPUs = gpus
	}()
	go func() {
		defer wg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		var installed, running struct {
			Models []Model `json:"models"`
		}
		if err := s.get(ctx, "/api/tags", &installed); err != nil {
			result.OllamaError = "Cannot reach Ollama at 127.0.0.1:11434. Start Ollama to see your models."
			return
		}
		result.OllamaOnline = true
		if installed.Models != nil {
			result.Models = installed.Models
		}
		if err := s.get(ctx, "/api/ps", &running); err != nil {
			result.OllamaError = "Ollama is online, but running model information is unavailable."
			return
		}
		if running.Models != nil {
			result.Running = running.Models
		}
	}()
	wg.Wait()
	for index := range result.Models {
		result.Models[index].Provider = "ollama"
		result.Models[index].ModelID = result.Models[index].Name
	}
	flmEndpoint := s.flmURL
	if flmEndpoint == "" {
		flmEndpoint = fastFlowURL()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	flmModels, flmErr := listFastFlow(ctx, s.client, flmEndpoint)
	cancel()
	flmStatus := ProviderStatus{ID: "fastflowlm", Name: "FastFlowLM", Endpoint: flmEndpoint, Online: flmErr == nil}
	if flmErr != nil {
		flmStatus.Error = "FastFlowLM is not reachable. Start flm serve on a supported laptop."
	}
	result.Providers = []ProviderStatus{{ID: "ollama", Name: "Ollama", Endpoint: s.baseURL, Online: result.OllamaOnline, Error: result.OllamaError}, flmStatus}
	result.InferenceOnline = result.OllamaOnline || flmStatus.Online
	result.Models = append(result.Models, flmModels...)
	return result
}

func parseGPUs(raw string) ([]GPU, error) {
	rows, err := csv.NewReader(strings.NewReader(raw)).ReadAll()
	if err != nil {
		return nil, fmt.Errorf("Cannot parse NVIDIA telemetry: %w", err)
	}
	gpus := []GPU{}
	for _, row := range rows {
		if len(row) != 7 {
			return nil, errors.New("Unexpected NVIDIA telemetry format")
		}
		gpus = append(gpus, GPU{ID: strings.TrimSpace(row[0]), Name: strings.TrimSpace(row[1]), MemoryUsed: metric(row[2]), MemoryTotal: metric(row[3]), Utilization: metric(row[4]), Temperature: metric(row[5]), Power: metric(row[6])})
	}
	return gpus, nil
}

func metric(raw string) *float64 {
	n, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil {
		return nil
	}
	return &n
}

func (s *MonitorService) get(ctx context.Context, path string, value any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.baseURL+path, nil)
	if err != nil {
		return err
	}
	res, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("Ollama returned HTTP %d", res.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(res.Body, 4<<20)).Decode(value)
}

func (s *MonitorService) Benchmark(model, prompt string) (BenchmarkResult, error) {
	if !s.benchmarkMu.TryLock() {
		return BenchmarkResult{}, errors.New("A benchmark is already running")
	}
	defer s.benchmarkMu.Unlock()
	if strings.HasPrefix(model, flmPrefix) {
		if strings.TrimSpace(prompt) == "" || len(prompt) > 16000 {
			return BenchmarkResult{}, errors.New("Enter a prompt between 1 and 16000 bytes")
		}
		started := time.Now()
		var answer strings.Builder
		worker := &ChatService{client: s.client, flmURL: s.flmURL, emit: func(event ChatEvent) { answer.WriteString(event.Text) }}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		terminal := worker.streamFastFlow(ctx, "benchmark", model, []ChatMessage{{Role: "user", Content: prompt}}, 256)
		if terminal.Error != "" {
			return BenchmarkResult{}, errors.New(terminal.Error)
		}
		result := BenchmarkResult{Timestamp: time.Now().Format(time.RFC3339), Model: model, Tokens: terminal.Tokens, TokensPerSecond: terminal.TokensPerSecond, TotalSeconds: time.Since(started).Seconds(), LoadSeconds: -1, Response: answer.String(), RateBasis: "estimated"}
		if err := s.saveResult(result); err != nil {
			result.SaveWarning = err.Error()
		}
		return result, nil
	}
	model, prompt = strings.TrimSpace(model), strings.TrimSpace(prompt)
	if model == "" || len(model) > 256 {
		return BenchmarkResult{}, errors.New("Select an installed model")
	}
	// Ollama cloud-backed tags are excluded: this dashboard benchmarks local inference.
	if strings.Contains(strings.ToLower(model), "cloud") {
		return BenchmarkResult{}, errors.New("Select a local model, rather than a cloud-backed model")
	}
	if prompt == "" || len(prompt) > 16000 {
		return BenchmarkResult{}, errors.New("Enter a prompt between 1 and 16000 bytes")
	}
	body, err := json.Marshal(map[string]any{"model": model, "prompt": prompt, "stream": false, "options": map[string]any{"num_predict": 256, "temperature": 0, "seed": 42}})
	if err != nil {
		return BenchmarkResult{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.baseURL+"/api/generate", bytes.NewReader(body))
	if err != nil {
		return BenchmarkResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := s.client.Do(req)
	if err != nil {
		return BenchmarkResult{}, fmt.Errorf("Benchmark failed (3 minute time limit): %w", err)
	}
	defer res.Body.Close()
	var generated struct {
		Model         string `json:"model"`
		Response      string `json:"response"`
		Error         string `json:"error"`
		EvalCount     int    `json:"eval_count"`
		EvalDuration  int64  `json:"eval_duration"`
		TotalDuration int64  `json:"total_duration"`
		LoadDuration  int64  `json:"load_duration"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 4<<20)).Decode(&generated); err != nil {
		return BenchmarkResult{}, fmt.Errorf("Cannot read Ollama response: %w", err)
	}
	if generated.Error != "" {
		return BenchmarkResult{}, errors.New(generated.Error)
	}
	if res.StatusCode != http.StatusOK {
		return BenchmarkResult{}, fmt.Errorf("Ollama returned HTTP %d", res.StatusCode)
	}
	if generated.EvalDuration <= 0 || generated.EvalCount <= 0 {
		return BenchmarkResult{}, errors.New("Ollama returned no generation timing. Try another prompt or model")
	}
	result := BenchmarkResult{Timestamp: time.Now().Format(time.RFC3339), Model: model, Tokens: generated.EvalCount, TokensPerSecond: float64(generated.EvalCount) / float64(generated.EvalDuration) * 1e9, TotalSeconds: float64(generated.TotalDuration) / 1e9, LoadSeconds: float64(generated.LoadDuration) / 1e9, Response: generated.Response}
	if err := s.saveResult(result); err != nil {
		result.SaveWarning = "Benchmark completed, but history could not be saved: " + err.Error()
	}
	return result, nil
}

func (s *MonitorService) History() ([]BenchmarkResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.readHistory()
}

func (s *MonitorService) readHistory() ([]BenchmarkResult, error) {
	results := []BenchmarkResult{}
	if s.historyPath == "" {
		return results, errors.New("User configuration directory unavailable")
	}
	data, err := os.ReadFile(s.historyPath)
	if errors.Is(err, os.ErrNotExist) {
		return results, nil
	}
	if err != nil {
		return results, err
	}
	if err := json.Unmarshal(data, &results); err != nil {
		return nil, fmt.Errorf("Cannot read benchmark history: %w", err)
	}
	if results == nil {
		results = []BenchmarkResult{}
	}
	return results, nil
}

func (s *MonitorService) saveResult(result BenchmarkResult) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	history, err := s.readHistory()
	if err != nil {
		return err
	}
	history = append([]BenchmarkResult{result}, history...)
	if len(history) > 100 {
		history = history[:100]
	}
	data, err := json.MarshalIndent(history, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.historyPath), 0700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(s.historyPath), "benchmarks-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(temp.Name(), s.historyPath)
}
