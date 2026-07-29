package gateway

// Preflight checks run at startup and on SIGHUP reload. They are warn-only:
// they log actionable lines and never stop the gateway.

import (
	"fmt"
	"io"
	"os"

	"github.com/ckorhonen/openrouter-switch/gateway/internal/auth"
	"github.com/ckorhonen/openrouter-switch/gateway/internal/config"
)

// preflightConfigPath mirrors Gateway.activeConfigPath for a bare Config.
func preflightConfigPath(cfg *Config) string {
	if cfg.ConfigPath != "" {
		return cfg.ConfigPath
	}
	return config.DefaultPath()
}

// applyGlobalAuth loads gateway.yaml and applies global.auth to the
// process environment exactly like the admin config PUT path
// (applyConfigEnv), then refreshes the key fields on cfg from the
// environment. This keeps startup, SIGHUP, and admin updates consistent.
func applyGlobalAuth(cfg *Config) {
	f, err := config.Load(preflightConfigPath(cfg))
	if err != nil {
		return
	}
	applyConfigEnv(f)
	key, source, err := resolveConfigCredential(*cfg)
	cfg.OpenRouterKey = key
	cfg.CredentialSource = source
	cfg.CredentialFingerprint = auth.CredentialFingerprint(key)
	cfg.CredentialError = err
}

// UnresolvedPlaceholders returns the ${VAR} placeholder names referenced
// by the config that are not set in the process environment. Exported
// for `openrouter-switch doctor`, which runs the same detection out of process
// (and additionally consults the gateway's env file, which the gateway
// itself loads into its environment before this check runs).
func UnresolvedPlaceholders(f *config.File) []string {
	var out []string
	for _, name := range f.CollectPlaceholders() {
		if os.Getenv(name) == "" {
			out = append(out, name)
		}
	}
	return out
}

// warnUnresolvedPlaceholders logs one actionable line per unresolved
// ${VAR}, naming the variable and where to set it.
func warnUnresolvedPlaceholders(f *config.File, out io.Writer) {
	for _, name := range UnresolvedPlaceholders(f) {
		fmt.Fprintf(out,
			"[gateway] warning: gateway.yaml references ${%s} but %s is not set; it expands to empty. Fix: add %s=... to %s or export it in the gateway's environment\n",
			name, name, name, config.EnvFilePath())
	}
}

// openrouterRoutedClients returns a display entry per enabled client whose
// route or fallback_route is openrouter. Passthrough routes (anthropic,
// openai) are excluded: they use harness credential passthrough and need
// no gateway-side key.
func openrouterRoutedClients(resolved []resolvedClientConfig) []string {
	var out []string
	for _, rc := range resolved {
		switch {
		case rc.Route == "openrouter":
			out = append(out, rc.Name+" (route: openrouter)")
		case rc.FallbackRoute == "openrouter":
			out = append(out, rc.Name+" (fallback_route: openrouter)")
		}
	}
	return out
}

func hasOpenRouterCredential(apiKey string) bool {
	return apiKey != ""
}

// warnMissingOpenRouterCreds prints a prominent banner naming the affected
// clients and the fix. Warn-only.
func warnMissingOpenRouterCreds(names []string, out io.Writer) {
	if len(names) == 0 {
		return
	}
	rule := "[gateway] =============================================================="
	fmt.Fprintln(out, rule)
	fmt.Fprintln(out, "[gateway] WARNING: no OpenRouter API key found. These clients route")
	fmt.Fprintln(out, "[gateway] to OpenRouter and their requests")
	fmt.Fprintln(out, "[gateway] will fail until a credential is configured:")
	for _, n := range names {
		fmt.Fprintf(out, "[gateway]   - %s\n", n)
	}
	fmt.Fprintln(out, "[gateway] Fix: run 'openrouter-switch auth set-key', or set")
	fmt.Fprintf(out, "[gateway] OPENROUTER_API_KEY in %s.\n", config.EnvFilePath())
	fmt.Fprintln(out, rule)
}

// runPreflight runs the warn-only checks: unresolved ${VAR} placeholders
// against the process environment, and openrouter-routed clients without a
// usable credential. Never fatal.
func runPreflight(cfg *Config, resolved []resolvedClientConfig, out io.Writer) {
	if f, err := config.Load(preflightConfigPath(cfg)); err == nil {
		warnUnresolvedPlaceholders(f, out)
	}
	names := openrouterRoutedClients(resolved)
	if len(names) == 0 {
		return
	}
	if hasOpenRouterCredential(cfg.OpenRouterKey) {
		return
	}
	warnMissingOpenRouterCreds(names, out)
}
