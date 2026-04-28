package bridge

import (
	"encoding/json"

	"moonbridge/internal/extension/codex"
	"moonbridge/internal/foundation/openai"
	"moonbridge/internal/protocol/anthropic"
)

func (bridge *Bridge) ConvertStreamEvents(events []anthropic.StreamEvent, model string) []openai.StreamEvent {
	return bridge.ConvertStreamEventsWithContext(events, model, codex.ConversionContext{}, nil)
}

type StreamOptions struct {
	PersistFinalTextReasoning bool
}

func (bridge *Bridge) ConvertStreamEventsWithContext(events []anthropic.StreamEvent, model string, context codex.ConversionContext, extData map[string]any, opts ...StreamOptions) []openai.StreamEvent {
	var opt StreamOptions
	if len(opts) > 0 {
		opt = opts[0]
	}
	converter := streamConverter{
		bridge:               bridge,
		model:                model,
		persistTextReasoning: opt.PersistFinalTextReasoning,
		contentText:          map[int]string{},
		toolArguments:        map[int]string{},
		itemIDs:              map[int]string{},
		outputIndexes:        map[int]int{},
		extStreamStates:      bridge.hooks.NewStreamStates(model),
		codexStream:          codex.NewStreamAdapter(context),
	}
	var converted []openai.StreamEvent
	for _, event := range events {
		converted = append(converted, converter.convert(event)...)
	}

	// Let extensions persist stream state back to the session.
	if extData != nil {
		bridge.hooks.OnStreamComplete(model, converter.extStreamStates, converter.response.OutputText, extData)
	}

	return converted
}

type streamConverter struct {
	bridge               *Bridge
	model                string
	sequence             int64
	response             openai.Response
	contentText          map[int]string
	toolArguments        map[int]string
	persistTextReasoning bool
	itemIDs              map[int]string
	outputIndexes        map[int]int
	extStreamStates      map[string]any
	codexStream          *codex.StreamAdapter
}

func (converter *streamConverter) convert(event anthropic.StreamEvent) []openai.StreamEvent {
	switch event.Type {
	case "message_start":
		rid := responseID(event.Message.ID)
		converter.response = openai.Response{
			ID:     rid,
			Object: "response",
			Status: "in_progress",
			Model:  converter.model,
			Output: []openai.OutputItem{},
			Usage:  normalizeUsage(event.Message.Usage),
		}
		return []openai.StreamEvent{
			converter.lifecycle("response.created"),
			converter.lifecycle("response.in_progress"),
		}
	case "content_block_start":
		return converter.contentBlockStart(event)
	case "content_block_delta":
		return converter.contentBlockDelta(event)
	case "content_block_stop":
		return converter.contentBlockStop(event)
	case "message_delta":
		if event.Delta.StopReason != "" {
			converter.response.Status, converter.response.IncompleteDetails = statusFromStopReason(event.Delta.StopReason)
		}
		if event.Usage != nil {
			updated := normalizeUsage(*event.Usage)
			if converter.response.Usage.InputTokens > 0 {
				updated.InputTokens = converter.response.Usage.InputTokens
				updated.InputTokensDetails = converter.response.Usage.InputTokensDetails
			}
			updated.TotalTokens = updated.InputTokens + updated.OutputTokens
			converter.response.Usage = updated
		}
	case "message_stop":
		if converter.response.Status == "" || converter.response.Status == "in_progress" {
			converter.response.Status = "completed"
		}
		if converter.response.Status == "incomplete" {
			return []openai.StreamEvent{converter.lifecycle("response.incomplete")}
		}
		return []openai.StreamEvent{converter.lifecycle("response.completed")}
	case "error":
		converter.response.Status = "failed"
		if event.Error != nil {
			converter.response.Error = &openai.ErrorObject{Message: event.Error.Message, Type: "server_error", Code: event.Error.Type}
		}
		return []openai.StreamEvent{converter.lifecycle("response.failed")}
	}
	return nil
}

func (converter *streamConverter) outputAt(index int) openai.OutputItem {
	outputIndex := converter.sliceIndex(index)
	if outputIndex < len(converter.response.Output) {
		return converter.response.Output[outputIndex]
	}
	return openai.OutputItem{}
}

func (converter *streamConverter) setOutput(index int, item openai.OutputItem) {
	sliceIdx, ok := converter.outputIndexes[index]
	if !ok || sliceIdx < 0 || sliceIdx >= len(converter.response.Output) {
		return
	}
	converter.response.Output[sliceIdx] = item
}

func (converter *streamConverter) addOutput(index int, item openai.OutputItem) {
	converter.outputIndexes[index] = len(converter.response.Output)
	converter.response.Output = append(converter.response.Output, item)
}

// resetBlockState starts a fresh logical content block for providers that
// reuse Anthropic stream indexes while preserving already-emitted outputs.
func (converter *streamConverter) resetBlockState(index int) {
	delete(converter.contentText, index)
	delete(converter.toolArguments, index)
	delete(converter.itemIDs, index)
	delete(converter.outputIndexes, index)
	converter.codexStream.ResetBlock(index)
	converter.bridge.hooks.ResetStreamBlock(converter.model, index, converter.extStreamStates)
}

func (converter *streamConverter) hasOutput(index int) bool {
	_, ok := converter.outputIndexes[index]
	return ok
}

func (converter *streamConverter) removeOutput(index int) {
	sliceIdx, ok := converter.outputIndexes[index]
	if !ok {
		return
	}
	if sliceIdx == len(converter.response.Output)-1 {
		converter.response.Output = converter.response.Output[:sliceIdx]
		delete(converter.outputIndexes, index)
		delete(converter.itemIDs, index)
	}
}

// sliceIndex returns the position in the internal response.Output slice.
func (converter *streamConverter) sliceIndex(index int) int {
	return converter.outputIndexes[index]
}

// outputIndex returns the wire-level output index for SSE events.
func (converter *streamConverter) outputIndex(index int) int {
	return converter.outputIndexes[index]
}

func (converter *streamConverter) lifecycle(event string) openai.StreamEvent {
	converter.response.Status = statusForLifecycle(event, converter.response.Status)
	return openai.StreamEvent{
		Event: event,
		Data: openai.ResponseLifecycleEvent{
			Type:           event,
			SequenceNumber: converter.next(),
			Response:       converter.response,
		},
	}
}

func (converter *streamConverter) outputItem(event string, index int, item openai.OutputItem) openai.StreamEvent {
	return openai.StreamEvent{
		Event: event,
		Data: openai.OutputItemEvent{
			Type:           event,
			SequenceNumber: converter.next(),
			OutputIndex:    converter.outputIndex(index),
			Item:           item,
		},
	}
}

func (converter *streamConverter) outputItemAt(event string, outputIndex int, item openai.OutputItem) openai.StreamEvent {
	return openai.StreamEvent{
		Event: event,
		Data: openai.OutputItemEvent{
			Type:           event,
			SequenceNumber: converter.next(),
			OutputIndex:    outputIndex,
			Item:           item,
		},
	}
}

func (converter *streamConverter) emitPendingReasoningItem() []openai.StreamEvent {
	item, ok := converter.codexStream.PendingReasoningItem(len(converter.response.Output))
	if !ok {
		return nil
	}
	converter.response.Output = append(converter.response.Output, item)
	outputIndex := len(converter.response.Output) - 1
	return []openai.StreamEvent{
		converter.outputItemAt("response.output_item.added", outputIndex, item),
		converter.outputItemAt("response.output_item.done", outputIndex, item),
	}
}

func (converter *streamConverter) contentPart(event string, outputIndex int, contentIndex int, part openai.ContentPart) openai.StreamEvent {
	return openai.StreamEvent{
		Event: event,
		Data: openai.ContentPartEvent{
			Type:           event,
			SequenceNumber: converter.next(),
			ItemID:         converter.itemIDs[outputIndex],
			OutputIndex:    converter.outputIndex(outputIndex),
			ContentIndex:   contentIndex,
			Part:           part,
		},
	}
}

func (converter *streamConverter) next() int64 {
	converter.sequence++
	return converter.sequence
}

func statusForLifecycle(event string, current string) string {
	switch event {
	case "response.created", "response.in_progress":
		return "in_progress"
	case "response.completed":
		return "completed"
	case "response.incomplete":
		return "incomplete"
	case "response.failed":
		return "failed"
	default:
		return current
	}
}

func compactJSON(value string) string {
	var raw any
	if err := json.Unmarshal([]byte(value), &raw); err != nil {
		return value
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return value
	}
	return string(data)
}
