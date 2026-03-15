package queue

import (
	"api/pkg/config"
	"api/pkg/logger"
)

// Task type constants
const (
	TypeModerationText  = "moderation:text"
	TypeModerationImage = "moderation:image"
	TypeArtifactProcess = "artifact:process"
	TypeEmailSend       = "email:send"
)

// Client is the task queue client (producer)
// TODO: Implement with asynq when needed
type Client struct {
	// asynq.Client will be added here
}

// Server is the task queue server (consumer)
// TODO: Implement with asynq when needed
type Server struct {
	// asynq.Server will be added here
}

// NewClient creates a new queue client
func NewClient(cfg config.RedisConfig) (*Client, error) {
	if !cfg.Enabled {
		logger.Info("Queue client disabled (Redis not enabled)")
		return nil, nil
	}

	// TODO: Initialize asynq client
	logger.Info("Queue client initialized")
	return &Client{}, nil
}

// NewServer creates a new queue server
func NewServer(cfg config.RedisConfig) (*Server, error) {
	if !cfg.Enabled {
		logger.Info("Queue server disabled (Redis not enabled)")
		return nil, nil
	}

	// TODO: Initialize asynq server
	logger.Info("Queue server initialized")
	return &Server{}, nil
}

// Enqueue adds a task to the queue
func (c *Client) Enqueue(taskType string, payload []byte) error {
	// TODO: Implement with asynq
	return nil
}

// Start starts the queue server
func (s *Server) Start() error {
	// TODO: Implement with asynq
	return nil
}

// Stop stops the queue server
func (s *Server) Stop() {
	// TODO: Implement with asynq
}
