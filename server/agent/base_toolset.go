package agent

import (
	"fmt"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/tool"

	toolsartifacts "github.com/achetronic/magec/server/agent/tools/artifacts"
)

type baseToolset struct {
	tools         []tool.Tool
	artifactTools *toolsartifacts.Toolset
}

func newBaseToolset() (*baseToolset, error) {
	artifactTs, err := toolsartifacts.NewToolset()
	if err != nil {
		return nil, fmt.Errorf("failed to create artifact toolset: %w", err)
	}

	return &baseToolset{
		tools:         []tool.Tool{},
		artifactTools: artifactTs,
	}, nil
}

func (b *baseToolset) Name() string {
	return "base_toolset"
}

func (b *baseToolset) Tools(ctx agent.ReadonlyContext) ([]tool.Tool, error) {
	artTools, err := b.artifactTools.Tools(ctx)
	if err != nil {
		return b.tools, nil
	}
	all := make([]tool.Tool, 0, len(b.tools)+len(artTools))
	all = append(all, b.tools...)
	all = append(all, artTools...)
	return all, nil
}
