package main

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func testWorkspace(t *testing.T) *WorkspaceService {
	t.Helper()
	return &WorkspaceService{directory: t.TempDir()}
}
func importText(t *testing.T, s *WorkspaceService, name, text string) Document {
	t.Helper()
	doc, err := s.Import(name, base64.StdEncoding.EncodeToString([]byte(text)))
	if err != nil {
		t.Fatal(err)
	}
	return doc
}
func TestDocumentImportSearchAndGroundedContext(t *testing.T) {
	s := testWorkspace(t)
	doc := importText(t, s, "architecture.md", "The aurora project uses Go and Wails. Deployment region is Kuala Lumpur.")
	_ = importText(t, s, "unrelated.txt", "A recipe for sourdough bread.")
	hits, err := s.Search("aurora deployment", nil)
	if err != nil || len(hits) != 1 || hits[0].DocumentID != doc.ID {
		t.Fatalf("Unexpected search results: %+v, %v", hits, err)
	}
	if hits, err := s.Search("nonexistentword", nil); err != nil || len(hits) != 0 {
		t.Fatalf("Expected no matches: %+v, %v", hits, err)
	}
	messages, sources, err := s.contextFor([]ChatMessage{{Role: "user", Content: "Summarize this", DocumentIDs: []string{doc.ID}}}, false)
	if err != nil || len(sources) != 1 || sources[0].Label != "D1" || !strings.Contains(messages[0].Content, "Kuala Lumpur") {
		t.Fatalf("Missing attachment context: %+v %+v %v", messages, sources, err)
	}
	metadata, err := s.Documents()
	if err != nil || len(metadata) != 2 || metadata[0].Text != "" {
		t.Fatalf("Unexpected document metadata: %+v %v", metadata, err)
	}
	loaded, err := s.Document(doc.ID)
	if err != nil || loaded.Text == "" {
		t.Fatalf("Cannot reopen document: %+v %v", loaded, err)
	}
	if _, err := s.Document("../escape"); err == nil {
		t.Fatal("Path traversal was accepted")
	}
	if _, err := s.Import("photo.png", base64.StdEncoding.EncodeToString([]byte("binary"))); err == nil {
		t.Fatal("Unsupported file accepted")
	}
	if _, err := s.Import("binary.txt", base64.StdEncoding.EncodeToString([]byte{0, 1, 2})); err == nil {
		t.Fatal("Binary text accepted")
	}
}
func TestDocxTextExtraction(t *testing.T) {
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	file, err := writer.Create("word/document.xml")
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprint(file, `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>Hello &amp; welcome</w:t></w:r></w:p><w:p><w:r><w:t>Second paragraph</w:t></w:r></w:p></w:body></w:document>`)
	writer.Close()
	text, kind, err := extractDocument("notes.docx", archive.Bytes())
	if err != nil || kind != "docx" || text != "Hello & welcome\nSecond paragraph\n" {
		t.Fatalf("Unexpected Word extraction: %q %s %v", text, kind, err)
	}
}
func TestPDFTextExtraction(t *testing.T) {
	// A real minimal PDF with text, cross-reference offsets and a Helvetica font.
	objects := []string{"<< /Type /Catalog /Pages 2 0 R >>", "<< /Type /Pages /Kids [3 0 R] /Count 1 >>", "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 4 0 R >> >> /Contents 5 0 R >>", "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>"}
	stream := "BT /F1 12 Tf 72 720 Td (Aurora PDF reference) Tj ET"
	objects = append(objects, fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(stream), stream))
	var data bytes.Buffer
	data.WriteString("%PDF-1.4\n")
	offsets := []int{0}
	for index, object := range objects {
		offsets = append(offsets, data.Len())
		fmt.Fprintf(&data, "%d 0 obj\n%s\nendobj\n", index+1, object)
	}
	xref := data.Len()
	fmt.Fprintf(&data, "xref\n0 6\n0000000000 65535 f \n")
	for _, offset := range offsets[1:] {
		fmt.Fprintf(&data, "%010d 00000 n \n", offset)
	}
	fmt.Fprintf(&data, "trailer\n<< /Size 6 /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF", xref)
	text, _, err := extractDocument("reference.pdf", data.Bytes())
	if err != nil || !strings.Contains(text, "Aurora PDF reference") {
		t.Fatalf("PDF extraction failed: %q, %v", text, err)
	}
}
func TestArtifactPreviewExportAndSaveCancellation(t *testing.T) {
	s := testWorkspace(t)
	artifact, err := s.SaveArtifact(Artifact{Title: "Go example", Kind: "code", Language: "go", Content: "package main\n"})
	if err != nil {
		t.Fatal(err)
	}
	file, err := s.ExportData(artifact.ID, "source")
	if err != nil || file.Name != "Go example.go" {
		t.Fatalf("Wrong export: %+v %v", file, err)
	}
	decoded, _ := base64.StdEncoding.DecodeString(file.Data)
	if string(decoded) != artifact.Content {
		t.Fatal("Export changed source bytes")
	}
	artifact.Content = "<script>alert(1)</script>"
	artifact.Kind = "text"
	if _, err := s.SaveArtifact(artifact); err != nil {
		t.Fatal(err)
	}
	file, err = s.ExportData(artifact.ID, "html")
	decoded, _ = base64.StdEncoding.DecodeString(file.Data)
	if err != nil || strings.Contains(string(decoded), "<script>") || !strings.Contains(string(decoded), "&lt;script&gt;") {
		t.Fatalf("HTML export did not escape content: %s %v", decoded, err)
	}
	s.chooseExport = func(string) (string, error) { return "", nil }
	if path, err := s.Export(artifact.ID, "source"); err != nil || path != "" {
		t.Fatalf("Cancelled save is not a failure: %s %v", path, err)
	}
	destination := filepath.Join(t.TempDir(), "export.txt")
	s.chooseExport = func(string) (string, error) { return destination, nil }
	if _, err := s.Export(artifact.ID, "source"); err != nil {
		t.Fatal(err)
	}
	written, _ := os.ReadFile(destination)
	if string(written) != artifact.Content {
		t.Fatal("Native export bytes differ")
	}
	if _, err := s.SaveArtifact(Artifact{Kind: "json", Content: "{broken}"}); err == nil {
		t.Fatal("Invalid JSON accepted")
	}
	if _, err := s.ExportData("../escape", "source"); err == nil {
		t.Fatal("Artifact traversal accepted")
	}
}
func TestComparisonRunsSequentiallyAndPersists(t *testing.T) {
	var active, maximum atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/tags" {
			fmt.Fprint(w, `{"models":[{"name":"local:a"},{"name":"local:b"}]}`)
			return
		}
		count := active.Add(1)
		defer active.Add(-1)
		if count > maximum.Load() {
			maximum.Store(count)
		}
		var request struct {
			Model    string        `json:"model"`
			Messages []ChatMessage `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		if request.Messages[0].Content != "same prompt" {
			t.Error("Models received different prompts")
		}
		fmt.Fprintln(w, `{"message":{"content":"answer"}}`)
		fmt.Fprintln(w, `{"done":true,"eval_count":20,"eval_duration":1000000000}`)
	}))
	defer server.Close()
	chat := NewChatService(func(ChatEvent) {})
	chat.client = server.Client()
	chat.baseURL = server.URL
	events := make(chan ComparisonEvent, 30)
	service := &ComparisonService{chat: chat, workspace: testWorkspace(t), emit: func(event ComparisonEvent) { events <- event }}
	if err := service.Start("comparison-test", "same prompt", []string{"local:a", "local:b"}); err != nil {
		t.Fatal(err)
	}
	if err := chat.Start("blocked", "local:a", []ChatMessage{{Role: "user", Content: "hi"}}); err == nil {
		t.Fatal("Chat must not overlap a comparison")
	}
	for {
		select {
		case event := <-events:
			if event.Complete {
				goto complete
			}
		case <-time.After(3 * time.Second):
			t.Fatal("Comparison did not finish")
		}
	}
complete:
	if maximum.Load() != 1 {
		t.Fatal("Comparison loaded models concurrently")
	}
	items, err := service.History()
	if err != nil || len(items) != 1 {
		t.Fatalf("Comparison was not saved: %+v %v", items, err)
	}
	saved, err := service.Get(items[0].ID)
	if err != nil || len(saved.Results) != 2 || saved.Results[1].Response != "answer" || saved.Results[0].TokensPerSecond != 20 {
		t.Fatalf("Wrong comparison results: %+v %v", saved, err)
	}
}

func TestComparisonStopCancelsActiveRequestAndSavesPartialResults(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/tags" {
			fmt.Fprint(w, `{"models":[{"name":"local:a"},{"name":"local:b"}]}`)
			return
		}
		fmt.Fprintln(w, `{"message":{"content":"partial"}}`)
		w.(http.Flusher).Flush()
		close(started)
		<-r.Context().Done()
	}))
	defer server.Close()
	chat := NewChatService(func(ChatEvent) {})
	chat.client = server.Client()
	chat.baseURL = server.URL
	events := make(chan ComparisonEvent, 30)
	service := &ComparisonService{chat: chat, workspace: testWorkspace(t), emit: func(event ComparisonEvent) { events <- event }}
	if err := service.Start("stop-test", "same prompt", []string{"local:a", "local:b"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("Comparison did not start")
	}
	service.Cancel("stop-test")
	for {
		select {
		case event := <-events:
			if event.Complete {
				if !event.Cancelled {
					t.Fatal("Stop was not reported")
				}
				goto complete
			}
		case <-time.After(3 * time.Second):
			t.Fatal("Comparison did not stop")
		}
	}
complete:
	items, err := service.History()
	if err != nil || len(items) != 1 {
		t.Fatalf("Partial comparison not saved: %+v %v", items, err)
	}
	saved, err := service.Get(items[0].ID)
	if err != nil || len(saved.Results) != 1 || saved.Results[0].Error != "Stopped" {
		t.Fatalf("Unexpected partial record: %+v %v", saved, err)
	}
	chat.mu.Lock()
	defer chat.mu.Unlock()
	if chat.cancel != nil {
		t.Fatal("Generation gate remained locked after stop")
	}
}
