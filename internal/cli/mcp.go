package cli

import (
	"context"

	"github.com/urfave/cli/v3"

	"github.com/vkdprojects/vkd/internal/mcp"
)

func (a *App) mcpCmd() *cli.Command {
	return &cli.Command{
		Name: "mcp", Usage: "serve vkd as an MCP server on stdio",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			eng, err := a.engine(cmd)
			if err != nil {
				return err
			}
			return mcp.Serve(ctx, a.In, a.Out, eng, Version)
		},
	}
}
