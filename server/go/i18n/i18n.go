package i18n

import "sync"

// En is the English string map.
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
	"dash.title":         "Dashboard",
	"dash.subtitle":      "Live connection status",
	"dash.onlineNow":     "Online Now",
	"dash.uploadToday":   "Upload Today",
	"dash.downloadToday": "Download Today",
	"dash.activeConns":   "Active Connections",
	"dash.autoRefresh":   "Auto-refresh every 5s",
	"dash.noUsers":       "No users connected",

	// ── Online table ─────────────────────────────────────────────────────────
	"tbl.user":     "User",
	"tbl.ipLoc":    "IP / Location",
	"tbl.connAt":   "Connected At",
	"tbl.upload":   "Upload",
	"tbl.download": "Download",

	// ── Users ────────────────────────────────────────────────────────────────
	"users.title":      "User Management",
	"users.count":      "users",
	"users.newBtn":     "+ New User",
	"users.colUser":    "Username",
	"users.colStatus":  "Status",
	"users.colDevice":  "Device",
	"users.colSeen":    "Last Seen",
	"users.colCreated": "Created",
	"users.colActions": "Actions",
	"users.online":     "Online",
	"users.offline":    "Offline",
	"users.disabled":   "Disabled",
	"users.noDevice":   "No Device",
	"users.empty":      "No users. Click New User to get started.",
	"users.regCode":    "Reg. Code",
	"users.kick":       "Kick",
	"users.chgPw":      "Password",
	"users.enable":     "Enable",
	"users.disable":    "Disable",
	"users.delete":     "Delete",
	"users.limits":     "Limits",

	// ── Limits modal ─────────────────────────────────────────────────────────
	"modal.limits.title":       "Speed Limits & Quota",
	"modal.limits.speedUp":     "Upload limit (Kbps, empty = unlimited)",
	"modal.limits.speedDown":   "Download limit (Kbps, empty = unlimited)",
	"modal.limits.quotaGB":     "Quota (GB, empty = unlimited)",
	"modal.limits.quotaPeriod": "Quota period",
	"modal.limits.daily":       "Daily",
	"modal.limits.weekly":      "Weekly",
	"modal.limits.monthly":     "Monthly",
	"modal.limits.cancel":      "Cancel",
	"modal.limits.save":        "Save",
	"modal.limits.unlimited":   "Unlimited",

	// ── Delete user modal ────────────────────────────────────────────────────
	"modal.delete.title":   "Delete User",
	"modal.delete.message": "Are you sure you want to delete this user? This cannot be undone.",
	"modal.delete.cancel":  "Cancel",
	"modal.delete.confirm": "Delete",

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
	"modal.code.title": "Registration Code",
	"modal.code.valid": "Valid for 15 minutes · single use",
	"modal.code.share": "Share this code with the user",
	"modal.code.close": "Close",

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

// Zh is the Chinese string map.
var Zh = map[string]string{
	// ── App ──────────────────────────────────────────────────────────────────
	"app.name":    "为爱鼓掌 VPN",
	"app.console": "管理控制台",

	// ── Login ────────────────────────────────────────────────────────────────
	"login.title":    "管理员登录",
	"login.username": "用户名",
	"login.password": "密码",
	"login.submit":   "登录",
	"login.lanOnly":  "仅限局域网访问",

	// ── Nav ──────────────────────────────────────────────────────────────────
	"nav.dashboard": "仪表板",
	"nav.users":     "用户管理",
	"nav.logs":      "访问日志",
	"nav.stats":     "流量统计",
	"nav.signOut":   "退出登录",

	// ── Dashboard ────────────────────────────────────────────────────────────
	"dash.title":         "仪表板",
	"dash.subtitle":      "实时连接状态",
	"dash.onlineNow":     "当前在线",
	"dash.uploadToday":   "今日上传",
	"dash.downloadToday": "今日下载",
	"dash.activeConns":   "当前连接",
	"dash.autoRefresh":   "每 5 秒自动刷新",
	"dash.noUsers":       "暂无在线用户",

	// ── Online table ─────────────────────────────────────────────────────────
	"tbl.user":     "用户",
	"tbl.ipLoc":    "IP / 位置",
	"tbl.connAt":   "连接时间",
	"tbl.upload":   "上传",
	"tbl.download": "下载",

	// ── Users ────────────────────────────────────────────────────────────────
	"users.title":      "用户管理",
	"users.count":      "个用户",
	"users.newBtn":     "+ 新建用户",
	"users.colUser":    "用户名",
	"users.colStatus":  "状态",
	"users.colDevice":  "设备",
	"users.colSeen":    "最后在线",
	"users.colCreated": "创建时间",
	"users.colActions": "操作",
	"users.online":     "在线",
	"users.offline":    "离线",
	"users.disabled":   "已禁用",
	"users.noDevice":   "未绑定",
	"users.empty":      "暂无用户，点击右上角新建",
	"users.regCode":    "验证码",
	"users.kick":       "踢出",
	"users.chgPw":      "改密",
	"users.enable":     "启用",
	"users.disable":    "禁用",
	"users.delete":     "删除",
	"users.limits":     "限制",

	// ── Limits modal ─────────────────────────────────────────────────────────
	"modal.limits.title":       "限速与流量配额",
	"modal.limits.speedUp":     "上传限速（Kbps，留空 = 不限）",
	"modal.limits.speedDown":   "下载限速（Kbps，留空 = 不限）",
	"modal.limits.quotaGB":     "流量配额（GB，留空 = 不限）",
	"modal.limits.quotaPeriod": "周期",
	"modal.limits.daily":       "按天",
	"modal.limits.weekly":      "按周",
	"modal.limits.monthly":     "按月",
	"modal.limits.cancel":      "取消",
	"modal.limits.save":        "保存",
	"modal.limits.unlimited":   "无限制",

	// ── Delete user modal ────────────────────────────────────────────────────
	"modal.delete.title":   "删除用户",
	"modal.delete.message": "确定要删除此用户吗？此操作无法撤销。",
	"modal.delete.cancel":  "取消",
	"modal.delete.confirm": "删除",

	// ── New user modal ────────────────────────────────────────────────────────
	"modal.newUser.title":    "新建用户",
	"modal.newUser.username": "用户名",
	"modal.newUser.password": "密码（至少 8 位）",
	"modal.newUser.cancel":   "取消",
	"modal.newUser.create":   "创建",

	// ── Change password modal ─────────────────────────────────────────────────
	"modal.pw.title":  "修改密码",
	"modal.pw.newPw":  "新密码（至少 8 位）",
	"modal.pw.cancel": "取消",
	"modal.pw.save":   "保存",

	// ── Reg code modal ────────────────────────────────────────────────────────
	"modal.code.title": "设备验证码",
	"modal.code.valid": "有效期 15 分钟 · 使用后立即失效",
	"modal.code.share": "请将此验证码告知用户",
	"modal.code.close": "关闭",

	// ── Logs ──────────────────────────────────────────────────────────────────
	"logs.title":       "访问日志",
	"logs.subtitle":    "按用户和时间段查询访问记录（保留最近 90 天）",
	"logs.filterUser":  "用户",
	"logs.filterFrom":  "开始日期",
	"logs.filterTo":    "结束日期",
	"logs.selectUser":  "— 选择用户 —",
	"logs.search":      "查询",
	"logs.count":       "条记录（最多 1000 条）",
	"logs.colHost":     "域名 / IP",
	"logs.colHour":     "时间（小时）",
	"logs.colReqs":     "请求数",
	"logs.colUpload":   "上传",
	"logs.colDownload": "下载",
	"logs.noResults":   "该时间段内无访问记录",
	"logs.selectFirst": "请选择用户后点击查询",

	// ── Stats ─────────────────────────────────────────────────────────────────
	"stats.title":         "流量统计",
	"stats.subtitle":      "按用户查看上传/下载用量",
	"stats.filterUser":    "用户",
	"stats.filterPeriod":  "时间段",
	"stats.today":         "今天",
	"stats.week":          "本周（7天）",
	"stats.month":         "本月",
	"stats.custom":        "自定义",
	"stats.from":          "开始",
	"stats.to":            "结束",
	"stats.search":        "查询",
	"stats.totalUpload":   "总上传",
	"stats.totalDownload": "总下载",
	"stats.sessions":      "连接次数",
	"stats.daily":         "每日明细",
	"stats.colDate":       "日期",
	"stats.colUpload":     "上传",
	"stats.colDownload":   "下载",
	"stats.colTotal":      "合计",
	"stats.noData":        "该时间段内无流量记录",
	"stats.selectFirst":   "请选择用户后点击查询",
}

var (
	mu      sync.RWMutex
	Current = En
)

// SetLang switches the active language. Supported: "zh", "en".
func SetLang(lang string) {
	mu.Lock()
	if lang == "zh" {
		Current = Zh
	} else {
		Current = En
	}
	mu.Unlock()
}

// T returns the translated string for key, falling back to the key itself.
func T(key string) string {
	mu.RLock()
	s, ok := Current[key]
	mu.RUnlock()
	if ok {
		return s
	}
	return key
}
