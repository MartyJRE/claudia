// claudia-notify: posts native macOS notifications for claudia and handles clicks.
//
//   claudia-notify post --title T [--subtitle S] [--body B] [--thread G] [--id ID]
//                       [--exec "shell command"] [--url URL] [--image PATH] [--sound none]
//
// Launched without arguments (macOS does that when a notification is clicked while
// the app isn't running), it waits for the click response, runs its action and quits.
import AppKit
import UserNotifications

final class App: NSObject, NSApplicationDelegate, UNUserNotificationCenterDelegate {
    let args = CommandLine.arguments

    func opt(_ name: String) -> String? {
        guard let i = args.firstIndex(of: "--" + name), i + 1 < args.count else { return nil }
        return args[i + 1]
    }

    func applicationDidFinishLaunching(_ note: Notification) {
        UNUserNotificationCenter.current().delegate = self
        if args.count > 1 && args[1] == "post" {
            post()
            return
        }
        DispatchQueue.main.asyncAfter(deadline: .now() + 10) { NSApp.terminate(nil) }
    }

    func post() {
        let center = UNUserNotificationCenter.current()
        center.requestAuthorization(options: [.alert, .sound]) { granted, error in
            guard granted else {
                fputs("claudia-notify: notifications not allowed \(error?.localizedDescription ?? "")\n", stderr)
                exit(2)
            }
            let c = UNMutableNotificationContent()
            c.title = self.opt("title") ?? "claudia"
            c.subtitle = self.opt("subtitle") ?? ""
            c.body = self.opt("body") ?? ""
            if let t = self.opt("thread") { c.threadIdentifier = t }
            if self.opt("sound") != "none" { c.sound = .default }
            var info: [String: String] = [:]
            for key in ["exec", "url"] {
                if let v = self.opt(key) { info[key] = v }
            }
            c.userInfo = info
            if let img = self.opt("image"),
               let a = try? UNNotificationAttachment(identifier: "image", url: URL(fileURLWithPath: img)) {
                c.attachments = [a]
            }
            let req = UNNotificationRequest(identifier: self.opt("id") ?? UUID().uuidString, content: c, trigger: nil)
            center.add(req) { error in
                if let error { fputs("claudia-notify: \(error.localizedDescription)\n", stderr) }
                exit(error == nil ? 0 : 1)
            }
        }
    }

    func userNotificationCenter(_ center: UNUserNotificationCenter, didReceive response: UNNotificationResponse,
                                withCompletionHandler done: @escaping () -> Void) {
        let info = response.notification.request.content.userInfo
        if response.actionIdentifier == UNNotificationDefaultActionIdentifier {
            if let cmd = info["exec"] as? String {
                let p = Process()
                p.executableURL = URL(fileURLWithPath: "/bin/sh")
                p.arguments = ["-c", cmd]
                try? p.run()
                p.waitUntilExit()
            }
            if let u = info["url"] as? String, let url = URL(string: u) {
                NSWorkspace.shared.open(url)
            }
        }
        done()
        NSApp.terminate(nil)
    }

    // Show banners even if this helper happens to be running.
    func userNotificationCenter(_ center: UNUserNotificationCenter, willPresent notification: UNNotification,
                                withCompletionHandler done: @escaping (UNNotificationPresentationOptions) -> Void) {
        done([.banner, .list, .sound])
    }
}

let app = NSApplication.shared
let delegate = App()
app.delegate = delegate
app.setActivationPolicy(.accessory)
app.run()
