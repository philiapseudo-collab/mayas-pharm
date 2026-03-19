package http

import "github.com/gofiber/fiber/v2"

func authCookieSettings(c *fiber.Ctx) (secure bool, sameSite string) {
	if c != nil && c.Protocol() == "https" {
		return true, "None"
	}
	return false, "Lax"
}
