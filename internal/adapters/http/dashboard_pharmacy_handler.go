package http

import (
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/philia-technologies/mayas-pharm/internal/core"
	"github.com/philia-technologies/mayas-pharm/internal/service"
)

// GetStaffAccounts returns pharmacist/dispatcher identities for login selection.
func (h *DashboardHandler) GetStaffAccounts(c *fiber.Ctx) error {
	accounts, err := h.dashboardService.GetStaffAccounts(c.Context())
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(accounts)
}

// StaffLogin handles pharmacist/dispatcher PIN login.
func (h *DashboardHandler) StaffLogin(c *fiber.Ctx) error {
	var req struct {
		PIN    string `json:"pin"`
		UserID string `json:"user_id"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}

	var (
		token string
		err   error
	)
	if strings.TrimSpace(req.UserID) != "" {
		token, err = h.dashboardService.VerifyStaffPINForUser(c.Context(), req.UserID, req.PIN)
	} else {
		token, err = h.dashboardService.VerifyStaffPIN(c.Context(), req.PIN)
	}
	if err != nil {
		status := fiber.StatusUnauthorized
		if strings.Contains(err.Error(), "exactly 4 digits") || strings.Contains(err.Error(), "account") {
			status = fiber.StatusBadRequest
		}
		return c.Status(status).JSON(fiber.Map{"error": err.Error()})
	}

	secureCookie, sameSite := authCookieSettings(c)
	c.Cookie(&fiber.Cookie{
		Name:     "auth_token",
		Value:    token,
		Expires:  time.Now().Add(7 * 24 * time.Hour),
		HTTPOnly: true,
		Secure:   secureCookie,
		SameSite: sameSite,
	})

	return c.JSON(fiber.Map{
		"message": "login successful",
		"token":   token,
	})
}

// ListStaff returns all configured staff accounts.
func (h *DashboardHandler) ListStaff(c *fiber.Ctx) error {
	users, err := h.dashboardService.ListStaff(c.Context())
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(users)
}

// CreateStaff creates a staff account.
func (h *DashboardHandler) CreateStaff(c *fiber.Ctx) error {
	var req struct {
		Name  string `json:"name"`
		Phone string `json:"phone"`
		PIN   string `json:"pin"`
		Role  string `json:"role"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}
	user, err := h.dashboardService.CreateStaff(c.Context(), service.CreateStaffInput{
		Name:  req.Name,
		Phone: req.Phone,
		PIN:   req.PIN,
		Role:  req.Role,
	})
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.Status(fiber.StatusCreated).JSON(user)
}

// UpdateStaff updates a staff account.
func (h *DashboardHandler) UpdateStaff(c *fiber.Ctx) error {
	var req struct {
		Name     string `json:"name"`
		Phone    string `json:"phone"`
		Role     string `json:"role"`
		IsActive *bool  `json:"is_active"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}
	if req.IsActive == nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "is_active is required"})
	}
	user, err := h.dashboardService.UpdateStaff(c.Context(), service.UpdateStaffInput{
		ID:       c.Params("id"),
		Name:     req.Name,
		Phone:    req.Phone,
		Role:     req.Role,
		IsActive: *req.IsActive,
	})
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(user)
}

// ResetStaffPIN resets a pharmacist/dispatcher PIN.
func (h *DashboardHandler) ResetStaffPIN(c *fiber.Ctx) error {
	var req struct {
		PIN string `json:"pin"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}
	if err := h.dashboardService.ResetStaffPIN(c.Context(), c.Params("id"), req.PIN); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"message": "staff PIN reset successfully"})
}

// ListPendingPrescriptions returns the pharmacist review queue.
func (h *DashboardHandler) ListPendingPrescriptions(c *fiber.Ctx) error {
	prescriptions, err := h.dashboardService.ListPendingPrescriptions(c.Context(), defaultOrdersLimit)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(prescriptions)
}

// ReviewPrescription approves or rejects a prescription submission.
func (h *DashboardHandler) ReviewPrescription(c *fiber.Ctx) error {
	var req struct {
		Decision string `json:"decision"`
		Notes    string `json:"notes"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}
	actorUserID, _ := c.Locals("user_id").(string)
	order, err := h.dashboardService.ReviewPrescription(c.Context(), c.Params("id"), actorUserID, req.Decision, req.Notes)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(order)
}

// GetDeliveryZones returns all active delivery zones.
func (h *DashboardHandler) GetDeliveryZones(c *fiber.Ctx) error {
	zones, err := h.dashboardService.ListDeliveryZones(c.Context())
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(zones)
}

// UpsertDeliveryZone creates or updates a delivery zone.
func (h *DashboardHandler) UpsertDeliveryZone(c *fiber.Ctx) error {
	var zone core.DeliveryZone
	if err := c.BodyParser(&zone); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}
	if zone.ID == "" {
		zone.ID = c.Params("id")
	}
	if err := h.dashboardService.SaveDeliveryZone(c.Context(), &zone); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(zone)
}

// GetBusinessHours returns configured operating hours.
func (h *DashboardHandler) GetBusinessHours(c *fiber.Ctx) error {
	hours, err := h.dashboardService.ListBusinessHours(c.Context())
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(hours)
}

// UpsertBusinessHours creates or updates a business-hours entry.
func (h *DashboardHandler) UpsertBusinessHours(c *fiber.Ctx) error {
	var hours core.BusinessHours
	if err := c.BodyParser(&hours); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}
	if hours.ID == "" {
		hours.ID = c.Params("id")
	}
	if err := h.dashboardService.SaveBusinessHours(c.Context(), &hours); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(hours)
}

// MarkOrderOutForDelivery updates an order status after dispatch pickup.
func (h *DashboardHandler) MarkOrderOutForDelivery(c *fiber.Ctx) error {
	actorUserID, _ := c.Locals("user_id").(string)
	if err := h.dashboardService.MarkOrderOutForDelivery(c.Context(), c.Params("id"), actorUserID); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"message": "order marked as OUT_FOR_DELIVERY"})
}
