package proxy

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// StreamSession manages a long-lived streaming connection between client and upstream.
type StreamSession struct {
	ID        string
	Model     string
	Provider  ProviderConfig
	CreatedAt time.Time

	mu       sync.Mutex
	closed   bool
	messages chan []byte
}

// Close marks the session as closed and drains the message channel.
func (s *StreamSession) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	close(s.messages)
}

// IsClosed returns whether the session has been closed.
func (s *StreamSession) IsClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

// Send enqueues a message to be delivered via the SSE events endpoint.
// Returns an error if the session is closed or the channel is full.
func (s *StreamSession) Send(data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return fmt.Errorf("session %s is closed", s.ID)
	}
	select {
	case s.messages <- data:
		return nil
	default:
		return fmt.Errorf("session %s message buffer full", s.ID)
	}
}

// StreamManager manages active streaming sessions.
type StreamManager struct {
	sessions map[string]*StreamSession
	mu       sync.RWMutex
}

// NewStreamManager creates a new StreamManager.
func NewStreamManager() *StreamManager {
	return &StreamManager{
		sessions: make(map[string]*StreamSession),
	}
}

// Create creates a new streaming session for the given model and provider.
func (m *StreamManager) Create(model string, provider ProviderConfig) *StreamSession {
	m.mu.Lock()
	defer m.mu.Unlock()

	session := &StreamSession{
		ID:        uuid.New().String(),
		Model:     model,
		Provider:  provider,
		CreatedAt: time.Now(),
		messages:  make(chan []byte, 256),
	}
	m.sessions[session.ID] = session
	return session
}

// Get returns the session with the given ID, or nil if not found.
func (m *StreamManager) Get(id string) *StreamSession {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.sessions[id]
}

// Delete closes and removes the session with the given ID.
func (m *StreamManager) Delete(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if s, ok := m.sessions[id]; ok {
		s.Close()
		delete(m.sessions, id)
	}
}

// Count returns the number of active sessions.
func (m *StreamManager) Count() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.sessions)
}

// streamCreateRequest is the JSON body for POST /v1/stream/create.
type streamCreateRequest struct {
	Model string `json:"model"`
}

// streamCreateResponse is the JSON response for POST /v1/stream/create.
type streamCreateResponse struct {
	ID        string `json:"id"`
	Model     string `json:"model"`
	CreatedAt string `json:"created_at"`
}

// streamSendRequest is the JSON body for POST /v1/stream/{id}/send.
type streamSendRequest struct {
	Message string `json:"message"`
	Role    string `json:"role,omitempty"`
}

// HandleStreamCreate creates a new streaming session.
func (h *ProxyHandler) HandleStreamCreate(w http.ResponseWriter, r *http.Request) {
	var req streamCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	defer r.Body.Close()

	if req.Model == "" {
		writeJSONError(w, http.StatusBadRequest, "model is required")
		return
	}

	// Resolve provider for the model.
	baseURL, apiKey, format, err := h.resolveProvider(req.Model)
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, fmt.Sprintf("no provider for model: %s", req.Model))
		return
	}

	provider := ProviderConfig{
		BaseURL: baseURL,
		APIKey:  apiKey,
		Format:  format,
	}

	session := h.streams.Create(req.Model, provider)

	resp := streamCreateResponse{
		ID:        session.ID,
		Model:     session.Model,
		CreatedAt: session.CreatedAt.UTC().Format(time.RFC3339),
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(resp)
}

// HandleStreamSend sends a message through an existing streaming session.
func (h *ProxyHandler) HandleStreamSend(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	session := h.streams.Get(id)
	if session == nil {
		writeJSONError(w, http.StatusNotFound, "session not found")
		return
	}

	if session.IsClosed() {
		writeJSONError(w, http.StatusGone, "session is closed")
		return
	}

	var req streamSendRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	defer r.Body.Close()

	if req.Message == "" {
		writeJSONError(w, http.StatusBadRequest, "message is required")
		return
	}

	role := req.Role
	if role == "" {
		role = "user"
	}

	// Build an event payload for the SSE stream.
	event := map[string]string{
		"role":      role,
		"content":   req.Message,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	}
	data, err := json.Marshal(event)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to marshal event")
		return
	}

	if err := session.Send(data); err != nil {
		writeJSONError(w, http.StatusServiceUnavailable, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "sent"})
}

// HandleStreamEvents is the SSE endpoint for receiving responses from a session.
func (h *ProxyHandler) HandleStreamEvents(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	session := h.streams.Get(id)
	if session == nil {
		writeJSONError(w, http.StatusNotFound, "session not found")
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ctx := r.Context()

	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-session.messages:
			if !ok {
				// Session closed; send a final event.
				fmt.Fprintf(w, "event: close\ndata: {\"reason\":\"session_closed\"}\n\n")
				flusher.Flush()
				return
			}
			fmt.Fprintf(w, "data: %s\n\n", msg)
			flusher.Flush()
		}
	}
}

// HandleStreamDelete closes and removes a streaming session.
func (h *ProxyHandler) HandleStreamDelete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	session := h.streams.Get(id)
	if session == nil {
		writeJSONError(w, http.StatusNotFound, "session not found")
		return
	}

	h.streams.Delete(id)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "deleted"})
}
