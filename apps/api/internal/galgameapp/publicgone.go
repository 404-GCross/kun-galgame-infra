package galgameapp

import (
	"api/internal/app"
	"api/pkg/errors"
	"api/pkg/response"

	"github.com/gofiber/fiber/v3"
)

const (
	retiredSuccessorLink = `<https://api.nextmoe.dev/v1/catalog>; rel="successor-version"`
	retiredPublicMessage = "the /v1/galgame face was retired on 2026-07-30; " +
		"use the canonical /v1/catalog face instead — https://developer.nextmoe.dev/docs/catalog"
)

func MountRetiredPublic(a *app.App) {
	gone := func(c fiber.Ctx) error {
		c.Set("Link", retiredSuccessorLink)
		return response.Error(c, fiber.StatusGone, errors.ErrGone, retiredPublicMessage)
	}
	a.Fiber.All("/v1/galgame", gone)
	a.Fiber.All("/v1/galgame/*", gone)
}
