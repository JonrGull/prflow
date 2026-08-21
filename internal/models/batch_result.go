package models

// BatchStatus represents the status of a batch PR operation for a single repo
type BatchStatus interface {
	isBatchStatus()
}

type batchStatusCreated struct{}
type batchStatusUpdated struct{}
type batchStatusSkipped struct{ Reason string }
type batchStatusFailed struct{ Error string }

func (batchStatusCreated) isBatchStatus() {}
func (batchStatusUpdated) isBatchStatus() {}
func (batchStatusSkipped) isBatchStatus() {}
func (batchStatusFailed) isBatchStatus()  {}

// BatchStatus variants
var (
	// Created indicates PR was created successfully
	Created BatchStatus = batchStatusCreated{}
	// Updated indicates existing PR was updated
	Updated BatchStatus = batchStatusUpdated{}
)

// Skipped creates a BatchStatus for a skipped repo with a reason
func Skipped(reason string) BatchStatus {
	return batchStatusSkipped{Reason: reason}
}

// Failed creates a BatchStatus for a failed operation with an error message
func Failed(err string) BatchStatus {
	return batchStatusFailed{Error: err}
}

// BatchResult represents the result of processing a single repo in batch mode
type BatchResult struct {
	// Repo is the repository info
	Repo RepoInfo
	// Status of the operation
	Status BatchStatus
	// PrURL if created/updated
	PrURL *string
	// PrNumber if created/updated
	PrNumber uint64
	// Tickets found in commits
	Tickets []string
}

// IsStatusCreated returns true if status is Created
func IsStatusCreated(s BatchStatus) bool {
	_, ok := s.(batchStatusCreated)
	return ok
}

// IsStatusUpdated returns true if status is Updated
func IsStatusUpdated(s BatchStatus) bool {
	_, ok := s.(batchStatusUpdated)
	return ok
}

// IsStatusSkipped returns true if status is Skipped
func IsStatusSkipped(s BatchStatus) bool {
	_, ok := s.(batchStatusSkipped)
	return ok
}

// IsStatusFailed returns true if status is Failed
func IsStatusFailed(s BatchStatus) bool {
	_, ok := s.(batchStatusFailed)
	return ok
}

// IsStatusSuccess returns true if status is Created or Updated
func IsStatusSuccess(s BatchStatus) bool {
	return IsStatusCreated(s) || IsStatusUpdated(s)
}

// GetStatusReason returns the reason string for Skipped or Failed statuses
func GetStatusReason(s BatchStatus) string {
	if skipped, ok := s.(batchStatusSkipped); ok {
		return skipped.Reason
	}
	if failed, ok := s.(batchStatusFailed); ok {
		return failed.Error
	}
	return ""
}
