package api

import (
	"errors"
	"fmt"
	"net/netip"
	"time"
)

// Recursors (SPEC §20): PowerDNS Recursor or Unbound hosts the nodes use as upstreams,
// run by dnsjos-agent in recursor mode.

const (
	RecursorPDNS    = "pdns"
	RecursorUnbound = "unbound"
)

var RecursorKinds = []string{RecursorPDNS, RecursorUnbound}

// CmdRestartResolver restarts pdns-recursor / unbound (recursor command).
const CmdRestartResolver = "restart_resolver"

// RecursorCommandTypes are the commands a recursor agent understands.
var RecursorCommandTypes = []string{CmdReapply, CmdRestartResolver, CmdUpgradeAgent}

// Limits of RecursorSettings.
const (
	MaxRecursorAllow    = 256
	MaxRecursorECSZones = 100
	MinRecursorCacheMB  = 32
	MaxRecursorCacheMB  = 65536
	// Manual allow-list entries must not open the resolver to the internet.
	MinAllowPrefixV4 = 8
	MinAllowPrefixV6 = 16
)

// RecursorSettings are the panel-managed resolver settings. Every live node is allowed
// automatically; AllowExtra adds addresses or CIDRs (monitoring hosts, private networks).
type RecursorSettings struct {
	AllowExtra []string `json:"allow_extra"`
	// ECS passes the client subnet dnsdist adds on to authoritative servers: all of them,
	// or only those of ECSZones.
	ECS      bool     `json:"ecs"`
	ECSZones []string `json:"ecs_zones"`
	// CacheMB is the approximate cache memory; 0 sizes it from the host's RAM.
	CacheMB int `json:"cache_mb"`
}

// Validate reports every problem in the settings (nil when valid).
func (s RecursorSettings) Validate() error {
	var errs []error
	if len(s.AllowExtra) > MaxRecursorAllow {
		errs = append(errs, fmt.Errorf("allow_extra: at most %d entries", MaxRecursorAllow))
	}
	for i, a := range s.AllowExtra {
		if err := CheckAllowEntry(a); err != nil {
			errs = append(errs, fmt.Errorf("allow_extra[%d]: %w", i, err))
		}
	}
	if len(s.ECSZones) > MaxRecursorECSZones {
		errs = append(errs, fmt.Errorf("ecs_zones: at most %d zones", MaxRecursorECSZones))
	}
	for i, z := range s.ECSZones {
		if len(z) > 253 || !hostnameRe.MatchString(z) {
			errs = append(errs, fmt.Errorf("ecs_zones[%d]: %q is not a domain name", i, z))
		}
	}
	if s.CacheMB != 0 && (s.CacheMB < MinRecursorCacheMB || s.CacheMB > MaxRecursorCacheMB) {
		errs = append(errs, fmt.Errorf("cache_mb: 0 (automatic) or %d..%d", MinRecursorCacheMB, MaxRecursorCacheMB))
	}
	return errors.Join(errs...)
}

// AllowPrefix parses an allow-list entry (an address or a CIDR) into a masked prefix.
func AllowPrefix(s string) (netip.Prefix, error) {
	if p, err := netip.ParsePrefix(s); err == nil {
		return p.Masked(), nil
	}
	a, err := netip.ParseAddr(s)
	if err != nil || a.Zone() != "" {
		return netip.Prefix{}, fmt.Errorf("%q is not an IP address or CIDR", s)
	}
	a = a.Unmap()
	return netip.PrefixFrom(a, a.BitLen()), nil
}

// CheckAllowEntry rejects entries that are not addresses/CIDRs or that are too wide.
func CheckAllowEntry(s string) error {
	p, err := AllowPrefix(s)
	if err != nil {
		return err
	}
	if p.Addr().Is4() && p.Bits() < MinAllowPrefixV4 || p.Addr().Is6() && p.Bits() < MinAllowPrefixV6 {
		return fmt.Errorf("%q is too wide (at least /%d for IPv4, /%d for IPv6): it would open the resolver to the internet",
			s, MinAllowPrefixV4, MinAllowPrefixV6)
	}
	return nil
}

// Recursor is GET /api/v1/recursors/{id}.
type Recursor struct {
	ID              string           `json:"id"`
	Name            string           `json:"name"`
	Kind            string           `json:"kind"`
	Hostname        string           `json:"hostname"`
	PublicIP        string           `json:"public_ip"`
	ListenIP        string           `json:"listen_ip"`   // from the latest heartbeat
	ListenIPv6      string           `json:"listen_ipv6"` // "" without IPv6
	Status          string           `json:"status"`
	Settings        RecursorSettings `json:"settings"`
	EnrolledAt      *time.Time       `json:"enrolled_at"`
	LastSeenAt      *time.Time       `json:"last_seen_at"`
	AgentVersion    string           `json:"agent_version"`
	AgentOutdated   bool             `json:"agent_outdated"`
	ResolverVersion string           `json:"resolver_version"`
	OS              string           `json:"os"`
	Arch            string           `json:"arch"`
	// AppliedConfigVersion is what the agent runs; DesiredConfigVersion what it should.
	AppliedConfigVersion *int      `json:"applied_config_version"`
	DesiredConfigVersion int       `json:"desired_config_version"`
	ConfigInSync         bool      `json:"config_in_sync"`
	LastError            string    `json:"last_error"`
	CreatedAt            time.Time `json:"created_at"`
	QPS                  float64   `json:"qps"`
	CacheHitRatio        float64   `json:"cache_hit_ratio"`
	LatencyAvgMs         float64   `json:"latency_avg_ms"`
	// Allow is the effective allow-list sent to the agent (loopback + nodes + AllowExtra).
	Allow []string `json:"allow"`
	// UsedBy lists the nodes whose served config has this recursor as an upstream.
	UsedBy []RecursorUser `json:"used_by"`
}

// RecursorUser is one node upstream pointing at a recursor.
type RecursorUser struct {
	NodeID   string `json:"node_id"`
	NodeName string `json:"node_name"`
	Address  string `json:"address"`
	ECS      bool   `json:"ecs"`
}

// RecursorPatch is PATCH /api/v1/recursors/{id}; nil fields are left unchanged.
type RecursorPatch struct {
	Name     *string           `json:"name"`
	PublicIP *string           `json:"public_ip"`
	Settings *RecursorSettings `json:"settings"`
}

type RecursorTokenCreate struct {
	Name     string `json:"name"` // "" = the host's hostname
	Kind     string `json:"kind"`
	TTLHours int    `json:"ttl_hours"` // default 24, 1..720
}

type RecursorTokenCreated struct {
	ID             string    `json:"id"`
	Token          string    `json:"token"`
	InstallCommand string    `json:"install_command"`
	ExpiresAt      time.Time `json:"expires_at"`
}

type RecursorToken struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Kind      string     `json:"kind"`
	CreatedBy *string    `json:"created_by"`
	CreatedAt time.Time  `json:"created_at"`
	ExpiresAt time.Time  `json:"expires_at"`
	UsedAt    *time.Time `json:"used_at"`
	UsedBy    *string    `json:"used_by"`
}

// RecursorEnrollRequest is POST /agent/v1/recursor/enroll.
type RecursorEnrollRequest struct {
	Token           string `json:"token"`
	Hostname        string `json:"hostname"`
	Kind            string `json:"kind"` // what the installer set up; must match the token
	OS              string `json:"os"`
	Arch            string `json:"arch"`
	AgentVersion    string `json:"agent_version"`
	ResolverVersion string `json:"resolver_version"`
	PublicIP        string `json:"public_ip"` // optional; default: the request's source address
}

type RecursorEnrollResponse struct {
	RecursorID    string `json:"recursor_id"`
	Name          string `json:"name"`
	Kind          string `json:"kind"`
	Token         string `json:"token"`
	PollIntervalS int    `json:"poll_interval_s"`
}

// RecursorConfig is GET /agent/v1/recursor/config.
type RecursorConfig struct {
	Version            int              `json:"version"`
	Kind               string           `json:"kind"`
	PublicIP           string           `json:"public_ip"`
	Allow              []string         `json:"allow"` // complete allow-list, CIDRs
	Settings           RecursorSettings `json:"settings"`
	PollIntervalS      int              `json:"poll_interval_s"`
	HeartbeatIntervalS int              `json:"heartbeat_interval_s"`
}

// RecursorCounters are raw, monotonically increasing resolver counters.
type RecursorCounters struct {
	Queries     int64 `json:"queries"`
	CacheHits   int64 `json:"cache_hits"`
	CacheMisses int64 `json:"cache_misses"`
	Servfail    int64 `json:"servfail"`
	Outgoing    int64 `json:"outgoing"` // queries sent to authoritative servers
}

// RecursorHeartbeat is POST /agent/v1/recursor/heartbeat.
type RecursorHeartbeat struct {
	Time                 time.Time        `json:"time"`
	AgentVersion         string           `json:"agent_version"`
	ResolverVersion      string           `json:"resolver_version"`
	OS                   string           `json:"os"`
	UptimeS              int64            `json:"uptime_s"` // agent uptime
	ResolverRunning      bool             `json:"resolver_running"`
	ProbeOK              bool             `json:"probe_ok"` // a test lookup through the resolver answered
	ProbeError           string           `json:"probe_error"`
	ProbeMs              float64          `json:"probe_ms"`
	AppliedConfigVersion int              `json:"applied_config_version"`
	ApplyError           string           `json:"apply_error"`
	StatsError           string           `json:"stats_error"`
	ListenIP             string           `json:"listen_ip"`
	ListenIPv6           string           `json:"listen_ipv6"`
	System               SystemStats      `json:"system"`
	Counters             RecursorCounters `json:"counters"`
	LatencyAvgMs         float64          `json:"latency_avg_ms"`
	AckedCommands        []int64          `json:"acked_commands"`
	LastUpgrade          *UpgradeResult   `json:"last_upgrade,omitempty"`
}

type RecursorAck struct {
	ConfigVersion int       `json:"config_version"`
	Commands      []Command `json:"commands,omitempty"`
}

// RecursorLive is GET /api/v1/recursors/{id}/live; Heartbeat is null before the first one.
type RecursorLive struct {
	RecursorID string             `json:"recursor_id"`
	Heartbeat  *RecursorHeartbeat `json:"heartbeat"`
	ReceivedAt *time.Time         `json:"received_at"`
}

type RecursorMetricSample struct {
	TS            time.Time `json:"ts"`
	Queries       int64     `json:"queries"`
	CacheHits     int64     `json:"cache_hits"`
	CacheMisses   int64     `json:"cache_misses"`
	Servfail      int64     `json:"servfail"`
	Outgoing      int64     `json:"outgoing"`
	LatencyAvgMs  float64   `json:"latency_avg_ms"`
	QPS           float64   `json:"qps"`
	CacheHitRatio float64   `json:"cache_hit_ratio"`
	ServfailRatio float64   `json:"servfail_ratio"`
	OutgoingQPS   float64   `json:"outgoing_qps"`
}

// RecursorMetricSeries is GET /api/v1/recursors/{id}/metrics (same range params as nodes).
type RecursorMetricSeries struct {
	From   time.Time              `json:"from"`
	To     time.Time              `json:"to"`
	StepS  int                    `json:"step_s"`
	Points []RecursorMetricSample `json:"points"`
}
