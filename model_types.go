package gent

import (
	"context"
	core "github.com/rickchristie/gent/internal/core"
)

// ModelMessage is the provider-neutral request message. Continuation contains the exact ordered
// provider items for an assistant turn. An adapter must preserve them or reject the request.
type ModelMessage = core.ModelMessage

type ProviderContinuation = core.ProviderContinuation

type ChatMessageType = core.ChatMessageType

// ChatMessageTypeSystem identifies system instructions outside compacted conversation input.
const ChatMessageTypeSystem ChatMessageType = core.ChatMessageTypeSystem

// ChatMessageTypeHuman identifies a user-role message; saved origin determines user authorship.
const ChatMessageTypeHuman ChatMessageType = core.ChatMessageTypeHuman

// ChatMessageTypeAI identifies an assistant response or continuation.
const ChatMessageTypeAI ChatMessageType = core.ChatMessageTypeAI

// ChatMessageTypeTool identifies prepared tool results supplied to the model.
const ChatMessageTypeTool ChatMessageType = core.ChatMessageTypeTool

// ContentPart is implemented by the supported text, media, and native call values.
type ContentPart = core.ContentPart

type TextContent = core.TextContent

type ImageURLContent = core.ImageURLContent

type BinaryContent = core.BinaryContent

type FunctionCall = core.FunctionCall

type ModelToolCallRequest = core.ModelToolCallRequest

type ToolDefinition = core.ToolDefinition

type FunctionDefinition = core.FunctionDefinition

type ToolChoice = core.ToolChoice

type FunctionReference = core.FunctionReference

type CacheControl = core.CacheControl

type CachedContent = core.CachedContent

func TextParts(role ChatMessageType, texts ...string) *ModelMessage {
	return core.TextParts(role, texts...)
}

// ImageURLPart retains a remote reference. The caller owns URL expiry and content changes,
// including changes between requests or after recovery. Use BytesPart to preserve content.
// Adapters send the URL when supported, otherwise load the image for that request. A load
// failure becomes safe steering text; Gent does not repeat the source tool.
func ImageURLPart(url string) ContentPart {
	return core.ImageURLPart(url)
}

func BinaryPart(mime string, data []byte) BinaryContent {
	return core.BinaryPart(mime, data)
}

// CallOptions describes provider controls independently of an SDK. Unsupported settings must
// produce an adapter error; silently dropping a setting can change execution behavior.
// Temperature and TopP are optional. A pointer preserves an explicit zero.
type CallOptions = core.CallOptions

type CallOption = core.CallOption

func WithModel(model string) CallOption {
	return core.WithModel(model)
}

func WithMaxTokens(n int) CallOption {
	return core.WithMaxTokens(n)
}

func WithTemperature(n float64) CallOption {
	return core.WithTemperature(n)
}

func WithTopP(n float64) CallOption {
	return core.WithTopP(n)
}

func WithTools(tools []*ToolDefinition) CallOption {
	return core.WithTools(tools)
}

func WithOptions(options *CallOptions) CallOption {
	return core.WithOptions(options)
}

// WithStreamingFunc receives answer or unlabelled text. Read Stream.Chunks for all channels,
// including provider-labelled commentary, without mixing them into the answer callback.
func WithStreamingFunc(f func(context.Context, []byte) error) CallOption {
	return core.WithStreamingFunc(f)
}

func WithStreamingReasoningFunc(f func(context.Context, []byte, []byte) error) CallOption {
	return core.WithStreamingReasoningFunc(f)
}

// ThinkingMode passes an open provider-native mode name. It is not a closed Gent enum.
type ThinkingMode = core.ThinkingMode

type ThinkingConfig = core.ThinkingConfig

func WithStreamThinking(enabled bool) CallOption {
	return core.WithStreamThinking(enabled)
}
