package smtpplugin

import (
	"context"
	"sync"

	"github.com/wmentor/go-magnetar/internal/plugin"
	"github.com/wmentor/go-magnetar/internal/tools/smtp"
)

func init() {
	plugin.Register("smtp", &Plugin{})
}

type Plugin struct {
	mu      sync.Mutex
	state   *plugin.State
	tools   *smtp.SMTPTools
	enabled bool
}

func (p *Plugin) Init(s *plugin.State, hub plugin.Hub) error {
	p.state = s

	if !s.Config.Bool("smtp.enable") || s.Config.String("smtp.server.host") == "" {
		p.enabled = false
		return nil
	}

	p.enabled = true
	tools, err := p.get()
	if err != nil {
		return err
	}

	hub.RegisterTool(plugin.LLMTool{
		Definition: smtp.StaticDefinition,
		Execute: func(_ context.Context, args string) (string, error) {
			return tools.Dispatch("send_mail", args), nil
		},
	})

	return nil
}

func (p *Plugin) get() (*smtp.SMTPTools, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.tools != nil {
		return p.tools, nil
	}
	t, err := smtp.New(p.state.Config)
	if err != nil {
		return nil, err
	}
	p.tools = t
	return p.tools, nil
}

func (p *Plugin) IsEnabled() bool {
	return p.enabled
}
