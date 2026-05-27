package agent

import "github.com/mdzunic/cli-agent/internal/ollama"

const maxMessages = 40 // keep last N messages to avoid context overflow

// ConvContext manages conversation history.
type ConvContext struct {
	messages []ollama.Message
}

func NewConvContext() *ConvContext {
	return &ConvContext{}
}

func (c *ConvContext) Add(msg ollama.Message) {
	c.messages = append(c.messages, msg)
	// Sliding window: drop older messages (but never the first system message).
	if len(c.messages) > maxMessages {
		c.messages = c.messages[len(c.messages)-maxMessages:]
	}
}

func (c *ConvContext) Messages() []ollama.Message {
	return c.messages
}

func (c *ConvContext) Clear() {
	c.messages = nil
}

func (c *ConvContext) Set(msgs []ollama.Message) {
	c.messages = msgs
}

func (c *ConvContext) Len() int {
	return len(c.messages)
}
