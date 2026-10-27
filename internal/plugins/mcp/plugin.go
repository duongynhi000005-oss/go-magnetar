package mcpplugin

import (
	"context"
	"sync"

	"github.com/wmentor/go-magnetar/internal/plugin"
	"github.com/wmentor/go-magnetar/internal/tools/mcp"
)

func init() {
	plugin.Register("mcp", &Plugin{})
}

type Plugin struct {
	mu      sync.Mutex
	state   *plugin.State
	mgr     *mcp.Manager
	enabled bool
}

func (p *Plugin) Init(s *plugin.State, hub plugin.Hub) error {
	p.state = s

	if !s.Config.Bool("mcp.enable") || len(s.Config.MapKeys("mcp.servers")) == 0 {
		p.enabled = false
		return nil
	}

	mgr, err := mcp.NewManager(context.Background(), s.Config)
	if err != nil {
		return err
	}

	p.mu.Lock()
	p.mgr = mgr
	p.enabled = true
	p.mu.Unlock()

	for _, bound := range mgr.Tools() {
		tool := bound
		hub.RegisterTool(plugin.LLMTool{
			Definition: tool.Definition,
			Execute:    tool.Execute,
		})
	}

	hub.Go(func(ctx context.Context) {
		<-ctx.Done()
		_ = mgr.Close()
	})

	return nil
}

func (p *Plugin) IsEnabled() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.enabled
}
