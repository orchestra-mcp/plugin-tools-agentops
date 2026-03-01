package toolsagentops

import (
	"github.com/orchestra-mcp/plugin-tools-agentops/internal"
	"github.com/orchestra-mcp/plugin-tools-agentops/internal/store"
	"github.com/orchestra-mcp/sdk-go/plugin"
)

// Register adds all 8 agent ops tools to the builder.
func Register(builder *plugin.PluginBuilder) error {
	accountStore, err := store.NewAccountStore()
	if err != nil {
		return err
	}
	usageTracker, err := store.NewUsageTracker(accountStore)
	if err != nil {
		return err
	}
	ap := &internal.AgentOpsPlugin{
		Store:   accountStore,
		Tracker: usageTracker,
	}
	ap.RegisterTools(builder)
	return nil
}
