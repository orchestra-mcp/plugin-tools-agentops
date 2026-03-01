// Command tools-agentops is the entry point for the tools.agentops plugin
// binary. It manages AI provider accounts, tracks token usage, and enforces
// budgets. This plugin does NOT need orchestrator storage; it stores data
// locally at ~/.orchestra/agentops/.
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/orchestra-mcp/sdk-go/plugin"
	"github.com/orchestra-mcp/plugin-tools-agentops/internal"
	"github.com/orchestra-mcp/plugin-tools-agentops/internal/store"
)

func main() {
	builder := plugin.New("tools.agentops").
		Version("0.1.0").
		Description("AI account management, token usage tracking, and budget enforcement").
		Author("Orchestra").
		Binary("tools-agentops")

	// Initialize local data stores.
	accountStore, err := store.NewAccountStore()
	if err != nil {
		log.Fatalf("tools.agentops: init account store: %v", err)
	}

	usageTracker, err := store.NewUsageTracker(accountStore)
	if err != nil {
		log.Fatalf("tools.agentops: init usage tracker: %v", err)
	}

	ap := &internal.AgentOpsPlugin{
		Store:   accountStore,
		Tracker: usageTracker,
	}
	ap.RegisterTools(builder)

	p := builder.BuildWithTools()
	p.ParseFlags()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
	}()

	if err := p.Run(ctx); err != nil {
		log.Fatalf("tools.agentops: %v", err)
	}
}
