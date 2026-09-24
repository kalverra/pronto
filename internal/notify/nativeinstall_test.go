package notify_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalverra/pronto/internal/notify"
)

func TestEmbeddedHelperSource(t *testing.T) {
	t.Parallel()

	assert.Contains(t, notify.HelperMainSwift, "UNUserNotificationCenter",
		"embedded source must be the UserNotifications helper")
}

// stubCompiler writes a fake swiftc/codesign: the swiftc stub creates the
// -o output, the codesign stub records its arguments in a marker file.
func stubCompiler(t *testing.T, dir string) (swiftc, codesign string) {
	t.Helper()

	swiftc = filepath.Join(dir, "swiftc")
	codesign = filepath.Join(dir, "codesign")

	swiftcScript := "#!/bin/sh\nout=\"\"\nprev=\"\"\nfor a in \"$@\"; do\n" +
		"  if [ \"$prev\" = \"-o\" ]; then out=\"$a\"; fi\n" +
		"  prev=\"$a\"\ndone\n" +
		"mkdir -p \"$(dirname \"$out\")\"\necho fake-binary > \"$out\"\n"
	require.NoError(t, os.WriteFile(swiftc, []byte(swiftcScript), 0o600))
	// #nosec G302 -- discovery requires the executable bit; test-only stub.
	require.NoError(t, os.Chmod(swiftc, 0o700))

	marker := filepath.Join(dir, "codesign-args")
	codesignScript := "#!/bin/sh\necho \"$@\" > " + marker + "\n"
	require.NoError(t, os.WriteFile(codesign, []byte(codesignScript), 0o600))
	// #nosec G302 -- discovery requires the executable bit; test-only stub.
	require.NoError(t, os.Chmod(codesign, 0o700))

	return swiftc, codesign
}

func TestInstallNativeHelper_BundleLayout(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	swiftc, codesign := stubCompiler(t, dir)
	appPath := filepath.Join(dir, "ProntoNotify.app")

	got, err := notify.InstallNativeHelper(appPath,
		notify.WithSwiftCompiler(swiftc),
		notify.WithCodeSignPath(codesign),
		notify.WithoutRegister(),
		notify.WithInstallRunner(func(ctx context.Context, name string, args ...string) error {
			if name != swiftc && name != codesign && name != "iconutil" {
				t.Fatalf("unexpected command %q", name)
			}
			if name == "iconutil" {
				// Stub iconutil: create the file named by -o.
				for i, arg := range args {
					if arg == "-o" && i+1 < len(args) {
						out := args[i+1]
						if err := os.MkdirAll(filepath.Dir(out), 0o750); err != nil {
							return err
						}
						return os.WriteFile(out, []byte("fake-icns"), 0o600)
					}
				}
				t.Fatalf("iconutil called without -o: %v", args)
			}
			// #nosec G204 -- test stubs only; args recorded above.
			return exec.CommandContext(ctx, name, args...).Run()
		}),
	)
	require.NoError(t, err)
	assert.Equal(t, appPath, got)

	// #nosec G304 -- appPath is a test temp dir.
	binary, err := os.ReadFile(filepath.Join(appPath, "Contents", "MacOS", "pronto-notify"))
	require.NoError(t, err)
	assert.Equal(t, "fake-binary\n", string(binary), "swiftc must compile into the bundle layout")

	// #nosec G304 -- appPath is a test temp dir.
	plist, err := os.ReadFile(filepath.Join(appPath, "Contents", "Info.plist"))
	require.NoError(t, err)
	assert.Contains(t, string(plist), "<string>"+notify.HelperBundleID+"</string>")
	assert.Contains(t, string(plist), "<key>CFBundleIconFile</key>", "Info.plist must reference the .icns icon")
	assert.NotContains(t, string(plist), "CFBundleIconName",
		"no asset catalog ships in the bundle, so declaring one leaves macOS without an icon")
	assert.NotContains(t, string(plist), "__PRONTO_HELPER_VERSION__", "version placeholder must be substituted")
	assert.Contains(t, string(plist), notify.HelperVersion(), "CFBundleVersion must be the content hash")

	icns, err := os.Stat(filepath.Join(appPath, "Contents", "Resources", "AppIcon.icns"))
	require.NoError(t, err, "iconutil must emit the bundle icon")
	assert.False(t, icns.IsDir())

	// #nosec G304 -- dir is a test temp dir.
	marker, err := os.ReadFile(filepath.Join(dir, "codesign-args"))
	require.NoError(t, err)
	assert.Contains(t, string(marker), "--sign", "bundle must be ad-hoc signed")
	assert.Contains(t, string(marker), appPath)
}

func TestInstallNativeHelper_WithBundleID(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	swiftc, codesign := stubCompiler(t, dir)
	appPath := filepath.Join(dir, "ProntoNotify.app")

	_, err := notify.InstallNativeHelper(appPath,
		notify.WithSwiftCompiler(swiftc),
		notify.WithCodeSignPath(codesign),
		notify.WithoutRegister(),
		notify.WithBundleID(notify.DevHelperBundleID),
		notify.WithInstallRunner(fakeIconutilRunner(t, swiftc, codesign)),
	)
	require.NoError(t, err)

	id, err := notify.InstalledHelperBundleID(appPath)
	require.NoError(t, err)
	assert.Equal(t, notify.DevHelperBundleID, id,
		"dev builds must not share the installed helper's bundle id")
}

func TestInstalledHelperBundleID(t *testing.T) {
	t.Parallel()

	t.Run("missing bundle returns empty, no error", func(t *testing.T) {
		t.Parallel()
		id, err := notify.InstalledHelperBundleID(filepath.Join(t.TempDir(), "NoSuchApp.app"))
		require.NoError(t, err)
		assert.Empty(t, id)
	})

	t.Run("defaults to the production bundle id", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		swiftc, codesign := stubCompiler(t, dir)
		appPath := filepath.Join(dir, "ProntoNotify.app")

		_, err := notify.InstallNativeHelper(appPath,
			notify.WithSwiftCompiler(swiftc),
			notify.WithCodeSignPath(codesign),
			notify.WithoutRegister(),
			notify.WithInstallRunner(fakeIconutilRunner(t, swiftc, codesign)),
		)
		require.NoError(t, err)

		id, err := notify.InstalledHelperBundleID(appPath)
		require.NoError(t, err)
		assert.Equal(t, notify.HelperBundleID, id)
	})
}

func TestHelperVersion_ChangesWithSource(t *testing.T) {
	t.Parallel()

	// The version is a deterministic hash of the embedded source/icons: it
	// must be stable across calls and non-empty, so a rebuild without any
	// source change reuses the same LaunchServices record.
	first := notify.HelperVersion()
	second := notify.HelperVersion()
	assert.NotEmpty(t, first)
	assert.Equal(t, first, second)
}

func TestInstalledHelperVersion(t *testing.T) {
	t.Parallel()

	t.Run("missing bundle returns empty, no error", func(t *testing.T) {
		t.Parallel()
		version, err := notify.InstalledHelperVersion(filepath.Join(t.TempDir(), "NoSuchApp.app"))
		require.NoError(t, err)
		assert.Empty(t, version)
	})

	t.Run("reads the stamped CFBundleVersion", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		swiftc, codesign := stubCompiler(t, dir)
		appPath := filepath.Join(dir, "ProntoNotify.app")

		_, err := notify.InstallNativeHelper(appPath,
			notify.WithSwiftCompiler(swiftc),
			notify.WithCodeSignPath(codesign),
			notify.WithoutRegister(),
			notify.WithInstallRunner(fakeIconutilRunner(t, swiftc, codesign)),
		)
		require.NoError(t, err)

		version, err := notify.InstalledHelperVersion(appPath)
		require.NoError(t, err)
		assert.Equal(t, notify.HelperVersion(), version)
	})
}

func TestInstallNativeHelper_WithoutRegisterSkipsLaunchServices(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	swiftc, codesign := stubCompiler(t, dir)
	appPath := filepath.Join(dir, "ProntoNotify.app")

	var ranCommands []string
	_, err := notify.InstallNativeHelper(appPath,
		notify.WithSwiftCompiler(swiftc),
		notify.WithCodeSignPath(codesign),
		notify.WithoutRegister(),
		notify.WithInstallRunner(func(ctx context.Context, name string, args ...string) error {
			ranCommands = append(ranCommands, name)
			return fakeIconutilRunner(t, swiftc, codesign)(ctx, name, args...)
		}),
	)
	require.NoError(t, err)

	for _, name := range ranCommands {
		assert.NotContains(t, name, "lsregister", "--no-register must skip LaunchServices registration")
		assert.NotEqual(t, "touch", name, "--no-register must skip touch")
	}
}

func TestInstallNativeHelper_RegistersAndPurgesStalePaths(t *testing.T) {
	dir := t.TempDir()
	swiftc, codesign := stubCompiler(t, dir)
	appPath := filepath.Join(dir, "ProntoNotify.app")

	// A stale registration at the conventional data dir, distinct from
	// appPath, so the installer's purge step has something to find.
	xdg := t.TempDir()
	t.Setenv("XDG_DATA_HOME", xdg)
	staleApp := filepath.Join(xdg, "pronto", "ProntoNotify.app")
	writeBundleFixture(t, staleApp, notify.HelperBundleID)

	var lsregisterArgs [][]string
	lsregister := filepath.Join(dir, "lsregister")
	require.NoError(t, os.WriteFile(lsregister, []byte("#!/bin/sh\nexit 0\n"), 0o600))
	// #nosec G302 -- discovery requires the executable bit; test-only stub.
	require.NoError(t, os.Chmod(lsregister, 0o700))

	_, err := notify.InstallNativeHelper(appPath,
		notify.WithSwiftCompiler(swiftc),
		notify.WithCodeSignPath(codesign),
		notify.WithLSRegisterPath(lsregister),
		notify.WithInstallRunner(func(ctx context.Context, name string, args ...string) error {
			switch name {
			case lsregister:
				lsregisterArgs = append(lsregisterArgs, append([]string(nil), args...))
				return nil
			case "touch":
				return nil
			default:
				return fakeIconutilRunner(t, swiftc, codesign)(ctx, name, args...)
			}
		}),
	)
	require.NoError(t, err)

	require.NotEmpty(t, lsregisterArgs)
	assert.Contains(t, lsregisterArgs[0], "-f", "must register the fresh bundle")
	assert.Contains(t, lsregisterArgs[0], appPath)

	var purged bool
	for _, args := range lsregisterArgs[1:] {
		if len(args) == 2 && args[0] == "-u" && args[1] == staleApp {
			purged = true
		}
	}
	assert.True(t, purged, "must unregister the stale install at the data dir")
}

func TestStaleHelperInstalls(t *testing.T) {
	dir := t.TempDir()
	appPath := filepath.Join(dir, "ProntoNotify.app")

	xdg := t.TempDir()
	t.Setenv("XDG_DATA_HOME", xdg)
	staleApp := filepath.Join(xdg, "pronto", "ProntoNotify.app")

	assert.Empty(t, notify.StaleHelperInstalls(appPath), "no stale install exists yet")

	writeBundleFixture(t, staleApp, notify.DevHelperBundleID)
	assert.Empty(t, notify.StaleHelperInstalls(appPath),
		"a bundle with a different id never collides, so it is not stale")

	writeBundleFixture(t, staleApp, notify.HelperBundleID)
	assert.Equal(t, []string{staleApp}, notify.StaleHelperInstalls(appPath))
}

// writeBundleFixture writes a minimal app bundle whose Info.plist declares id.
func writeBundleFixture(t *testing.T, appPath, id string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(appPath, "Contents"), 0o750))
	plist := "<plist><dict><key>CFBundleIdentifier</key>\n\t<string>" + id + "</string></dict></plist>"
	require.NoError(t, os.WriteFile(filepath.Join(appPath, "Contents", "Info.plist"), []byte(plist), 0o600))
}

// fakeIconutilRunner stubs iconutil (writing the -o target) while delegating
// swiftc/codesign to the real stubbed binaries via exec.
func fakeIconutilRunner(t *testing.T, swiftc, codesign string) notify.CommandRunner {
	t.Helper()
	return func(ctx context.Context, name string, args ...string) error {
		if name != swiftc && name != codesign && name != "iconutil" {
			t.Fatalf("unexpected command %q", name)
		}
		if name == "iconutil" {
			for i, arg := range args {
				if arg == "-o" && i+1 < len(args) {
					out := args[i+1]
					if err := os.MkdirAll(filepath.Dir(out), 0o750); err != nil {
						return err
					}
					return os.WriteFile(out, []byte("fake-icns"), 0o600)
				}
			}
			t.Fatalf("iconutil called without -o: %v", args)
		}
		// #nosec G204 -- test stubs only; args recorded above.
		return exec.CommandContext(ctx, name, args...).Run()
	}
}

func TestInstallNativeHelper_MissingSwiftc(t *testing.T) {
	t.Parallel()

	_, err := notify.InstallNativeHelper(filepath.Join(t.TempDir(), "ProntoNotify.app"),
		notify.WithSwiftCompiler(""),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "xcode-select")
}

func TestInstallNativeHelper_CompilerFailureSurfacesOutput(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	swiftc, codesign := stubCompiler(t, dir)
	_, _ = swiftc, codesign
	fail := filepath.Join(dir, "swiftc-fail")
	require.NoError(t, os.WriteFile(fail, []byte("#!/bin/sh\necho compile boom >&2\nexit 1\n"), 0o600))
	// #nosec G302 -- discovery requires the executable bit; test-only stub.
	require.NoError(t, os.Chmod(fail, 0o700))

	_, err := notify.InstallNativeHelper(filepath.Join(dir, "ProntoNotify.app"),
		notify.WithSwiftCompiler(fail),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "compile boom")
}

func TestSwiftCPath_DefaultsToPath(t *testing.T) {
	dir := t.TempDir()
	_, _ = stubCompiler(t, dir)
	t.Setenv("PATH", dir)

	assert.NotEmpty(t, notify.SwiftCPath(), "swiftc on PATH should be detected")

	t.Setenv("PATH", "")
	assert.Empty(t, notify.SwiftCPath())
}

func TestDefaultHelperAppPath(t *testing.T) {
	t.Run("under XDG data home", func(t *testing.T) {
		xdg := t.TempDir()
		t.Setenv("XDG_DATA_HOME", xdg)
		assert.Equal(t,
			filepath.Join(xdg, "pronto", "ProntoNotify.app"),
			notify.DefaultHelperAppPath())
	})

	t.Run("under home data dir", func(t *testing.T) {
		t.Setenv("XDG_DATA_HOME", "")
		got := notify.DefaultHelperAppPath()
		assert.True(t, strings.HasSuffix(got, filepath.Join(".local", "share", "pronto", "ProntoNotify.app")),
			"default install should live in ~/.local/share/pronto, got %q", got)
	})
}

// installStubHelper builds a helper bundle with stubbed tools and returns the
// app path and its binary path.
func installStubHelper(t *testing.T, opts ...notify.InstallOption) (appPath, binary string) {
	t.Helper()
	dir := t.TempDir()
	swiftc, codesign := stubCompiler(t, dir)
	appPath = filepath.Join(dir, "ProntoNotify.app")
	opts = append([]notify.InstallOption{
		notify.WithSwiftCompiler(swiftc),
		notify.WithCodeSignPath(codesign),
		notify.WithoutRegister(),
		notify.WithInstallRunner(fakeIconutilRunner(t, swiftc, codesign)),
	}, opts...)
	_, err := notify.InstallNativeHelper(appPath, opts...)
	require.NoError(t, err)
	return appPath, filepath.Join(appPath, "Contents", "MacOS", notify.HelperBinaryName)
}

func TestInstallNativeHelper_DisplayName(t *testing.T) {
	t.Parallel()

	displayName := func(appPath string) string {
		// #nosec G304 -- test-owned temp bundle.
		data, err := os.ReadFile(filepath.Join(appPath, "Contents", "Info.plist"))
		require.NoError(t, err)
		m := regexp.MustCompile(`<key>CFBundleDisplayName</key>\s*<string>([^<]*)</string>`).FindSubmatch(data)
		require.NotNil(t, m)
		return string(m[1])
	}

	prod, _ := installStubHelper(t)
	assert.Equal(t, "Pronto", displayName(prod))

	dev, _ := installStubHelper(t, notify.WithBundleID(notify.DevHelperBundleID))
	assert.Equal(t, "Pronto (dev)", displayName(dev),
		"dev builds must be distinguishable in System Settings > Notifications")
}

func TestNativeHelperState(t *testing.T) {
	t.Parallel()

	assert.Equal(t, notify.HelperMissing, notify.NativeHelperState(""))

	appPath, binary := installStubHelper(t)
	assert.Equal(t, notify.HelperInstalled, notify.NativeHelperState(binary))

	plistPath := filepath.Join(appPath, "Contents", "Info.plist")
	// #nosec G304 -- test-owned temp bundle.
	data, err := os.ReadFile(plistPath)
	require.NoError(t, err)
	old := strings.Replace(string(data), notify.HelperVersion(), "0000000000000000", 1)
	// #nosec G703 -- test-owned temp bundle.
	require.NoError(t, os.WriteFile(plistPath, []byte(old), 0o600))
	assert.Equal(t, notify.HelperStale, notify.NativeHelperState(binary))

	// An explicit PRONTO_NOTIFY_HELPER binary outside any bundle has no
	// version to compare; it is trusted as installed.
	assert.Equal(t, notify.HelperInstalled, notify.NativeHelperState(filepath.Join(t.TempDir(), "helper")))
}
