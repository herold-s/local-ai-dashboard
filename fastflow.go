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
	"net/url"
	"os"
	"strings"
	"time"
)

const flmPrefix = "fastflowlm::"

type ProviderStatus struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Endpoint string `json:"endpoint"`
	Online   bool   `json:"online"`
	Error    string `json:"error,omitempty"`
}

func fastFlowURL() string {
	if value := os.Getenv("LOCAL_AI_FLM_URL"); value != "" {
		return strings.TrimSuffix(strings.TrimRight(value, "/"), "/v1")
	}
	return "http://127.0.0.1:52625"
}
func validateLocalURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil {
		return err
	}
	host := strings.ToLower(parsed.Hostname())
	if (parsed.Scheme != "http" && parsed.Scheme != "https") || (host != "localhost" && host != "127.0.0.1" && host != "::1") || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Path != "" {
		return errors.New("FastFlowLM endpoint must be a localhost HTTP(S) URL, optionally ending in /v1")
	}
	return nil
}
func localProviderClient(client *http.Client) *http.Client {
	copy := *client
	copy.CheckRedirect = func(*http.Request, []*http.Request) error {
		return errors.New("Local model redirects are not supported")
	}
	return &copy
}
func listFastFlow(ctx context.Context, client *http.Client, endpoint string) ([]Model, error) {
	if endpoint == "" {
		endpoint = fastFlowURL()
	}
	if err := validateLocalURL(endpoint); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"/v1/models", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer flm")
	res, err := localProviderClient(client).Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("FastFlowLM returned HTTP %d", res.StatusCode)
	}
	var body struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 4<<20)).Decode(&body); err != nil {
		return nil, err
	}
	models := []Model{}
	for _, item := range body.Data {
		if item.ID != "" && !strings.Contains(strings.ToLower(item.ID), "cloud") {
			models = append(models, Model{Name: flmPrefix + item.ID, Provider: "fastflowlm", ModelID: item.ID})
		}
	}
	return models, nil
}
func (s *ChatService) streamFastFlow(ctx context.Context, id, model string, messages []ChatMessage, maxTokens int) ChatEvent {
	terminal := ChatEvent{ID: id, Done: true, RateBasis: "estimated"}
	fail := func(err error) ChatEvent {
		if errors.Is(ctx.Err(), context.Canceled) {
			terminal.Cancelled = true
		} else if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			terminal.Error = "FastFlowLM request timed out"
		} else {
			terminal.Error = err.Error()
		}
		return terminal
	}
	endpoint := s.flmURL
	if endpoint == "" {
		endpoint = fastFlowURL()
	}
	checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	models, err := listFastFlow(checkCtx, s.client, endpoint)
	cancel()
	if err != nil {
		return fail(fmt.Errorf("Cannot reach FastFlowLM. Start flm serve on this laptop: %w", err))
	}
	found := false
	for _, item := range models {
		if item.Name == model {
			found = true
			break
		}
	}
	if !found {
		return fail(errors.New("Selected FastFlowLM model is not available. Refresh models"))
	}
	wire := make([]map[string]string, len(messages))
	for i, message := range messages {
		wire[i] = map[string]string{"role": message.Role, "content": message.Content}
	}
	body, err := json.Marshal(map[string]any{"model": strings.TrimPrefix(model, flmPrefix), "messages": wire, "stream": true, "max_tokens": maxTokens, "stream_options": map[string]bool{"include_usage": true}})
	if err != nil {
		return fail(err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return fail(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer flm")
	res, err := localProviderClient(s.client).Do(req)
	if err != nil {
		return fail(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fail(fmt.Errorf("FastFlowLM returned HTTP %d: %s", res.StatusCode, readProviderError(res.Body)))
	}
	scanner := bufio.NewScanner(io.LimitReader(res.Body, 8<<20))
	scanner.Buffer(make([]byte, 4096), 1<<20)
	first := time.Time{}
	var eventData []string
	consume := func(data string) (bool, error) {
		if data == "[DONE]" {
			return true, nil
		}
		var part struct {
			Choices []struct {
				Delta struct {
					Content            string `json:"content"`
					Reasoning          string `json:"reasoning_content"`
					AlternateReasoning string `json:"reasoning"`
				} `json:"delta"`
			} `json:"choices"`
			Usage *struct {
				Tokens int `json:"completion_tokens"`
			} `json:"usage"`
			Error json.RawMessage `json:"error"`
		}
		if err := json.Unmarshal([]byte(data), &part); err != nil {
			return false, fmt.Errorf("Invalid FastFlowLM stream: %w", err)
		}
		if len(part.Error) > 0 && string(part.Error) != "null" {
			return false, fmt.Errorf("FastFlowLM stream error: %s", part.Error)
		}
		if part.Usage != nil {
			terminal.Tokens = part.Usage.Tokens
		}
		for _, choice := range part.Choices {
			reasoning := choice.Delta.Reasoning
			if reasoning == "" {
				reasoning = choice.Delta.AlternateReasoning
			}
			if choice.Delta.Content != "" || reasoning != "" {
				if first.IsZero() {
					first = time.Now()
				}
				s.emit(ChatEvent{ID: id, Text: choice.Delta.Content, Reasoning: reasoning})
			}
		}
		return false, nil
	}
	finish := func() ChatEvent {
		if terminal.Tokens > 0 && !first.IsZero() {
			terminal.TokensPerSecond = float64(terminal.Tokens) / max(time.Since(first).Seconds(), .001)
		}
		return terminal
	}
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			if len(eventData) > 0 {
				done, err := consume(strings.Join(eventData, "\n"))
				eventData = nil
				if err != nil {
					return fail(err)
				}
				if done {
					return finish()
				}
			}
			continue
		}
		if strings.HasPrefix(line, "data:") {
			eventData = append(eventData, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	if len(eventData) > 0 {
		done, err := consume(strings.Join(eventData, "\n"))
		if err != nil {
			return fail(err)
		}
		if done {
			return finish()
		}
	}
	if err := scanner.Err(); err != nil {
		return fail(err)
	}
	return fail(errors.New("FastFlowLM disconnected before the stream completed"))
}
func readProviderError(reader io.Reader) string {
	data, _ := io.ReadAll(io.LimitReader(reader, 2048))
	return strings.TrimSpace(string(data))
}
