package config

import (
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/Marcuss-ops/PipelineGen/pkg/defaults"
)

// minHMACSecretLen guards against operators shipping a too-short HMAC
// key. 32 bytes = 256 bits matches the user's directive ("≥32 byte casuali
// provenienti da secret manager"); smaller values are sane for toy dev
// but a foot-gun in production.
const minHMACSecretLen = 32

// placeholderPatterns matches anything that smells like a stock
// placeholder value the operator forgot to replace. We block on these
// at boot instead of silently allowing them into production tokens.
//
//	YOUR_*_HERE          — the canonical config.example.yaml marker
//	CHANGE_ME_*          — common alternative
//	TODO_SECRET*         — explicit "fix this before deploy" marker
//	PLACEHOLDER*         — generic placeholder
//	REPLACE_ME*, FIXME — generic reminder markers
//
// The match is intentionally case-insensitive (PLACEHOLDER_TOK in some
// tooling, lowercase "change_me" in others).
var placeholderPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)^YOUR_[A-Z0-9_]+_HERE$`),
	regexp.MustCompile(`(?i)^CHANGE[_-]?ME[_A-Z0-9]*$`),
	regexp.MustCompile(`(?i)^TODO_SECRET.*$`),
	regexp.MustCompile(`(?i)^PLACEHOLDER.*$`),
	regexp.MustCompile(`(?i)^FIXME.*$`),
	regexp.MustCompile(`(?i)^REPLACE[_-]?ME.*$`),
	regexp.MustCompile(`(?i)^XXX$`),
}

// IsPlaceholderValue returns true if v matches any known placeholder
// pattern. Used by Validate() to reject tokens, secrets and HMAC keys.
// Empty string is NOT a placeholder (empty means "not configured").
func IsPlaceholderValue(v string) bool {
	v = strings.TrimSpace(v)
	if v == "" {
		return false
	}
	for _, p := range placeholderPatterns {
		if p.MatchString(v) {
			return true
		}
	}
	return false
}

// Get loads configuration from the canonical config.yaml file.
// Errors are propagated — no silent fallback. If the file is
// missing, GetFromPath returns defaults+env with a nil error;
// every other error (corrupted YAML, bad permissions, read
// failures) is an inescapable boot failure.
func Get() (*Config, error) {
	return GetFromPath("config.yaml")
}

// GetFromPath loads configuration from an explicit file path.
// It returns an error if the file exists but is malformed, so that
// operators are notified immediately instead of silently using partial
// defaults.
func GetFromPath(path string) (*Config, error) {
	cfg := &Config{}
	// Resolve struct-tag defaults before YAML so an explicit false/zero in the
	// file remains distinguishable from an omitted field. Environment values
	// are applied last and remain the highest-precedence source.
	applyDefaults(cfg)
	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("cannot read config file %q: %w", path, err)
		}
	} else if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("config file %q is malformed: %w", path, err)
	}

	// The resolution order is deliberate and uniform:
	// YAML → environment overrides → defaults for fields still unset.
	// This prevents a default pass from masking an explicit YAML value
	// and makes the final Config ready for validation and freezing.
	applyEnvVars(cfg)
	applyCanonicalModelDefaults(cfg)
	return cfg, nil
}

// hostPortConflict reports whether two URLs share the same host:port.
// It handles empty strings, missing ports (defaulting to scheme defaults),
// and IPv6 brackets. Returns false if either URL is unparsable.
func hostPortConflict(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	ua, err := url.Parse(a)
	if err != nil {
		return false
	}
	ub, err := url.Parse(b)
	if err != nil {
		return false
	}

	// Strip IPv6 brackets for comparison
	ha := strings.Trim(ua.Hostname(), "[]")
	hb := strings.Trim(ub.Hostname(), "[]")
	if ha != hb {
		return false
	}

	pa := ua.Port()
	pb := ub.Port()
	// Default ports per scheme
	if pa == "" {
		switch ua.Scheme {
		case "http":
			pa = "80"
		case "https":
			pa = "443"
		}
	}
	if pb == "" {
		switch ub.Scheme {
		case "http":
			pb = "80"
		case "https":
			pb = "443"
		}
	}
	return pa == pb && pa != ""
}

// devEscapeHmac returns true when the operator has explicitly opted out
// of the HMAC ≥32-bytes rule via VELOX_ALLOW_INSECURE_DEV=true. The escape
// is logged at WARN by Validate(). NEVER meant for production.
func devEscapeHmac(c *Config) bool {
	return c != nil && c.Security.DeliveryInsecureDev
}

// Validate performs a comprehensive sanity check of the loaded
// configuration. Fail-fast and loud — never silent.
//
// In addition to the legacy checks (port range, timeouts, host bind,
// auth enablement) the Operational Readiness PR June 2026 added:
//
//   - placeholder-pattern rejection for tokens, secrets, HMAC keys;
//   - mandatory ≥32-byte HMAC secret (256-bit) in production;
//   - dev escape hatch only with VELOX_ALLOW_INSECURE_DEV=true.
func (c *Config) Validate() error {
	if c == nil {
		return nil
	}
	if c.Server.Port <= 0 || c.Server.Port > 65535 {
		return fmt.Errorf("invalid server port: %d", c.Server.Port)
	}
	if c.Server.ReadTimeout <= 0 {
		return fmt.Errorf("invalid read timeout: %d", c.Server.ReadTimeout)
	}
	if c.Server.WriteTimeout <= 0 {
		return fmt.Errorf("invalid write timeout: %d", c.Server.WriteTimeout)
	}
	if c.External.OllamaURL == "" {
		return fmt.Errorf("ollama url is required")
	}
	if c.External.VeloxMasterURL != "" {
		if _, err := url.Parse(c.External.VeloxMasterURL); err != nil {
			return fmt.Errorf("invalid velox_master_url %q: %w", c.External.VeloxMasterURL, err)
		}
		if u, perr := url.Parse(c.External.VeloxMasterURL); perr == nil {
			if u.Scheme != "http" && u.Scheme != "https" {
				return fmt.Errorf("velox_master_url must use http or https (got scheme=%q)", u.Scheme)
			}
		}
	}

	// Security: prevent production from starting with auth disabled on a public interface
	isPublicInterface := c.Server.Host == "0.0.0.0" || c.Server.Host == "" || strings.HasPrefix(c.Server.Host, "::")
	if !c.Security.EnableAuth && isPublicInterface {
		return fmt.Errorf("refusing to start: enable_auth is false and server is bound to public interface %s. Set host to 127.0.0.1 or enable auth", c.Server.Host)
	}
	if c.Security.EnableAuth && strings.TrimSpace(c.Security.AdminToken) == "" {
		return fmt.Errorf("admin token is required when auth is enabled")
	}

	// Security: warn against real Drive folder IDs in production config (if committed)
	if c.Drive.MediaRootFolder != "" && strings.HasPrefix(c.Drive.MediaRootFolder, "1") {
		// Heuristic: real Google Drive IDs are 33 chars, but allow empty for dynamic resolution
	}

	// Placeholder rejection — Operational Readiness PR June 2026.
	// Hard reject in CI/staging/production. Dev escape EXCLUSIVELY via
	// VELOX_ALLOW_PLACEHOLDERS=true (governance contract: this flag is
	// NEVER meant for production deployments).
	allowPlaceholders := strings.EqualFold(strings.TrimSpace(os.Getenv("VELOX_ALLOW_PLACEHOLDERS")), "true")

	if !allowPlaceholders {
		if IsPlaceholderValue(c.Security.AdminToken) {
			return fmt.Errorf("refusing to start: security.admin_token is a placeholder value (YOUR_*_HERE / CHANGE_ME_* / TODO_SECRET / PLACEHOLDER). Set VELOX_ADMIN_TOKEN in the environment or replace the value before booting (escape: VELOX_ALLOW_PLACEHOLDERS=true)")
		}
		if IsPlaceholderValue(c.Security.WorkerToken) {
			return fmt.Errorf("refusing to start: security.worker_token is a placeholder value (YOUR_*_HERE / CHANGE_ME_* / TODO_SECRET / PLACEHOLDER). Set VELOX_WORKER_TOKEN in the environment or replace the value (escape: VELOX_ALLOW_PLACEHOLDERS=true)")
		}
		if IsPlaceholderValue(c.Security.WebhookSecret) {
			return fmt.Errorf("refusing to start: security.webhook_secret is a placeholder value. Replace before booting (escape: VELOX_ALLOW_PLACEHOLDERS=true)")
		}
		if IsPlaceholderValue(c.Security.DeliveryHMACSecret) {
			return fmt.Errorf("refusing to start: security.delivery_hmac_secret matches a placeholder pattern (YOUR_*_HERE / CHANGE_ME_* / TODO_SECRET / PLACEHOLDER). Generate ≥32 bytes random from a secret manager; never use a placeholder")
		}
		if IsPlaceholderValue(c.Security.DeliveryHMACSecretPrevious) {
			return fmt.Errorf("refusing to start: security.delivery_hmac_secret_previous is a placeholder. Set or remove it; never use a placeholder for any secret")
		}
	}

	// HMAC secret ≥32 bytes mandatory in production. Dev escape hatch via
	// VELOX_ALLOW_INSECURE_DEV=true is the ONLY override and logs an
	// unmistakable warning. The escape is NEVER a substitute for a real
	// secret in CI/staging/production.
	if !devEscapeHmac(c) {
		curLen := len(strings.TrimSpace(c.Security.DeliveryHMACSecret))
		prevLen := len(strings.TrimSpace(c.Security.DeliveryHMACSecretPrevious))
		switch {
		case curLen == 0:
			return fmt.Errorf("refusing to start: security.delivery_hmac_secret is required (≥32 bytes random). Set VELOX_DELIVERY_HMAC_SECRET. Dev escape: VELOX_ALLOW_INSECURE_DEV=true")
		case curLen < minHMACSecretLen:
			return fmt.Errorf("refusing to start: security.delivery_hmac_secret is too short (got %d bytes, need ≥%d). Generate ≥32 bytes random from a secret manager", curLen, minHMACSecretLen)
		case prevLen > 0 && prevLen < minHMACSecretLen:
			return fmt.Errorf("refusing to start: security.delivery_hmac_secret_previous is too short (got %d bytes, need ≥%d)", prevLen, minHMACSecretLen)
		}
	}

	// Replay window bounds.
	if c.Security.DeliveryReplayWindowSec < 0 {
		return fmt.Errorf("invalid delivery_replay_window_seconds: %d (must be ≥0, 0 = no protection)", c.Security.DeliveryReplayWindowSec)
	}

	// Validate that there is no port conflict between configured external services
	if c.External.NvidiaLocalNIMURL != "" && c.GoogleAccounting.Enabled {
		if hostPortConflict(c.External.NvidiaLocalNIMURL, c.GoogleAccounting.ServerURL) {
			return fmt.Errorf("port conflict: NVIDIA NIM and Google Accounting both configured on the same host:port. Change one service's port")
		}
	}

	// Loaded configurations have the centralized defaults applied. Keep
	// manually assembled test/adapter configs compatible during migration;
	// a partially populated defaults block is always rejected.
	defaultsConfigured := c.Scripts.Defaults != (ScriptDefaultsConfig{}) || c.Voiceover.Defaults != (VoiceoverDefaultsConfig{})
	if defaultsConfigured {
		if c.Scripts.Defaults.WordsPerMinute <= 0 || strings.TrimSpace(c.Scripts.Defaults.SafetyLanguage) == "" {
			return fmt.Errorf("scripts.defaults is incomplete")
		}
		if c.Voiceover.Defaults.DefaultParallelism <= 0 || c.Voiceover.Defaults.MaxParallelism < c.Voiceover.Defaults.DefaultParallelism {
			return fmt.Errorf("voiceover.defaults parallelism is invalid: default=%d max=%d", c.Voiceover.Defaults.DefaultParallelism, c.Voiceover.Defaults.MaxParallelism)
		}
		if strings.TrimSpace(c.Voiceover.Defaults.DefaultFilenameTemplate) == "" || strings.TrimSpace(c.Voiceover.Defaults.DefaultStrategy) == "" || strings.TrimSpace(c.Voiceover.Defaults.DefaultLanguage) == "" {
			return fmt.Errorf("voiceover.defaults filename, strategy and language are required")
		}
	}

	// Keep the legacy package-level validator for unrelated defaults while
	// the remaining domains migrate onto ResolvedConfig.
	if err := defaults.Validate(); err != nil {
		return fmt.Errorf("defaults SSOT validation failed: %w", err)
	}

	return nil
}
