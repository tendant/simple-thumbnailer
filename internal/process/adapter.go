// internal/process/adapter.go
package process

import sp "github.com/tendant/simple-process"

type Job = sp.Job
type JobStatus = sp.JobStatus

func NewJob(kind, id string, input any) *sp.Job {
	return &sp.Job{
		ID:   id,
		Kind: kind,
	}
}

func MarkRunning(j *sp.Job)   { j.Status = sp.JobStatusRunning }
func MarkSucceeded(j *sp.Job) { j.Status = sp.JobStatusSucceeded }
func MarkFailed(j *sp.Job, err error) {
	j.Status = sp.JobStatusFailed
	if err != nil {
		j.Error = err.Error()
	}
}
