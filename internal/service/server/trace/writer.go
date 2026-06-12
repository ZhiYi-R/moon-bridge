// Package trace provides request/response trace writing.
//
// It extracts trace recording from the Server god object into an
// independent service sub-package with a clean Writer interface.
package trace

import (
	"fmt"
	"io"

	mbtrace "moonbridge/internal/service/trace"
)

// Writer is the interface for recording request/response traces.
type Writer interface {
	// WriteTrace records a complete trace record, dispatching to the
	// appropriate category (Chat, Response, Anthropic) as needed.
	WriteTrace(record mbtrace.Record)

	// WriteCategory writes a trace record for a specific category
	// (e.g. "Response", "Anthropic", "Chat").
	WriteCategory(category string, requestNumber uint64, record mbtrace.Record)
}

// FileWriter implements Writer by delegating to a *mbtrace.Tracer.
type FileWriter struct {
	tracer *mbtrace.Tracer
	errors io.Writer
}

// NewFileWriter creates a new FileWriter.
func NewFileWriter(tracer *mbtrace.Tracer, errors io.Writer) *FileWriter {
	return &FileWriter{tracer: tracer, errors: errors}
}

// WriteTrace dispatches a record to the appropriate trace categories.
func (w *FileWriter) WriteTrace(record mbtrace.Record) {
	if w.tracer == nil || !w.tracer.Enabled() {
		return
	}
	requestNumber := w.tracer.NextRequestNumber()

	if shouldWriteChatTrace(record) {
		w.WriteCategory("Chat", requestNumber, mbtrace.Record{
			HTTPRequest:      record.HTTPRequest,
			Model:            record.Model,
			ChatRequest:      record.ChatRequest,
			ChatResponse:     record.ChatResponse,
			ChatStreamEvents: record.ChatStreamEvents,
			Error:            record.Error,
		})
	}

	if shouldWriteResponseTrace(record) {
		w.WriteCategory("Response", requestNumber, mbtrace.Record{
			HTTPRequest:        record.HTTPRequest,
			OpenAIRequest:      record.OpenAIRequest,
			Model:              record.Model,
			OpenAIResponse:     record.OpenAIResponse,
			OpenAIStreamEvents: record.OpenAIStreamEvents,
			UpstreamRequest:    record.UpstreamRequest,
			UpstreamResponse:   record.UpstreamResponse,
			Error:              record.Error,
		})
	}

	if shouldWriteAnthropicTrace(record) {
		w.WriteCategory("Anthropic", requestNumber, mbtrace.Record{
			HTTPRequest:           record.HTTPRequest,
			AnthropicRequest:      record.AnthropicRequest,
			Model:                 record.Model,
			AnthropicResponse:     record.AnthropicResponse,
			AnthropicStreamEvents: record.AnthropicStreamEvents,
			Error:                 record.Error,
		})
	}
}

// WriteCategory writes a trace record to a specific category directory.
func (w *FileWriter) WriteCategory(category string, requestNumber uint64, record mbtrace.Record) {
	if _, err := w.tracer.WriteNumbered(category, requestNumber, record); err != nil && w.errors != nil {
		fmt.Fprintf(w.errors, "跟踪 %s 写入失败: %v\n", category, err)
	}
}

// Helper functions duplicated from dispatch.go for encapsulation.

func shouldWriteResponseTrace(record mbtrace.Record) bool {
	return record.OpenAIRequest != nil || record.OpenAIResponse != nil || record.OpenAIStreamEvents != nil || record.UpstreamRequest != nil
}

func shouldWriteAnthropicTrace(record mbtrace.Record) bool {
	return record.AnthropicRequest != nil || record.AnthropicResponse != nil || record.AnthropicStreamEvents != nil
}

func shouldWriteChatTrace(record mbtrace.Record) bool {
	return record.ChatRequest != nil || record.ChatResponse != nil || record.ChatStreamEvents != nil
}
