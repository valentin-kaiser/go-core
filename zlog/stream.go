package zlog

import (
	"strings"
	"sync"
)

// StreamWriter is an io.Writer that streams log entries to listeners
type StreamWriter struct {
	mu        sync.RWMutex
	listeners []chan string
	buffer    []string
	bufferMax int
}

// NewStreamWriter creates a new StreamWriter with optional buffer size
func NewStreamWriter(bufferMax int) *StreamWriter {
	if bufferMax <= 0 {
		bufferMax = 100 // default buffer size
	}
	return &StreamWriter{
		listeners: make([]chan string, 0),
		buffer:    make([]string, 0),
		bufferMax: bufferMax,
	}
}

// Write implements io.Writer interface
func (sw *StreamWriter) Write(p []byte) (n int, err error) {
	logEntry := strings.TrimSpace(string(p))
	if logEntry == "" {
		return len(p), nil
	}

	sw.mu.Lock()
	sw.addToBuffer(logEntry)
	sw.broadcast(logEntry)
	sw.mu.Unlock()
	return len(p), nil
}

// AddListener adds a new listener channel and sends buffered entries
func (sw *StreamWriter) AddListener(ch chan string) {
	sw.mu.Lock()
	defer sw.mu.Unlock()

	// Send buffered entries to new listener
	sw.sendBufferedEntries(ch)
	sw.listeners = append(sw.listeners, ch)
}

// RemoveListener removes a specific listener channel
func (sw *StreamWriter) RemoveListener(ch chan string) {
	sw.mu.Lock()
	defer sw.mu.Unlock()

	for i, listener := range sw.listeners {
		if listener == ch {
			sw.removeListener(i)
			break
		}
	}
}

// HasListeners returns true if there are active listeners
func (sw *StreamWriter) HasListeners() bool {
	sw.mu.RLock()
	defer sw.mu.RUnlock()
	return len(sw.listeners) > 0
}

// GetListenerCount returns the number of active listeners
func (sw *StreamWriter) GetListenerCount() int {
	sw.mu.RLock()
	defer sw.mu.RUnlock()
	return len(sw.listeners)
}

// addToBuffer adds an entry to the buffer, maintaining max size
func (sw *StreamWriter) addToBuffer(entry string) {
	if len(sw.buffer) >= sw.bufferMax {
		// Remove oldest entry
		sw.buffer = sw.buffer[1:]
	}
	sw.buffer = append(sw.buffer, entry)
}

// broadcast sends log entry to all listeners
func (sw *StreamWriter) broadcast(entry string) {
	for i := len(sw.listeners) - 1; i >= 0; i-- {
		select {
		case sw.listeners[i] <- entry:
			// Successfully sent
		default:
			// Channel is full or closed, remove listener
			sw.removeListener(i)
		}
	}
}

// removeListener removes a listener at the specified index
func (sw *StreamWriter) removeListener(index int) {
	if index < 0 || index >= len(sw.listeners) {
		return
	}
	close(sw.listeners[index])
	sw.listeners = append(sw.listeners[:index], sw.listeners[index+1:]...)
}

// sendBufferedEntries sends all buffered entries to a channel, stopping if channel is full
func (sw *StreamWriter) sendBufferedEntries(ch chan string) {
	for i := len(sw.buffer) - 1; i >= 0; i-- {
		entry := sw.buffer[i]
		select {
		case ch <- entry:
		default:
			// Channel is full, stop sending remaining buffered entries
			return
		}
	}
}
