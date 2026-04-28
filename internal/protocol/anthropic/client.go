package anthropic

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"moonbridge/internal/foundation/logger"
	"net/http"
	"strings"
)

type ClientConfig struct {
	BaseURL   string
	APIKey    string
	Version   string
	UserAgent string
	Client    *http.Client
}

type Client struct {
	baseURL   string
	apiKey    string
	version   string
	userAgent string
	client    *http.Client
}

type ProviderError struct {
	StatusCode int
	Type       string
	Message    string
	RequestID  string
}

func (err *ProviderError) Error() string {
	if err.Message != "" {
		return err.Message
	}
	return http.StatusText(err.StatusCode)
}

type Stream interface {
	Next() (StreamEvent, error)
	Close() error
}

func NewClient(cfg ClientConfig) *Client {
	httpClient := cfg.Client
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &Client{
		baseURL:   strings.TrimRight(cfg.BaseURL, "/"),
		apiKey:    cfg.APIKey,
		version:   cfg.Version,
		userAgent: strings.TrimSpace(cfg.UserAgent),
		client:    httpClient,
	}
}

func (client *Client) CreateMessage(ctx context.Context, request MessageRequest) (MessageResponse, error) {
	request.Stream = false
	log := logger.L().With("model", request.Model)
	log.Debug("正在创建消息", "max_tokens", request.MaxTokens, "messages", len(request.Messages), "tools", len(request.Tools))

	httpRequest, err := client.newRequest(ctx, request)
	if err != nil {
		log.Error("构建请求失败", "error", err)
		return MessageResponse{}, err
	}

	response, err := client.client.Do(httpRequest)
	if err != nil {
		log.Error("请求失败", "error", err)
		return MessageResponse{}, err
	}
	defer response.Body.Close()

	if response.StatusCode < 200 || response.StatusCode > 299 {
		err := decodeProviderError(response)
		log.Error("提供商错误", "status", response.StatusCode, "error", err)
		return MessageResponse{}, err
	}

	var message MessageResponse
	if err := json.NewDecoder(response.Body).Decode(&message); err != nil {
		log.Error("解析响应失败", "error", err)
		return MessageResponse{}, err
	}
	log.Info("消息已创建", "id", message.ID, "stop_reason", message.StopReason, "input_tokens", message.Usage.InputTokens, "output_tokens", message.Usage.OutputTokens)
	return message, nil
}

func (client *Client) StreamMessage(ctx context.Context, request MessageRequest) (Stream, error) {
	request.Stream = true
	log := logger.L().With("model", request.Model)
	log.Debug("开始流式传输", "max_tokens", request.MaxTokens, "messages", len(request.Messages), "tools", len(request.Tools))

	httpRequest, err := client.newRequest(ctx, request)
	if err != nil {
		log.Error("构建请求失败", "error", err)
		return nil, err
	}

	response, err := client.client.Do(httpRequest)
	if err != nil {
		log.Error("请求失败", "error", err)
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode > 299 {
		defer response.Body.Close()
		err := decodeProviderError(response)
		log.Error("提供商错误", "status", response.StatusCode, "error", err)
		return nil, err
	}

	log.Debug("流已连接")
	return &sseStream{body: response.Body, scanner: bufio.NewScanner(response.Body)}, nil
}

func (client *Client) ProbeWebSearch(ctx context.Context, model string) (bool, error) {
	stream, err := client.StreamMessage(ctx, MessageRequest{
		Model:     model,
		MaxTokens: 1,
		Messages: []Message{{
			Role:    "user",
			Content: []ContentBlock{{Type: "text", Text: "Reply with ok. Do not search."}},
		}},
		Tools: []Tool{{
			Name:    "web_search",
			Type:    "web_search_20250305",
			MaxUses: 1,
		}},
		ToolChoice: ToolChoice{Type: "auto"},
	})
	if err != nil {
		if IsUnsupportedWebSearchError(err) {
			return false, nil
		}
		// Context deadline exceeded means the provider didn't respond in time.
		// Treat as unsupported so the caller disables web search gracefully.
		if errors.Is(err, context.DeadlineExceeded) {
			return false, nil
		}
		return false, err
	}
	defer stream.Close()
	for {
		event, err := stream.Next()
		if err == io.EOF {
			return true, nil
		}
		if err != nil {
			// Context deadline exceeded during streaming means the probe
			// couldn't complete; treat as unsupported.
			if errors.Is(err, context.DeadlineExceeded) {
				return false, nil
			}
			return false, err
		}
		if event.Type == "error" && event.Error != nil {
			providerError := &ProviderError{StatusCode: http.StatusBadRequest, Type: event.Error.Type, Message: event.Error.Message}
			if IsUnsupportedWebSearchError(providerError) {
				return false, nil
			}
			return false, providerError
		}
		switch event.Type {
		case "message_start", "content_block_start", "content_block_delta", "message_delta", "message_stop":
			return true, nil
		case "ping":
			continue
		default:
			return true, nil
		}
	}
}

func (client *Client) newRequest(ctx context.Context, messageRequest MessageRequest) (*http.Request, error) {
	data, err := json.Marshal(messageRequest)
	if err != nil {
		return nil, err
	}

	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, client.baseURL+"/v1/messages", bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	httpRequest.Header.Set("content-type", "application/json")
	httpRequest.Header.Set("x-api-key", client.apiKey)
	if client.version != "" {
		httpRequest.Header.Set("anthropic-version", client.version)
	}
	if client.userAgent != "" {
		httpRequest.Header.Set("user-agent", client.userAgent)
	}
	return httpRequest, nil
}

func decodeProviderError(response *http.Response) error {
	body, _ := io.ReadAll(response.Body)
	providerError := &ProviderError{StatusCode: response.StatusCode, RequestID: response.Header.Get("request-id")}

	var payload struct {
		Error ErrorObject `json:"error"`
	}
	if err := json.Unmarshal(body, &payload); err == nil {
		providerError.Type = payload.Error.Type
		providerError.Message = payload.Error.Message
	}
	if providerError.Message == "" {
		providerError.Message = string(body)
	}
	if providerError.Message == "" {
		providerError.Message = http.StatusText(response.StatusCode)
	}
	return providerError
}

type sseStream struct {
	body    io.Closer
	scanner *bufio.Scanner
	event   string
	data    strings.Builder
}

func (stream *sseStream) Next() (StreamEvent, error) {
	for stream.scanner.Scan() {
		line := stream.scanner.Text()
		if line == "" {
			if stream.data.Len() == 0 {
				continue
			}
			data := stream.data.String()
			stream.data.Reset()
			var event StreamEvent
			if err := json.Unmarshal([]byte(data), &event); err != nil {
				return StreamEvent{}, err
			}
			if event.Type == "" {
				event.Type = stream.event
			}
			stream.event = ""
			return event, nil
		}
		if strings.HasPrefix(line, "event:") {
			stream.event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			continue
		}
		if strings.HasPrefix(line, "data:") {
			if stream.data.Len() > 0 {
				stream.data.WriteByte('\n')
			}
			stream.data.WriteString(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	if err := stream.scanner.Err(); err != nil {
		return StreamEvent{}, err
	}
	if stream.data.Len() > 0 {
		data := stream.data.String()
		stream.data.Reset()
		var event StreamEvent
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			return StreamEvent{}, err
		}
		if event.Type == "" {
			event.Type = stream.event
		}
		return event, nil
	}
	return StreamEvent{}, io.EOF
}

func (stream *sseStream) Close() error {
	if stream.body == nil {
		return nil
	}
	return stream.body.Close()
}

func IsProviderError(err error) (*ProviderError, bool) {
	var providerError *ProviderError
	if errors.As(err, &providerError) {
		return providerError, true
	}
	return nil, false
}

func IsUnsupportedWebSearchError(err error) bool {
	providerError, ok := IsProviderError(err)
	if !ok {
		return false
	}
	if providerError.StatusCode != http.StatusBadRequest && providerError.StatusCode != http.StatusUnprocessableEntity {
		return false
	}
	message := strings.ToLower(providerError.Type + " " + providerError.Message)
	if !strings.Contains(message, "web_search") {
		return strings.Contains(message, "input_schema") || strings.Contains(message, "tools")
	}
	for _, marker := range []string{"unsupported", "not supported", "not_support", "unknown", "invalid", "unrecognized", "input_schema", "field required", "missing"} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}

func (err *ProviderError) OpenAIStatus() int {
	switch err.StatusCode {
	case http.StatusUnauthorized:
		return http.StatusUnauthorized
	case http.StatusForbidden:
		return http.StatusForbidden
	case http.StatusTooManyRequests:
		return http.StatusTooManyRequests
	case http.StatusGatewayTimeout:
		return http.StatusGatewayTimeout
	}
	if err.StatusCode >= 500 {
		return http.StatusBadGateway
	}
	if err.StatusCode >= 400 {
		return http.StatusBadRequest
	}
	return http.StatusBadGateway
}

func (err *ProviderError) OpenAICode() string {
	switch err.StatusCode {
	case http.StatusUnauthorized:
		return "invalid_api_key"
	case http.StatusForbidden:
		return "permission_denied"
	case http.StatusTooManyRequests:
		return "rate_limit_exceeded"
	case http.StatusGatewayTimeout:
		return "provider_timeout"
	}
	if err.StatusCode >= 500 {
		return "provider_error"
	}
	return "invalid_request_error"
}

func (err *ProviderError) OpenAIType() string {
	if err.StatusCode >= 500 {
		return "server_error"
	}
	return "invalid_request_error"
}

func UnsupportedStreamEvent(event string) error {
	return fmt.Errorf("unsupported stream event %q", event)
}
