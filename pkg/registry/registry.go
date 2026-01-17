// Package registry provides generic registries for stream handlers and extractors.
package registry

import (
	"sync"

	"media-proxy-go/pkg/interfaces"
	"media-proxy-go/pkg/types"
)

// StreamHandlerRegistry manages stream handlers.
type StreamHandlerRegistry struct {
	mu       sync.RWMutex
	handlers []interfaces.StreamHandler
	fallback interfaces.StreamHandler
}

// NewStreamHandlerRegistry creates a new stream handler registry.
func NewStreamHandlerRegistry() *StreamHandlerRegistry {
	return &StreamHandlerRegistry{
		handlers: make([]interfaces.StreamHandler, 0),
	}
}

// Register adds a stream handler to the registry.
func (r *StreamHandlerRegistry) Register(handler interfaces.StreamHandler) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.handlers = append(r.handlers, handler)
}

// SetFallback sets the fallback handler used when no handler matches.
func (r *StreamHandlerRegistry) SetFallback(handler interfaces.StreamHandler) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.fallback = handler
}

// Get returns the appropriate handler for the given URL.
func (r *StreamHandlerRegistry) Get(url string) interfaces.StreamHandler {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, h := range r.handlers {
		if h.CanHandle(url) {
			return h
		}
	}
	return r.fallback
}

// GetByType returns the handler for a specific stream type.
func (r *StreamHandlerRegistry) GetByType(t types.StreamType) interfaces.StreamHandler {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, h := range r.handlers {
		if h.Type() == t {
			return h
		}
	}
	return r.fallback
}

// All returns all registered handlers.
func (r *StreamHandlerRegistry) All() []interfaces.StreamHandler {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]interfaces.StreamHandler, len(r.handlers))
	copy(result, r.handlers)
	return result
}

// ExtractorRegistry manages URL extractors.
type ExtractorRegistry struct {
	mu         sync.RWMutex
	extractors []interfaces.Extractor
	byName     map[string]interfaces.Extractor
	fallback   interfaces.Extractor
}

// NewExtractorRegistry creates a new extractor registry.
func NewExtractorRegistry() *ExtractorRegistry {
	return &ExtractorRegistry{
		extractors: make([]interfaces.Extractor, 0),
		byName:     make(map[string]interfaces.Extractor),
	}
}

// Register adds an extractor to the registry.
func (r *ExtractorRegistry) Register(extractor interfaces.Extractor) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.extractors = append(r.extractors, extractor)
	r.byName[extractor.Name()] = extractor
}

// SetFallback sets the fallback extractor used when no extractor matches.
func (r *ExtractorRegistry) SetFallback(extractor interfaces.Extractor) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.fallback = extractor
}

// Get returns the appropriate extractor for the given URL.
func (r *ExtractorRegistry) Get(url string) interfaces.Extractor {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, e := range r.extractors {
		if e.CanExtract(url) {
			return e
		}
	}
	return r.fallback
}

// GetByName returns an extractor by its name.
func (r *ExtractorRegistry) GetByName(name string) interfaces.Extractor {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if e, ok := r.byName[name]; ok {
		return e
	}
	return r.fallback
}

// All returns all registered extractors.
func (r *ExtractorRegistry) All() []interfaces.Extractor {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]interfaces.Extractor, len(r.extractors))
	copy(result, r.extractors)
	return result
}

// Close closes all registered extractors.
func (r *ExtractorRegistry) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, e := range r.extractors {
		_ = e.Close()
	}
	if r.fallback != nil {
		_ = r.fallback.Close()
	}
	return nil
}
