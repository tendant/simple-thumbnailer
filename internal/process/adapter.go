// internal/process/adapter.go
package process

// JobStatus represents the lifecycle state of a processing job.
type JobStatus string

const (
	JobStatusPending   JobStatus = "pending"
	JobStatusRunning   JobStatus = "running"
	JobStatusSucceeded JobStatus = "succeeded"
	JobStatusFailed    JobStatus = "failed"
)

// Job captures the minimal metadata the worker tracks for auditing purposes.
type Job struct {
	ID     string
	Kind   string
	Input  any
	Status JobStatus
	Error  string
}

func NewJob(kind, id string, input any) *Job {
	return &Job{
		ID:     id,
		Kind:   kind,
		Input:  input,
		Status: JobStatusPending,
	}
}

func MarkRunning(j *Job)   { j.Status = JobStatusRunning }
func MarkSucceeded(j *Job) { j.Status = JobStatusSucceeded }
func MarkFailed(j *Job, err error) {
	j.Status = JobStatusFailed
	if err != nil {
		j.Error = err.Error()
	}
}
