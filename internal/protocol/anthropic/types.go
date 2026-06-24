package anthropic

import "encoding/json"

type CacheControl struct {
	Type string `json:"type"`
	TTL  string `json:"ttl,omitempty"`
}

type MessageRequest struct {
	Model         string          `json:"model"`
	MaxTokens     int             `json:"max_tokens"`
	System        []ContentBlock  `json:"system,omitempty"`
	Messages      []Message       `json:"messages"`
	Tools         []Tool          `json:"tools,omitempty"`
	ToolChoice    *ToolChoice     `json:"tool_choice,omitempty"`
	Temperature   *float64        `json:"temperature,omitempty"`
	TopP          *float64        `json:"top_p,omitempty"`
	TopK          *int            `json:"top_k,omitempty"`
	StopSequences []string        `json:"stop_sequences,omitempty"`
	Metadata      map[string]any  `json:"metadata,omitempty"`
	Stream        bool            `json:"stream,omitempty"`
	CacheControl  *CacheControl   `json:"cache_control,omitempty"`
	Raw           json.RawMessage `json:"-"`
	Thinking      *ThinkingConfig `json:"thinking,omitempty"`
	OutputConfig  *OutputConfig   `json:"output_config,omitempty"`
}

type Message struct {
	Role    string         `json:"role"`
	Content []ContentBlock `json:"content"`
}

// UnmarshalJSON implements json.Unmarshaler for Message.
// Anthropic allows content to be either a string ("hello") or an array
// ([{"type": "text", "text": "hello"}]). This handles both formats.
func (m *Message) UnmarshalJSON(data []byte) error {
	// Raw struct to capture Role and optionally Content as raw JSON.
	var raw struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	m.Role = raw.Role

	if len(raw.Content) == 0 {
		m.Content = nil
		return nil
	}

	// Try as array of ContentBlock first.
	var blocks []ContentBlock
	if err := json.Unmarshal(raw.Content, &blocks); err == nil {
		m.Content = blocks
		return nil
	}

	// Try as plain string.
	var text string
	if err := json.Unmarshal(raw.Content, &text); err == nil && text != "" {
		m.Content = []ContentBlock{{Type: "text", Text: text}}
		return nil
	}

	// Fall through: return the original array-parse error.
	// (The standard library default error is more descriptive than ours.)
	return json.Unmarshal(raw.Content, &blocks)
}

type ContentBlock struct {
	Type         string          `json:"type"`
	Text         string          `json:"text,omitempty"`
	Thinking     string          `json:"thinking,omitempty"`
	Signature    string          `json:"signature,omitempty"`
	Source       *ImageSource    `json:"source,omitempty"`
	ID           string          `json:"id,omitempty"`
	Name         string          `json:"name,omitempty"`
	Input        json.RawMessage `json:"input,omitempty"`
	ToolUseID    string          `json:"tool_use_id,omitempty"`
	Content      any             `json:"content,omitempty"`
	CacheControl *CacheControl   `json:"cache_control,omitempty"`
}

type ImageSource struct {
	Type      string `json:"type"`
	URL       string `json:"url,omitempty"`
	MediaType string `json:"media_type,omitempty"`
	Data      string `json:"data,omitempty"`
}

// UnmarshalJSON implements json.Unmarshaler for MessageRequest.
// Anthropic allows system to be either a string or an array of ContentBlock.
func (m *MessageRequest) UnmarshalJSON(data []byte) error {
	// Raw struct mirrors MessageRequest but accepts system as raw JSON.
	type rawMessageRequest struct {
		Model         string          `json:"model"`
		MaxTokens     int             `json:"max_tokens"`
		System        json.RawMessage `json:"system,omitempty"`
		Messages      []Message       `json:"messages"`
		Tools         []Tool          `json:"tools,omitempty"`
		ToolChoice    *ToolChoice     `json:"tool_choice,omitempty"`
		Temperature   *float64        `json:"temperature,omitempty"`
		TopP          *float64        `json:"top_p,omitempty"`
		TopK          *int            `json:"top_k,omitempty"`
		StopSequences []string        `json:"stop_sequences,omitempty"`
		Metadata      map[string]any  `json:"metadata,omitempty"`
		Stream        bool            `json:"stream,omitempty"`
		CacheControl  *CacheControl   `json:"cache_control,omitempty"`
		Raw           json.RawMessage `json:"-"`
		Thinking      *ThinkingConfig `json:"thinking,omitempty"`
		OutputConfig  *OutputConfig   `json:"output_config,omitempty"`
	}
	var raw rawMessageRequest
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	m.Model = raw.Model
	m.MaxTokens = raw.MaxTokens
	m.Messages = raw.Messages
	m.Tools = raw.Tools
	m.ToolChoice = raw.ToolChoice
	m.Temperature = raw.Temperature
	m.TopP = raw.TopP
	m.TopK = raw.TopK
	m.StopSequences = raw.StopSequences
	m.Metadata = raw.Metadata
	m.Stream = raw.Stream
	m.CacheControl = raw.CacheControl
	m.Raw = raw.Raw
	m.Thinking = raw.Thinking
	m.OutputConfig = raw.OutputConfig

	// Handle system: string or []ContentBlock.
	if len(raw.System) > 0 {
		var blocks []ContentBlock
		if err := json.Unmarshal(raw.System, &blocks); err == nil {
			m.System = blocks
			return nil
		}
		var text string
		if err := json.Unmarshal(raw.System, &text); err == nil && text != "" {
			m.System = []ContentBlock{{Type: "text", Text: text}}
			return nil
		}
	}
	return nil
}

func (block ContentBlock) MarshalJSON() ([]byte, error) {
	type contentBlock ContentBlock
	if block.Type != "thinking" || block.Thinking != "" {
		return json.Marshal(contentBlock(block))
	}
	type thinkingBlock struct {
		Type         string        `json:"type"`
		Thinking     string        `json:"thinking"`
		Signature    string        `json:"signature,omitempty"`
		CacheControl *CacheControl `json:"cache_control,omitempty"`
	}
	return json.Marshal(thinkingBlock{
		Type:         block.Type,
		Thinking:     block.Thinking,
		Signature:    block.Signature,
		CacheControl: block.CacheControl,
	})
}

type Tool struct {
	Name         string         `json:"name"`
	Type         string         `json:"type,omitempty"`
	Description  string         `json:"description,omitempty"`
	InputSchema  map[string]any `json:"input_schema,omitempty"`
	MaxUses      int            `json:"max_uses,omitempty"`
	CacheControl *CacheControl  `json:"cache_control,omitempty"`
}

type ToolChoice struct {
	Type string `json:"type,omitempty"`
	Name string `json:"name,omitempty"`
}

func (choice ToolChoice) IsZero() bool {
	return choice.Type == "" && choice.Name == ""
}

type MessageResponse struct {
	ID           string         `json:"id"`
	Type         string         `json:"type"`
	Role         string         `json:"role"`
	Model        string         `json:"model,omitempty"`
	Content      []ContentBlock `json:"content,omitempty"`
	StopReason   string         `json:"stop_reason,omitempty"`
	StopSequence string         `json:"stop_sequence,omitempty"`
	Usage        Usage          `json:"usage,omitempty"`
}

type Usage struct {
	InputTokens              int            `json:"input_tokens,omitempty"`
	OutputTokens             int            `json:"output_tokens,omitempty"`
	CacheCreationInputTokens int            `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens     int            `json:"cache_read_input_tokens,omitempty"`
	CacheCreation            *CacheCreation `json:"cache_creation,omitempty"`
}

type CacheCreation struct {
	Ephemeral5mInputTokens int `json:"ephemeral_5m_input_tokens,omitempty"`
	Ephemeral1hInputTokens int `json:"ephemeral_1h_input_tokens,omitempty"`
}

type StreamEvent struct {
	Type         string           `json:"type"`
	Message      *MessageResponse `json:"message,omitempty"`
	Index        int              `json:"index,omitempty"`
	ContentBlock *ContentBlock    `json:"content_block,omitempty"`
	Delta        StreamDelta      `json:"delta,omitempty"`
	Usage        *Usage           `json:"usage,omitempty"`
	Error        *ErrorObject     `json:"error,omitempty"`
}

type StreamDelta struct {
	Type        string `json:"type,omitempty"`
	Text        string `json:"text,omitempty"`
	Thinking    string `json:"thinking,omitempty"`
	Signature   string `json:"signature,omitempty"`
	PartialJSON string `json:"partial_json,omitempty"`
	StopReason  string `json:"stop_reason,omitempty"`
}

type ErrorObject struct {
	Type    string `json:"type,omitempty"`
	Message string `json:"message,omitempty"`
}
type ThinkingConfig struct {
	Type         string `json:"type,omitempty"`
	BudgetTokens int    `json:"budget_tokens,omitempty"`
}

type OutputConfig struct {
	Effort string `json:"effort,omitempty"`
}
