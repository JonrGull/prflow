package models

// CommitInfo contains information about a git commit
type CommitInfo struct {
	// Hash is the short commit hash (7 characters)
	Hash string
	// Message is the first line of commit message
	Message string
	// Tickets are Linear ticket IDs found in the message (e.g., ["ATT-123", "ATT-456"])
	Tickets []string
}

// NewCommitInfo creates a new CommitInfo
func NewCommitInfo(hash, message string, tickets []string) CommitInfo {
	return CommitInfo{
		Hash:    hash,
		Message: message,
		Tickets: tickets,
	}
}
