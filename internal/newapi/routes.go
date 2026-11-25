package newapi

// Route is one new-api dashboard endpoint and the access token scope it
// requires. Path uses gin's syntax (":id") and equals gin's FullPath, so the
// contract check can compare it with new-api's route tables verbatim. An
// empty Scope marks an anonymous endpoint.
type Route struct {
	Method string `json:"method"`
	Path   string `json:"path"`
	Scope  string `json:"scope,omitempty"`
}

func (r Route) String() string { return r.Method + " " + r.Path }

// Every endpoint this service calls. Only this file knows new-api's paths.
var (
	RouteStatus   = Route{"GET", "/api/status", ""}
	RouteUserSelf = Route{"GET", "/api/user/self", "profile:read"}

	// Channels
	RouteChannelList          = Route{"GET", "/api/channel/", "channel:read"}
	RouteChannelSearch        = Route{"GET", "/api/channel/search", "channel:read"}
	RouteChannelGet           = Route{"GET", "/api/channel/:id", "channel:read"}
	RouteChannelOps           = Route{"GET", "/api/channel/ops", "channel:read"}
	RouteChannelPriorityOrder = Route{"GET", "/api/channel/priority/order", "channel:read"}
	RouteChannelCodexUsage    = Route{"GET", "/api/channel/:id/codex/usage", "channel:read"}
	RouteChannelTest          = Route{"GET", "/api/channel/test/:id", "channel:operate"}
	RouteChannelStatus        = Route{"POST", "/api/channel/:id/status", "channel:operate"}
	RouteChannelStatusBatch   = Route{"POST", "/api/channel/status/batch", "channel:operate"}
	RouteChannelMultiKey      = Route{"POST", "/api/channel/multi_key/manage", "channel:operate"}
	RouteChannelFetchModels   = Route{"GET", "/api/channel/fetch_models/:id", "channel:operate"}
	RouteChannelUpdate        = Route{"PUT", "/api/channel/", "channel:write"}

	// Logs, tasks, audit
	RouteLogList            = Route{"GET", "/api/log/", "log:read"}
	RouteLogStat            = Route{"GET", "/api/log/stat", "log:read"}
	RouteTaskList           = Route{"GET", "/api/task", "log:read"}
	RouteAuditList          = Route{"GET", "/api/audit", "audit:read"}
	RouteAffinityUsageCache = Route{"GET", "/api/log/channel_affinity_usage_cache", "log:read"}
	RouteAffinityCacheStats = Route{"GET", "/api/option/channel_affinity_cache", "option:read"}
	RouteAffinityCacheClear = Route{"DELETE", "/api/option/channel_affinity_cache", "option:write"}
	RouteDataQuota          = Route{"GET", "/api/data/", "log:read"}
	RouteDataQuotaByUser    = Route{"GET", "/api/data/users", "log:read"}
	RouteDataPerfSummary    = Route{"GET", "/api/data/perf-metrics/summary", "log:read"}

	// Users
	RouteUserSearch = Route{"GET", "/api/user/search", "user:read"}
	RouteUserGet    = Route{"GET", "/api/user/:id", "user:read"}
	RouteUserManage = Route{"POST", "/api/user/manage", "user:write"}
	RouteUserUpdate = Route{"PUT", "/api/user/", "user:write"}

	// Pricing and options
	RoutePricingGet     = Route{"GET", "/api/option/model_pricing", "option:read"}
	RoutePricingIssues  = Route{"GET", "/api/option/model_pricing/issues", "option:read"}
	RoutePricingPreview = Route{"POST", "/api/option/model_pricing/preview", "option:read"}
	RoutePricingUpdate  = Route{"PATCH", "/api/option/model_pricing", "option:write"}
	RouteOptionList     = Route{"GET", "/api/option/", "option:read"}
	RouteOptionUpdate   = Route{"PUT", "/api/option/", "option:write"}

	// Models, groups, operations
	RouteModelList    = Route{"GET", "/api/models/", "model:read"}
	RouteModelSearch  = Route{"GET", "/api/models/search", "model:read"}
	RouteModelMissing = Route{"GET", "/api/models/missing", "model:read"}
	RouteGroupList    = Route{"GET", "/api/group/", "group:read"}
	RoutePerfStats    = Route{"GET", "/api/performance/stats", "ops:read"}
	RouteSystemNodes  = Route{"GET", "/api/system-info/instances", "ops:read"}
	RouteSystemTasks  = Route{"GET", "/api/system-task/list", "ops:read"}
)
