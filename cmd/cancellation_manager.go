package cmd

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/charmbracelet/log"
)

const (
	// Maximum number of concurrent requests to track (prevent memory exhaustion)
	MaxConcurrentRequests = 1000
	// Default cleanup timeout
	DefaultCleanupTimeout = 30 * time.Minute
)

// CancellationManager handles manual cancellation for MCP requests
type CancellationManager struct {
	mu          sync.RWMutex
	cancellable map[string]context.CancelFunc // requestID -> cancel function
	timeouts    map[string]*time.Timer        // requestID -> cleanup timer
	maxRequests int
}

// NewCancellationManager creates a new cancellation manager
func NewCancellationManager() *CancellationManager {
	return &CancellationManager{
		cancellable: make(map[string]context.CancelFunc),
		timeouts:    make(map[string]*time.Timer),
		maxRequests: MaxConcurrentRequests,
	}
}

// RegisterCancellable registers a context for potential cancellation
func (cm *CancellationManager) RegisterCancellable(requestID string, cancelFunc context.CancelFunc) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	// Check capacity limit to prevent resource exhaustion
	if len(cm.cancellable) >= cm.maxRequests {
		log.Warn("Maximum concurrent requests reached, rejecting new request",
			"current", len(cm.cancellable), "max", cm.maxRequests)
		return ErrTooManyRequests
	}

	// Validate request ID
	if len(requestID) > 256 {
		log.Warn("Request ID too long, truncating", "length", len(requestID))
		requestID = requestID[:256]
	}

	// Clean up existing registration if it exists (prevent timer leak)
	if existingTimer, exists := cm.timeouts[requestID]; exists {
		existingTimer.Stop()
		log.Debug("Cleaned up existing timer for request", "requestID", requestID)
	}

	cm.cancellable[requestID] = cancelFunc

	// Set up automatic cleanup after timeout
	timer := time.AfterFunc(DefaultCleanupTimeout, func() {
		cm.cleanup(requestID)
	})
	cm.timeouts[requestID] = timer

	log.Debug("Registered cancellable request", "requestID", requestID)
	return nil
}

// Cancel cancels a request by ID
func (cm *CancellationManager) Cancel(requestID string, reason string) bool {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	// Validate inputs
	if requestID == "" {
		log.Warn("Empty request ID provided for cancellation")
		return false
	}
	if len(reason) > 500 {
		reason = reason[:500] // Truncate long reasons
	}

	cancelFunc, exists := cm.cancellable[requestID]
	if !exists {
		log.Debug("Cancellation requested for unknown request", "requestID", requestID, "reason", reason)
		return false
	}

	log.Info("Cancelling request", "requestID", requestID, "reason", reason)
	cancelFunc()
	cm.cleanupLocked(requestID)
	return true
}

// Complete marks a request as completed (removes from cancellable list)
func (cm *CancellationManager) Complete(requestID string) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.cleanupLocked(requestID)
}

// cleanup removes a request from tracking (thread-safe)
func (cm *CancellationManager) cleanup(requestID string) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.cleanupLocked(requestID)
}

// cleanupLocked removes a request from tracking (must hold lock)
func (cm *CancellationManager) cleanupLocked(requestID string) {
	delete(cm.cancellable, requestID)
	if timer, exists := cm.timeouts[requestID]; exists {
		timer.Stop()
		delete(cm.timeouts, requestID)
	}
	log.Debug("Cleaned up request tracking", "requestID", requestID)
}

// ActiveRequests returns the number of currently tracked requests
func (cm *CancellationManager) ActiveRequests() int {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	return len(cm.cancellable)
}

// Shutdown cleanly shuts down the cancellation manager
func (cm *CancellationManager) Shutdown() {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	// Stop all timers and cancel all requests
	for requestID, timer := range cm.timeouts {
		timer.Stop()
		if cancelFunc, exists := cm.cancellable[requestID]; exists {
			cancelFunc()
		}
	}

	// Clear maps
	cm.cancellable = make(map[string]context.CancelFunc)
	cm.timeouts = make(map[string]*time.Timer)

	log.Info("Cancellation manager shutdown completed")
}

// Custom errors
var ErrTooManyRequests = fmt.Errorf("too many concurrent requests")
