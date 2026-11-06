package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

// Both interfaces share this service contract and the same persisted history.
type monitorAPI interface {
	Snapshot() Snapshot
	Benchmark(string, string) (BenchmarkResult, error)
	History() ([]BenchmarkResult, error)
}

func newRootCommand(service monitorAPI, desktop func() error) *cobra.Command {
	var asJSON bool
	root := &cobra.Command{
		Use: "local-ai", Short: "Local AI chat, documents, artifacts, model comparison and GPU tools",
		Long: "Local AI Dashboard: document search, artifact export, model comparison and GPU telemetry.\nRun without a command (or use gui) to open the desktop workspace.",
		Args: cobra.NoArgs, SilenceErrors: true, SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error { return desktop() },
	}
	root.PersistentFlags().BoolVar(&asJSON, "json", false, "Print command results as JSON")
	addWorkspaceCommands(root, NewWorkspaceService(), &asJSON)
	root.AddCommand(&cobra.Command{Use: "gui", Short: "Open the desktop dashboard", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error { return desktop() }})
	writeJSON := func(cmd *cobra.Command, value any) error {
		encoder := json.NewEncoder(cmd.OutOrStdout())
		encoder.SetIndent("", "  ")
		return encoder.Encode(value)
	}
	for _, name := range []string{"status", "gpu", "models"} {
		root.AddCommand(&cobra.Command{Use: name, Short: map[string]string{"status": "Show GPU and Ollama status", "gpu": "List NVIDIA GPU metrics", "models": "List installed Ollama models"}[name], Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, args []string) error {
				state := service.Snapshot()
				var runtimeErr error
				if (name == "gpu" || name == "status" && !state.InferenceOnline) && state.GPUError != "" {
					runtimeErr = errors.New(state.GPUError)
				}
				if name != "gpu" && !state.InferenceOnline && !state.OllamaOnline && state.OllamaError != "" {
					runtimeErr = errors.Join(runtimeErr, errors.New(state.OllamaError))
				}
				if asJSON {
					var value any = state
					if name == "gpu" {
						value = state.GPUs
					}
					if name == "models" {
						value = state.Models
					}
					if err := writeJSON(cmd, value); err != nil {
						return err
					}
					return runtimeErr
				}
				w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
				if name != "models" {
					fmt.Fprintln(w, "GPU\tVRAM USED MiB\tVRAM TOTAL MiB\tUTIL %\tTEMP C\tPOWER W")
					for _, gpu := range state.GPUs {
						fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", gpu.Name, formatMetric(gpu.MemoryUsed), formatMetric(gpu.MemoryTotal), formatMetric(gpu.Utilization), formatMetric(gpu.Temperature), formatMetric(gpu.Power))
					}
					if len(state.GPUs) == 0 {
						fmt.Fprintln(w, "No NVIDIA GPUs detected.")
					}
				}
				if name != "gpu" {
					fmt.Fprintf(w, "Ollama online:\t%t\n", state.OllamaOnline)
					for _, provider := range state.Providers {
						if provider.ID != "ollama" {
							fmt.Fprintf(w, "%s online:\t%t\n", provider.Name, provider.Online)
						}
					}
					fmt.Fprintln(w, "MODEL\tPARAMETERS\tQUANTIZATION\tDISK GiB\tSTATUS")
					loaded := map[string]bool{}
					for _, model := range state.Running {
						loaded[model.Name] = true
					}
					for _, model := range state.Models {
						status := "on disk"
						if loaded[model.Name] {
							status = "loaded"
						}
						size := fmt.Sprintf("%.2f", float64(model.Size)/(1<<30))
						if model.Provider == "fastflowlm" {
							status = "available"
							if model.Size == 0 {
								size = "N/A"
							}
						}
						fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", model.Name, model.Details.ParameterSize, model.Details.Quantization, size, status)
					}
					if len(state.Models) == 0 {
						fmt.Fprintln(w, "No models available.")
					}
				}
				if err := w.Flush(); err != nil {
					return err
				}
				return runtimeErr
			}})
	}
	var model, prompt string
	benchmark := &cobra.Command{Use: "benchmark", Short: "Run a local generation benchmark and save its result", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			result, err := service.Benchmark(model, prompt)
			if err != nil {
				return err
			}
			if result.SaveWarning != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), result.SaveWarning)
			}
			if asJSON {
				return writeJSON(cmd, result)
			}
			load := "N/A"
			if result.LoadSeconds >= 0 {
				load = fmt.Sprintf("%.2fs", result.LoadSeconds)
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "%s: %s tokens/s | %d tokens | total %.2fs | load %s\n\n%s\n", result.Model, formatSpeed(result.TokensPerSecond, result.RateBasis), result.Tokens, result.TotalSeconds, load, result.Response)
			return err
		}}
	benchmark.Flags().StringVar(&model, "model", "", "Installed local model name (required)")
	benchmark.Flags().StringVar(&prompt, "prompt", "Write a Go function that checks whether a string is a palindrome. Include two test cases.", "Benchmark prompt")
	if err := benchmark.MarkFlagRequired("model"); err != nil {
		panic(err)
	}
	root.AddCommand(benchmark)
	root.AddCommand(&cobra.Command{Use: "history", Short: "Show saved benchmark results", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			runs, err := service.History()
			if err != nil {
				return err
			}
			if asJSON {
				return writeJSON(cmd, runs)
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
			fmt.Fprintln(w, "WHEN\tMODEL\tTOKENS/S\tTOKENS\tTOTAL S")
			for _, run := range runs {
				fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%.2f\n", run.Timestamp, run.Model, formatSpeed(run.TokensPerSecond, run.RateBasis), run.Tokens, run.TotalSeconds)
			}
			if len(runs) == 0 {
				fmt.Fprintln(w, "No benchmark history yet.")
			}
			return w.Flush()
		}})
	return root
}

func formatMetric(value *float64) string {
	if value == nil {
		return "N/A"
	}
	return fmt.Sprintf("%.1f", *value)
}

func formatSpeed(value float64, basis string) string {
	if value <= 0 {
		return "N/A"
	}
	prefix := ""
	if basis == "estimated" {
		prefix = "~"
	}
	return fmt.Sprintf("%s%.2f", prefix, value)
}
