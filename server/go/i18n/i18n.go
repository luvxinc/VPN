package i18n

// Strings holds all UI strings. Default language is English.
// To add a language, copy the En map, translate the values, and swap Current.
var En = map[string]string{
	// ── App ──────────────────────────────────────────────────────────────────
	"app.name":    "WeiAi VPN",
	"app.console": "Admin Console",

	// ── Login ────────────────────────────────────────────────────────────────
	"login.title":    "Admin Login",
	"login.username": "Username",
	"login.password": "Password",
	"login.submit":   "Sign In",
	"login.lanOnly":  "LAN access only",

	// ── Nav ──────────────────────────────────────────────────────────────────
	"nav.dashboard": "Dashboard",
	"nav.users":     "Users",
	"nav.logs":      "Access Logs",
	"nav.stats":     "Traffic Stats",
	"nav.signOut":   "Sign Out",

	// ── Dashboard ────────────────────────────────────────────────────────────
	"dash.title":        "Dashboard",
	"dash.subtitle":     "Live connection status",
	"dash.onlineNow":    "Online Now",
	"dash.uploadToday":  "Upload Today",
	"dash.downloadToday":"Download Today",
	"dash.activeConns":  "Active Connections",
	"dash.autoRefresh":  "Auto-refresh every 5s",
	"dash.noUsers":      "No users connected",

	// ── Online table ─────────────────────────────────────────────────────────
	"tbl.user":      "User",
	"tbl.ipLoc":     "IP / Location",
	"tbl.connAt":    "Connected At",
	"tbl.upload":    "Upload",
	"tbl.download":  "Download",

	// ── Users ────────────────────────────────────────────────────────────────
	"users.title":    "User Management",
	"users.count":    "users",
	"users.newBtn":   "+ New User",
	"users.colUser":  "Username",
	"users.colStatus":"Status",
	"users.colDevice":"Device",
	"users.colSeen":  "Last Seen",
	"users.colCreated":"Created",
	"users.colActions":"Actions",
	"users.online":   "Online",
	"users.offline":  "Offline",
	"users.disabled": "Disabled",
	"users.noDevice": "No Device",
	"users.empty":    "No users. Click New User to get started.",
	"users.regCode":  "Reg. Code",
	"users.kick":     "Kick",
	"users.chgPw":    "Password",
	"users.enable":   "Enable",
	"users.disable":  "Disable",
	"users.delete":   "Delete",

	// ── New user modal ────────────────────────────────────────────────────────
	"modal.newUser.title":    "New User",
	"modal.newUser.username": "Username",
	"modal.newUser.password": "Password (min 8 chars)",
	"modal.newUser.cancel":   "Cancel",
	"modal.newUser.create":   "Create",

	// ── Change password modal ─────────────────────────────────────────────────
	"modal.pw.title":  "Change Password",
	"modal.pw.newPw":  "New password (min 8 chars)",
	"modal.pw.cancel": "Cancel",
	"modal.pw.save":   "Save",

	// ── Reg code modal ────────────────────────────────────────────────────────
	"modal.code.title":   "Registration Code",
	"modal.code.valid":   "Valid for 15 minutes · single use",
	"modal.code.share":   "Share this code with the user",
	"modal.code.close":   "Close",

	// ── Logs ──────────────────────────────────────────────────────────────────
	"logs.title":       "Access Logs",
	"logs.subtitle":    "Query access records by user and date (90-day retention)",
	"logs.filterUser":  "User",
	"logs.filterFrom":  "From",
	"logs.filterTo":    "To",
	"logs.selectUser":  "— Select User —",
	"logs.search":      "Search",
	"logs.count":       "records (max 1000)",
	"logs.colHost":     "Domain / IP",
	"logs.colHour":     "Hour",
	"logs.colReqs":     "Requests",
	"logs.colUpload":   "Upload",
	"logs.colDownload": "Download",
	"logs.noResults":   "No access records for this period.",
	"logs.selectFirst": "Select a user and click Search.",

	// ── Stats ─────────────────────────────────────────────────────────────────
	"stats.title":         "Traffic Stats",
	"stats.subtitle":      "Upload / download usage by user",
	"stats.filterUser":    "User",
	"stats.filterPeriod":  "Period",
	"stats.today":         "Today",
	"stats.week":          "This Week (7 days)",
	"stats.month":         "This Month",
	"stats.custom":        "Custom",
	"stats.from":          "From",
	"stats.to":            "To",
	"stats.search":        "Search",
	"stats.totalUpload":   "Total Upload",
	"stats.totalDownload": "Total Download",
	"stats.sessions":      "Sessions",
	"stats.daily":         "Daily Breakdown",
	"stats.colDate":       "Date",
	"stats.colUpload":     "Upload",
	"stats.colDownload":   "Download",
	"stats.colTotal":      "Total",
	"stats.noData":        "No traffic data for this period.",
	"stats.selectFirst":   "Select a user and click Search.",
}

// Current is the active language. Swap to add multilingual support.
var Current = En

// T returns the translated string for key, falling back to the key itself.
func T(key string) string {
	if s, ok := Current[key]; ok {
		return s
	}
	return key
}
