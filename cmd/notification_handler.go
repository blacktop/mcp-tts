package cmd

import (
	"context"
	"encoding/json"

	"github.com/charmbracelet/log"
	"github.com/mark3labs/mcp-go/mcp"
)

// HandleCancellationNotification processes incoming cancellation notifications
func HandleCancellationNotification(ctx context.Context, notification mcp.JSONRPCNotification) {
	log.Debug("Received notification", "method", notification.Method)

	if notification.Method != "notifications/cancelled" {
		return
	}

	// Validate cancellation manager is available
	if cancellationManager == nil {
		log.Error("Cancellation manager not initialized")
		return
	}

	// Parse the cancellation notification safely
	var params struct {
		RequestId string `json:"requestId"`
		Reason    string `json:"reason,omitempty"`
	}

	// Safer approach: try to extract params directly if possible
	if notification.Params.AdditionalFields != nil {
		if requestId, exists := notification.Params.AdditionalFields["requestId"]; exists {
			if id, ok := requestId.(string); ok {
				params.RequestId = id
			}
		}
		if reason, exists := notification.Params.AdditionalFields["reason"]; exists {
			if r, ok := reason.(string); ok {
				params.Reason = r
			}
		}
	}

	// Fallback to JSON processing if direct extraction failed
	if params.RequestId == "" {
		paramsJSON, err := json.Marshal(notification.Params)
		if err != nil {
			log.Error("Failed to marshal notification params", "error", err)
			return
		}

		// Limit JSON size to prevent DoS
		if len(paramsJSON) > 4096 {
			log.Warn("Notification params too large, truncating", "size", len(paramsJSON))
			paramsJSON = paramsJSON[:4096]
		}

		if err := json.Unmarshal(paramsJSON, &params); err != nil {
			log.Error("Failed to unmarshal cancellation params", "error", err)
			return
		}
	}

	// Validate inputs
	if params.RequestId == "" {
		log.Warn("Received cancellation notification without requestId")
		return
	}

	// Sanitize inputs
	params.RequestId = sanitizeNotificationRequestID(params.RequestId)
	if len(params.Reason) > 500 {
		params.Reason = params.Reason[:500]
	}

	log.Info("Processing cancellation notification", "requestId", params.RequestId, "reason", params.Reason)

	// Attempt to cancel the request
	cancelled := cancellationManager.Cancel(params.RequestId, params.Reason)
	if cancelled {
		log.Info("Successfully cancelled request", "requestId", params.RequestId)
	} else {
		log.Debug("Request not found or already completed", "requestId", params.RequestId)
	}
}

// sanitizeNotificationRequestID ensures request IDs from notifications are safe
func sanitizeNotificationRequestID(id string) string {
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
		// Don't generate fallback for invalid notification IDs
		return "invalid"
	}
	return result
}

// SetupNotificationHandlers configures notification handlers for the server
func SetupNotificationHandlers(s interface{}) {
	// Note: This is a placeholder for when mcp-go supports notification handlers
	// For now, we'll need to handle notifications at a different level
	log.Debug("Notification handlers would be configured here")
}
