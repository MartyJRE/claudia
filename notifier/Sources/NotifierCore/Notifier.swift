// claudia-notify: posts native macOS notifications for claudia and handles clicks.
//
//   claudia-notify post --title T [--subtitle S] [--body B] [--thread G] [--id ID]
//                       [--exec "shell command"] [--url URL] [--image PATH] [--sound none]
//
// Launched without arguments (macOS does that when a notification is clicked while
// the app isn't running), it waits for the click response, runs its action and quits.
import Foundation
import UserNotifications

public protocol NotificationPosting {
    func requestAuthorization(_ done: @escaping (Bool, Error?) -> Void)
    func post(_ request: UNNotificationRequest, _ done: @escaping (Error?) -> Void)
}

public protocol URLOpening {
    @discardableResult func open(_ url: URL) -> Bool
}

public protocol CommandRunning {
    func run(_ command: String)
}

public protocol AppLifecycle {
    func terminate()
    func exit(_ code: Int32)
    func after(_ seconds: Double, _ block: @escaping () -> Void)
}

/// Runs a command through /bin/sh and waits for it.
public struct Shell: CommandRunning {
    public init() {}

    public func run(_ command: String) {
        let p = Process()
        p.executableURL = URL(fileURLWithPath: "/bin/sh")
        p.arguments = ["-c", command]
        try? p.run()
        p.waitUntilExit()
    }
}

public struct Arguments {
    public let args: [String]

    public init(_ args: [String]) { self.args = args }

    public var isPost: Bool { args.count > 1 && args[1] == "post" }

    public func opt(_ name: String) -> String? {
        guard let i = args.firstIndex(of: "--" + name), i + 1 < args.count else { return nil }
        return args[i + 1]
    }

    public func content() -> UNMutableNotificationContent {
        let c = UNMutableNotificationContent()
        c.title = opt("title") ?? "claudia"
        c.subtitle = opt("subtitle") ?? ""
        c.body = opt("body") ?? ""
        if let t = opt("thread") { c.threadIdentifier = t }
        if opt("sound") != "none" { c.sound = .default }
        var info: [String: String] = [:]
        for key in ["exec", "url"] {
            if let v = opt(key) { info[key] = v }
        }
        c.userInfo = info
        if let img = opt("image"),
           let a = try? UNNotificationAttachment(identifier: "image", url: URL(fileURLWithPath: img)) {
            c.attachments = [a]
        }
        return c
    }

    public func request() -> UNNotificationRequest {
        UNNotificationRequest(identifier: opt("id") ?? UUID().uuidString, content: content(), trigger: nil)
    }
}

public final class Notifier {
    public static let authorization: UNAuthorizationOptions = [.alert, .sound]
    // Show banners even if this helper happens to be running.
    public static let presentation: UNNotificationPresentationOptions = [.banner, .list, .sound]

    let center: NotificationPosting
    let opener: URLOpening
    let runner: CommandRunning
    let app: AppLifecycle
    let log: (String) -> Void

    public init(center: NotificationPosting, opener: URLOpening, runner: CommandRunning = Shell(),
                app: AppLifecycle, log: @escaping (String) -> Void) {
        self.center = center
        self.opener = opener
        self.runner = runner
        self.app = app
        self.log = log
    }

    public func launched(_ args: [String]) {
        let a = Arguments(args)
        if a.isPost {
            post(a)
            return
        }
        app.after(10) { self.app.terminate() }
    }

    func post(_ a: Arguments) {
        center.requestAuthorization { granted, error in
            guard granted else {
                self.log("claudia-notify: notifications not allowed \(error?.localizedDescription ?? "")\n")
                self.app.exit(2)
                return
            }
            self.center.post(a.request()) { error in
                if let error { self.log("claudia-notify: \(error.localizedDescription)\n") }
                self.app.exit(error == nil ? 0 : 1)
            }
        }
    }

    public func clicked(action: String, userInfo: [AnyHashable: Any], done: () -> Void) {
        if action == UNNotificationDefaultActionIdentifier {
            if let cmd = userInfo["exec"] as? String {
                runner.run(cmd)
            }
            if let u = userInfo["url"] as? String, let url = URL(string: u) {
                opener.open(url)
            }
        }
        done()
        app.terminate()
    }
}
