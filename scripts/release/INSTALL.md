# Install OpenRouter Switch on macOS

> **Beta:** OpenRouter Switch is currently a public beta. The CLI and app are
> ad-hoc signed but are not Apple-notarized. macOS may require explicit
> approval before the app opens for the first time.

Homebrew is the canonical public install and upgrade channel:

```sh
brew install ckorhonen/openrouter-switch/openrouter-switch
```

Then authenticate and start the gateway:

```sh
openrouter-switch setup
openrouter-switch up --install
openrouter-switch claude on
openrouter-switch doctor --probe
```

## Direct release asset

Approved direct installs use
`openrouter-switch_<version>_darwin_universal.zip`. The release also publishes
`checksums.txt`. Verify the ZIP's SHA-256 entry before extracting it.

The release ZIP contains the universal, ad-hoc signed CLI, a nested ad-hoc signed
`OpenRouter Switch.app.zip`, and `install.sh`. Run:

```sh
unzip openrouter-switch_<version>_darwin_universal.zip \
  -d openrouter-switch_<version>
cd openrouter-switch_<version>
./install.sh
```

The installer validates the ad-hoc signatures, matching versions, and universal
architectures. An ad-hoc signature detects changes after packaging, but it does
not establish an Apple-verified developer identity or notarization.

## First launch approval

If macOS blocks the beta app on first launch:

1. In Finder, open `~/Applications`, double-click **OpenRouter Switch**, then
   dismiss the warning.
2. Choose **Apple menu > System Settings > Privacy & Security**.
3. Scroll to the **Security** section and click **Open Anyway** for OpenRouter
   Switch.
4. Confirm **Open** and authenticate if macOS asks.

The **Open Anyway** button is available for about one hour after the blocked
launch attempt. A managed Mac may prevent this override. OpenRouter Switch does
not ask users to clear quarantine metadata or disable Gatekeeper.

## Uninstall

```sh
openrouter-switch uninstall --dry-run
openrouter-switch uninstall
```

Use `openrouter-switch uninstall --purge --yes` to remove retained OpenRouter
Switch config, telemetry, logs, and backups. The OpenRouter Keychain entry is
retained unless explicitly removed in the app or macOS Keychain Access. The
command prints manual instructions when the macOS app's Start at Login item
prevents safe automated bundle removal.

The repository README contains the supported install, operation,
troubleshooting, and uninstall instructions.
