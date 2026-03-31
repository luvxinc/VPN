package handlers

import (
	"os"
	"path/filepath"

	"github.com/gofiber/fiber/v2"
)

// Health returns server status and version.
func Health(version string) fiber.Handler {
	return func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{
			"status":  "ok",
			"version": version,
		})
	}
}

// DownloadClient serves the macOS client zip file.
func DownloadClient(zipPath string) fiber.Handler {
	return func(c *fiber.Ctx) error {
		// Resolve relative to binary dir if not absolute
		if !filepath.IsAbs(zipPath) {
			exe, err := os.Executable()
			if err == nil {
				zipPath = filepath.Join(filepath.Dir(exe), zipPath)
			}
		}
		if _, err := os.Stat(zipPath); os.IsNotExist(err) {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
				"detail": "Client package not available. Contact your administrator.",
			})
		}
		c.Set("Cache-Control", "no-store, no-cache, must-revalidate")
		c.Set("Pragma", "no-cache")
		return c.Download(zipPath, "为爱鼓掌.zip")
	}
}
