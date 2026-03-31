package middleware

import (
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/luvxinc/vpn/server/auth"
	"github.com/luvxinc/vpn/server/config"
	"github.com/luvxinc/vpn/server/store"
)

// RateLimit blocks IPs that have exceeded 10 failed credential attempts per 15-minute window.
// Only counts requests where the handler explicitly calls c.Locals("rate_limit_hit", true).
func RateLimit(rdb *store.Redis) fiber.Handler {
	return func(c *fiber.Ctx) error {
		ip := c.IP()
		count, err := rdb.GetRateLimit(c.Context(), ip)
		if err == nil && count >= 10 {
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

// realIP returns the true client IP. When the connection arrives from localhost
// (i.e. via Cloudflare Tunnel), the CF-Connecting-IP header carries the real
// external IP and takes precedence so that the LAN check is not bypassed.
func realIP(c *fiber.Ctx) string {
	ip := c.IP()
	if strings.HasPrefix(ip, "127.") {
		if cf := c.Get("CF-Connecting-IP"); cf != "" {
			return cf
		}
	}
	return ip
}

// RequireLAN rejects requests from non-LAN IP addresses.
func RequireLAN(cfg *config.Config) fiber.Handler {
	return func(c *fiber.Ctx) error {
		if !isLAN(realIP(c), cfg.Admin.AllowedLANPrefixes) {
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
		if !isLAN(realIP(c), cfg.Admin.AllowedLANPrefixes) {
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
