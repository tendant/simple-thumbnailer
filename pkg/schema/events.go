// pkg/schema/events.go
package schema

type ImageUploaded struct {
	ID         string `json:"id"`
	Filename   string `json:"filename"`
	Path       string `json:"path"`
	HappenedAt int64  `json:"happened_at"`
}

type ProcessingStage string

const (
	StageValidation   ProcessingStage = "validation"
	StageProcessing   ProcessingStage = "processing"
	StageUpload       ProcessingStage = "upload"
	StageCompleted    ProcessingStage = "completed"
	StageFailed       ProcessingStage = "failed"
)

type FailureType string

const (
	FailureTypeRetryable   FailureType = "retryable"
	FailureTypePermanent   FailureType = "permanent"
	FailureTypeValidation  FailureType = "validation"
)

type DerivationParams struct {
	SourceWidth     int     `json:"source_width"`
	SourceHeight    int     `json:"source_height"`
	TargetWidth     int     `json:"target_width"`
	TargetHeight    int     `json:"target_height"`
	Algorithm       string  `json:"algorithm"`
	Quality         int     `json:"quality,omitempty"`
	ProcessingTime  int64   `json:"processing_time_ms"`
	GeneratedAt     int64   `json:"generated_at"`
}

type ThumbnailResult struct {
	Size             string            `json:"size"`
	ContentID        string            `json:"content_id"`
	ObjectID         string            `json:"object_id"`
	UploadURL        string            `json:"upload_url,omitempty"`
	Width            int               `json:"width"`
	Height           int               `json:"height"`
	Status           string            `json:"status"`
	DerivationParams *DerivationParams `json:"derivation_params,omitempty"`
}

type ThumbnailLifecycleEvent struct {
	JobID            string          `json:"job_id"`
	ParentContentID  string          `json:"parent_content_id"`
	ParentStatus     string          `json:"parent_status"`
	Stage            ProcessingStage `json:"stage"`
	ThumbnailSizes   []string        `json:"thumbnail_sizes,omitempty"`
	ProcessingStart  int64           `json:"processing_start,omitempty"`
	ProcessingEnd    int64           `json:"processing_end,omitempty"`
	Error            string          `json:"error,omitempty"`
	FailureType      FailureType     `json:"failure_type,omitempty"`
	HappenedAt       int64           `json:"happened_at"`
}

type ThumbnailDone struct {
	ID               string                   `json:"id"`
	SourcePath       string                   `json:"source_path"`
	ParentContentID  string                   `json:"parent_content_id"`
	ParentStatus     string                   `json:"parent_status"`
	TotalProcessed   int                      `json:"total_processed"`
	TotalFailed      int                      `json:"total_failed"`
	ProcessingTimeMs int64                    `json:"processing_time_ms"`
	Results          []ThumbnailResult        `json:"results,omitempty"`
	Lifecycle        []ThumbnailLifecycleEvent `json:"lifecycle,omitempty"`
	Error            string                   `json:"error,omitempty"`
	FailureType      FailureType              `json:"failure_type,omitempty"`
	HappenedAt       int64                    `json:"happened_at"`
}
