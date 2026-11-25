package models

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"image/color"
	"net/http"
	"strings"
	"testing"

	"github.com/rickchristie/gent"
	"github.com/stretchr/testify/assert"
)

func TestResponsesImageAnswerParts(t *testing.T) {
	type input struct {
		provider   string
		modalities []string
		items      []any
	}
	type expected struct {
		parts  []gent.ContentPart
		source *gent.ContentSource
		err    error
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	img := imageFixture(t, 2, color.RGBA{B: 255, A: 255})
	imageItem := map[string]any{"type": "image_generation_call", "id": "unsaved-image",
		"status": "completed", "output_format": "jpeg",
		"result": base64.StdEncoding.EncodeToString(img.Data)}
	message := func(text string) any {
		return map[string]any{"type": "message", "role": "assistant", "content": []any{
			map[string]any{"type": "output_text", "text": text}}}
	}
	source := func(ids ...string) *gent.ContentSource { return &gent.ContentSource{MessageIds: ids} }
	imageOnly := []string{"image"}
	const ref = `<grok type="image" image_id="68cd18bb-40fc-43ca-8dc9-01dfe69979c7" />`
	const second = `<grok type="image" image_id="5e432785_call-b2f34d5b-07db-4029-983f-0" />`
	const generate = `<grok type="imagine_text_to_image" prompt="A blue square" aspect_ratio="1:1" />`
	const render = `<grok type="render_image" image_id="picture-1" />`
	const function = `<grok:function_call name="imagine_text_to_image">
<parameter name="prompt">An orange can &amp; two stones</parameter>
<parameter name="aspect_ratio">1:1</parameter>
</grok:function_call>`
	const unknown = `<grok type="text" prompt="A blue square" />`
	const nested = `<grok type="image" image_id="one">Here is the image.</grok>`
	const invalid = `<grok type="image" image_id="" />`
	const caption = "Here is the image."
	cases := []testCase{
		{"empty text before image", input{"openai", nil, []any{message(""), imageItem}},
			expected{[]gent.ContentPart{img}, source("output:1"), nil}},
		{"empty text after image", input{"openai", nil, []any{imageItem, message("")}},
			expected{[]gent.ContentPart{img}, source("output:0"), nil}},
		{"text before image", input{"openai", nil, []any{message(caption), imageItem}}, expected{
			[]gent.ContentPart{gent.TextPart(caption), img}, source("output:0", "output:1"), nil}},
		{"text after image", input{"openai", nil, []any{imageItem, message(caption)}}, expected{
			[]gent.ContentPart{img, gent.TextPart(caption)}, source("output:0", "output:1"), nil}},
		{"whitespace remains content", input{"openai", nil, []any{imageItem, message(" ")}}, expected{
			[]gent.ContentPart{img, gent.TextPart(" ")}, source("output:0", "output:1"), nil}},
		{"xAI reference after image", input{"xai", imageOnly, []any{imageItem, message(ref)}},
			expected{[]gent.ContentPart{img}, source("output:0"), nil}},
		{"xAI references before image", input{"xai", imageOnly, []any{message(ref + second), imageItem}},
			expected{[]gent.ContentPart{img}, source("output:1"), nil}},
		{"xAI generation markup", input{"xai", imageOnly, []any{message(generate), imageItem}},
			expected{[]gent.ContentPart{img}, source("output:1"), nil}},
		{"xAI function before image", input{"xai", imageOnly, []any{message(function), imageItem}},
			expected{[]gent.ContentPart{img}, source("output:1"), nil}},
		{"xAI render and image function after image", input{"xai", imageOnly,
			[]any{imageItem, message(render + function)}}, expected{[]gent.ContentPart{img},
			source("output:0"), nil}},
		{"xAI render keeps earlier caption", input{"xai", imageOnly,
			[]any{message(caption), imageItem, message(render)}}, expected{
			[]gent.ContentPart{gent.TextPart(caption), img}, source("output:0", "output:1"), nil}},
		{"xAI image function without native image", input{"xai", imageOnly, []any{message(function)}},
			expected{[]gent.ContentPart{gent.TextPart(function)}, source("output:0"), nil}},
		{"xAI mixed output includes image function", input{"xai", []string{"image", "text"},
			[]any{imageItem, message(function)}}, expected{[]gent.ContentPart{img, gent.TextPart(function)},
			source("output:0", "output:1"), nil}},
		{"xAI generation and display markup", input{"xai", imageOnly,
			[]any{imageItem, message(generate + ref)}}, expected{[]gent.ContentPart{img},
			source("output:0"), nil}},
		{"xAI reference without image", input{"xai", imageOnly, []any{message(ref)}},
			expected{[]gent.ContentPart{gent.TextPart(ref)}, source("output:0"), nil}},
		{"xAI reference preserves earlier caption", input{"xai", imageOnly,
			[]any{message(caption), imageItem, message(ref)}}, expected{
			[]gent.ContentPart{gent.TextPart(caption), img}, source("output:0", "output:1"), nil}},
		{"xAI caption with reference", input{"xai", imageOnly, []any{imageItem, message(ref + caption)}},
			expected{[]gent.ContentPart{img, gent.TextPart(ref + caption)}, source("output:0", "output:1"),
				nil}},
		{"xAI invalid reference", input{"xai", imageOnly, []any{imageItem, message(invalid)}},
			expected{[]gent.ContentPart{img, gent.TextPart(invalid)}, source("output:0", "output:1"), nil}},
		{"xAI unrelated markup", input{"xai", imageOnly, []any{imageItem, message(unknown)}},
			expected{[]gent.ContentPart{img, gent.TextPart(unknown)}, source("output:0", "output:1"), nil}},
		{"xAI nested caption", input{"xai", imageOnly, []any{imageItem, message(nested)}},
			expected{[]gent.ContentPart{img, gent.TextPart(nested)}, source("output:0", "output:1"), nil}},
		{"xAI mixed output", input{"xai", []string{"image", "text"}, []any{imageItem, message(ref)}},
			expected{[]gent.ContentPart{img, gent.TextPart(ref)}, source("output:0", "output:1"), nil}},
		{"OpenAI literal reference", input{"openai", imageOnly, []any{imageItem, message(ref)}},
			expected{[]gent.ContentPart{img, gent.TextPart(ref)}, source("output:0", "output:1"), nil}},
	}
	type literalCase struct{ name, text string }
	for _, literal := range []literalCase{
		{"caption beside function", function + caption},
		{"caption inside function", strings.Replace(function, "</grok:", caption+"</grok:", 1)},
		{"unrelated function", strings.Replace(function, "imagine_text_to_image", "lookup", 1)},
		{"missing prompt", strings.Replace(function, `name="prompt"`, `name="size"`, 1)},
		{"empty prompt", strings.Replace(function, "An orange can &amp; two stones", " ", 1)},
		{"duplicate prompt", strings.Replace(function, `name="aspect_ratio"`, `name="prompt"`, 1)},
		{"missing render image ID", `<grok type="render_image" />`},
		{"nested argument", strings.Replace(function, "two stones", "<caption>stones</caption>", 1)},
		{"unrecognized element", strings.ReplaceAll(function, "parameter", "caption")},
		{"incomplete function", strings.TrimSuffix(function, ">")},
	} {
		cases = append(cases, testCase{literal.name, input{"xai", imageOnly,
			[]any{imageItem, message(literal.text)}}, expected{[]gent.ContentPart{img,
			gent.TextPart(literal.text)}, source("output:0", "output:1"), nil}})
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			items := test.input.items
			payload, err := json.Marshal(map[string]any{"status": "completed", "output": items})
			if !assert.NoError(t, err) {
				return
			}
			client := &http.Client{Transport: responseTransport(func(*http.Request) (*http.Response, error) {
				return fixtureHTTP(string(payload), "application/json"), nil
			})}
			request := gent.NewModelCallRequest([]*gent.ModelMessage{
				gent.TextParts(gent.ChatMessageTypeHuman, "Generate an image.")})
			request.Options.ResponseModalities = test.input.modalities
			stream, err := fixtureModel(test.input.provider, client).GenerateContentStream(
				context.Background(), request)
			if !assert.Equal(t, test.expected.err, err) {
				return
			}
			defer stream.Close()
			for range stream.Chunks() {
			}
			response, err := stream.Response()
			if !assert.Equal(t, test.expected.err, err) || !assert.Len(t, response.Choices, 1) {
				return
			}
			choice := response.Choices[0]
			assert.Equal(t, test.expected, expected{choice.CandidateContent(), choice.CandidateSource, err})
			assert.Equal(t, gent.ModelCompletion{Status: gent.ModelStatusAnswer,
				ProviderReason: "completed"}, choice.Completion)
			original, err := json.Marshal(items)
			assert.NoError(t, err)
			saved, err := json.Marshal(choice.Continuation.Items)
			assert.NoError(t, err)
			assert.JSONEq(t, string(original), string(saved))
		})
	}
}

func TestResponsesImageReplay(t *testing.T) {
	type input struct {
		provider, item string
	}
	type expected struct {
		messages string
		requests int
		err      string
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	const raw = `{"type":"image_generation_call","id":"unsaved-image","status":"completed",
"output_format":"jpeg","result":"AQID"}`
	const image = `[{"role":"user","content":[
{"type":"input_text","text":"Image from the previous assistant response:"},
{"type":"input_image","image_url":"data:image/jpeg;base64,AQID"}]}]`
	cases := []testCase{
		{"OpenAI owned bytes", input{"openai", raw}, expected{image, 1, ""}},
		{"xAI native item", input{"xai", raw}, expected{"[" + raw + "]", 1, ""}},
		{"OpenAI default PNG", input{"openai", `{"type":"image_generation_call","result":"AQID"}`},
			expected{`[{"role":"user","content":[
{"type":"input_text","text":"Image from the previous assistant response:"},
{"type":"input_image","image_url":"data:image/png;base64,AQID"}]}]`, 1, ""}},
		{"OpenAI missing bytes", input{"openai", `{"type":"image_generation_call","id":"unsaved"}`},
			expected{requests: 0, err: "saved image generation item has no image bytes"}},
		{"invalid item", input{"openai", `{`},
			expected{requests: 0, err: "invalid saved provider item"}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			var body map[string]json.RawMessage
			actual := expected{}
			client := &http.Client{Transport: responseTransport(func(request *http.Request) (
				*http.Response, error,
			) {
				actual.requests++
				if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
					return nil, err
				}
				return fixtureHTTP(providerResponse(test.input.provider, 3), "application/json"), nil
			})}
			saved := &gent.ProviderContinuation{Provider: test.input.provider,
				Items: []json.RawMessage{json.RawMessage(test.input.item)}}
			stream, err := fixtureModel(test.input.provider, client).GenerateContentStream(
				context.Background(), gent.NewModelCallRequest([]*gent.ModelMessage{
					{Role: gent.ChatMessageTypeAI, Continuation: saved},
				}))
			if err == nil {
				defer stream.Close()
				for range stream.Chunks() {
				}
				_, err = stream.Response()
			}
			if err != nil {
				actual.err = err.Error()
			}
			if len(body) > 0 {
				assert.JSONEq(t, test.expected.messages, string(body["input"]))
				actual.messages = test.expected.messages
			}
			assert.Equal(t, test.expected, actual)
			assert.Equal(t, test.input.item, string(saved.Items[0]))
		})
	}
}
