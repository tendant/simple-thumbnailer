// pkg/schema/events.go
package schema

type ImageUploaded struct {
	ID         string `json:"id"`
	Filename   string `json:"filename"`
	Path       string `json:"path"`
	HappenedAt int64  `json:"happened_at"`
}

type ThumbnailResult struct {
	Size      string `json:"size"`
	ThumbPath string `json:"thumb_path,omitempty"`
	UploadURL string `json:"upload_url,omitempty"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
}

type ThumbnailDone struct {
	ID         string            `json:"id"`
	SourcePath string            `json:"source_path"`
	Results    []ThumbnailResult `json:"results,omitempty"`
	Error      string            `json:"error,omitempty"`
	HappenedAt int64             `json:"happened_at"`
}
