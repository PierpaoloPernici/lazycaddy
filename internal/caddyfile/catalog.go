package caddyfile

// ArgRole is the advisory role of one directive or global-option argument.
// Roles describe how an argument is usually interpreted for presentation
// purposes; they never define Caddy syntax.
type ArgRole string

const (
	ArgUpstream ArgRole = "upstream"
	ArgPath     ArgRole = "path"
	ArgDomain   ArgRole = "domain"
	ArgPort     ArgRole = "port"
	ArgMatcher  ArgRole = "matcher"
	ArgStatus   ArgRole = "status-code"
	ArgDuration ArgRole = "duration"
	ArgString   ArgRole = "string"
	ArgFormat   ArgRole = "format"
	ArgField    ArgRole = "field"
	ArgAddress  ArgRole = "address"
	ArgMode     ArgRole = "mode"
)

// StructuredOp is one operation the structured edit planner supports for a
// directive or block.
type StructuredOp string

const (
	OpSetValue StructuredOp = "set-value"
	OpInsert   StructuredOp = "insert"
	OpDelete   StructuredOp = "delete"
	OpReorder  StructuredOp = "reorder"
)

// DirectiveMeta is advisory metadata for one directive or global option.
//
// The catalog is presentation-only: it never decides parser validity, it
// never hides unsupported or unknown directives, and its entries are only
// suggestions. Catalog returns nil for names it does not know, so callers
// keep showing and preserving unknown/plugin directives untouched.
type DirectiveMeta struct {
	// Name is the directive or global option name.
	Name string
	// Description is a one-line human description.
	Description string
	// Args lists the advisory roles of the positional arguments, in
	// order. The last role repeats for any further arguments.
	Args []ArgRole
	// Ops lists the structured operations the planner supports for this
	// construct. Unknown directives have no entry and no operations.
	Ops []StructuredOp
	// Suggestions are optional, conservative usage hints.
	Suggestions []string
	// Since is the Caddy version that introduced the construct, when
	// verified; empty when not verified.
	Since string
	// Module is the module that provides the construct; "" means the core
	// HTTP server module.
	Module string
}

// allOps and editOps are the operation sets reported by the catalog: the
// planner can insert every catalogued directive into a supported context,
// so the full set applies; global options that are not insertable report
// the edit-only set.
var (
	allOps  = []StructuredOp{OpSetValue, OpInsert, OpDelete, OpReorder}
	editOps = []StructuredOp{OpSetValue, OpDelete, OpReorder}
)

// directives is the advisory catalog for common site-level directives.
var directives = map[string]DirectiveMeta{
	"reverse_proxy": {
		Name:        "reverse_proxy",
		Description: "Proxies requests to one or more upstream servers.",
		Args:        []ArgRole{ArgMatcher, ArgUpstream},
		Ops:         allOps,
		Suggestions: []string{"Use a named matcher to restrict which requests are proxied", "Put transport and header settings in the nested block"},
	},
	"tls": {
		Name:        "tls",
		Description: "Configures TLS certificate management and connection settings.",
		Args:        []ArgRole{ArgDomain},
		Ops:         allOps,
		Suggestions: []string{"A bare tls directive enables automatic HTTPS with defaults", "The optional argument is an email address or \"internal\""},
	},
	"encode": {
		Name:        "encode",
		Description: "Compresses responses with the listed encodings.",
		Args:        []ArgRole{ArgFormat},
		Ops:         allOps,
		Suggestions: []string{"Order matters: gzip, then zstd", "Encode is a terminal handler; keep it early in the route"},
	},
	"log": {
		Name:        "log",
		Description: "Configures access logging for the site, or global logging as a global option.",
		Args:        []ArgRole{ArgString},
		Ops:         allOps,
		Suggestions: []string{"Use a named log to enable multiple site logs", "The global log option configures the process-wide logger"},
	},
	"file_server": {
		Name:        "file_server",
		Description: "Serves static files from the site root.",
		Args:        []ArgRole{ArgMode},
		Ops:         allOps,
		Suggestions: []string{"Use browse to enable directory listings", "Set the root inside the block or with the root directive"},
	},
	"php_fastcgi": {
		Name:        "php_fastcgi",
		Description: "Proxies PHP requests to a FastCGI server.",
		Args:        []ArgRole{ArgUpstream},
		Ops:         allOps,
		Suggestions: []string{"Point the matcher at .php paths", "Keep php_fastcgi before file_server in the route"},
	},
	"header": {
		Name:        "header",
		Description: "Sets or removes response headers.",
		Args:        []ArgRole{ArgField, ArgString},
		Ops:         allOps,
		Suggestions: []string{"Prefix a field with - to remove it", "Use a named matcher as the first argument to scope the rule"},
	},
	"redir": {
		Name:        "redir",
		Description: "Redirects requests to another destination.",
		Args:        []ArgRole{ArgMatcher, ArgPath, ArgString, ArgStatus},
		Ops:         allOps,
		Suggestions: []string{"Omit the status code for a 308 permanent redirect", "The first argument may be a named matcher"},
	},
	"respond": {
		Name:        "respond",
		Description: "Writes a static response.",
		Args:        []ArgRole{ArgStatus, ArgString},
		Ops:         allOps,
		Suggestions: []string{"A bare status code responds with an empty body", "Combine a quoted body with a status code"},
	},
}

// globalOptions is the advisory catalog for common global options.
var globalOptions = map[string]DirectiveMeta{
	"email": {
		Name:        "email",
		Description: "The default email address used for ACME certificate registration.",
		Args:        []ArgRole{ArgDomain},
		Ops:         editOps,
	},
	"admin": {
		Name:        "admin",
		Description: "Configures the Admin API endpoint and access control.",
		Args:        []ArgRole{ArgAddress},
		Ops:         editOps,
	},
	"acme_ca": {
		Name:        "acme_ca",
		Description: "The directory URL of the Certificate Authority used for new certificates.",
		Args:        []ArgRole{ArgString},
		Ops:         editOps,
	},
	"acme_dns": {
		Name:        "acme_dns",
		Description: "The DNS challenge provider used for ACME challenges.",
		Args:        []ArgRole{ArgString},
		Ops:         editOps,
	},
	"auto_https": {
		Name:        "auto_https",
		Description: "Controls automatic HTTPS behavior (on, off, disable_redirects).",
		Args:        []ArgRole{ArgMode},
		Ops:         editOps,
	},
	"debug": {
		Name:        "debug",
		Description: "Enables debug-level logging.",
		Ops:         editOps,
	},
	"http_port": {
		Name:        "http_port",
		Description: "The port used for plaintext HTTP traffic.",
		Args:        []ArgRole{ArgPort},
		Ops:         editOps,
	},
	"https_port": {
		Name:        "https_port",
		Description: "The port used for HTTPS traffic.",
		Args:        []ArgRole{ArgPort},
		Ops:         editOps,
	},
	"local_certs": {
		Name:        "local_certs",
		Description: "Uses internally generated certificates instead of ACME.",
		Ops:         editOps,
	},
	"log": {
		Name:        "log",
		Description: "Configures the process-wide logger.",
		Args:        []ArgRole{ArgString},
		Ops:         allOps,
	},
	"ocsp_stapling": {
		Name:        "ocsp_stapling",
		Description: "Controls OCSP stapling for managed certificates.",
		Args:        []ArgRole{ArgMode},
		Ops:         editOps,
	},
	"order": {
		Name:        "order",
		Description: "Customizes the order in which directives are executed.",
		Args:        []ArgRole{ArgString},
		Ops:         editOps,
	},
	"persist_config": {
		Name:        "persist_config",
		Description: "Controls whether the loaded configuration is persisted to disk.",
		Args:        []ArgRole{ArgMode},
		Ops:         editOps,
	},
	"servers": {
		Name:        "servers",
		Description: "Configures server-wide options.",
		Ops:         editOps,
	},
	"skip_install_trust": {
		Name:        "skip_install_trust",
		Description: "Skips trusting the local CA on the host system.",
		Args:        []ArgRole{ArgMode},
		Ops:         editOps,
	},
	"storage": {
		Name:        "storage",
		Description: "Configures the storage backend for certificates and data.",
		Args:        []ArgRole{ArgString},
		Ops:         editOps,
	},
	"storage_clean_interval": {
		Name:        "storage_clean_interval",
		Description: "The interval at which the storage cleaner runs.",
		Args:        []ArgRole{ArgDuration},
		Ops:         editOps,
	},
}

// Catalog returns advisory metadata for a directive or global option name,
// or nil when the name is not in the catalog. Unknown and plugin directives
// are never hidden and never rejected: callers that receive nil keep
// showing the raw directive as-is.
func Catalog(name string) *DirectiveMeta {
	if m, ok := directives[name]; ok {
		return &m
	}
	if m, ok := globalOptions[name]; ok {
		return &m
	}
	return nil
}
