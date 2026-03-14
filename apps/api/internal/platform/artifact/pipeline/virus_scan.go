package pipeline

import (
	"context"
)

// VirusScanStep scans files for viruses
type VirusScanStep struct{}

// Name returns the step name
func (s *VirusScanStep) Name() string {
	return "virus_scan"
}

// Execute scans the file for viruses
func (s *VirusScanStep) Execute(ctx context.Context, artifact *ArtifactContext) error {
	// TODO: implement virus scanning with ClamAV
	// 1. Connect to ClamAV daemon
	// 2. Scan the file
	// 3. Return error if virus found
	return nil
}
