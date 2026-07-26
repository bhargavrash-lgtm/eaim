// Package nmregister registers/unregisters eami-agent as a browser
// native-messaging host.
package nmregister

// HostName is the native messaging host identifier the extension calls
// chrome.runtime.connectNative(HostName) with. Must match the "name"
// field of the manifest and (on Windows) the registry key name exactly.
const HostName = "com.eami.agent"

// LauncherBaseName is the filename (no extension) of a second directory
// entry for the eami-agent binary -- a hard link, so it's always
// byte-identical to the real binary at zero extra disk space and can
// never fall out of sync on upgrade -- that the native-messaging
// manifest's "path" actually points at, instead of the real
// "eami-agent"/"eami-agent.exe" name.
//
// Why this indirection exists: Chrome/Edge's native-messaging manifest
// schema has no "args"/"arguments" field -- the browser execs whatever
// "path" names with only its own fixed positional arguments (the calling
// extension's origin, and on Windows a window handle), never anything the
// manifest itself supplies. Without this, a real browser launching
// eami-agent per the manifest would run it with no --native-messaging-host
// flag at all, and it would silently fall through to the normal poll loop
// instead of acting as a host -- caught by code review before any real
// extension existed to catch it in practice. cmd/agent/main.go detects
// invocation under this name (checking both os.Args[0] and the resolved
// os.Executable() path, in case one differs from the other on some
// platform/shell) and behaves exactly as if --native-messaging-host had
// been passed explicitly.
const LauncherBaseName = "eami-agent-nmhost"
