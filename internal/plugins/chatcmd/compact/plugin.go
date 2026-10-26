package compactplugin

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/wmentor/go-magnetar/internal/plugin"
	"github.com/wmentor/go-magnetar/internal/printer"
)

func init() {
	plugin.Register("chatcmd.compact", &Plugin{})
}

// Plugin registers the /compact chat command.
type Plugin struct{}

func (p *Plugin) Init(_ *plugin.State, hub plugin.Hub) error {
	hub.RegisterChatCommand(plugin.ChatCommand{
		Name: "compact",
		Help: "Compress conversation history via summarizer ([soft|full]).",
		Execute: func(_ context.Context, params *plugin.ChatCommandInput) (*plugin.ChatCommandResponse, error) {
			arg := strings.ToLower(strings.TrimSpace(params.Args))
			switch arg {
			case "", "soft", "full", "compact":
			default:
				fmt.Fprintln(os.Stdout, "Usage: /compact [soft|full]")
				return plugin.NopChatCommandResponse, nil
			}

			before := len(params.AgentHandler.Messages())
			var err error
			if arg == "" {
				err = params.AgentHandler.Compact()
			} else {
				err = params.AgentHandler.Compact(arg)
			}
			if err != nil {
				printer.Error("chat: manual compaction failed", "err", err)
				fmt.Fprintln(os.Stdout, "Error: compaction failed — see logs for details.")
				return plugin.NopChatCommandResponse, nil
			}
			after := len(params.AgentHandler.Messages())
			fmt.Fprintf(os.Stdout, "Context compacted: %d → %d messages.\n\n", before, after)
			return plugin.NopChatCommandResponse, nil
		},
	})
	return nil
}
