// Package deepseekv4 implements the DeepSeek V4 extension.
//
// DSML (DeepSeek Markup Language) support: intercepts DSML-formatted tool calls
// embedded in text responses and converts them to standard tool_use content blocks.
package deepseekv4

import (
	"encoding/json"
	"context"
	"fmt"
	"strings"

	"moonbridge/internal/format"
)

// dsmlMarkers defines the DSML tag boundaries.
const (
	dsmlFunctionCallsOpen  = "<|DSML|function_calls>"
	dsmlFunctionCallsClose = "</|DSML|function_calls>"
	dsmlInvokeOpen         = "<|DSML|invoke"
	dsmlInvokeClose        = "</|DSML|invoke>"
	dsmlParameterOpen      = "<|DSML|parameter"
	dsmlParameterClose     = "</|DSML|parameter>"
)

// dsmlInvoke represents a parsed DSML tool invocation.
type dsmlInvoke struct {
	Name       string
	Parameters map[string]any
}

// TransformDSMLBlocks scans text content blocks for DSML tool calls and splits
// them into separate text and tool_use blocks. Non-DSML text is preserved;
// DSML markers and their content are replaced with proper tool_use blocks.
//
// Returns the transformed block list, or the original if no DSML markers found.
func TransformDSMLBlocks(blocks []format.CoreContentBlock) []format.CoreContentBlock {
	if len(blocks) == 0 {
		return blocks
	}

	// First pass: check if any text block contains DSML markers.
	hasDSML := false
	for _, b := range blocks {
		if b.Type == "text" && strings.Contains(b.Text, dsmlFunctionCallsOpen) {
			hasDSML = true
			break
		}
	}
	if !hasDSML {
		return blocks
	}

	// Second pass: split text blocks at DSML boundaries.
	result := make([]format.CoreContentBlock, 0, len(blocks))
	for _, b := range blocks {
		if b.Type != "text" {
			result = append(result, b)
			continue
		}
		splitBlocks := splitDSMLText(b.Text)
		result = append(result, splitBlocks...)
	}
	return result
}

// splitDSMLText splits a text string containing DSML markers into
// text + tool_use content blocks.
func splitDSMLText(text string) []format.CoreContentBlock {
	var blocks []format.CoreContentBlock
	remaining := text

	for {
		openIdx := strings.Index(remaining, dsmlFunctionCallsOpen)
		if openIdx < 0 {
			// No more DSML blocks — emit remaining text.
			if remaining != "" {
				blocks = append(blocks, format.CoreContentBlock{
					Type: "text",
					Text: remaining,
				})
			}
			break
		}

		// Emit text before the DSML block.
		if openIdx > 0 {
			blocks = append(blocks, format.CoreContentBlock{
				Type: "text",
				Text: remaining[:openIdx],
			})
		}

		// Find the closing tag.
		closeIdx := strings.Index(remaining[openIdx:], dsmlFunctionCallsClose)
		if closeIdx < 0 {
			// Malformed DSML — no closing tag. Treat rest as text.
			blocks = append(blocks, format.CoreContentBlock{
				Type: "text",
				Text: remaining[openIdx:],
			})
			break
		}

		// Parse the DSML block.
		dsmlContent := remaining[openIdx+len(dsmlFunctionCallsOpen) : openIdx+closeIdx]
		invoices := parseDSMLInvoices(dsmlContent)

		// Emit tool_use blocks for each parsed invocation.
		for _, inv := range invoices {
			toolBlock := format.CoreContentBlock{
				Type:      "tool_use",
				ToolUseID: generateDSMLToolCallID(inv.Name, inv.Parameters),
				ToolName:  inv.Name,
			}
			if len(inv.Parameters) > 0 {
				input, err := json.Marshal(inv.Parameters)
				if err == nil {
					toolBlock.ToolInput = input
				}
			} else {
				toolBlock.ToolInput = json.RawMessage("{}")
			}
			blocks = append(blocks, toolBlock)
		}

		// Advance past the DSML block.
		remaining = remaining[openIdx+closeIdx+len(dsmlFunctionCallsClose):]
	}

	return blocks
}

// parseDSMLInvoices parses DSML invoke elements from raw DSML content.
func parseDSMLInvoices(content string) []dsmlInvoke {
	var invoices []dsmlInvoke

	remaining := strings.TrimSpace(content)
	for {
		openIdx := strings.Index(remaining, dsmlInvokeOpen)
		if openIdx < 0 {
			break
		}

		// Find the end of the opening tag (>).
		tagEnd := strings.Index(remaining[openIdx:], ">")
		if tagEnd < 0 {
			break
		}
		openTag := remaining[openIdx : openIdx+tagEnd+1]

		// Extract the name attribute.
		name := extractDSMLAttr(openTag, "name")

		// Find the closing </|DSML|invoke> tag.
		closeIdx := strings.Index(remaining[openIdx+tagEnd+1:], dsmlInvokeClose)
		if closeIdx < 0 {
			break
		}

		// Parse parameters from the body.
		body := remaining[openIdx+tagEnd+1 : openIdx+tagEnd+1+closeIdx]
		params := parseDSMLParameters(body)

		invoices = append(invoices, dsmlInvoke{
			Name:       name,
			Parameters: params,
		})

		// Advance past this invoke.
		remaining = remaining[openIdx+tagEnd+1+closeIdx+len(dsmlInvokeClose):]
	}

	return invoices
}

// parseDSMLParameters parses DSML parameter elements from raw invoke body.
func parseDSMLParameters(body string) map[string]any {
	params := make(map[string]any)
	remaining := body

	for {
		openIdx := strings.Index(remaining, dsmlParameterOpen)
		if openIdx < 0 {
			break
		}

		// Find the end of the opening tag (>).
		tagEnd := strings.Index(remaining[openIdx:], ">")
		if tagEnd < 0 {
			break
		}
		openTag := remaining[openIdx : openIdx+tagEnd+1]

		// Extract attributes.
		name := extractDSMLAttr(openTag, "name")
		isString := extractDSMLAttr(openTag, "string") == "true"

		// Find the closing tag.
		closeTag := dsmlParameterClose
		closeIdx := strings.Index(remaining[openIdx+tagEnd+1:], closeTag)
		if closeIdx < 0 {
			break
		}

		// Extract the value.
		value := strings.TrimSpace(remaining[openIdx+tagEnd+1 : openIdx+tagEnd+1+closeIdx])

		if name != "" {
			if isString {
				params[name] = value
			} else {
				// Try to parse as JSON.
				var parsed any
				if err := json.Unmarshal([]byte(value), &parsed); err == nil {
					params[name] = parsed
				} else {
					// Fallback: treat as string.
					params[name] = value
				}
			}
		}

		// Advance past this parameter.
		remaining = remaining[openIdx+tagEnd+1+closeIdx+len(closeTag):]
	}

	return params
}

// extractDSMLAttr extracts an attribute value from a DSML opening tag.
// Example: extractDSMLAttr(`<|DSML|parameter name="location" string="true">`, "name") -> "location"
func extractDSMLAttr(tag, attrName string) string {
	// Search for attrName="..." or attrName='...'.
	search := attrName + "=\""
	idx := strings.Index(tag, search)
	if idx < 0 {
		search = attrName + "='"
		idx = strings.Index(tag, search)
		if idx < 0 {
			return ""
		}
	}
	start := idx + len(search)
	end := strings.IndexAny(tag[start:], "\"'")
	if end < 0 {
		return ""
	}
	return tag[start : start+end]
}

// generateDSMLToolCallID generates a stable tool call ID from the tool name
// and parameters.
func generateDSMLToolCallID(name string, params map[string]any) string {
	// Build a stable key from name and sorted parameter values.
	var sb strings.Builder
	sb.WriteString(name)
	// Sort keys for deterministic output.
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	// Simple insertion sort for small maps.
	for i := 1; i < len(keys); i++ {
		j := i
		for j > 0 && keys[j] < keys[j-1] {
			keys[j], keys[j-1] = keys[j-1], keys[j]
			j--
		}
	}
	for _, k := range keys {
		sb.WriteString("|")
		sb.WriteString(k)
		sb.WriteString("=")
		sb.WriteString(fmt.Sprintf("%v", params[k]))
	}

	var hash uint64 = 14695981039346656037
	for _, c := range sb.String() {
		hash ^= uint64(c)
		hash *= 1099511628211
	}
	return fmt.Sprintf("dsml_%x", hash)
}

// TransformStreamEvents wraps a channel of CoreStreamEvent and intercepts
// text content blocks that contain DSML tool calls. When DSML is detected
// in a text block, the text events are replaced with cleaned text and
// tool_use events.
//
// Non-DSML text blocks stream through normally. DSML-containing blocks
// buffer their text until the block completes, then emit transformed events.
func TransformStreamEvents(ctx context.Context, model string, src <-chan format.CoreStreamEvent) <-chan format.CoreStreamEvent {
	out := make(chan format.CoreStreamEvent, 64)

	go func() {
		defer close(out)

		// Per-block text buffer: block index -> accumulated text
		textBuf := make(map[int]string)
		// Track which blocks have been detected as DSML
		dsmlBlocks := make(map[int]bool)
		// Active block index offsets for when DSML blocks expand into multiple blocks
		nextBlockIndex := 0
		indexRemap := make(map[int]int) // original index -> new index

		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-src:
				if !ok {
					return
				}

				switch ev.Type {
				case format.CoreContentBlockStarted:
					if ev.ContentBlock != nil && ev.ContentBlock.Type == "text" {
						// Start buffering text for this block.
						textBuf[ev.Index] = ""
						// Check if the initial content already has DSML markers.
						if strings.Contains(ev.ContentBlock.Text, dsmlFunctionCallsOpen) {
							dsmlBlocks[ev.Index] = true
						}
						// Don't emit block_start yet for DSML blocks — defer until we know the structure.
						if !dsmlBlocks[ev.Index] {
							// Remap index for non-DSML blocks.
							newIdx := nextBlockIndex
							indexRemap[ev.Index] = newIdx
							nextBlockIndex++
							ev.Index = newIdx
							sendOrCancel(ctx, out, ev)
						}
						continue
					}
					// Non-text blocks: remap and pass through.
					newIdx := nextBlockIndex
					indexRemap[ev.Index] = newIdx
					nextBlockIndex++
					ev.Index = newIdx
					sendOrCancel(ctx, out, ev)

				case format.CoreTextDelta:
					if dsmlBlocks[ev.Index] {
						// Buffer DSML text.
						textBuf[ev.Index] += ev.Delta
						continue
					}
					// Accumulate text for non-DSML blocks too, for late detection.
					textBuf[ev.Index] += ev.Delta
					// Late detection: check if accumulated text now contains DSML.
					if strings.Contains(textBuf[ev.Index], dsmlFunctionCallsOpen) {
						dsmlBlocks[ev.Index] = true
						// The text already emitted can't be undone, but the full
						// block will be transformed on block_stop.
						continue
					}
					newIdx := indexRemap[ev.Index]
					ev.Index = newIdx
					sendOrCancel(ctx, out, ev)

				case format.CoreContentBlockDone:
					if dsmlBlocks[ev.Index] {
						// Transform the DSML text block.
						fullText := textBuf[ev.Index]
						transformed := splitDSMLText(fullText)

						for _, block := range transformed {
							newIdx := nextBlockIndex
							nextBlockIndex++

							// Emit block_start.
							sendOrCancel(ctx, out, format.CoreStreamEvent{
								Type:  format.CoreContentBlockStarted,
								Index: newIdx,
								ContentBlock: &format.CoreContentBlock{
									Type:      block.Type,
									ToolUseID: block.ToolUseID,
									ToolName:  block.ToolName,
								},
							})

							if block.Type == "text" && block.Text != "" {
								// Emit text delta + done.
								sendOrCancel(ctx, out, format.CoreStreamEvent{
									Type:  format.CoreTextDelta,
									Index: newIdx,
									Delta: block.Text,
								})
							} else if block.Type == "tool_use" {
								// Emit tool_use args delta + done.
								if len(block.ToolInput) > 0 {
									sendOrCancel(ctx, out, format.CoreStreamEvent{
										Type:  format.CoreToolCallArgsDelta,
										Index: newIdx,
										Delta: string(block.ToolInput),
									})
								}
								sendOrCancel(ctx, out, format.CoreStreamEvent{
									Type:  format.CoreToolCallArgsDone,
									Index: newIdx,
								})
							}

							// Emit block_done.
							sendOrCancel(ctx, out, format.CoreStreamEvent{
								Type:  format.CoreContentBlockDone,
								Index: newIdx,
							})
							sendOrCancel(ctx, out, format.CoreStreamEvent{
								Type: format.CoreItemDone,
							})
						}

						// Cleanup.
						delete(textBuf, ev.Index)
						delete(dsmlBlocks, ev.Index)
						continue
					}

					// Non-DSML block: remap and pass through.
					newIdx := indexRemap[ev.Index]
					ev.Index = newIdx
					sendOrCancel(ctx, out, ev)

					// Emit item_done.
					sendOrCancel(ctx, out, format.CoreStreamEvent{
						Type: format.CoreItemDone,
					})

					// Cleanup.
					delete(textBuf, ev.Index)

				default:
					// Pass through other events unchanged.
					sendOrCancel(ctx, out, ev)
				}
			}
		}
	}()

	return out
}

// sendOrCancel sends an event to the channel, respecting context cancellation.
func sendOrCancel(ctx context.Context, ch chan<- format.CoreStreamEvent, ev format.CoreStreamEvent) {
	select {
	case <-ctx.Done():
	case ch <- ev:
	}
}
