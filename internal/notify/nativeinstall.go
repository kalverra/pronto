package notify

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"time"

	"github.com/kalverra/pronto/assets"
)

// HelperMainSwift is the Swift source for the native helper app, embedded so
// binary-only installs can build it with swiftc.
//
//go:embed nativehelper/main.swift
var HelperMainSwift string

// HelperInfoPlist is the Info.plist template for the helper app bundle. Its
// CFBundleVersion is the helperVersionPlaceholder token, substituted with
// HelperVersion() at install time so every rebuild with different source or
// icons is a new version LaunchServices treats as fresh (see HelperVersion).
// Its CFBundleIdentifier is the helperBundleIDPlaceholder token, substituted
// with HelperBundleID (or WithBundleID's override).
//
//go:embed nativehelper/Info.plist
var HelperInfoPlist []byte

// The native helper's Swift source and Info.plist travel inside the pronto
// binary, so a `go install`ed or Homebrew-installed pronto can build and
// install the helper with nothing but the Xcode command line tools.
const swiftCompileTimeout = 5 * time.Minute

// helperVersionPlaceholder is substituted in the embedded Info.plist template
// with HelperVersion() at install time.
const helperVersionPlaceholder = "__PRONTO_HELPER_VERSION__"

// helperBundleIDPlaceholder is substituted in the embedded Info.plist template
// with the bundle identifier at install time.
const helperBundleIDPlaceholder = "__PRONTO_HELPER_BUNDLE_ID__"

// helperDisplayNamePlaceholder is substituted in the embedded Info.plist
// template with the name shown in banners and System Settings.
const helperDisplayNamePlaceholder = "__PRONTO_HELPER_DISPLAY_NAME__"

// helperDisplayName labels non-production bundles so a dev build is
// distinguishable from the installed helper in System Settings.
func helperDisplayName(bundleID string) string {
	if bundleID == HelperBundleID {
		return "Pronto"
	}
	return "Pronto (dev)"
}

// HelperBundleID is the bundle identifier of the installed native helper.
// macOS caches a notification icon per bundle id beyond version bumps,
// re-registration, and usernoted restarts; the previous id
// (com.kalverra.pronto.notify) was stuck with a blank one, so changing the id
// is the reliable reset.
const HelperBundleID = "com.kalverra.pronto.notifier"

// DevHelperBundleID is the bundle identifier for throwaway dev builds
// (`mise run bundle`). It must differ from HelperBundleID: usernoted resolves
// notification clients by bundle id through LaunchServices, so two bundles
// sharing one id make it reject the installed helper ("Notifications are not
// allowed for this application") whenever the other copy is registered or
// unregistered.
const DevHelperBundleID = HelperBundleID + ".dev"

// cfBundleIdentifierRE extracts the CFBundleIdentifier string from a bundle's
// Info.plist.
var cfBundleIdentifierRE = regexp.MustCompile(`(?s)<key>CFBundleIdentifier</key>\s*<string>([^<]*)</string>`)

// cfBundleVersionRE extracts the CFBundleVersion string from an installed
// bundle's Info.plist, so setup can detect a stale build without recompiling.
var cfBundleVersionRE = regexp.MustCompile(`(?s)<key>CFBundleVersion</key>\s*<string>([^<]*)</string>`)

type helperInstaller struct {
	swiftc     string
	codesign   string
	lsregister string
	explicit   bool
	bundleID   string
	register   bool
	runner     CommandRunner
}

// InstallOption configures InstallNativeHelper.
type InstallOption func(*helperInstaller)

// WithSwiftCompiler overrides the swiftc path; an empty override means
// "not installed" and skips the PATH lookup.
func WithSwiftCompiler(path string) InstallOption {
	return func(i *helperInstaller) {
		i.swiftc = path
		i.explicit = true
	}
}

// WithBundleID overrides the helper bundle identifier (default
// HelperBundleID). Dev builds use DevHelperBundleID so they never collide
// with the installed helper.
func WithBundleID(id string) InstallOption {
	return func(i *helperInstaller) {
		i.bundleID = id
	}
}

// WithCodeSignPath overrides the codesign path.
func WithCodeSignPath(path string) InstallOption {
	return func(i *helperInstaller) {
		i.codesign = path
	}
}

// WithLSRegisterPath overrides the lsregister path.
func WithLSRegisterPath(path string) InstallOption {
	return func(i *helperInstaller) {
		i.lsregister = path
	}
}

// WithInstallRunner overrides the build command executor for testing.
func WithInstallRunner(runner CommandRunner) InstallOption {
	return func(i *helperInstaller) {
		i.runner = runner
	}
}

// WithoutRegister skips LaunchServices registration (lsregister, touch, and
// purging stale records). Dev builds under build/ use this so they don't
// pollute the user's LaunchServices database with a throwaway bundle.
func WithoutRegister() InstallOption {
	return func(i *helperInstaller) {
		i.register = false
	}
}

// DefaultHelperAppPath returns the conventional install location for the
// native helper bundle: ~/.local/share/pronto/ProntoNotify.app (XDG_DATA_HOME
// aware), which DiscoverNativeHelper also searches.
func DefaultHelperAppPath() string {
	dir := dataDir()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, HelperAppName)
}

// HelperVersion derives a CFBundleVersion for the helper bundle from the
// embedded Swift source, Info.plist template, and icon PNGs, so every change to either produces a
// new version. usernoted (macOS's notification icon cache) keys its cached
// icon by bundle id and version; without a version bump, a rebuilt helper
// with a new icon never displays it.
func HelperVersion() string {
	h := sha256.New()
	h.Write([]byte(HelperMainSwift))
	h.Write(HelperInfoPlist)
	for _, size := range assets.AppIconSizes {
		data, err := assets.AppIconSet.ReadFile("appiconset/" + size + ".png")
		if err != nil {
			continue
		}
		h.Write(data)
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// InstalledHelperVersion reads the CFBundleVersion stamped in an installed
// helper bundle's Info.plist. It returns "" (no error) when the bundle or the
// key is missing, so callers can treat "not installed" and "no version found"
// the same way: build it.
func InstalledHelperVersion(appPath string) (string, error) {
	return installedPlistString(appPath, cfBundleVersionRE)
}

// HelperState classifies an installed native helper.
type HelperState string

// Native helper states reported by NativeHelperState.
const (
	HelperMissing   HelperState = "missing"
	HelperStale     HelperState = "stale"
	HelperInstalled HelperState = "installed"
)

// NativeHelperState reports whether the helper binary at helperPath (as
// returned by DiscoverNativeHelper) is missing, stale, or current. A binary
// with no readable bundle version (e.g. a bare PRONTO_NOTIFY_HELPER override)
// is trusted as installed. Cheap: only reads Info.plist.
func NativeHelperState(helperPath string) HelperState {
	if helperPath == "" {
		return HelperMissing
	}
	appPath := filepath.Dir(filepath.Dir(filepath.Dir(helperPath)))
	installed, err := InstalledHelperVersion(appPath)
	if err != nil {
		return HelperMissing
	}
	if installed != "" && installed != HelperVersion() {
		return HelperStale
	}
	return HelperInstalled
}

// InstalledHelperBundleID reads the CFBundleIdentifier of an installed helper
// bundle, with the same "" (no error) semantics as InstalledHelperVersion.
func InstalledHelperBundleID(appPath string) (string, error) {
	return installedPlistString(appPath, cfBundleIdentifierRE)
}

// installedPlistString extracts re's first submatch from appPath's Info.plist.
func installedPlistString(appPath string, re *regexp.Regexp) (string, error) {
	// #nosec G304 -- appPath is a locally configured install location, not user input.
	data, err := os.ReadFile(filepath.Join(appPath, "Contents", "Info.plist"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	m := re.FindSubmatch(data)
	if m == nil {
		return "", nil
	}
	return string(m[1]), nil
}

// InstallNativeHelper builds the embedded helper source with swiftc and
// assembles the app bundle at appPath, ad-hoc signed so macOS delivers its
// notifications without any Apple Developer account. No Apple Developer
// account or App Store registration is involved.
func InstallNativeHelper(appPath string, opts ...InstallOption) (string, error) {
	installer := &helperInstaller{runner: runCommand, register: true, bundleID: HelperBundleID}
	for _, opt := range opts {
		opt(installer)
	}
	if !installer.explicit {
		installer.swiftc = SwiftCPath()
	}
	if installer.swiftc == "" {
		return "", errors.New(
			"swiftc not found; install the Xcode command line tools with 'xcode-select --install' (free, no Apple Developer account)",
		)
	}

	ctx, cancel := context.WithTimeout(context.Background(), swiftCompileTimeout)
	defer cancel()

	// Materialize the embedded sources for swiftc.
	tmp, err := os.MkdirTemp("", "pronto-notify-build")
	if err != nil {
		return "", fmt.Errorf("create build dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	source := filepath.Join(tmp, "main.swift")
	if err := os.WriteFile(source, []byte(HelperMainSwift), 0o600); err != nil {
		return "", fmt.Errorf("write helper source: %w", err)
	}

	binDir := filepath.Join(appPath, "Contents", "MacOS")
	if err := os.MkdirAll(binDir, 0o750); err != nil {
		return "", fmt.Errorf("create bundle layout: %w", err)
	}
	binary := filepath.Join(binDir, HelperBinaryName)
	if err := installer.run(ctx, installer.swiftc, "-O", "-o", binary, source); err != nil {
		return "", fmt.Errorf("compile helper (swiftc): %w", err)
	}

	plist := bytes.ReplaceAll(HelperInfoPlist, []byte(helperVersionPlaceholder), []byte(HelperVersion()))
	plist = bytes.ReplaceAll(plist, []byte(helperBundleIDPlaceholder), []byte(installer.bundleID))
	plist = bytes.ReplaceAll(plist, []byte(helperDisplayNamePlaceholder), []byte(helperDisplayName(installer.bundleID)))
	// #nosec G306 -- user-local app bundle; no secrets, but gosec prefers 0600.
	if err := os.WriteFile(filepath.Join(appPath, "Contents", "Info.plist"), plist, 0o600); err != nil {
		return "", fmt.Errorf("write Info.plist: %w", err)
	}

	if err := writeIconset(filepath.Join(tmp, "AppIcon.iconset")); err != nil {
		return "", fmt.Errorf("materialize icon set: %w", err)
	}
	resDir := filepath.Join(appPath, "Contents", "Resources")
	if err := os.MkdirAll(resDir, 0o750); err != nil {
		return "", fmt.Errorf("create bundle resources: %w", err)
	}
	icns := filepath.Join(resDir, "AppIcon.icns")
	if err := installer.run(ctx, "iconutil", "-c", "icns", "-o", icns,
		filepath.Join(tmp, "AppIcon.iconset")); err != nil {
		return "", fmt.Errorf("generate app icon (iconutil): %w", err)
	}

	if installer.codesign == "" {
		installer.codesign = "codesign"
	}
	if err := installer.run(ctx, installer.codesign, "--force", "--sign", "-", appPath); err != nil {
		return "", fmt.Errorf("ad-hoc sign helper: %w", err)
	}

	if installer.register {
		if err := installer.registerWithLaunchServices(ctx, appPath); err != nil {
			return "", err
		}
	}

	return appPath, nil
}

// RegisterHelper registers appPath with LaunchServices and purges stale
// registrations for the same bundle id, without rebuilding. `pronto notify
// setup` uses this when the installed helper is already up to date, so
// LaunchServices state stays correct even when a rebuild was skipped.
// WithoutRegister makes this a no-op.
func RegisterHelper(ctx context.Context, appPath string, opts ...InstallOption) error {
	installer := &helperInstaller{runner: runCommand, register: true}
	for _, opt := range opts {
		opt(installer)
	}
	if !installer.register {
		return nil
	}
	return installer.registerWithLaunchServices(ctx, appPath)
}

// StaleHelperInstalls reports other on-disk locations of a bundle declaring
// HelperBundleID besides appPath (bundles with another id, such as dev
// builds, never collide). Even after LaunchServices registration is fixed,
// usernoted (macOS's notification icon cache) can keep serving an icon it
// already resolved for this session; `pronto notify setup` uses this to warn
// the user that a one-time `killall usernoted NotificationCenter` (safe: both
// respawn) may still be needed.
func StaleHelperInstalls(appPath string) []string {
	var candidates []string
	if cwd, err := os.Getwd(); err == nil {
		candidates = append(candidates, filepath.Join(cwd, "build", HelperAppName))
	}
	if dir := dataDir(); dir != "" {
		candidates = append(candidates, filepath.Join(dir, HelperAppName))
	}
	out := candidates[:0]
	for _, c := range candidates {
		if c == appPath {
			continue
		}
		if id, err := InstalledHelperBundleID(c); err == nil && id == HelperBundleID {
			out = append(out, c)
		}
	}
	return out
}

// registerWithLaunchServices registers appPath, purges any other known
// registration for the same bundle so LaunchServices adopts the fresh
// icon/version instead of keeping a stale cached one, and touches the bundle
// so Finder/usernoted notice the change.
func (i *helperInstaller) registerWithLaunchServices(ctx context.Context, appPath string) error {
	lsregister := i.lsregister
	if lsregister == "" {
		lsregister = defaultLSRegisterPath
	}
	if err := i.run(ctx, lsregister, "-f", appPath); err != nil {
		return fmt.Errorf("register with LaunchServices (lsregister): %w", err)
	}
	for _, stale := range StaleHelperInstalls(appPath) {
		// Best-effort: an old registration that fails to unregister is not
		// fatal, it just leaves usernoted with a stale icon a bit longer.
		_ = i.run(ctx, lsregister, "-u", stale)
	}
	if err := i.run(ctx, "touch", appPath); err != nil {
		return fmt.Errorf("touch bundle: %w", err)
	}
	return nil
}

// defaultLSRegisterPath is lsregister's fixed location inside the
// LaunchServices framework; it is not on PATH.
const defaultLSRegisterPath = "/System/Library/Frameworks/CoreServices.framework/Frameworks/LaunchServices.framework/Support/lsregister"

// iconsetEntries maps iconutil's expected .iconset filenames to the PNG sizes
// embedded from the assets package.
var iconsetEntries = map[string]string{
	"icon_16x16.png":      "16.png",
	"icon_16x16@2x.png":   "32.png",
	"icon_32x32.png":      "32.png",
	"icon_32x32@2x.png":   "64.png",
	"icon_128x128.png":    "128.png",
	"icon_128x128@2x.png": "256.png",
	"icon_256x256.png":    "256.png",
	"icon_256x256@2x.png": "512.png",
	"icon_512x512.png":    "512.png",
	"icon_512x512@2x.png": "1024.png",
}

// writeIconset materializes the embedded icon PNGs under the names iconutil
// expects.
func writeIconset(dir string) error {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	for name, source := range iconsetEntries {
		data, err := assets.AppIconSet.ReadFile("appiconset/" + source)
		if err != nil {
			return fmt.Errorf("read embedded %s: %w", source, err)
		}
		// #nosec G304 -- dir is a fresh temp dir built from fixed parts.
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
			return err
		}
	}
	return nil
}

func (i *helperInstaller) run(ctx context.Context, name string, args ...string) error {
	if i.runner == nil {
		return runCommand(ctx, name, args...)
	}
	return i.runner(ctx, name, args...)
}

// SwiftCPath resolves swiftc on PATH, or "" if the Xcode command line tools
// are not installed. `pronto notify setup` uses this to give a precise hint
// (xcode-select --install) instead of a generic compile failure.
func SwiftCPath() string {
	path, err := exec.LookPath("swiftc")
	if err != nil {
		return ""
	}
	return path
}
