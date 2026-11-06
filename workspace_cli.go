package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

func addWorkspaceCommands(root *cobra.Command, workspace *WorkspaceService, asJSON *bool) {
	output := func(cmd *cobra.Command, value any) error {
		encoder := json.NewEncoder(cmd.OutOrStdout())
		encoder.SetIndent("", "  ")
		return encoder.Encode(value)
	}
	documents := &cobra.Command{Use: "documents", Short: "Import and search local documents"}
	documents.AddCommand(&cobra.Command{Use: "list", Short: "List indexed documents", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		items, err := workspace.Documents()
		if err != nil {
			return err
		}
		if *asJSON {
			return output(cmd, items)
		}
		for _, doc := range items {
			fmt.Fprintf(cmd.OutOrStdout(), "%s  %s (%d bytes)\n", doc.ID, doc.Name, doc.Size)
		}
		return nil
	}})
	documents.AddCommand(&cobra.Command{Use: "import FILE", Short: "Import a text/code, PDF, or Word file", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		file, err := os.Open(args[0])
		if err != nil {
			return err
		}
		defer file.Close()
		data, err := io.ReadAll(io.LimitReader(file, maxAttachmentBytes+1))
		if err != nil {
			return err
		}
		doc, err := workspace.Import(filepath.Base(args[0]), base64.StdEncoding.EncodeToString(data))
		if err != nil {
			return err
		}
		if *asJSON {
			return output(cmd, doc)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Imported %s (%s)\n", doc.Name, doc.ID)
		return nil
	}})
	documents.AddCommand(&cobra.Command{Use: "search QUERY", Short: "Search indexed text without Ollama", Args: cobra.MinimumNArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		hits, err := workspace.Search(strings.Join(args, " "), nil)
		if err != nil {
			return err
		}
		if *asJSON {
			return output(cmd, hits)
		}
		for _, hit := range hits {
			fmt.Fprintf(cmd.OutOrStdout(), "%s — excerpt %d (%.2f)\n%s\n\n", hit.Name, hit.Chunk, hit.Score, hit.Text)
		}
		if len(hits) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "No matching excerpts.")
		}
		return nil
	}})
	root.AddCommand(documents)
	artifacts := &cobra.Command{Use: "artifacts", Short: "List and export saved artifacts"}
	artifacts.AddCommand(&cobra.Command{Use: "list", Short: "List saved artifacts", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		items, err := workspace.Artifacts()
		if err != nil {
			return err
		}
		if *asJSON {
			return output(cmd, items)
		}
		for _, item := range items {
			fmt.Fprintf(cmd.OutOrStdout(), "%s  %s [%s]\n", item.ID, item.Title, item.Kind)
		}
		return nil
	}})
	var format, destination string
	var force bool
	export := &cobra.Command{Use: "export ID", Short: "Export an artifact to a chosen file", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		file, err := workspace.ExportData(args[0], format)
		if err != nil {
			return err
		}
		data, err := base64.StdEncoding.DecodeString(file.Data)
		if err != nil {
			return err
		}
		flags := os.O_WRONLY | os.O_CREATE | os.O_EXCL
		if force {
			flags = os.O_WRONLY | os.O_CREATE | os.O_TRUNC
		}
		target, err := os.OpenFile(destination, flags, 0600)
		if err != nil {
			return err
		}
		_, writeErr := target.Write(data)
		closeErr := target.Close()
		if writeErr != nil {
			return writeErr
		}
		if closeErr != nil {
			return closeErr
		}
		if *asJSON {
			return output(cmd, map[string]string{"path": destination, "name": file.Name})
		}
		fmt.Fprintln(cmd.OutOrStdout(), destination)
		return nil
	}}
	export.Flags().StringVar(&format, "format", "source", "source, text, or html")
	export.Flags().StringVar(&destination, "output", "", "Output file path (required)")
	export.Flags().BoolVar(&force, "force", false, "Overwrite an existing output file")
	_ = export.MarkFlagRequired("output")
	artifacts.AddCommand(export)
	root.AddCommand(artifacts)
	var prompt string
	var models []string
	compare := &cobra.Command{Use: "compare", Short: "Compare 2–4 local models sequentially and save results", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
		defer stop()
		random := make([]byte, 16)
		if _, err := rand.Read(random); err != nil {
			return err
		}
		id := hex.EncodeToString(random)
		done := make(chan struct{}, 1)
		service := &ComparisonService{chat: NewChatService(func(ChatEvent) {}), workspace: workspace, emit: func(event ComparisonEvent) {
			if event.Complete {
				done <- struct{}{}
			}
		}}
		if err := service.Start(id, prompt, models); err != nil {
			return err
		}
		select {
		case <-done:
		case <-ctx.Done():
			service.Cancel(id)
			<-done
			return context.Canceled
		}
		hash := sha256.Sum256([]byte(id))
		record, err := service.Get(hex.EncodeToString(hash[:16]))
		if err != nil {
			return err
		}
		failed := false
		for _, result := range record.Results {
			if result.Error != "" {
				failed = true
			}
		}
		if *asJSON {
			if err := output(cmd, record); err != nil {
				return err
			}
		} else {
			for _, result := range record.Results {
				fmt.Fprintf(cmd.OutOrStdout(), "%s: %s tok/s, %.2fs total\n%s\n%s\n\n", result.Model, formatSpeed(result.TokensPerSecond, result.RateBasis), result.Seconds, result.Response, result.Error)
			}
		}
		if failed {
			return errors.New("One or more models failed; see the saved comparison results")
		}
		return nil
	}}
	compare.Flags().StringVar(&prompt, "prompt", "", "Prompt sent identically to every model")
	compare.Flags().StringSliceVar(&models, "models", nil, "Comma-separated local model names")
	_ = compare.MarkFlagRequired("prompt")
	_ = compare.MarkFlagRequired("models")
	root.AddCommand(compare)
}
