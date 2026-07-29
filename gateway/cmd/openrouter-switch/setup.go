package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/ckorhonen/openrouter-switch/gateway/internal/config"
)

var errSetupCredentialUnavailable = errors.New("OpenRouter API key is missing or unusable")

type setupDependencies struct {
	loadCredential func() (string, error)
	setKey         func(io.Writer) (string, error)
	configPath     func() string
	stat           func(string) (os.FileInfo, error)
	initConfig     func(string, bool, io.Writer) int
}

func defaultSetupDependencies() setupDependencies {
	return setupDependencies{
		loadCredential: loadCurrentSetupCredential,
		setKey: func(promptOut io.Writer) (string, error) {
			metadata, err := promptValidateAndStoreAPIKey(promptOut)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("valid Keychain key (%s)", metadata.Label), nil
		},
		configPath: func() string {
			return envDefault("OPENROUTER_SWITCH_CONFIG_PATH", config.DefaultPath())
		},
		stat:       os.Stat,
		initConfig: runConfigInit,
	}
}

func cmdSetup(args []string) int {
	if len(args) != 0 {
		fmt.Fprintln(os.Stderr, "usage: openrouter-switch setup")
		return 2
	}
	return runSetup(defaultSetupDependencies(), os.Stdout, os.Stderr)
}

func runSetup(deps setupDependencies, out, errOut io.Writer) int {
	credential, err := deps.loadCredential()
	if err != nil {
		fmt.Fprintln(out, "OpenRouter API key: unavailable; enter a key to continue.")
		credential, err = deps.setKey(errOut)
		if err != nil {
			fmt.Fprintln(errOut, "setup: OpenRouter API key validation or storage failed")
			return 1
		}
	}
	fmt.Fprintf(out, "OpenRouter API key: %s\n", credential)

	path := deps.configPath()
	if _, err := deps.stat(path); err == nil {
		fmt.Fprintf(out, "Gateway config: using existing %s\n", path)
	} else if !os.IsNotExist(err) {
		fmt.Fprintf(errOut, "setup: inspect gateway config %s: %v\n", path, err)
		return 1
	} else {
		if code := deps.initConfig(path, false, io.Discard); code != 0 {
			fmt.Fprintf(errOut, "setup: could not initialize gateway config at %s\n", path)
			return 1
		}
		fmt.Fprintf(out, "Gateway config: created %s\n", path)
	}

	fmt.Fprintln(out, "\nNext commands:")
	fmt.Fprintln(out, "openrouter-switch up --install")
	fmt.Fprintln(out, "openrouter-switch menubar")
	fmt.Fprintln(out, "select an account model in the app, then turn OpenRouter routing on")
	fmt.Fprintln(out, "openrouter-switch claude on")
	fmt.Fprintln(out, "openrouter-switch codex route <account-model>")
	fmt.Fprintln(out, "openrouter-switch codex on")
	fmt.Fprintln(out, "openrouter-switch doctor --probe")
	return 0
}

func loadCurrentSetupCredential() (string, error) {
	apiKey, source, err := resolveAuthAPIKey()
	if err != nil {
		return "", fmt.Errorf("%w", errSetupCredentialUnavailable)
	}
	ctx, cancel := context.WithTimeout(context.Background(), authRequestTimeout)
	defer cancel()
	metadata, err := validateAuthAPIKey(ctx, apiKey)
	if err != nil {
		return "", fmt.Errorf("%w", errSetupCredentialUnavailable)
	}
	return fmt.Sprintf("valid %s key (%s)", source, metadata.Label), nil
}
