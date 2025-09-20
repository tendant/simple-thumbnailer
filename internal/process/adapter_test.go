package process

import (
	"errors"
	"testing"
)

func TestNewJobCapturesInput(t *testing.T) {
	payload := map[string]string{"id": "123"}
	job := NewJob("thumbnail", "job-1", payload)

	if job.Kind != "thumbnail" || job.ID != "job-1" {
		t.Fatalf("unexpected job identity: %+v", job)
	}

	got, ok := job.Input.(map[string]string)
	if !ok {
		t.Fatalf("job input type mismatch: %#v", job.Input)
	}
	if got["id"] != "123" {
		t.Fatalf("job input not preserved: %#v", got)
	}
}

func TestMarkFailedSetsStatusAndError(t *testing.T) {
	job := NewJob("thumbnail", "job-2", nil)
	MarkFailed(job, errors.New("boom"))

	if job.Status != JobStatusFailed {
		t.Fatalf("job status not failed: %v", job.Status)
	}
	if job.Error == "" {
		t.Fatal("job error not recorded")
	}
}

func TestMarkFailedDoesNotOverwriteErrorWhenNil(t *testing.T) {
	job := NewJob("thumbnail", "job-3", nil)
	MarkFailed(job, nil)

	if job.Status != JobStatusFailed {
		t.Fatalf("job status not failed: %v", job.Status)
	}
	if job.Error != "" {
		t.Fatalf("expected empty error string, got %q", job.Error)
	}
}
