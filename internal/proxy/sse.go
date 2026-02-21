package proxy

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// SSEEvent represents a single Server-Sent Event with optional event type, data, and ID.
type SSEEvent struct {
	Event string
	Data  string
	ID    string
}

// SSEReader reads Server-Sent Events from an io.Reader, parsing the SSE wire
// format (event:, data:, id: lines separated by blank lines).
type SSEReader struct {
	scanner *bufio.Scanner
}

// NewSSEReader creates a new SSEReader that reads from the given io.Reader.
// The scanner buffer is sized at 64KB initial / 10MB max to handle large SSE
// lines containing tool call outputs, code blocks, or base64-encoded content.
func NewSSEReader(r io.Reader) *SSEReader {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 10*1024*1024)
	return &SSEReader{
		scanner: scanner,
	}
}

// Next reads and returns the next complete SSE event from the stream.
// An event is terminated by a blank line. Returns io.EOF when the stream ends.
// Lines beginning with ":" (comment lines) are silently skipped.
func (s *SSEReader) Next() (*SSEEvent, error) {
	var evt SSEEvent
	hasData := false

	for s.scanner.Scan() {
		line := s.scanner.Text()

		// A blank line signals the end of an event.
		if line == "" {
			if hasData || evt.Event != "" || evt.ID != "" {
				return &evt, nil
			}
			// Empty event boundary with no accumulated data; continue reading.
			continue
		}

		// Skip SSE comment lines (lines starting with ":").
		if strings.HasPrefix(line, ":") {
			continue
		}

		// Parse field: value pairs.
		field, value := parseSSELine(line)
		switch field {
		case "event":
			evt.Event = value
		case "data":
			if hasData {
				evt.Data += "\n" + value
			} else {
				evt.Data = value
				hasData = true
			}
		case "id":
			evt.ID = value
		}
	}

	if err := s.scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading SSE stream: %w", err)
	}

	// If we accumulated an event before EOF, return it.
	if hasData || evt.Event != "" || evt.ID != "" {
		return &evt, nil
	}

	return nil, io.EOF
}

// parseSSELine splits an SSE line into its field name and value.
// The format is "field: value" where the space after the colon is optional.
func parseSSELine(line string) (field, value string) {
	idx := strings.Index(line, ":")
	if idx < 0 {
		return line, ""
	}
	field = line[:idx]
	value = line[idx+1:]
	// Strip a single leading space from the value if present.
	if strings.HasPrefix(value, " ") {
		value = value[1:]
	}
	return field, value
}

// SSEWriter writes Server-Sent Events to an http.ResponseWriter, flushing
// after each event to ensure real-time delivery to the client.
type SSEWriter struct {
	w       http.ResponseWriter
	flusher http.Flusher
}

// NewSSEWriter creates a new SSEWriter. It checks if the http.ResponseWriter
// supports the http.Flusher interface for real-time event delivery.
func NewSSEWriter(w http.ResponseWriter) *SSEWriter {
	flusher, _ := w.(http.Flusher)
	return &SSEWriter{
		w:       w,
		flusher: flusher,
	}
}

// WriteEvent writes a single SSE event to the underlying ResponseWriter and flushes.
// The event type line is only written if evt.Event is non-empty.
// The id line is only written if evt.ID is non-empty.
// Multiple data lines are written if the Data field contains newlines.
func (s *SSEWriter) WriteEvent(evt *SSEEvent) error {
	if evt.Event != "" {
		if _, err := fmt.Fprintf(s.w, "event: %s\n", evt.Event); err != nil {
			return fmt.Errorf("writing SSE event type: %w", err)
		}
	}

	if evt.ID != "" {
		if _, err := fmt.Fprintf(s.w, "id: %s\n", evt.ID); err != nil {
			return fmt.Errorf("writing SSE event id: %w", err)
		}
	}

	// Write each line of data separately per the SSE spec.
	dataLines := strings.Split(evt.Data, "\n")
	for _, dl := range dataLines {
		if _, err := fmt.Fprintf(s.w, "data: %s\n", dl); err != nil {
			return fmt.Errorf("writing SSE data line: %w", err)
		}
	}

	// Blank line terminates the event.
	if _, err := fmt.Fprint(s.w, "\n"); err != nil {
		return fmt.Errorf("writing SSE event terminator: %w", err)
	}

	s.Flush()
	return nil
}

// Flush flushes the underlying ResponseWriter if it supports the http.Flusher interface.
func (s *SSEWriter) Flush() {
	if s.flusher != nil {
		s.flusher.Flush()
	}
}
