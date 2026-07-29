package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/ckorhonen/openrouter-switch/gateway/internal/auth"
	"golang.org/x/term"
)

var readAuthAPIKey = readAPIKeyFromTerminal

func runAuthSetKey(args []string, out, errOut io.Writer) int {
	if len(args) != 0 {
		printAuthUsage(errOut)
		return 2
	}
	metadata, err := promptValidateAndStoreAPIKey(errOut)
	if err != nil {
		switch {
		case errors.Is(err, errReadAPIKey):
			fmt.Fprintln(errOut, "auth set-key: could not read the API key")
		case errors.Is(err, errValidateAPIKey):
			fmt.Fprintln(errOut, "auth set-key: validation failed; the existing Keychain key was not changed")
		case errors.Is(err, errStoreAPIKey):
			fmt.Fprintln(errOut, "auth set-key: validation succeeded, but the key could not be stored in Keychain")
		default:
			fmt.Fprintln(errOut, "auth set-key failed")
		}
		return 1
	}
	fmt.Fprintf(out, "OpenRouter API key validated (%s) and stored in Keychain.\n", metadata.Label)
	reloadRouterAfterCredentialChange(errOut)
	return 0
}

var (
	errReadAPIKey     = errors.New("read OpenRouter API key")
	errValidateAPIKey = errors.New("validate OpenRouter API key")
	errStoreAPIKey    = errors.New("store OpenRouter API key")
)

func promptValidateAndStoreAPIKey(promptOut io.Writer) (auth.KeyMetadata, error) {
	fmt.Fprint(promptOut, "OpenRouter API key: ")
	apiKey, err := readAuthAPIKey()
	fmt.Fprintln(promptOut)
	if err != nil {
		return auth.KeyMetadata{}, fmt.Errorf("%w: terminal input failed", errReadAPIKey)
	}
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return auth.KeyMetadata{}, fmt.Errorf("%w: key was empty", errReadAPIKey)
	}
	ctx, cancel := context.WithTimeout(context.Background(), authRequestTimeout)
	defer cancel()
	metadata, err := validateAuthAPIKey(ctx, apiKey)
	if err != nil {
		return auth.KeyMetadata{}, fmt.Errorf("%w", errValidateAPIKey)
	}
	if err := storeAuthAPIKey(apiKey); err != nil {
		return auth.KeyMetadata{}, fmt.Errorf("%w", errStoreAPIKey)
	}
	return metadata, nil
}

func readAPIKeyFromTerminal() (string, error) {
	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		value, err := term.ReadPassword(fd)
		return string(value), err
	}
	value, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	return value, nil
}

func reloadRouterAfterCredentialChange(out io.Writer) {
	switch state, pid := classifyPidfile(gatewayPidfilePath()); state {
	case pidfileAlive:
		if err := signalRouter(pid); err != nil {
			fmt.Fprintf(out, "warning: could not SIGHUP router pid %d; run 'openrouter-switch restart' to load the new key\n", pid)
		} else {
			fmt.Fprintf(out, "router reloaded (SIGHUP pid %d)\n", pid)
		}
	default:
		fmt.Fprintln(out, "router not running; the new key loads at the next start")
	}
}
