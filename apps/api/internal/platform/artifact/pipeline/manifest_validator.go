package pipeline

import (
	"context"
)

// ManifestValidatorStep validates manifest.json in artifacts
type ManifestValidatorStep struct{}

// Name returns the step name
func (s *ManifestValidatorStep) Name() string {
	return "manifest_validator"
}

// Execute validates the manifest.json if present
func (s *ManifestValidatorStep) Execute(ctx context.Context, artifact *ArtifactContext) error {
	// TODO: implement manifest validation
	// 1. Check if artifact is a zip file
	// 2. Look for manifest.json
	// 3. Validate manifest structure
	// 4. Store manifest info in artifact metadata
	return nil
}
