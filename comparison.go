package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type ComparisonResult struct {
	RateBasis       string  `json:"rateBasis,omitempty"`
	Model           string  `json:"model"`
	Response        string  `json:"response"`
	Reasoning       string  `json:"reasoning,omitempty"`
	Tokens          int     `json:"tokens"`
	TokensPerSecond float64 `json:"tokensPerSecond"`
	Seconds         float64 `json:"seconds"`
	Error           string  `json:"error,omitempty"`
}
type Comparison struct {
	ID      string             `json:"id"`
	Prompt  string             `json:"prompt"`
	Created string             `json:"created"`
	Results []ComparisonResult `json:"results"`
}
type ComparisonEvent struct {
	ID          string            `json:"id"`
	Model       string            `json:"model,omitempty"`
	Index       int               `json:"index"`
	Text        string            `json:"text,omitempty"`
	Reasoning   string            `json:"reasoning,omitempty"`
	Result      *ComparisonResult `json:"result,omitempty"`
	Complete    bool              `json:"complete"`
	Cancelled   bool              `json:"cancelled"`
	SaveWarning string            `json:"saveWarning,omitempty"`
}
type ComparisonService struct {
	chat      *ChatService
	workspace *WorkspaceService
	emit      func(ComparisonEvent)
}

func (s *ComparisonService) Start(id, prompt string, models []string) error {
	if !chatIDPattern.MatchString(id) || strings.TrimSpace(prompt) == "" || len(prompt) > 16000 {
		return errors.New("Enter a prompt of 1–16000 bytes")
	}
	if len(models) < 2 || len(models) > 4 {
		return errors.New("Select 2–4 distinct local models")
	}
	seen := map[string]bool{}
	for _, model := range models {
		if model == "" || len(model) > 256 || strings.Contains(strings.ToLower(model), "cloud") || seen[model] {
			return errors.New("Select distinct local model names")
		}
		seen[model] = true
	}
	s.chat.mu.Lock()
	if s.chat.cancel != nil {
		s.chat.mu.Unlock()
		return errors.New("Chat or another comparison is running. Stop it first")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	s.chat.activeID = id
	s.chat.cancel = cancel
	s.chat.mu.Unlock()
	go func() {
		hash := sha256.Sum256([]byte(id))
		record := Comparison{ID: hex.EncodeToString(hash[:16]), Prompt: prompt, Created: time.Now().UTC().Format(time.RFC3339Nano), Results: []ComparisonResult{}}
		for index, model := range models {
			if ctx.Err() != nil {
				break
			}
			s.emit(ComparisonEvent{ID: id, Model: model, Index: index})
			result := ComparisonResult{Model: model}
			started := time.Now()
			worker := &ChatService{client: s.chat.client, baseURL: s.chat.baseURL, flmURL: s.chat.flmURL, emit: func(event ChatEvent) {
				result.Response += event.Text
				result.Reasoning += event.Reasoning
				s.emit(ComparisonEvent{ID: id, Model: model, Index: index, Text: event.Text, Reasoning: event.Reasoning})
			}}
			modelCtx, modelCancel := context.WithTimeout(ctx, 10*time.Minute)
			terminal := worker.stream(modelCtx, id, model, []ChatMessage{{Role: "user", Content: prompt}})
			modelCancel()
			result.Tokens = terminal.Tokens
			result.RateBasis = terminal.RateBasis
			result.TokensPerSecond = terminal.TokensPerSecond
			result.Error = terminal.Error
			result.Seconds = time.Since(started).Seconds()
			if terminal.Cancelled {
				result.Error = "Stopped"
			}
			record.Results = append(record.Results, result)
			s.emit(ComparisonEvent{ID: id, Model: model, Index: index, Result: &result})
		}
		cancelled := ctx.Err() != nil
		cancel()
		warning := ""
		root, err := s.workspace.root("comparisons")
		if err == nil {
			s.workspace.mu.Lock()
			err = writeJSONFile(root, record.ID, record)
			s.workspace.mu.Unlock()
		}
		if err != nil {
			warning = "Comparison finished, but could not save: " + err.Error()
		}
		s.chat.mu.Lock()
		s.chat.activeID = ""
		s.chat.cancel = nil
		s.chat.mu.Unlock()
		s.emit(ComparisonEvent{ID: id, Complete: true, Cancelled: cancelled, SaveWarning: warning})
	}()
	return nil
}
func (s *ComparisonService) Cancel(id string) { s.chat.Cancel(id) }
func (s *ComparisonService) History() ([]Comparison, error) {
	s.workspace.mu.Lock()
	defer s.workspace.mu.Unlock()
	root, err := s.workspace.root("comparisons")
	if err != nil {
		return nil, err
	}
	items, err := readJSONObjects[Comparison](root)
	if err != nil {
		return nil, err
	}
	for i := range items {
		for j := range items[i].Results {
			items[i].Results[j].Response = ""
			items[i].Results[j].Reasoning = ""
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Created > items[j].Created })
	return items, nil
}
func (s *ComparisonService) Get(id string) (Comparison, error) {
	if !objectID.MatchString(id) {
		return Comparison{}, errors.New("Invalid comparison ID")
	}
	s.workspace.mu.Lock()
	defer s.workspace.mu.Unlock()
	root, err := s.workspace.root("comparisons")
	if err != nil {
		return Comparison{}, err
	}
	data, err := os.ReadFile(filepath.Join(root, id+".json"))
	if err != nil {
		return Comparison{}, err
	}
	var result Comparison
	err = json.Unmarshal(data, &result)
	return result, err
}
