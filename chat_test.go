package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func chatFixture(t *testing.T, handler http.HandlerFunc) (*ChatService, <-chan ChatEvent) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	events := make(chan ChatEvent, 100)
	s := NewChatService(func(event ChatEvent) { events <- event })
	s.baseURL = server.URL
	s.client = server.Client()
	s.directory = filepath.Join(t.TempDir(), "conversations")
	return s, events
}
func awaitChatDone(t *testing.T, events <-chan ChatEvent) []ChatEvent {
	t.Helper()
	result := []ChatEvent{}
	for {
		select {
		case event := <-events:
			result = append(result, event)
			if event.Done {
				return result
			}
		case <-time.After(3 * time.Second):
			t.Fatal("Chat did not finish")
			return nil
		}
	}
}
func TestChatStreamsAndReportsGenerationTiming(t *testing.T) {
	s, events := chatFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/tags" {
			fmt.Fprint(w, `{"models":[{"name":"local:7b"}]}`)
			return
		}
		var body struct {
			Model    string        `json:"model"`
			Messages []ChatMessage `json:"messages"`
			Stream   bool          `json:"stream"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body.Model != "local:7b" || !body.Stream || len(body.Messages) != 1 {
			t.Errorf("Unexpected request: %+v", body)
		}
		fmt.Fprintln(w, `{"message":{"thinking":"consider"}}`)
		fmt.Fprintln(w, `{"message":{"content":"Hello "}}`)
		fmt.Fprintln(w, `{"message":{"content":"world"}}`)
		fmt.Fprintln(w, `{"done":true,"eval_count":100,"eval_duration":2000000000}`)
	})
	if err := s.Start("test-1", "local:7b", []ChatMessage{{Role: "user", Content: "hello"}}); err != nil {
		t.Fatal(err)
	}
	result := awaitChatDone(t, events)
	if len(result) != 4 || result[0].Reasoning != "consider" || result[1].Text != "Hello " || result[3].TokensPerSecond != 50 || result[3].Error != "" {
		t.Fatalf("Unexpected stream: %+v", result)
	}
}
func TestChatCancellationClosesRequestAndRejectsConcurrentRun(t *testing.T) {
	started := make(chan struct{})
	s, events := chatFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/tags" {
			fmt.Fprint(w, `{"models":[{"name":"local:7b"}]}`)
			return
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		fmt.Fprintln(w, `{"message":{"content":"partial"}}`)
		w.(http.Flusher).Flush()
		close(started)
		<-r.Context().Done()
	})
	if err := s.Start("cancel-me", "local:7b", []ChatMessage{{Role: "user", Content: "hello"}}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("Request not started")
	}
	if err := s.Start("another", "local:7b", []ChatMessage{{Role: "user", Content: "hello"}}); err == nil {
		t.Fatal("Concurrent chat should be rejected")
	}
	s.Cancel("cancel-me")
	result := awaitChatDone(t, events)
	if !result[len(result)-1].Cancelled {
		t.Fatalf("Expected cancellation: %+v", result)
	}
}
func TestChatRejectsCloudAndIncompleteStreams(t *testing.T) {
	for _, test := range []struct{ name, tags, stream, contains string }{
		{"remote", `{"models":[{"name":"alias:7b","remote_model":"remote"}]}`, "", "Cloud-backed"},
		{"missing", `{"models":[]}`, "", "no longer installed"},
		{"truncated", `{"models":[{"name":"alias:7b"}]}`, `{"message":{"content":"partial"}}`, "disconnected"},
		{"server-error", `{"models":[{"name":"alias:7b"}]}`, `{"error":"out of memory"}`, "out of memory"},
	} {
		t.Run(test.name, func(t *testing.T) {
			s, events := chatFixture(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/tags" {
					fmt.Fprint(w, test.tags)
				} else {
					fmt.Fprintln(w, test.stream)
				}
			})
			if err := s.Start("request", "alias:7b", []ChatMessage{{Role: "user", Content: "hi"}}); err != nil {
				t.Fatal(err)
			}
			result := awaitChatDone(t, events)
			if !strings.Contains(result[len(result)-1].Error, test.contains) {
				t.Fatalf("Unexpected error: %+v", result)
			}
		})
	}
}
func TestConversationPersistenceAndTraversalProtection(t *testing.T) {
	s := NewChatService(func(ChatEvent) {})
	s.directory = filepath.Join(t.TempDir(), "conversations")
	entry := Conversation{ID: "thread-1", Title: "Go question", Model: "test:7b", Repository: json.RawMessage(`{"messages":[{"message":{"content":"hello"}}]}`)}
	if _, err := s.Save(entry); err != nil {
		t.Fatal(err)
	}
	entry.Title = "Updated title"
	if _, err := s.Save(entry); err != nil {
		t.Fatal(err)
	}
	items, err := s.Conversations()
	if err != nil || len(items) != 1 || items[0].Title != entry.Title || items[0].Repository != nil {
		t.Fatalf("Unexpected sidebar metadata: %+v, %v", items, err)
	}
	loaded, err := s.Conversation(entry.ID)
	var originalJSON, loadedJSON bytes.Buffer
	_ = json.Compact(&originalJSON, entry.Repository)
	_ = json.Compact(&loadedJSON, loaded.Repository)
	if err != nil || originalJSON.String() != loadedJSON.String() {
		t.Fatalf("Conversation did not round trip: %+v, %v", loaded, err)
	}
	entry.ID = "../outside"
	if _, err := s.Save(entry); err == nil {
		t.Fatal("Path traversal was accepted")
	}
	if _, err := s.Conversation("../outside"); err == nil {
		t.Fatal("Path traversal read was accepted")
	}
}
