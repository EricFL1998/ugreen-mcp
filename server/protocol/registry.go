package protocol

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
)

type ToolHandler func(ctx context.Context, args map[string]interface{}) (interface{}, error)

type ToolEntry struct {
	Tool    Tool
	Handler ToolHandler
}

type ToolRegistry struct {
	mu    sync.RWMutex
	tools map[string]*ToolEntry
}

var (
	defaultRegistry *ToolRegistry
	registryOnce    sync.Once
)

func GetRegistry() *ToolRegistry {
	registryOnce.Do(func() {
		defaultRegistry = &ToolRegistry{
			tools: make(map[string]*ToolEntry),
		}
	})
	return defaultRegistry
}

func (r *ToolRegistry) Register(tool Tool, handler ToolHandler) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools[tool.Name] = &ToolEntry{
		Tool:    tool,
		Handler: handler,
	}
}

func (r *ToolRegistry) ListTools() []Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	list := make([]Tool, 0, len(r.tools))
	for _, entry := range r.tools {
		list = append(list, entry.Tool)
	}
	return list
}

func (r *ToolRegistry) CallTool(ctx context.Context, name string, rawArgs json.RawMessage) (*ToolCallResult, error) {
	r.mu.RLock()
	entry, exists := r.tools[name]
	r.mu.RUnlock()

	if !exists {
		return &ToolCallResult{
			Content: []ContentItem{{Type: "text", Text: fmt.Sprintf("Tool %s not found", name)}},
			IsError: true,
		}, fmt.Errorf("tool not found: %s", name)
	}

	args := make(map[string]interface{})
	if len(rawArgs) > 0 {
		if err := json.Unmarshal(rawArgs, &args); err != nil {
			return &ToolCallResult{
				Content: []ContentItem{{Type: "text", Text: fmt.Sprintf("Invalid arguments: %v", err)}},
				IsError: true,
			}, err
		}
	}

	res, err := entry.Handler(ctx, args)
	if err != nil {
		return &ToolCallResult{
			Content: []ContentItem{{Type: "text", Text: fmt.Sprintf("Error: %v", err)}},
			IsError: true,
		}, nil
	}

	jsonBytes, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		return &ToolCallResult{
			Content: []ContentItem{{Type: "text", Text: fmt.Sprintf("%v", res)}},
		}, nil
	}

	return &ToolCallResult{
		Content: []ContentItem{{Type: "text", Text: string(jsonBytes)}},
	}, nil
}
