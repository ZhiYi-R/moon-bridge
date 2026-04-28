package bridge

import (
	"fmt"
	"strings"

	"moonbridge/internal/extension/codex"
	"moonbridge/internal/foundation/openai"
	"moonbridge/internal/protocol/anthropic"
)

func (converter *streamConverter) contentBlockStart(event anthropic.StreamEvent) []openai.StreamEvent {
	index := event.Index
	block := event.ContentBlock
	if block == nil {
		return nil
	}
	converter.resetBlockState(index)
	if converter.bridge.hooks.OnStreamBlockStart(converter.model, index, block, converter.extStreamStates) {
		return nil
	}
	switch block.Type {
	case "text":
		converter.itemIDs[index] = fmt.Sprintf("msg_item_%d", index)
		converter.contentText[index] = ""
		return nil
	case "tool_use":
		result := converter.codexStream.ToolUseStart(index, *block)
		converter.itemIDs[index] = result.ItemID
		var events []openai.StreamEvent
		if result.EmitPendingReasoning {
			events = converter.emitPendingReasoningItem()
		}
		converter.bridge.hooks.OnStreamToolCall(converter.model, block.ID, converter.extStreamStates)
		converter.addOutput(index, result.Item)
		return append(events, converter.outputItem("response.output_item.added", index, result.Item))
	case "server_tool_use":
		result := converter.codexStream.ServerToolUseStart(index, *block)
		if !result.Handled {
			return nil
		}
		converter.itemIDs[index] = result.ItemID
		return nil
	}
	return nil
}

func (converter *streamConverter) contentBlockDelta(event anthropic.StreamEvent) []openai.StreamEvent {
	index := event.Index
	if converter.bridge.hooks.OnStreamBlockDelta(converter.model, index, event.Delta, converter.extStreamStates) {
		return nil
	}
	switch event.Delta.Type {
	case "text_delta":
		if event.Delta.Text == "" {
			return nil
		}
		current := converter.contentText[index]
		converter.contentText[index] = current + event.Delta.Text
		if codex.IsEmptyWebSearchPrelude(converter.contentText[index]) {
			return nil
		}
		delta := event.Delta.Text
		var events []openai.StreamEvent
		if !converter.hasOutput(index) {
			item := openai.OutputItem{
				Type:    "message",
				ID:      converter.itemIDs[index],
				Status:  "in_progress",
				Role:    "assistant",
				Content: []openai.ContentPart{},
			}
			if converter.persistTextReasoning {
				events = append(events, converter.emitPendingReasoningItem()...)
			}
			converter.addOutput(index, item)
			events = append(events,
				converter.outputItem("response.output_item.added", index, item),
				converter.contentPart("response.content_part.added", index, 0, openai.ContentPart{Type: "output_text"}),
			)
			if current != "" {
				delta = converter.contentText[index]
			}
		}
		events = append(events, openai.StreamEvent{
			Event: "response.output_text.delta",
			Data: openai.OutputTextDeltaEvent{
				Type:           "response.output_text.delta",
				SequenceNumber: converter.next(),
				ItemID:         converter.itemIDs[index],
				OutputIndex:    converter.outputIndex(index),
				ContentIndex:   0,
				Delta:          delta,
			},
		})
		return events
	case "input_json_delta":
		if result := converter.codexStream.InputJSONDelta(index, event.Delta.PartialJSON); result.Handled {
			if result.SuppressDelta {
				return nil
			}
		}
		converter.toolArguments[index] += event.Delta.PartialJSON
		// Guard against orphan delta events without a preceding content_block_start.
		if !converter.hasOutput(index) || converter.itemIDs[index] == "" {
			return nil
		}
		// local_shell_call items accumulate silently; Codex only expects
		// the completed item via response.output_item.done.
		if strings.HasPrefix(converter.itemIDs[index], "lc_") {
			return nil
		}
		return []openai.StreamEvent{{
			Event: "response.function_call_arguments.delta",
			Data: openai.FunctionCallArgumentsDeltaEvent{
				Type:           "response.function_call_arguments.delta",
				SequenceNumber: converter.next(),
				ItemID:         converter.itemIDs[index],
				OutputIndex:    converter.outputIndex(index),
				Delta:          event.Delta.PartialJSON,
			},
		}}
	}
	return nil
}

func (converter *streamConverter) contentBlockStop(event anthropic.StreamEvent) []openai.StreamEvent {
	index := event.Index
	if consumed, reasoningText := converter.bridge.hooks.OnStreamBlockStop(converter.model, index, converter.extStreamStates); consumed {
		converter.codexStream.SetPendingReasoning(reasoningText)
		return nil
	}
	if text, ok := converter.contentText[index]; ok {
		if codex.IsEmptyWebSearchPrelude(text) {
			return nil
		}
		var events []openai.StreamEvent
		if !converter.hasOutput(index) {
			if text == "" {
				return nil
			}
			item := openai.OutputItem{
				Type:    "message",
				ID:      converter.itemIDs[index],
				Status:  "in_progress",
				Role:    "assistant",
				Content: []openai.ContentPart{},
			}
			if converter.persistTextReasoning {
				events = append(events, converter.emitPendingReasoningItem()...)
			}
			converter.addOutput(index, item)
			events = append(events,
				converter.outputItem("response.output_item.added", index, item),
				converter.contentPart("response.content_part.added", index, 0, openai.ContentPart{Type: "output_text"}),
			)
		}
		converter.response.OutputText += text
		item := openai.OutputItem{
			Type:    "message",
			ID:      converter.itemIDs[index],
			Status:  "completed",
			Role:    "assistant",
			Content: []openai.ContentPart{{Type: "output_text", Text: text}},
		}
		converter.setOutput(index, item)
		events = append(events,
			openai.StreamEvent{
				Event: "response.output_text.done",
				Data: openai.OutputTextDoneEvent{
					Type:           "response.output_text.done",
					SequenceNumber: converter.next(),
					ItemID:         converter.itemIDs[index],
					OutputIndex:    converter.outputIndex(index),
					ContentIndex:   0,
					Text:           text,
				},
			},
			converter.contentPart("response.content_part.done", index, 0, openai.ContentPart{Type: "output_text", Text: text}),
			converter.outputItem("response.output_item.done", index, item),
		)
		return events
	}
	if result := converter.codexStream.Stop(index, converter.itemIDs[index], converter.outputAt(index), converter.toolArguments[index]); result.Handled {
		if result.AddOutput {
			converter.addOutput(index, result.Item)
			added := converter.outputItem("response.output_item.added", index, result.Item)
			result.Item.Status = "completed"
			converter.setOutput(index, result.Item)
			return []openai.StreamEvent{added, converter.outputItem("response.output_item.done", index, result.Item)}
		}
		if result.SetOutput {
			converter.setOutput(index, result.Item)
			events := make([]openai.StreamEvent, 0, 2)
			if result.CustomToolInputDelta != "" {
				events = append(events, openai.StreamEvent{
					Event: "response.custom_tool_call_input.delta",
					Data: openai.CustomToolCallInputDeltaEvent{
						Type:           "response.custom_tool_call_input.delta",
						SequenceNumber: converter.next(),
						ItemID:         converter.itemIDs[index],
						CallID:         result.Item.CallID,
						OutputIndex:    converter.outputIndex(index),
						Delta:          result.CustomToolInputDelta,
					},
				})
			}
			events = append(events, converter.outputItem("response.output_item.done", index, result.Item))
			return events
		}
		return nil
	}
	if arguments, ok := converter.toolArguments[index]; ok {
		if strings.HasPrefix(converter.itemIDs[index], "lc_") {
			item := codex.CompleteLocalShellCall(converter.itemIDs[index], compactJSON(arguments))
			converter.setOutput(index, item)
			return []openai.StreamEvent{
				converter.outputItem("response.output_item.done", index, item),
			}
		}
		item := converter.outputAt(index)
		item = codex.CompleteFunctionCall(item, converter.itemIDs[index], compactJSON(arguments))
		converter.setOutput(index, item)
		return []openai.StreamEvent{
			{
				Event: "response.function_call_arguments.done",
				Data: openai.FunctionCallArgumentsDoneEvent{
					Type:           "response.function_call_arguments.done",
					SequenceNumber: converter.next(),
					ItemID:         converter.itemIDs[index],
					OutputIndex:    converter.outputIndex(index),
					Arguments:      compactJSON(arguments),
				},
			},
			converter.outputItem("response.output_item.done", index, item),
		}
	}
	return nil
}
