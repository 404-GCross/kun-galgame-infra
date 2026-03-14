package pipeline

import (
	"context"
)

// Pipeline represents the artifact processing pipeline
type Pipeline struct {
	steps []Step
}

// Step represents a pipeline step
type Step interface {
	Name() string
	Execute(ctx context.Context, artifact *ArtifactContext) error
}

// ArtifactContext holds artifact processing context
type ArtifactContext struct {
	ArtifactID uint
	FileKey    string
	TempPath   string
	Checksum   string
	Metadata   map[string]any
	Errors     []error
}

// NewPipeline creates a new pipeline
func NewPipeline() *Pipeline {
	return &Pipeline{
		steps: []Step{
			&ChecksumStep{},
			&VirusScanStep{},
			&ManifestValidatorStep{},
		},
	}
}

// Execute executes the pipeline
func (p *Pipeline) Execute(ctx context.Context, artifactCtx *ArtifactContext) error {
	for _, step := range p.steps {
		if err := step.Execute(ctx, artifactCtx); err != nil {
			artifactCtx.Errors = append(artifactCtx.Errors, err)
			return err
		}
	}
	return nil
}

// AddStep adds a step to the pipeline
func (p *Pipeline) AddStep(step Step) {
	p.steps = append(p.steps, step)
}
