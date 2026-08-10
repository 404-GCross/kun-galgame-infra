package queue

import (
	"api/pkg/config"
	"api/pkg/logger"
)

const (
	TypeModerationText  = "moderation:text"
	TypeModerationImage = "moderation:image"
	TypeArtifactProcess = "artifact:process"
	TypeEmailSend       = "email:send"
)

type Client struct {
}

type Server struct {
}

func NewClient(cfg config.RedisConfig) (*Client, error) {
	if !cfg.Enabled {
		logger.Info("Queue client disabled (Redis not enabled)")
		return nil, nil
	}

	logger.Info("Queue client initialized")
	return &Client{}, nil
}

func NewServer(cfg config.RedisConfig) (*Server, error) {
	if !cfg.Enabled {
		logger.Info("Queue server disabled (Redis not enabled)")
		return nil, nil
	}

	logger.Info("Queue server initialized")
	return &Server{}, nil
}

func (c *Client) Enqueue(taskType string, payload []byte) error {
	return nil
}

func (s *Server) Start() error {
	return nil
}

func (s *Server) Stop() {
}
