package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

type fakeMonitor struct {
	model, prompt string
	failure       bool
}

func (f *fakeMonitor) Snapshot() Snapshot {
	s := Snapshot{GPUs: []GPU{{Name: "Test GPU"}}, Models: []Model{}, Running: []Model{}, OllamaOnline: true}
	if f.failure {
		s.OllamaOnline = false
		s.OllamaError = "Ollama offline"
	}
	return s
}
func (f *fakeMonitor) Benchmark(model, prompt string) (BenchmarkResult, error) {
	f.model, f.prompt = model, prompt
	if f.failure {
		return BenchmarkResult{}, errors.New("inference failed")
	}
	return BenchmarkResult{Model: model, Tokens: 100, TokensPerSecond: 50}, nil
}
func (f *fakeMonitor) History() ([]BenchmarkResult, error) { return []BenchmarkResult{}, nil }

func TestCLICommandsDoNotLaunchDesktop(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"status"}, {"gpu"}, {"models"}, {"history"}, {"completion", "powershell"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			root := newRootCommand(&fakeMonitor{}, func() error { t.Fatal("CLI command launched desktop"); return nil })
			var out bytes.Buffer
			root.SetOut(&out)
			root.SetErr(&out)
			root.SetArgs(args)
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			if out.Len() == 0 {
				t.Fatal("Expected command output")
			}
		})
	}
}

func TestCLIRoutesDesktopAndRejectsInvalidInput(t *testing.T) {
	for _, args := range [][]string{nil, {"gui"}} {
		called := false
		root := newRootCommand(&fakeMonitor{}, func() error { called = true; return nil })
		root.SetArgs(args)
		if err := root.Execute(); err != nil || !called {
			t.Fatalf("Desktop not launched: %v", err)
		}
	}
	for _, args := range [][]string{{"benchmark"}, {"gpu", "unexpected"}, {"unknown"}, {"status", "--unknown"}} {
		root := newRootCommand(&fakeMonitor{}, func() error { t.Fatal("Invalid input launched desktop"); return nil })
		root.SetArgs(args)
		if err := root.Execute(); err == nil {
			t.Fatalf("Expected argument validation for %v", args)
		}
	}
}

func TestCLIJSONAndBenchmarkFlags(t *testing.T) {
	fake := &fakeMonitor{}
	root := newRootCommand(fake, func() error { return nil })
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"benchmark", "--model", "test:7b", "--prompt", "hello", "--json"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	var result BenchmarkResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.TokensPerSecond != 50 || fake.model != "test:7b" || fake.prompt != "hello" {
		t.Fatalf("Unexpected result: %+v", result)
	}
	root = newRootCommand(&fakeMonitor{failure: true}, func() error { return nil })
	out.Reset()
	root.SetOut(&out)
	root.SetArgs([]string{"status", "--json"})
	if err := root.Execute(); err == nil {
		t.Fatal("Offline status must return a failure")
	}
	if !json.Valid(out.Bytes()) {
		t.Fatal("Partial JSON data must remain parseable on failure")
	}
}
