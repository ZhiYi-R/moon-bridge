package cache

import (
	"bytes"
	"encoding/json"
	"time"
)

// estimateTokens estimates the token count for an Anthropic MessageRequest.
// Used by the cache planner to decide whether caching is worthwhile.
func estimateTokens(request any) int {
	data, _ := json.Marshal(request)
	n := len(data)
	if n == 0 {
		return 0
	}
	// Base64 content (images) encodes at ~6-8 chars per token vs ~4 for normal JSON.
	b64Bytes := countBase64Bytes(data)
	textBytes := n - b64Bytes
	return textBytes/4 + b64Bytes/7 + 1
}

// estimatePartTokens estimates token count for any JSON-serializable slice.
func estimatePartTokens(part any) int {
	data, _ := json.Marshal(part)
	n := len(data)
	if n == 0 {
		return 0
	}
	b64Bytes := countBase64Bytes(data)
	textBytes := n - b64Bytes
	return textBytes/4 + b64Bytes/7 + 1
}

// countBase64Bytes estimates the number of bytes in JSON data that are
// base64-encoded image payloads (Anthropic format: "data" field after "media_type").
func countBase64Bytes(data []byte) int {
	total := 0
	marker := []byte(`"data":"`)
	for offset := 0; offset < len(data); {
		idx := bytes.Index(data[offset:], marker)
		if idx < 0 {
			break
		}
		pos := offset + idx
		// Check if "media_type" appears within 200 bytes before this "data" field
		windowStart := pos - 200
		if windowStart < 0 {
			windowStart = 0
		}
		if bytes.Contains(data[windowStart:pos], []byte(`"media_type"`)) {
			valueStart := pos + len(marker)
			valueEnd := bytes.IndexByte(data[valueStart:], '"')
			if valueEnd > 0 {
				total += valueEnd
				offset = valueStart + valueEnd + 1
				continue
			}
		}
		offset = pos + len(marker)
	}
	return total
}

// ParseTTL converts a TTL string (e.g. "5m", "1h") to time.Duration.
// Returns 0 on parse failure, letting callers fall back to their default.
func ParseTTL(ttl string) time.Duration {
	d, _ := time.ParseDuration(ttl)
	return d
}
