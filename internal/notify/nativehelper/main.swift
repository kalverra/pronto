// pronto-notify is the native macOS notification helper for pronto.
//
// It runs inside ProntoNotify.app (bundled + ad-hoc signed) so it can use the
// UserNotifications framework: native branding, click-to-open, thread
// grouping, and image attachments, with no Apple Developer account.
//
// Modes of operation, selected by argv[1]:
//
//  1. Poster (default, no flag): pronto spawns this binary with a JSON
//     payload on stdin (see internal/notify/native.go for the contract). The
//     notification is posted and the process exits immediately. Exit codes:
//     0 posted, 1 other failure, exitNotAuthorized (3) not authorized yet,
//     exitDenied (4) the user turned notifications off. pronto falls back to
//     terminal delivery on 3 and 4.
//
//  2. "--authorize": requests notification authorization (prompting the user
//     on first run) and prints {"status":"authorized|denied|notDetermined"}
//     as JSON to stdout. Used by `pronto notify setup`.
//
//  3. "--status": reports the current authorization status without
//     prompting, same JSON shape as --authorize.
//
//  4. Responder (no flag, empty stdin): when the user interacts with a
//     previously posted notification, macOS relaunches the app. No payload
//     arrives on stdin, so the process runs the NSApplication lifecycle
//     until the pending response arrives: a click opens the PR URL, a
//     dismiss does nothing. The response is only delivered as part of app
//     launch, so a bare run loop without NSApplication never receives it.
import AppKit
import Foundation
import UserNotifications

struct HelperPayload: Codable {
    let title: String
    let body: String
    var subtitle: String?
    var threadID: String?
    var sound: String?
    var imagePath: String?
    var url: String?

    // Wire keys are snake_case (see helperPayload in native.go); JSONDecoder
    // silently drops undeclared keys, so map them explicitly.
    enum CodingKeys: String, CodingKey {
        case title, body, subtitle, sound, url
        case threadID = "thread_id"
        case imagePath = "image_path"
    }
}

/// Handles notification interactions. Retained globally: the notification
/// center holds an unowned reference to its delegate.
final class ResponseDelegate: NSObject, UNUserNotificationCenterDelegate {
    static var current: ResponseDelegate?

    func userNotificationCenter(
        _ center: UNUserNotificationCenter,
        didReceive response: UNNotificationResponse,
        withCompletionHandler completionHandler: @escaping () -> Void
    ) {
        defer {
            completionHandler()
            // One response per launch; quit once it is handled.
            DispatchQueue.main.async { NSApp?.terminate(nil) }
        }
        guard let url = response.notification.request.content.userInfo["url"] as? String,
              !url.isEmpty, let target = URL(string: url)
        else { return }

        // Clicking the banner opens the PR; dismissing does nothing.
        if response.actionIdentifier == UNNotificationDefaultActionIdentifier {
            NSWorkspace.shared.open(target)
        }
    }

    func userNotificationCenter(
        _ center: UNUserNotificationCenter,
        willPresent notification: UNNotification,
        withCompletionHandler completionHandler: @escaping (UNNotificationPresentationOptions) -> Void
    ) {
        var options: UNNotificationPresentationOptions = [.banner]
        if notification.request.content.sound != nil {
            options.insert(.sound)
        }
        completionHandler(options)
    }
}

/// Setup-problem exit codes; must match helperExitNotAuthorized and
/// helperExitDenied in internal/notify/native.go.
let exitNotAuthorized: Int32 = 3
let exitDenied: Int32 = 4

func warn(_ message: String) {
    FileHandle.standardError.write(Data("pronto-notify: \(message)\n".utf8))
}

func fail(_ message: String, code: Int32 = 1) -> Never {
    warn(message)
    exit(code)
}

/// Resolves a payload sound name to a UNNotificationSound: "default" (or
/// unset, handled by the caller) is the OS default alert; any other name is a
/// macOS system or user sound played from its .aiff.
func notificationSound(named name: String) -> UNNotificationSound {
    if name == "default" {
        return .default
    }
    return UNNotificationSound(named: UNNotificationSoundName(rawValue: "\(name).aiff"))
}

/// Copies the image at path into a fresh temporary file and returns its URL.
/// UNNotificationAttachment *moves* the source file into the notification
/// data store, so attaching a shared, reused icon path directly would delete
/// it out from under the next notification for the same trigger.
func copyForAttachment(_ path: String) -> URL? {
    let source = URL(fileURLWithPath: path)
    let dest = FileManager.default.temporaryDirectory
        .appendingPathComponent(UUID().uuidString)
        .appendingPathExtension(source.pathExtension)
    do {
        try FileManager.default.copyItem(at: source, to: dest)
        return dest
    } catch {
        warn("could not copy image attachment \(path): \(error)")
        return nil
    }
}

/// Posts the payload supplied on stdin and exits without waiting for user
/// interaction; interaction arrives later via the responder mode.
func post(_ data: Data) -> Never {
    let payload: HelperPayload
    do {
        payload = try JSONDecoder().decode(HelperPayload.self, from: data)
    } catch {
        fail("invalid payload: \(error)")
    }

    // Never prompt from here: pronto kills the poster after a short delivery
    // timeout, and a prompt whose requester dies is recorded as denied. Only
    // "--authorize" (run untimed by `pronto notify setup`) prompts. Checked
    // before any work so a rejected post leaves nothing behind.
    let center = UNUserNotificationCenter.current()
    switch authorizationStatus(center) {
    case .notDetermined:
        fail("notifications not authorized yet; run 'pronto notify setup'", code: exitNotAuthorized)
    case .denied:
        fail("notifications denied; allow Pronto in System Settings > Notifications", code: exitDenied)
    default:
        break
    }

    let content = UNMutableNotificationContent()
    content.title = payload.title
    content.body = payload.body
    if let subtitle = payload.subtitle { content.subtitle = subtitle }
    if let threadID = payload.threadID { content.threadIdentifier = threadID }
    if let sound = payload.sound, !sound.isEmpty {
        content.sound = notificationSound(named: sound)
    }
    content.userInfo = ["url": payload.url ?? ""]

    var attachmentURL: URL?
    if let imagePath = payload.imagePath, !imagePath.isEmpty {
        attachmentURL = copyForAttachment(imagePath)
        if let url = attachmentURL {
            do {
                let attachment = try UNNotificationAttachment(identifier: "image", url: url)
                content.attachments = [attachment]
            } catch {
                warn("could not attach image \(imagePath): \(error)")
            }
        }
    }

    let request = UNNotificationRequest(identifier: UUID().uuidString, content: content, trigger: nil)
    if let err = addRequest(center, request) {
        // A successful add moves the copy into the notification store; on
        // failure it is still ours to clean up.
        if let url = attachmentURL { try? FileManager.default.removeItem(at: url) }
        // Right after a re-sign usernoted rejects the client as "not
        // allowed"; pronto respawns on this code, then falls back.
        if (err as? UNError)?.code == .notificationsNotAllowed {
            fail("\(err)", code: exitNotAuthorized)
        }
        fail("\(err)")
    }
    exit(0)
}

/// Adds request synchronously, returning the framework error if any.
func addRequest(_ center: UNUserNotificationCenter, _ request: UNNotificationRequest) -> Error? {
    let done = DispatchSemaphore(value: 0)
    var addError: Error?
    center.add(request) { err in
        addError = err
        done.signal()
    }
    done.wait()
    return addError
}

/// Reads the current authorization status synchronously.
func authorizationStatus(_ center: UNUserNotificationCenter) -> UNAuthorizationStatus {
    let done = DispatchSemaphore(value: 0)
    var status: UNAuthorizationStatus = .notDetermined
    center.getNotificationSettings { s in
        status = s.authorizationStatus
        done.signal()
    }
    done.wait()
    return status
}

/// Renders a UNAuthorizationStatus as the lowercase string pronto's setup
/// command matches against.
func statusName(_ status: UNAuthorizationStatus) -> String {
    switch status {
    case .authorized, .provisional, .ephemeral:
        return "authorized"
    case .denied:
        return "denied"
    case .notDetermined:
        return "notDetermined"
    @unknown default:
        return "notDetermined"
    }
}

func printStatus(_ status: UNAuthorizationStatus) {
    let json = "{\"status\":\"\(statusName(status))\"}"
    print(json)
}

/// "--status": reports current authorization without prompting.
func reportStatus() -> Never {
    printStatus(authorizationStatus(UNUserNotificationCenter.current()))
    exit(0)
}

/// "--authorize": requests authorization (prompting on first run) and prints
/// the resulting status, so `pronto notify setup` can tell the user what to
/// do next without guessing from a Boolean. Right after install, usernoted may
/// reject this process without prompting (status stays notDetermined); setup
/// respawns it, since the rejection is cached per process.
func authorize() -> Never {
    let center = UNUserNotificationCenter.current()
    let done = DispatchSemaphore(value: 0)
    center.requestAuthorization(options: [.alert, .sound]) { _, _ in done.signal() }
    done.wait()
    reportStatus()
}

// Main entry point. Top-level code runs on the main thread.
let arguments = CommandLine.arguments
if arguments.contains("--authorize") {
    authorize() // exits
}
if arguments.contains("--status") {
    reportStatus() // exits
}

let delegate = ResponseDelegate()
ResponseDelegate.current = delegate
UNUserNotificationCenter.current().delegate = delegate

let stdinData = FileHandle.standardInput.readDataToEndOfFile()
if !stdinData.isEmpty {
    post(stdinData)  // exits
}

// No payload: assume we were relaunched to deliver a pending notification
// response. UserNotifications hands the response over during app launch, so
// run a real (Dock-less) NSApplication; the delegate terminates once the
// response is handled, and a grace timer covers launches without one.
final class AppDelegate: NSObject, NSApplicationDelegate {
    func applicationWillFinishLaunching(_ notification: Notification) {
        // Apple requires the delegate be set before launch finishes.
        UNUserNotificationCenter.current().delegate = ResponseDelegate.current
    }

    func applicationDidFinishLaunching(_ notification: Notification) {
        DispatchQueue.main.asyncAfter(deadline: .now() + 5) { NSApp.terminate(nil) }
    }
}

let app = NSApplication.shared
app.setActivationPolicy(.accessory)
let appDelegate = AppDelegate()
app.delegate = appDelegate
app.run()
