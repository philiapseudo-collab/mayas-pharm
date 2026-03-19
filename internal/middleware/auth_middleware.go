package middleware

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/philia-technologies/mayas-pharm/internal/service"
	"github.com/gofiber/fiber/v2"
)

const requiredAuthTokenVersion = service.AuthTokenVersion

// AuthMiddleware creates a JWT authentication middleware
func AuthMiddleware(dashboardService *service.DashboardService) fiber.Handler {
	return func(c *fiber.Ctx) error {
		// Prioritize Authorization header to avoid stale cookie identity overriding
		// explicit per-device/session tokens.
		token := extractBearerToken(c.Get("Authorization"))

		// Cookie is fallback when Authorization header is absent.
		if token == "" {
			token = strings.TrimSpace(c.Cookies("auth_token"))
		}

		// EventSource cannot set Authorization headers in browsers.
		// Allow token query param fallback for the SSE endpoint only.
		if token == "" && strings.HasSuffix(c.Path(), "/events") {
			token = strings.TrimSpace(c.Query("token"))
		}

		if token == "" {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"error": "unauthorized: no token provided",
			})
		}

		// Validate token
		claims, err := dashboardService.ValidateJWT(token)
		if err != nil {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"error": "unauthorized: invalid token",
			})
		}

		authVersion, ok := readIntClaim(claims["auth_version"])
		if !ok || authVersion != requiredAuthTokenVersion {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"error": "unauthorized: stale session, please log in again",
			})
		}

		userID := readStringClaim(claims["user_id"])
		phone := readStringClaim(claims["phone"])
		name := readStringClaim(claims["name"])
		role := strings.ToUpper(readStringClaim(claims["role"]))

		if userID == "" || role == "" {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"error": "unauthorized: invalid token claims",
			})
		}

		if role == "BARTENDER" {
			bartenderCode := readStringClaim(claims["bartender_code"])
			if !isValidFourDigitCode(bartenderCode) {
				return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
					"error": "unauthorized: invalid bartender session, please log in again",
				})
			}

			c.Locals("bartender_code", bartenderCode)
		}

		// Store claims in context for use in handlers
		c.Locals("user_id", userID)
		c.Locals("phone", phone)
		c.Locals("name", name)
		c.Locals("role", role)

		return c.Next()
	}
}

// RequireRoles enforces role-based access control after AuthMiddleware.
func RequireRoles(allowedRoles ...string) fiber.Handler {
	allowed := make(map[string]struct{}, len(allowedRoles))
	for _, role := range allowedRoles {
		normalizedRole := strings.ToUpper(strings.TrimSpace(role))
		if normalizedRole != "" {
			allowed[normalizedRole] = struct{}{}
		}
	}

	return func(c *fiber.Ctx) error {
		role := strings.ToUpper(strings.TrimSpace(fmt.Sprintf("%v", c.Locals("role"))))
		if role == "" {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
				"error": "forbidden: role not found in token",
			})
		}

		if _, ok := allowed[role]; !ok {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
				"error": "forbidden: insufficient permissions",
			})
		}

		return c.Next()
	}
}

func extractBearerToken(authHeader string) string {
	parts := strings.Fields(strings.TrimSpace(authHeader))
	if len(parts) != 2 {
		return ""
	}
	if !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}
	return strings.TrimSpace(parts[1])
}

func readStringClaim(value interface{}) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(v)
	default:
		return strings.TrimSpace(fmt.Sprintf("%v", v))
	}
}

func readIntClaim(value interface{}) (int, bool) {
	switch v := value.(type) {
	case nil:
		return 0, false
	case int:
		return v, true
	case int32:
		return int(v), true
	case int64:
		return int(v), true
	case float32:
		if v != float32(int(v)) {
			return 0, false
		}
		return int(v), true
	case float64:
		if v != float64(int(v)) {
			return 0, false
		}
		return int(v), true
	case json.Number:
		n, err := strconv.Atoi(strings.TrimSpace(v.String()))
		if err != nil {
			return 0, false
		}
		return n, true
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil {
			return 0, false
		}
		return n, true
	default:
		return 0, false
	}
}

func isValidFourDigitCode(code string) bool {
	if len(code) != 4 {
		return false
	}

	for _, r := range code {
		if r < '0' || r > '9' {
			return false
		}
	}

	return true
}
