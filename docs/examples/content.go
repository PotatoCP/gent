package examples

import "github.com/rickchristie/gent"

func mediaParts(png, pdf, audio, video []byte) ([]gent.ContentPart, error) {
	// docs:start content-parts
	parts, err := gent.NewParts().
		Text("Read the attached files.").
		Data(map[string]any{"order_id": "ABC123"}).
		Build()
	if err != nil {
		return nil, err
	}
	// docs:end content-parts
	// docs:start content-media
	parts = append(parts,
		gent.PNGPart(png),
		gent.PDFPart(pdf),
		gent.AudioPart("audio/wav", audio),
		gent.VideoPart("video/mp4", video),
	)
	// These helpers take ownership of bytes. Do not change the buffers afterward.
	// docs:end content-media
	return parts, nil
}

func imageURL() *gent.Task {
	// docs:start content-url
	task := gent.NewTask(
		gent.TextPart("Describe this image."),
		gent.ImageURLPart("https://example.com/image.png"),
	)
	// The application owns URL access and lifetime, including after restoration.
	// docs:end content-url
	return task
}

func cachedPart() gent.ContentPart {
	// docs:start content-cache
	part := gent.CachedContent{
		ContentPart:  gent.TextPart("Use the account rules supplied by the application."),
		CacheControl: &gent.CacheControl{Type: "ephemeral"},
	}
	// Cache controls are provider-specific hints. They do not guarantee a cache hit.
	// docs:end content-cache
	return part
}
