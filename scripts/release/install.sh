#!/bin/sh
# Install an ad-hoc signed OpenRouter Switch beta release payload on macOS.
# Homebrew is the canonical public installation path. This installer is
# retained for release-asset verification and approved direct installs.
set -eu
cd "$(dirname "$0")"

fail() {
    echo "install.sh: $*" >&2
    exit 1
}

BIN_DIR="${OPENROUTER_SWITCH_BIN_DIR:-$HOME/.local/bin}"
APP_DIR="$HOME/Applications"
APP_ZIP="OpenRouter Switch.app.zip"
APP_NAME="OpenRouter Switch.app"

[ "$(uname -s)" = "Darwin" ] || fail "requires macOS"
[ -x bin/openrouter-switch ] || fail "missing executable bin/openrouter-switch"
[ -f "$APP_ZIP" ] || fail "missing nested app payload '$APP_ZIP'"

mkdir -p "$APP_DIR"
STAGE="$(mktemp -d "$APP_DIR/.openrouter-switch-install.XXXXXX")"
trap 'rm -rf "$STAGE"' EXIT HUP INT TERM
/usr/bin/ditto -x -k "$APP_ZIP" "$STAGE"
APP="$STAGE/$APP_NAME"
PLIST="$APP/Contents/Info.plist"
APP_BIN="$APP/Contents/MacOS/OpenRouterSwitch"

[ -d "$APP" ] || fail "nested app ZIP did not contain '$APP_NAME'"
[ -x "$APP_BIN" ] || fail "app executable is missing"
[ "$(/usr/bin/plutil -extract CFBundleIdentifier raw "$PLIST")" = "co.baseten.switch" ] \
    || fail "unexpected app bundle identifier"
[ "$(/usr/bin/plutil -extract CFBundleDisplayName raw "$PLIST")" = "OpenRouter Switch" ] \
    || fail "unexpected app display name"
[ "$(/usr/bin/plutil -extract CFBundleExecutable raw "$PLIST")" = "OpenRouterSwitch" ] \
    || fail "unexpected app executable name"

MARKETING_VERSION="$(/usr/bin/plutil -extract CFBundleShortVersionString raw "$PLIST")"
BUILD_NUMBER="$(/usr/bin/plutil -extract CFBundleVersion raw "$PLIST")"
printf '%s\n' "$MARKETING_VERSION" | grep -Eq '^[0-9]+(\.[0-9]+){1,2}$' \
    || fail "app marketing version is not numeric: $MARKETING_VERSION"
printf '%s\n' "$BUILD_NUMBER" | grep -Eq '^[0-9]+(\.[0-9]+)*$' \
    || fail "app build number is not numeric: $BUILD_NUMBER"

CLI_VERSION="$(bin/openrouter-switch --version)"
case "$CLI_VERSION" in
    "openrouter-switch v$MARKETING_VERSION") ;;
    *) fail "CLI version '$CLI_VERSION' does not match app version '$MARKETING_VERSION'" ;;
esac

for binary in bin/openrouter-switch "$APP_BIN"; do
    ARCHS="$(/usr/bin/lipo -archs "$binary")"
    case " $ARCHS " in *" arm64 "*) ;; *) fail "$binary is missing arm64" ;; esac
    case " $ARCHS " in *" x86_64 "*) ;; *) fail "$binary is missing x86_64" ;; esac
done

/usr/bin/codesign --verify --strict --verbose=2 bin/openrouter-switch \
    || fail "CLI ad-hoc signature is invalid"
/usr/bin/codesign --verify --deep --strict --verbose=2 "$APP" \
    || fail "app ad-hoc signature is invalid"
CLI_SIGNATURE="$(/usr/bin/codesign --display --verbose=4 bin/openrouter-switch 2>&1)"
APP_SIGNATURE="$(/usr/bin/codesign --display --verbose=4 "$APP" 2>&1)"
printf '%s\n' "$CLI_SIGNATURE" | grep -qxF 'Signature=adhoc' \
    || fail "CLI does not have the required ad-hoc beta signature"
printf '%s\n' "$APP_SIGNATURE" | grep -qxF 'Signature=adhoc' \
    || fail "app does not have the required ad-hoc beta signature"

mkdir -p "$BIN_DIR"
/usr/bin/install -m 0755 bin/openrouter-switch "$BIN_DIR/openrouter-switch"
echo "installed $BIN_DIR/openrouter-switch ($CLI_VERSION)"

TARGET="$APP_DIR/$APP_NAME"
BACKUP="$APP_DIR/$APP_NAME.previous"
[ ! -e "$BACKUP" ] \
    || fail "previous app backup already exists at '$BACKUP'; verify or remove it before retrying"
if [ -e "$TARGET" ]; then
    /bin/mv "$TARGET" "$BACKUP"
fi
if ! /bin/mv "$APP" "$TARGET"; then
    [ ! -e "$BACKUP" ] || /bin/mv "$BACKUP" "$TARGET"
    fail "could not activate the new app"
fi
echo "installed $TARGET"
if [ -e "$BACKUP" ]; then
    echo "previous app retained at $BACKUP until you verify the new version"
fi

case ":$PATH:" in
    *":$BIN_DIR:"*) ;;
    *)
        echo ""
        echo "NOTE: $BIN_DIR is not on PATH. Add it, then reopen Terminal:"
        echo "  echo 'export PATH=\"$BIN_DIR:\$PATH\"' >> ~/.zshrc"
        ;;
esac

cat <<'EOF'

Beta notice:

  This build is ad-hoc signed and is not Apple-notarized. On first launch,
  macOS may require you to try opening OpenRouter Switch once, then go to
  System Settings > Privacy & Security and click Open Anyway.

Quick start:

  openrouter-switch setup
  openrouter-switch up --install
  openrouter-switch claude on
  openrouter-switch doctor --probe

Homebrew is the canonical public install and upgrade channel:

  brew install basetenlabs/baseten/openrouter-switch

OpenRouter Switch runs locally. Request bodies leave the machine only for
the model provider selected by routing policy.
EOF
