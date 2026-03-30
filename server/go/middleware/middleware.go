package middleware

import (
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/luvxinc/vpn/server/auth"
	"github.com/luvxinc/vpn/server/config"
	"github.com/luvxinc/vpn/server/store"
)

// RateLimit blocks IPs that exceed 5 requests per 15-minute window.
func RateLimit(rdb *store.Redis) fiber.Handler {
	return func(c *fiber.Ctx) error {
		ip := c.IP()
		count, err := rdb.IncrRateLimit(c.Context(), ip)
		if err != nil {
			// Redis failure → let through (fail open)
			return c.Next()
		}
		if count > 5 {
			return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{
				"detail": "Too many requests",
			})
		}
		return c.Next()
	}
}

// isLAN returns true if ip has one of the allowed LAN prefixes.
func isLAN(ip string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(ip, prefix) {
			return true
		}
	}
	return false
}

// RequireLAN rejects requests from non-LAN IP addresses.
func RequireLAN(cfg *config.Config) fiber.Handler {
	return func(c *fiber.Ctx) error {
		if !isLAN(c.IP(), cfg.Admin.AllowedLANPrefixes) {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
				"detail": "Admin access restricted to LAN only",
			})
		}
		return c.Next()
	}
}

// RequireAdminAuth enforces LAN access + valid admin JWT cookie.
func RequireAdminAuth(cfg *config.Config) fiber.Handler {
	return func(c *fiber.Ctx) error {
		if !isLAN(c.IP(), cfg.Admin.AllowedLANPrefixes) {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
				"detail": "Admin access restricted to LAN only",
			})
		}
		token := c.Cookies("admin_token")
		if token == "" || !auth.VerifyAdminJWT(token, cfg.Auth.JWTSecret) {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"detail": "Not authenticated",
			})
		}
		return c.Next()
	}
}
