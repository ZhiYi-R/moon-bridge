package cache

import (
	"fmt"
	"strings"

	"moonbridge/internal/protocol/anthropic"
)

// MessageBreakpointCandidate represents a potential cache breakpoint location
// within the message list, used by the planner to select optimal breakpoints.
type MessageBreakpointCandidate struct {
	MessageIndex int
	ContentIndex int
	BlockPath    string
	Hash         string
	Role         string
}

// CacheMessageBreakpointCandidates computes breakpoint candidates from a
// message list by finding the last cacheable content block in each message.
func CacheMessageBreakpointCandidates(messages []anthropic.Message) []MessageBreakpointCandidate {
	candidates := make([]MessageBreakpointCandidate, 0, len(messages))
	for messageIndex, message := range messages {
		contentIndex := lastCacheableContentIndex(message.Content)
		if contentIndex < 0 {
			continue
		}
		blockPath := fmt.Sprintf("messages[%d].content[%d]", messageIndex, contentIndex)
		if contentIndex == len(message.Content)-1 {
			blockPath = fmt.Sprintf("messages[%d].content[last]", messageIndex)
		}
		candidates = append(candidates, MessageBreakpointCandidate{
			MessageIndex: messageIndex,
			ContentIndex: contentIndex,
			BlockPath:    blockPath,
			Role:         message.Role,
		})
	}
	return candidates
}

// lastCacheableContentIndex finds the last non-empty text block index in content.
func lastCacheableContentIndex(content []anthropic.ContentBlock) int {
	for index := len(content) - 1; index >= 0; index-- {
		block := content[index]
		if block.Type == "text" && strings.TrimSpace(block.Text) == "" {
			continue
		}
		return index
	}
	return -1
}
