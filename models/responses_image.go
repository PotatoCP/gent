package models

import (
	"bytes"
	"encoding/xml"
	"io"
	"strings"
)

// Grok can return image tool calls and display references as assistant text alongside native
// image bytes. Only a whole message of empty image tags is presentation markup. Keep any other
// content, including captions and malformed tags, available for normal output validation.
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
		if !ok || tag.Name != (xml.Name{Local: "grok"}) {
			return false
		}
		var image struct {
			Kind    string `xml:"type,attr"`
			ImageID string `xml:"image_id,attr"`
			Prompt  string `xml:"prompt,attr"`
			Body    string `xml:",innerxml"`
		}
		if err := decoder.DecodeElement(&image, &tag); err != nil || image.Body != "" {
			return false
		}
		reference := image.Kind == "image" && strings.TrimSpace(image.ImageID) != ""
		generation := image.Kind == "imagine_text_to_image" && strings.TrimSpace(image.Prompt) != ""
		if !reference && !generation {
			return false
		}
	}
}
