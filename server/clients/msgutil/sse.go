package msgutil

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"strings"
)

// SSEEventType classifies the type of event received from the ADK /run_sse endpoint.
type SSEEventType int

const (
	SSEEventText       SSEEventType = iota // Agent produced text content
	SSEEventToolCall                       // Agent called a tool (functionCall)
	SSEEventToolResult                     // Tool returned a result (functionResponse)
	SSEEventUnknown                        // Unrecognized event
)

// SSEEvent represents a single parsed event from the ADK /run_sse stream.
type SSEEvent struct {
	Type       SSEEventType
	Author     string
	Text       string
	ToolName   string
	ToolArgs   interface{}
	ToolResult interface{}
	Raw        map[string]interface{}
}

// ParseSSEStream reads a /run_sse response body and calls handler for each
// meaningful event as it arrives. The handler receives events one at a time.
// This is a blocking call that returns when the stream ends or an error occurs.
func ParseSSEStream(reader io.Reader, handler func(SSEEvent)) error {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 256*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := strings.TrimPrefix(line, "data: ")
		if data == "" {
			continue
		}

		var raw map[string]interface{}
		if err := json.Unmarshal([]byte(data), &raw); err != nil {
			continue
		}

		events := classifyEvent(raw)
		if len(events) == 0 {
			author, _ := raw["author"].(string)
			slog.Debug("SSE event dropped (no classifiable parts)",
				"author", author,
				"raw_keys", mapKeys(raw),
				"data_len", len(data),
			)
		}
		for _, evt := range events {
			handler(evt)
		}
	}

	return scanner.Err()
}

// classifyEvent examines a raw ADK event JSON and returns one or more typed SSEEvents.
func classifyEvent(raw map[string]interface{}) []SSEEvent {
	author, _ := raw["author"].(string)

	content, ok := raw["content"].(map[string]interface{})
	if !ok {
		return nil
	}

	parts, ok := content["parts"].([]interface{})
	if !ok {
		return nil
	}

	var events []SSEEvent

	for _, part := range parts {
		partMap, ok := part.(map[string]interface{})
		if !ok {
			continue
		}

		if text, ok := partMap["text"].(string); ok && text != "" {
			events = append(events, SSEEvent{
				Type:   SSEEventText,
				Author: author,
				Text:   text,
				Raw:    raw,
			})
		}

		if fc, ok := partMap["functionCall"].(map[string]interface{}); ok {
			name, _ := fc["name"].(string)
			events = append(events, SSEEvent{
				Type:     SSEEventToolCall,
				Author:   author,
				ToolName: name,
				ToolArgs: fc["args"],
				Raw:      raw,
			})
		}

		if fr, ok := partMap["functionResponse"].(map[string]interface{}); ok {
			name, _ := fr["name"].(string)
			events = append(events, SSEEvent{
				Type:       SSEEventToolResult,
				Author:     author,
				ToolName:   name,
				ToolResult: fr["response"],
				Raw:        raw,
			})
		}
	}

	return events
}

// CollectSSEEvents is a convenience wrapper that reads the full SSE stream
// and returns all events and the aggregated text response.
func CollectSSEEvents(reader io.Reader) ([]SSEEvent, string, error) {
	var events []SSEEvent
	var textParts []string

	err := ParseSSEStream(reader, func(evt SSEEvent) {
		events = append(events, evt)
		if evt.Type == SSEEventText {
			textParts = append(textParts, evt.Text)
		}
	})

	fullText := strings.Join(textParts, "")
	if fullText == "" {
		fullText = "(no response)"
	}

	return events, fullText, err
}

// FormatToolCallTelegram formats a single tool call as a Telegram expandable
// blockquote. The tool name is always visible; args are human-readable inside.
func FormatToolCallTelegram(evt SSEEvent) string {
	lines := humanArgLines(evt.ToolArgs)
	if len(lines) == 0 {
		return fmt.Sprintf("<blockquote>🔧 <b>%s</b></blockquote>", escapeHTML(evt.ToolName))
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("<blockquote expandable>🔧 <b>%s</b>\n", escapeHTML(evt.ToolName)))
	for _, l := range lines {
		b.WriteString(fmt.Sprintf("<b>%s</b>: %s\n", escapeHTML(l.Key), escapeHTML(l.Value)))
	}
	b.WriteString("</blockquote>")
	return b.String()
}

// FormatToolCallDiscord formats a single tool call for Discord.
func FormatToolCallDiscord(evt SSEEvent) string {
	lines := humanArgLines(evt.ToolArgs)
	if len(lines) == 0 {
		return fmt.Sprintf("> 🔧 **%s**", evt.ToolName)
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("> 🔧 **%s**\n", evt.ToolName))
	for _, l := range lines {
		b.WriteString(fmt.Sprintf("> **%s**: %s\n", l.Key, l.Value))
	}
	return strings.TrimRight(b.String(), "\n")
}

// FormatToolCallSlack formats a single tool call for Slack.
func FormatToolCallSlack(evt SSEEvent) string {
	lines := humanArgLines(evt.ToolArgs)
	if len(lines) == 0 {
		return fmt.Sprintf("> 🔧 *%s*", evt.ToolName)
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("> 🔧 *%s*\n", evt.ToolName))
	for _, l := range lines {
		b.WriteString(fmt.Sprintf("> *%s*: %s\n", l.Key, l.Value))
	}
	return strings.TrimRight(b.String(), "\n")
}

// argLine holds a key-value pair from tool arguments.
type argLine struct {
	Key   string
	Value string
}

// humanArgLines converts tool arguments into human-readable key-value pairs.
func humanArgLines(args interface{}) []argLine {
	if args == nil {
		return nil
	}
	m, ok := args.(map[string]interface{})
	if !ok {
		return nil
	}

	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var lines []argLine
	for _, k := range keys {
		v := m[k]
		s := humanValue(v)
		if s == "" {
			continue
		}
		lines = append(lines, argLine{Key: k, Value: s})
	}
	return lines
}

// humanValue formats a single value for human reading.
func humanValue(v interface{}) string {
	switch val := v.(type) {
	case string:
		return val
	case float64:
		if val == float64(int64(val)) {
			return fmt.Sprintf("%d", int64(val))
		}
		return fmt.Sprintf("%g", val)
	case bool:
		if val {
			return "true"
		}
		return "false"
	case nil:
		return ""
	case []interface{}:
		if len(val) == 0 {
			return "[]"
		}
		var items []string
		for _, item := range val {
			items = append(items, humanValue(item))
			if len(items) >= 5 {
				items = append(items, "…")
				break
			}
		}
		return "[" + strings.Join(items, ", ") + "]"
	case map[string]interface{}:
		b, _ := json.Marshal(val)
		s := string(b)
		if len(s) > 80 {
			s = s[:80] + "…"
		}
		return s
	default:
		return fmt.Sprintf("%v", v)
	}
}

func escapeHTML(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}

func mapKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
