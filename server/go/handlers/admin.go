package handlers

import (
	"context"
	"crypto/rand"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/luvxinc/vpn/server/auth"
	"github.com/luvxinc/vpn/server/config"
	"github.com/luvxinc/vpn/server/models"
	"github.com/luvxinc/vpn/server/singbox"
	"github.com/luvxinc/vpn/server/store"
)

type AdminHandler struct {
	DB  *store.DB
	RDB *store.Redis
	Cfg *config.Config
}

// ── Template data structs ─────────────────────────────────────────────────────

type DashboardData struct {
	CurrentPath        string
	OnlineSessions     []models.OnlineSession
	TotalUploadToday   int64
	TotalDownloadToday int64
}

type UsersData struct {
	CurrentPath string
	Users       []models.UserRow
}

type LogsData struct {
	CurrentPath  string
	Users        []models.UserIDRow
	Logs         []models.AccessLogRow
	SelectedUser string
	DateFrom     string
	DateTo       string
}

type StatsData struct {
	CurrentPath  string
	Users        []models.UserIDRow
	SelectedUser string
	Period       string
	DateFrom     string
	DateTo       string
	DailyRows    []models.DailyTrafficRow
	Summary      models.TrafficSummary
}

// ── Login ─────────────────────────────────────────────────────────────────────

func (h *AdminHandler) LoginPage(c *fiber.Ctx) error {
	return c.Render("login", fiber.Map{})
}

func (h *AdminHandler) Login(c *fiber.Ctx) error {
	username := c.FormValue("username")
	password := c.FormValue("password")

	if username != h.Cfg.Admin.Username {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"detail": "Invalid credentials"})
	}
	if !auth.CheckPassword(password, h.Cfg.Admin.PasswordHash) {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"detail": "Invalid credentials"})
	}

	token, err := auth.MakeAdminJWT(h.Cfg.Auth.JWTSecret)
	if err != nil {
		return err
	}

	c.Cookie(&fiber.Cookie{
		Name:     "admin_token",
		Value:    token,
		HTTPOnly: true,
		SameSite: "Strict",
		MaxAge:   8 * 3600,
	})
	return c.Redirect("/admin/dashboard", fiber.StatusSeeOther)
}

func (h *AdminHandler) Logout(c *fiber.Ctx) error {
	c.Cookie(&fiber.Cookie{
		Name:    "admin_token",
		Value:   "",
		MaxAge:  -1,
		Expires: time.Unix(0, 0),
	})
	return c.Redirect("/admin/login", fiber.StatusSeeOther)
}

// ── Dashboard ─────────────────────────────────────────────────────────────────

func (h *AdminHandler) Dashboard(c *fiber.Ctx) error {
	ctx := c.Context()
	sessions, err := h.DB.GetOnlineSessions(ctx)
	if err != nil {
		return err
	}
	upload, download, _ := h.DB.GetTodayTrafficTotals(ctx)
	return c.Render("dashboard", DashboardData{
		CurrentPath:        c.Path(),
		OnlineSessions:     sessions,
		TotalUploadToday:   upload,
		TotalDownloadToday: download,
	}, "layout")
}

func (h *AdminHandler) APIOnlineCount(c *fiber.Ctx) error {
	count, err := h.DB.CountOnlineSessions(c.Context())
	if err != nil {
		return err
	}
	return c.SendString(fmt.Sprintf("%d", count))
}

func (h *AdminHandler) APIOnline(c *fiber.Ctx) error {
	rows, err := h.DB.GetOnlineSessions(c.Context())
	if err != nil {
		return err
	}
	return c.Render("_online_rows", fiber.Map{
		"OnlineSessions": rows,
	})
}

// ── Users ─────────────────────────────────────────────────────────────────────

func (h *AdminHandler) UsersPage(c *fiber.Ctx) error {
	users, err := h.DB.GetUsers(c.Context())
	if err != nil {
		return err
	}
	return c.Render("users", UsersData{
		CurrentPath: c.Path(),
		Users:       users,
	}, "layout")
}

func (h *AdminHandler) CreateUser(c *fiber.Ctx) error {
	username := strings.TrimSpace(c.FormValue("username"))
	password := c.FormValue("password")
	notes := c.FormValue("notes")

	if username == "" || password == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"detail": "Username and password required"})
	}
	if len(password) < 8 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"detail": "Password must be at least 8 characters"})
	}

	hash, err := auth.HashPassword(password)
	if err != nil {
		return err
	}

	var notesPtr *string
	if notes != "" {
		notesPtr = &notes
	}

	if err := h.DB.CreateUser(c.Context(), username, hash, notesPtr); err != nil {
		if strings.Contains(err.Error(), "unique") || strings.Contains(err.Error(), "duplicate") ||
			strings.Contains(err.Error(), "UNIQUE") {
			return c.Status(fiber.StatusConflict).JSON(fiber.Map{"detail": "Username already exists"})
		}
		return err
	}
	return c.Redirect("/admin/users", fiber.StatusSeeOther)
}

func (h *AdminHandler) DeleteUser(c *fiber.Ctx) error {
	userID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"detail": "Invalid user ID"})
	}
	ctx := c.Context()
	if err := KickUserSessions(ctx, h.DB, h.RDB, h.Cfg, userID); err != nil {
		return err
	}
	if err := h.DB.DeleteUser(ctx, userID); err != nil {
		return err
	}
	return c.Redirect("/admin/users", fiber.StatusSeeOther)
}

func (h *AdminHandler) ChangePassword(c *fiber.Ctx) error {
	userID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"detail": "Invalid user ID"})
	}
	password := c.FormValue("password")
	if len(password) < 8 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"detail": "Password must be at least 8 characters"})
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return err
	}
	if err := h.DB.UpdateUserPassword(c.Context(), userID, hash); err != nil {
		return err
	}
	return c.Redirect("/admin/users", fiber.StatusSeeOther)
}

func (h *AdminHandler) ToggleUser(c *fiber.Ctx) error {
	userID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"detail": "Invalid user ID"})
	}
	ctx := c.Context()
	active, err := h.DB.GetUserActive(ctx, userID)
	if err == pgx.ErrNoRows {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"detail": "User not found"})
	}
	if err != nil {
		return err
	}
	newActive := !active
	if err := h.DB.SetUserActive(ctx, userID, newActive); err != nil {
		return err
	}
	if !newActive {
		KickUserSessions(ctx, h.DB, h.RDB, h.Cfg, userID)
	}
	return c.Redirect("/admin/users", fiber.StatusSeeOther)
}

func (h *AdminHandler) KickUser(c *fiber.Ctx) error {
	userID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"detail": "Invalid user ID"})
	}
	if err := KickUserSessions(c.Context(), h.DB, h.RDB, h.Cfg, userID); err != nil {
		return err
	}
	return c.Redirect("/admin/users", fiber.StatusSeeOther)
}

func (h *AdminHandler) GenerateVerifCode(c *fiber.Ctx) error {
	userID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"detail": "Invalid user ID"})
	}
	ctx := c.Context()
	username, err := h.DB.GetUsernameByID(ctx, userID)
	if err == pgx.ErrNoRows {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"detail": "User not found"})
	}
	if err != nil {
		return err
	}
	code := GenCode()
	if err := h.RDB.SetVerifCode(ctx, code, userID.String()); err != nil {
		return err
	}
	return c.JSON(fiber.Map{
		"code":               code,
		"expires_in_seconds": 900,
		"username":           username,
	})
}

// ── Logs ──────────────────────────────────────────────────────────────────────

func (h *AdminHandler) LogsPage(c *fiber.Ctx) error {
	ctx := c.Context()
	today := time.Now().Format("2006-01-02")
	sevenDaysAgo := time.Now().AddDate(0, 0, -7).Format("2006-01-02")

	userIDStr := c.Query("user_id")
	dateFrom := c.Query("from", sevenDaysAgo)
	dateTo := c.Query("to", today)

	users, err := h.DB.GetAllUsers(ctx)
	if err != nil {
		return err
	}

	var logs []models.AccessLogRow
	if userIDStr != "" {
		if uid, err := uuid.Parse(userIDStr); err == nil {
			logs, _ = h.DB.GetAccessLogs(ctx, uid, dateFrom, dateTo)
		}
	}

	return c.Render("logs", LogsData{
		CurrentPath:  c.Path(),
		Users:        users,
		Logs:         logs,
		SelectedUser: userIDStr,
		DateFrom:     dateFrom,
		DateTo:       dateTo,
	}, "layout")
}

// ── Stats ─────────────────────────────────────────────────────────────────────

func (h *AdminHandler) StatsPage(c *fiber.Ctx) error {
	ctx := c.Context()
	today := time.Now()
	userIDStr := c.Query("user_id")
	period := c.Query("period", "week")

	var dateFrom time.Time
	switch period {
	case "today":
		dateFrom = today
	case "month":
		dateFrom = time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, today.Location())
	case "custom":
		parsed, err := time.Parse("2006-01-02", c.Query("from", ""))
		if err != nil {
			parsed = today.AddDate(0, 0, -6)
		}
		dateFrom = parsed
	default: // week
		dateFrom = today.AddDate(0, 0, -6)
	}

	users, err := h.DB.GetAllUsers(ctx)
	if err != nil {
		return err
	}

	var dailyRows []models.DailyTrafficRow
	var summary models.TrafficSummary
	if userIDStr != "" {
		if uid, err := uuid.Parse(userIDStr); err == nil {
			from := dateFrom.Format("2006-01-02")
			to := today.Format("2006-01-02")
			dailyRows, _ = h.DB.GetDailyTraffic(ctx, uid, from, to)
			summary, _ = h.DB.GetTrafficSummary(ctx, uid, from, to)
		}
	}

	return c.Render("stats", StatsData{
		CurrentPath:  c.Path(),
		Users:        users,
		SelectedUser: userIDStr,
		Period:       period,
		DateFrom:     dateFrom.Format("2006-01-02"),
		DateTo:       today.Format("2006-01-02"),
		DailyRows:    dailyRows,
		Summary:      summary,
	}, "layout")
}

// ── KickUserSessions ─────────────────────────────────────────────────────────

// KickUserSessions force-disconnects all active sessions for a user.
// Exported for use in tests.
func KickUserSessions(ctx context.Context, db *store.DB, rdb *store.Redis, cfg *config.Config, userID uuid.UUID) error {
	actives, err := db.GetActiveSessionsByUser(ctx, userID)
	if err != nil {
		return err
	}
	for _, s := range actives {
		fp, err := db.GetDeviceFingerprintByID(ctx, s.DeviceID)
		if err == nil && fp != "" {
			rdb.DeleteKey(ctx, "active_session:"+fp)
		}
		rdb.DeleteKey(ctx, "vless_map:"+s.VlessUUID)
	}
	if err := db.DeactivateUserSessions(ctx, userID); err != nil {
		return err
	}
	// Update sing-box to a random UUID so the old user can't connect
	singbox.UpdateUUID(cfg.SingBox.ConfigPath, uuid.New().String())
	return nil
}

// ── GenCode ───────────────────────────────────────────────────────────────────

const codeAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

// GenCode generates an 8-char uppercase alphanumeric verification code.
func GenCode() string {
	buf := make([]byte, 8)
	for i := range buf {
		n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(codeAlphabet))))
		buf[i] = codeAlphabet[n.Int64()]
	}
	return string(buf)
}
