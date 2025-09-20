// pkg/schema/events.go
package schema

type ImageUploaded struct {
	ID         string `json:"id"`
	Filename   string `json:"filename"`
	Path       string `json:"path"`
	HappenedAt int64  `json:"happened_at"`
}

type ThumbnailDone struct {
	ID         string `json:"id"`
	SourcePath string `json:"source_path"`
	ThumbPath  string `json:"thumb_path,omitempty"`
	UploadURL  string `json:"upload_url,omitempty"`
	Error      string `json:"error,omitempty"`
	Width      int    `json:"width,omitempty"`
	Height     int    `json:"height,omitempty"`
	HappenedAt int64  `json:"happened_at"`
}
