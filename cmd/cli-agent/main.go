package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/mdzunic/cli-agent/internal/agent"
	"github.com/mdzunic/cli-agent/internal/config"
	"github.com/mdzunic/cli-agent/internal/ollama"
	"github.com/mdzunic/cli-agent/internal/tools"
	"github.com/mdzunic/cli-agent/internal/tui"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: config load failed: %v\n", err)
		cfg = config.Default()
	}

	ollamaURL := flag.String("ollama", cfg.OllamaURL, "Ollama base URL")
	model := flag.String("model", cfg.Model, "Model to use (default: prompt on startup)")
	headless := flag.Bool("headless", false, "Run without TUI, read prompt from --prompt flag")
	prompt := flag.String("prompt", "", "Prompt to send in headless mode")
	verbose := flag.Bool("v", false, "Verbose logging")
	flag.Parse()

	logLevel := slog.LevelWarn
	if *verbose {
		logLevel = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logLevel})))

	client := ollama.NewClient(*ollamaURL)

	// Check Ollama is reachable.
	if err := client.Ping(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\nMake sure Ollama is running: ollama serve\n", err)
		os.Exit(1)
	}

	registry := tools.DefaultRegistry()
	ag := agent.New(client, registry, *model, nil)

	if *headless {
		runHeadless(client, ag, *model, *prompt)
		return
	}

	if err := tui.Run(client, ag, cfg); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}

func runHeadless(client *ollama.Client, ag *agent.Agent, model, prompt string) {
	if prompt == "" {
		fmt.Fprintln(os.Stderr, "Error: --prompt is required in headless mode")
		os.Exit(1)
	}

	// Auto-select first model if none specified.
	if model == "" {
		models, err := client.ListModels(context.Background())
		if err != nil || len(models) == 0 {
			fmt.Fprintln(os.Stderr, "Error: could not list models:", err)
			os.Exit(1)
		}
		model = models[0].Name
		fmt.Fprintf(os.Stderr, "Using model: %s\n", model)
		ag.SetModel(model)
	}

	tokenCb := func(token string) {
		fmt.Print(token)
	}

	toolEvent := func(name, args, result string) {
		fmt.Fprintf(os.Stderr, "\n[tool: %s]\nargs: %s\nresult: %s\n\n", name, args, truncate(result, 200))
	}

	approval := func(toolName, args string) bool {
		fmt.Fprintf(os.Stderr, "\nTool '%s' needs approval.\nArgs: %s\nAllow? [y/N]: ", toolName, args)
		var answer string
		fmt.Scanln(&answer)
		return strings.ToLower(strings.TrimSpace(answer)) == "y"
	}
	ag.SetApproval(approval)

	if err := ag.Send(context.Background(), prompt, tokenCb, toolEvent); err != nil {
		fmt.Fprintln(os.Stderr, "\nError:", err)
		os.Exit(1)
	}
	fmt.Println()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
