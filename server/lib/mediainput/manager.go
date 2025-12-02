package mediainput

import (
	"context"
	"fmt"
	"sync"

	"github.com/onkernel/kernel-images/server/lib/logger"
)

// SimpleMediaInputManager is a simple implementation of MediaInputManager
type SimpleMediaInputManager struct {
	mu     sync.RWMutex
	inputs map[string]MediaInput
}

// NewSimpleMediaInputManager creates a new simple media input manager
func NewSimpleMediaInputManager() *SimpleMediaInputManager {
	return &SimpleMediaInputManager{
		inputs: make(map[string]MediaInput),
	}
}

func (m *SimpleMediaInputManager) GetMediaInput(id string) (MediaInput, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	input, ok := m.inputs[id]
	return input, ok
}

func (m *SimpleMediaInputManager) ListActiveInputs(ctx context.Context) []MediaInput {
	m.mu.RLock()
	defer m.mu.RUnlock()

	inputs := make([]MediaInput, 0, len(m.inputs))
	for _, input := range m.inputs {
		inputs = append(inputs, input)
	}

	return inputs
}

func (m *SimpleMediaInputManager) RegisterMediaInput(ctx context.Context, input MediaInput) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.inputs[input.ID()]; exists {
		return fmt.Errorf("media input with ID %s already exists", input.ID())
	}

	logger.Info("Registering media input: %s", input.ID())
	m.inputs[input.ID()] = input

	return nil
}

func (m *SimpleMediaInputManager) DeregisterMediaInput(ctx context.Context, input MediaInput) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	id := input.ID()
	if _, exists := m.inputs[id]; !exists {
		return fmt.Errorf("media input with ID %s not found", id)
	}

	logger.Info("Deregistering media input: %s", id)
	delete(m.inputs, id)

	return nil
}

func (m *SimpleMediaInputManager) StopAll(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	logger.Info("Stopping all media inputs")

	var lastErr error
	for id, input := range m.inputs {
		if err := input.Stop(ctx); err != nil {
			logger.Error("Failed to stop media input %s: %v", id, err)
			lastErr = err
		}
	}

	// Clear all inputs
	m.inputs = make(map[string]MediaInput)

	return lastErr
}
