package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/ckorhonen/openrouter-switch/gateway/internal/auth"
)

const authRequestTimeout = 15 * time.Second

var (
	resolveAuthAPIKey  = auth.ResolveDefaultAPIKey
	validateAuthAPIKey = func(
		ctx context.Context,
		apiKey string,
	) (auth.KeyMetadata, error) {
		return auth.ValidateAPIKey(
			ctx,
			&http.Client{Timeout: authRequestTimeout},
			envDefault("OPENROUTER_BASE_URL", auth.DefaultBaseURL),
			apiKey,
		)
	}
	storeAuthAPIKey = auth.StoreDefaultAPIKey
)

func cmdAuth(args []string) int {
	return runAuth(args, os.Stdout, os.Stderr)
}

func runAuth(args []string, out, errOut io.Writer) int {
	if len(args) == 0 {
		printAuthUsage(errOut)
		return 2
	}
	switch args[0] {
	case "set-key":
		return runAuthSetKey(args[1:], out, errOut)
	case "status":
		return runAuthStatus(args[1:], out, errOut)
	default:
		fmt.Fprintf(errOut, "unknown auth subcommand: %s\n", args[0])
		printAuthUsage(errOut)
		return 2
	}
}

func printAuthUsage(out io.Writer) {
	fmt.Fprintln(out, "usage: openrouter-switch auth set-key|status")
}

func runAuthStatus(args []string, out, errOut io.Writer) int {
	if len(args) != 0 {
		printAuthUsage(errOut)
		return 2
	}
	apiKey, source, err := resolveAuthAPIKey()
	if err != nil {
		if errors.Is(err, auth.ErrNoAPIKey) {
			fmt.Fprintln(errOut, "OpenRouter API key is not configured. Run 'openrouter-switch auth set-key' or set OPENROUTER_API_KEY.")
			return 3
		}
		fmt.Fprintln(errOut, "auth status: could not read the OpenRouter API key")
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), authRequestTimeout)
	defer cancel()
	metadata, err := validateAuthAPIKey(ctx, apiKey)
	if err != nil {
		if auth.IsValidationStatus(err, http.StatusUnauthorized) {
			fmt.Fprintln(errOut, "auth status: OpenRouter rejected the configured API key; run 'openrouter-switch auth set-key' to replace it")
		} else {
			fmt.Fprintln(errOut, "auth status: could not validate the configured OpenRouter API key")
		}
		return 1
	}
	printAuthMetadata(out, source, metadata)
	return 0
}

func printAuthMetadata(out io.Writer, source auth.Source, metadata auth.KeyMetadata) {
	fmt.Fprintln(out, "OpenRouter API key: valid")
	fmt.Fprintf(out, "  Source: %s\n", source)
	fmt.Fprintf(out, "  Label: %s\n", metadata.Label)
	if metadata.Limit == nil {
		fmt.Fprintln(out, "  Limit: none")
	} else {
		fmt.Fprintf(out, "  Limit: $%.2f\n", *metadata.Limit)
	}
	if metadata.LimitRemaining == nil {
		fmt.Fprintln(out, "  Remaining: unknown")
	} else {
		fmt.Fprintf(out, "  Remaining: $%.2f\n", *metadata.LimitRemaining)
	}
	if metadata.LimitReset == nil || *metadata.LimitReset == "" {
		fmt.Fprintln(out, "  Reset: none")
	} else {
		fmt.Fprintf(out, "  Reset: %s\n", *metadata.LimitReset)
	}
	fmt.Fprintf(out, "  Free tier: %t\n", metadata.IsFreeTier)
	if metadata.ExpiresAt == nil {
		fmt.Fprintln(out, "  Expires: none")
	} else {
		fmt.Fprintf(out, "  Expires: %s\n", metadata.ExpiresAt.UTC().Format(time.RFC3339))
	}
}
