package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
)

func TestWorkspaceCLIImportSearchAndExport(t *testing.T) {
	workspace := testWorkspace(t)
	var out bytes.Buffer
	run := func(args ...string) error {
		root := &cobra.Command{SilenceErrors: true, SilenceUsage: true}
		asJSON := true
		addWorkspaceCommands(root, workspace, &asJSON)
		root.SetOut(&out)
		root.SetErr(&out)
		root.SetArgs(args)
		out.Reset()
		return root.Execute()
	}
	file := filepath.Join(t.TempDir(), "reference.txt")
	if err := os.WriteFile(file, []byte("Aurora launch code ORBIT-42"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := run("documents", "import", file); err != nil {
		t.Fatal(err)
	}
	var doc Document
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil || doc.ID == "" {
		t.Fatalf("Import JSON: %s %v", out.String(), err)
	}
	if err := run("documents", "search", "unmatchedword"); err != nil {
		t.Fatal(err)
	}
	var hits []SearchHit
	if err := json.Unmarshal(out.Bytes(), &hits); err != nil || len(hits) != 0 {
		t.Fatalf("Keyword search uses full terms: %+v %v", hits, err)
	}
	if err := run("documents", "search", "ORBIT-42"); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(out.Bytes(), &hits); err != nil || len(hits) != 1 {
		t.Fatalf("Search JSON: %+v %v", hits, err)
	}
	artifact, err := workspace.SaveArtifact(Artifact{Title: "answer", Kind: "markdown", Content: "# Aurora"})
	if err != nil {
		t.Fatal(err)
	}
	exported := filepath.Join(t.TempDir(), "answer.md")
	if err := run("artifacts", "export", artifact.ID, "--output", exported); err != nil {
		t.Fatal(err)
	}
	if err := run("artifacts", "export", artifact.ID, "--output", exported); err == nil {
		t.Fatal("CLI export must not overwrite without --force")
	}
	if err := run("artifacts", "export", artifact.ID, "--output", exported, "--force"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(exported)
	if string(data) != "# Aurora" {
		t.Fatal("Export bytes changed")
	}
}
