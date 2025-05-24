package cmd

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/charmbracelet/log"
	"github.com/mark3labs/mcp-go/mcp"
)

// ToolHandlerFunc is the signature for tool handlers
type ToolHandlerFunc func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error)

// WithCancellation wraps a tool handler to support cancellation
func WithCancellation(handler ToolHandlerFunc) func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		// Try to extract request ID from JSON-RPC context or generate one
		requestID := extractOrGenerateRequestID(ctx, request)

		// Create cancellable context
		cancellableCtx, cancel := context.WithCancel(ctx)
		defer cancel() // Ensure cleanup

		// Register for cancellation
		if cancellationManager == nil {
			log.Error("Cancellation manager not initialized")
			return handler(ctx, request) // Fallback to original handler
		}

		if err := cancellationManager.RegisterCancellable(requestID, cancel); err != nil {
			log.Warn("Failed to register request for cancellation", "error", err, "requestID", requestID)
			return handler(ctx, request) // Fallback to original handler
		}
		defer cancellationManager.Complete(requestID)

		log.Debug("Starting tool execution", "tool", request.Params.Name, "requestID", requestID)

		// Execute the original handler with cancellable context
		result, err := handler(cancellableCtx, request)

		if err != nil {
			log.Debug("Tool execution failed", "tool", request.Params.Name, "requestID", requestID, "error", err)
		} else {
			log.Debug("Tool execution completed", "tool", request.Params.Name, "requestID", requestID)
		}

		return result, err
	}
}

// extractOrGenerateRequestID tries to extract request ID or generates a tracking ID
func extractOrGenerateRequestID(ctx context.Context, request mcp.CallToolRequest) string {
	// Try to get request ID from context (if available)
	// This might be set by a custom transport or middleware
	if requestID := ctx.Value("mcp_request_id"); requestID != nil {
		if id, ok := requestID.(string); ok {
			return sanitizeRequestID(id)
		}
		// Handle numeric IDs
		if id, ok := requestID.(int); ok {
			return strconv.Itoa(id)
		}
		if id, ok := requestID.(int64); ok {
			return strconv.FormatInt(id, 10)
		}
	}

	// Try to extract from Meta if present
	if request.Params.Meta != nil {
		if token := request.Params.Meta.ProgressToken; token != nil {
			if tokenStr, ok := token.(string); ok {
				return sanitizeRequestID(tokenStr)
			}
		}
	}

	// Generate a tracking ID based on tool name and timestamp
	timestamp := time.Now().UnixNano()
	toolName := sanitizeToolName(request.Params.Name)
	return fmt.Sprintf("%s-%d", toolName, timestamp)
}

// sanitizeRequestID ensures request IDs are safe for logging and storage
func sanitizeRequestID(id string) string {
	// Limit length to prevent memory issues
	if len(id) > 128 {
		id = id[:128]
	}

	// Remove potentially dangerous characters
	safe := make([]rune, 0, len(id))
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '-' || r == '_' {
			safe = append(safe, r)
		}
	}

	result := string(safe)
	if result == "" {
		// Fallback if all characters were stripped
		return fmt.Sprintf("safe-%d", time.Now().UnixNano())
	}
	return result
}

// sanitizeToolName ensures tool names are safe for use in request IDs
func sanitizeToolName(name string) string {
	// Limit length
	if len(name) > 64 {
		name = name[:64]
	}

	// Only allow alphanumeric and underscore
	safe := make([]rune, 0, len(name))
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '_' {
			safe = append(safe, r)
		}
	}

	result := string(safe)
	if result == "" {
		return "unknown"
	}
	return result
}

// CancelRequest manually cancels a request by ID
func CancelRequest(requestID string, reason string) bool {
	if cancellationManager == nil {
		log.Warn("Cancellation manager not initialized")
		return false
	}
	return cancellationManager.Cancel(requestID, reason)
}
