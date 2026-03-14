package pipeline

import (
	"context"
)

// ChecksumStep validates file checksum
type ChecksumStep struct{}

// Name returns the step name
func (s *ChecksumStep) Name() string {
	return "checksum"
}

// Execute validates the file checksum
func (s *ChecksumStep) Execute(ctx context.Context, artifact *ArtifactContext) error {
	// TODO: implement checksum validation
	// 1. Read file from temp path
	// 2. Calculate SHA256 hash
	// 3. Compare with provided checksum
	return nil
}
