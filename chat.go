package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

type ChatMessage struct {
	Role        string   `json:"role"`
	Content     string   `json:"content"`
	DocumentIDs []string `json:"documentIDs,omitempty"`
}

type ChatEvent struct {
	RateBasis       string      `json:"rateBasis,omitempty"`
	Sources         []SearchHit `json:"sources,omitempty"`
	ID              string      `json:"id"`
	Text            string      `json:"text"`
	Reasoning       string      `json:"reasoning"`
	Done            bool        `json:"done"`
	Cancelled       bool        `json:"cancelled"`
	Error           string      `json:"error,omitempty"`
	TokensPerSecond float64     `json:"tokensPerSecond"`
	Tokens          int         `json:"tokens"`
}

type Conversation struct {
	SearchLibrary bool            `json:"searchLibrary,omitempty"`
	ID            string          `json:"id"`
	Title         string          `json:"title"`
	Model         string          `json:"model"`
	Updated       string          `json:"updated"`
	Repository    json.RawMessage `json:"repository"`
}

// ChatService owns network streams and local conversation persistence. It does
// not expose a network listener or execute model-provided commands.
type ChatService struct {
	flmURL    string
	workspace *WorkspaceService
	client    *http.Client
	baseURL   string
	directory string
	emit      func(ChatEvent)
	mu        sync.Mutex
	activeID  string
	cancel    context.CancelFunc
	storeMu   sync.Mutex
}

var chatIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,80}$`)

func NewChatService(emit func(ChatEvent)) *ChatService {
	directory := ""
	if config, err := os.UserConfigDir(); err == nil {
		directory = filepath.Join(config, "LocalAIDashboard", "conversations")
	}
	return &ChatService{client: &http.Client{}, baseURL: "http://127.0.0.1:11434", directory: directory, emit: emit}
}

func (s *ChatService) Start(id, model string, messages []ChatMessage) error {
	return s.StartContext(id, model, messages, false)
}

func (s *ChatService) StartContext(id, model string, messages []ChatMessage, useLibrary bool) error {
	if !chatIDPattern.MatchString(id) {
		return errors.New("Invalid chat request ID")
	}
	if strings.TrimSpace(model) == "" || len(model) > 256 || strings.Contains(strings.ToLower(model), "cloud") {
		return errors.New("Choose an installed local model")
	}
	if len(messages) == 0 || len(messages) > 200 {
		return errors.New("A chat request must contain 1–200 messages; start a new conversation if this one is full")
	}
	total := 0
	for _, message := range messages {
		if message.Role != "user" && message.Role != "assistant" && message.Role != "system" {
			return errors.New("Unsupported chat message role")
		}
		total += len(message.Content)
	}
	if total > 512*1024 {
		return errors.New("Conversation is too large. Start a new chat")
	}
	s.mu.Lock()
	if s.cancel != nil {
		s.mu.Unlock()
		return errors.New("Another reply is still running. Stop it before sending again")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	s.activeID = id
	s.cancel = cancel
	s.mu.Unlock()
	go func() {
		var sources []SearchHit
		var contextErr error
		if s.workspace != nil {
			messages, sources, contextErr = s.workspace.contextFor(messages, useLibrary)
		}
		var event ChatEvent
		if contextErr != nil {
			event = ChatEvent{ID: id, Done: true, Error: contextErr.Error()}
		} else {
			if len(sources) > 0 {
				s.emit(ChatEvent{ID: id, Sources: sources})
			}
			event = s.stream(ctx, id, model, messages)
		}
		cancel()
		s.mu.Lock()
		s.cancel = nil
		s.activeID = ""
		s.mu.Unlock()
		s.emit(event)
	}()
	return nil
}

func (s *ChatService) Cancel(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.activeID == id && s.cancel != nil {
		s.cancel()
	}
}

func (s *ChatService) stream(ctx context.Context, id, model string, messages []ChatMessage) ChatEvent {
	if strings.HasPrefix(model, flmPrefix) {
		return s.streamFastFlow(ctx, id, model, messages, 2048)
	}
	terminal := ChatEvent{ID: id, Done: true}
	fail := func(err error) ChatEvent {
		if errors.Is(ctx.Err(), context.Canceled) {
			terminal.Cancelled = true
		} else if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			terminal.Error = "Reply timed out after 10 minutes. Try a shorter conversation or smaller model"
		} else {
			terminal.Error = err.Error()
		}
		return terminal
	}
	// Only installed local models may be selected. Cloud references are rejected.
	checkCtx, checkCancel := context.WithTimeout(ctx, 5*time.Second)
	var installed struct {
		Models []struct {
			Name        string `json:"name"`
			RemoteModel string `json:"remote_model"`
			RemoteHost  string `json:"remote_host"`
		} `json:"models"`
	}
	req, err := http.NewRequestWithContext(checkCtx, http.MethodGet, s.baseURL+"/api/tags", nil)
	if err != nil {
		checkCancel()
		return fail(err)
	}
	res, err := s.client.Do(req)
	if err != nil {
		checkCancel()
		return fail(fmt.Errorf("Cannot reach Ollama. Start Ollama and try again: %w", err))
	}
	err = json.NewDecoder(io.LimitReader(res.Body, 4<<20)).Decode(&installed)
	res.Body.Close()
	checkCancel()
	if err != nil || res.StatusCode != http.StatusOK {
		return fail(errors.New("Cannot read installed models from Ollama"))
	}
	found := false
	for _, item := range installed.Models {
		if item.Name == model {
			found = true
			if item.RemoteModel != "" || item.RemoteHost != "" {
				return fail(errors.New("Cloud-backed models are not supported in local chat"))
			}
		}
	}
	if !found {
		return fail(errors.New("Selected model is no longer installed. Refresh the model list"))
	}
	// Only role/content cross the Ollama boundary; local document IDs stay in the app.
	wireMessages := make([]map[string]string, len(messages))
	for i, message := range messages {
		wireMessages[i] = map[string]string{"role": message.Role, "content": message.Content}
	}
	body, err := json.Marshal(map[string]any{"model": model, "messages": wireMessages, "stream": true, "keep_alive": "5m", "options": map[string]any{"num_predict": 2048, "num_ctx": 8192}})
	if err != nil {
		return fail(err)
	}
	req, err = http.NewRequestWithContext(ctx, http.MethodPost, s.baseURL+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return fail(err)
	}
	req.Header.Set("Content-Type", "application/json")
	res, err = s.client.Do(req)
	if err != nil {
		return fail(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		var response struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(io.LimitReader(res.Body, 64<<10)).Decode(&response)
		if response.Error == "" {
			response.Error = fmt.Sprintf("Ollama returned HTTP %d", res.StatusCode)
		}
		return fail(errors.New(response.Error))
	}
	scanner := bufio.NewScanner(io.LimitReader(res.Body, 8<<20))
	scanner.Buffer(make([]byte, 4096), 1<<20)
	for scanner.Scan() {
		var part struct {
			Message struct {
				Content  string `json:"content"`
				Thinking string `json:"thinking"`
			} `json:"message"`
			Done         bool   `json:"done"`
			Error        string `json:"error"`
			EvalCount    int    `json:"eval_count"`
			EvalDuration int64  `json:"eval_duration"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &part); err != nil {
			return fail(fmt.Errorf("Invalid Ollama stream: %w", err))
		}
		if part.Error != "" {
			return fail(errors.New(part.Error))
		}
		if part.Message.Content != "" || part.Message.Thinking != "" {
			s.emit(ChatEvent{ID: id, Text: part.Message.Content, Reasoning: part.Message.Thinking})
		}
		if part.Done {
			terminal.Tokens = part.EvalCount
			if part.EvalDuration > 0 {
				terminal.TokensPerSecond = float64(part.EvalCount) / float64(part.EvalDuration) * 1e9
			}
			return terminal
		}
	}
	if err := scanner.Err(); err != nil {
		return fail(err)
	}
	return fail(errors.New("Ollama disconnected before finishing the reply"))
}

func (s *ChatService) Conversations() ([]Conversation, error) {
	s.storeMu.Lock()
	defer s.storeMu.Unlock()
	if s.directory == "" {
		return nil, errors.New("User configuration directory unavailable")
	}
	items, err := os.ReadDir(s.directory)
	if errors.Is(err, os.ErrNotExist) {
		return []Conversation{}, nil
	}
	if err != nil {
		return nil, err
	}
	result := []Conversation{}
	for _, item := range items {
		if item.IsDir() || filepath.Ext(item.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.directory, item.Name()))
		if err != nil {
			return nil, err
		}
		var conversation Conversation
		if err := json.Unmarshal(data, &conversation); err != nil {
			return nil, fmt.Errorf("Cannot read conversation %s: %w", item.Name(), err)
		}
		conversation.Repository = nil // Sidebar reads metadata; full threads load on demand.
		result = append(result, conversation)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Updated > result[j].Updated })
	return result, nil
}

func (s *ChatService) Conversation(id string) (Conversation, error) {
	if !chatIDPattern.MatchString(id) {
		return Conversation{}, errors.New("Invalid conversation ID")
	}
	if s.directory == "" {
		return Conversation{}, errors.New("User configuration directory unavailable")
	}
	s.storeMu.Lock()
	defer s.storeMu.Unlock()
	var result Conversation
	data, err := os.ReadFile(filepath.Join(s.directory, id+".json"))
	if err != nil {
		return result, err
	}
	err = json.Unmarshal(data, &result)
	return result, err
}

func (s *ChatService) Save(conversation Conversation) (Conversation, error) {
	if s.directory == "" {
		return Conversation{}, errors.New("User configuration directory unavailable")
	}
	if !chatIDPattern.MatchString(conversation.ID) {
		return Conversation{}, errors.New("Invalid conversation ID")
	}
	if len(conversation.Title) > 200 || len(conversation.Model) > 256 || len(conversation.Repository) > 4<<20 || !json.Valid(conversation.Repository) {
		return Conversation{}, errors.New("Invalid conversation data or size limit exceeded")
	}
	s.storeMu.Lock()
	defer s.storeMu.Unlock()
	conversation.Updated = time.Now().UTC().Format(time.RFC3339Nano)
	data, err := json.MarshalIndent(conversation, "", "  ")
	if err != nil {
		return Conversation{}, err
	}
	if err := os.MkdirAll(s.directory, 0700); err != nil {
		return Conversation{}, err
	}
	file, err := os.CreateTemp(s.directory, ".chat-*.tmp")
	if err != nil {
		return Conversation{}, err
	}
	defer os.Remove(file.Name())
	if _, err := file.Write(data); err != nil {
		file.Close()
		return Conversation{}, err
	}
	if err := file.Close(); err != nil {
		return Conversation{}, err
	}
	if err := os.Rename(file.Name(), filepath.Join(s.directory, conversation.ID+".json")); err != nil {
		return Conversation{}, err
	}
	conversation.Repository = nil
	return conversation, nil
}
