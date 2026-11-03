// The OS side of claudia-notify; the behaviour lives in NotifierCore.
import AppKit
import NotifierCore
import UserNotifications

extension UNUserNotificationCenter: NotificationPosting {
    public func requestAuthorization(_ done: @escaping (Bool, Error?) -> Void) {
        requestAuthorization(options: Notifier.authorization, completionHandler: done)
    }

    public func post(_ request: UNNotificationRequest, _ done: @escaping (Error?) -> Void) {
        add(request, withCompletionHandler: done)
    }
}

extension NSWorkspace: URLOpening {}

struct Lifecycle: AppLifecycle {
    func terminate() { NSApp.terminate(nil) }
    func exit(_ code: Int32) { Darwin.exit(code) }
    func after(_ seconds: Double, _ block: @escaping () -> Void) {
        DispatchQueue.main.asyncAfter(deadline: .now() + seconds, execute: block)
    }
}

final class Delegate: NSObject, NSApplicationDelegate, UNUserNotificationCenterDelegate {
    let notifier = Notifier(center: UNUserNotificationCenter.current(), opener: NSWorkspace.shared,
                            app: Lifecycle(), log: { fputs($0, stderr) })

    func applicationDidFinishLaunching(_ note: Notification) {
        UNUserNotificationCenter.current().delegate = self
        notifier.launched(CommandLine.arguments)
    }

    func userNotificationCenter(_ center: UNUserNotificationCenter, didReceive response: UNNotificationResponse,
                                withCompletionHandler done: @escaping () -> Void) {
        notifier.clicked(action: response.actionIdentifier,
                         userInfo: response.notification.request.content.userInfo, done: done)
    }

    func userNotificationCenter(_ center: UNUserNotificationCenter, willPresent notification: UNNotification,
                                withCompletionHandler done: @escaping (UNNotificationPresentationOptions) -> Void) {
        done(Notifier.presentation)
    }
}

let app = NSApplication.shared
let delegate = Delegate()
app.delegate = delegate
app.setActivationPolicy(.accessory)
app.run()
