package models

import (
	"bytes"
	"encoding/xml"
	"io"
	"strings"
)

// Grok can return image tool calls and display references as assistant text alongside native
// image bytes. Classify the whole message so a caption beside a tag still reaches validation.
func xaiImageMarkupOnly(text string) bool {
	if !strings.HasPrefix(strings.TrimSpace(text), "<grok") {
		return false
	}
	decoder := xml.NewDecoder(strings.NewReader(text))
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			return true
		}
		if err != nil {
			return false
		}
		if space, ok := token.(xml.CharData); ok && len(bytes.TrimSpace(space)) == 0 {
			continue
		}
		tag, ok := token.(xml.StartElement)
		if !ok {
			return false
		}
		switch tag.Name {
		case xml.Name{Local: "grok"}:
			if !xaiImageTag(decoder, &tag) {
				return false
			}
		case xml.Name{Space: "grok", Local: "function_call"}:
			// Grok omits the namespace declaration. encoding/xml keeps the prefix in Name.Space.
			if !xaiImageFunction(decoder, &tag) {
				return false
			}
		default:
			return false
		}
	}
}

func xaiImageTag(decoder *xml.Decoder, tag *xml.StartElement) bool {
	var image struct {
		Kind    string `xml:"type,attr"`
		ImageID string `xml:"image_id,attr"`
		Prompt  string `xml:"prompt,attr"`
		Body    string `xml:",innerxml"`
	}
	if err := decoder.DecodeElement(&image, tag); err != nil || image.Body != "" {
		return false
	}
	switch image.Kind {
	case "image", "render_image":
		return strings.TrimSpace(image.ImageID) != ""
	case "imagine_text_to_image":
		return strings.TrimSpace(image.Prompt) != ""
	default:
		return false
	}
}

// A Grok image call can encode its prompt and options as named parameters. Text outside the
// parameters, an unrelated function, or a nested element can be part of a literal answer.
func xaiImageFunction(decoder *xml.Decoder, tag *xml.StartElement) bool {
	var call struct {
		Name       string `xml:"name,attr"`
		Text       string `xml:",chardata"`
		Parameters []struct {
			XMLName xml.Name
			Name    string    `xml:"name,attr"`
			Value   string    `xml:",chardata"`
			Child   *struct{} `xml:",any"`
		} `xml:",any"`
	}
	if err := decoder.DecodeElement(&call, tag); err != nil ||
		call.Name != "imagine_text_to_image" || strings.TrimSpace(call.Text) != "" {
		return false
	}
	prompts := 0
	for _, parameter := range call.Parameters {
		if parameter.XMLName != (xml.Name{Local: "parameter"}) || parameter.Child != nil ||
			strings.TrimSpace(parameter.Name) == "" || strings.TrimSpace(parameter.Value) == "" {
			return false
		}
		if parameter.Name == "prompt" {
			prompts++
		}
	}
	return prompts == 1
}
