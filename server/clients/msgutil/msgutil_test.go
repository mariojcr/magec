package msgutil

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestValidateInputLength_Short(t *testing.T) {
	text := "hello world"
	result, truncated := ValidateInputLength(text, 100)
	if truncated {
		t.Error("expected no truncation")
	}
	if result != text {
		t.Errorf("expected %q, got %q", text, result)
	}
}

func TestValidateInputLength_Exact(t *testing.T) {
	text := strings.Repeat("a", 100)
	result, truncated := ValidateInputLength(text, 100)
	if truncated {
		t.Error("expected no truncation")
	}
	if result != text {
		t.Errorf("expected same text")
	}
}

func TestValidateInputLength_Truncated(t *testing.T) {
	text := strings.Repeat("a", 200)
	result, truncated := ValidateInputLength(text, 100)
	if !truncated {
		t.Error("expected truncation")
	}
	if !strings.HasSuffix(result, "[message truncated]") {
		t.Error("expected truncation suffix")
	}
	runes := []rune(result)
	if len(runes) > 100+len("\n\n[message truncated]") {
		t.Errorf("truncated result too long: %d runes", len(runes))
	}
}

func TestValidateInputLength_Unicode(t *testing.T) {
	text := strings.Repeat("日", 50)
	result, truncated := ValidateInputLength(text, 30)
	if !truncated {
		t.Error("expected truncation")
	}
	if !strings.HasPrefix(result, strings.Repeat("日", 30)) {
		t.Error("expected unicode-safe truncation")
	}
}

func TestSplitMessage_Short(t *testing.T) {
	text := "hello"
	chunks := SplitMessage(text, 100)
	if len(chunks) != 1 {
		t.Errorf("expected 1 chunk, got %d", len(chunks))
	}
	if chunks[0] != text {
		t.Errorf("expected %q, got %q", text, chunks[0])
	}
}

func TestSplitMessage_ParagraphBoundary(t *testing.T) {
	part1 := strings.Repeat("a", 40)
	part2 := strings.Repeat("b", 40)
	text := part1 + "\n\n" + part2
	chunks := SplitMessage(text, 50)
	if len(chunks) != 2 {
		t.Errorf("expected 2 chunks, got %d: %v", len(chunks), chunks)
	}
	if chunks[0] != part1 {
		t.Errorf("first chunk: expected %q, got %q", part1, chunks[0])
	}
	if chunks[1] != part2 {
		t.Errorf("second chunk: expected %q, got %q", part2, chunks[1])
	}
}

func TestSplitMessage_LineBoundary(t *testing.T) {
	part1 := strings.Repeat("a", 40)
	part2 := strings.Repeat("b", 40)
	text := part1 + "\n" + part2
	chunks := SplitMessage(text, 50)
	if len(chunks) != 2 {
		t.Errorf("expected 2 chunks, got %d: %v", len(chunks), chunks)
	}
}

func TestSplitMessage_WordBoundary(t *testing.T) {
	text := "hello world this is a test"
	chunks := SplitMessage(text, 15)
	for _, chunk := range chunks {
		if utf8.RuneCountInString(chunk) > 15 {
			t.Errorf("chunk exceeds max: %q (%d runes)", chunk, utf8.RuneCountInString(chunk))
		}
	}
	joined := strings.Join(chunks, " ")
	if !strings.Contains(joined, "hello") || !strings.Contains(joined, "test") {
		t.Error("content lost during split")
	}
}

func TestSplitMessage_HardCut(t *testing.T) {
	text := strings.Repeat("a", 100)
	chunks := SplitMessage(text, 30)
	for _, chunk := range chunks {
		if utf8.RuneCountInString(chunk) > 30 {
			t.Errorf("chunk exceeds max: %d runes", utf8.RuneCountInString(chunk))
		}
	}
	total := 0
	for _, c := range chunks {
		total += utf8.RuneCountInString(c)
	}
	if total != 100 {
		t.Errorf("content lost: expected 100 runes, got %d", total)
	}
}

func TestSplitMessage_TelegramLimit(t *testing.T) {
	text := strings.Repeat("word ", 2000) // ~10000 chars
	chunks := SplitMessage(text, TelegramMaxMessageLength)
	for i, chunk := range chunks {
		if utf8.RuneCountInString(chunk) > TelegramMaxMessageLength {
			t.Errorf("chunk %d exceeds Telegram limit: %d runes", i, utf8.RuneCountInString(chunk))
		}
	}
}

func TestSplitMessage_EmptyString(t *testing.T) {
	chunks := SplitMessage("", 100)
	if len(chunks) != 1 || chunks[0] != "" {
		t.Errorf("expected single empty chunk, got %v", chunks)
	}
}

func TestSplitMessage_Unicode(t *testing.T) {
	text := strings.Repeat("日本語", 100) // 300 unicode chars
	chunks := SplitMessage(text, 50)
	for i, chunk := range chunks {
		if utf8.RuneCountInString(chunk) > 50 {
			t.Errorf("chunk %d exceeds max: %d runes", i, utf8.RuneCountInString(chunk))
		}
	}
}
