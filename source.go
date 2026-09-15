package goodall

// SourceType says where the bytes of an image or document come from.
type SourceType string

const (
	// SourceBytes carries the data inline, base64-encoded, with a media type.
	SourceBytes SourceType = "base64"
	// SourceURL points at a publicly fetchable URL.
	SourceURL SourceType = "url"
	// SourceFile names a file already uploaded to the provider.
	SourceFile SourceType = "file"
)

// Source is the payload of an Image or Document block. Exactly one of Data,
// URL and FileID is set, chosen by Type; use the constructors rather than
// filling the struct by hand. Data marshals as base64.
type Source struct {
	Type      SourceType `json:"type"`
	MediaType string     `json:"media_type,omitzero"`
	Data      []byte     `json:"data,omitzero"`
	URL       string     `json:"url,omitzero"`
	FileID    string     `json:"file_id,omitzero"`
}

// BytesSource carries the bytes inline under the given IANA media type, such
// as "image/png" or "application/pdf".
func BytesSource(mediaType string, data []byte) Source {
	return Source{Type: SourceBytes, MediaType: mediaType, Data: data}
}

// URLSource points the provider at a URL it fetches itself.
func URLSource(url string) Source {
	return Source{Type: SourceURL, URL: url}
}

// FileSource names a file previously uploaded to the provider.
func FileSource(id string) Source {
	return Source{Type: SourceFile, FileID: id}
}
