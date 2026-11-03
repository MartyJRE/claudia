import AppKit
import NotifierCore
import UserNotifications
import XCTest

struct Failure: LocalizedError {
    let errorDescription: String?
}

final class FakeCenter: NotificationPosting {
    var granted = true
    var authError: Error?
    var postError: Error?
    var posted: [UNNotificationRequest] = []

    func requestAuthorization(_ done: @escaping (Bool, Error?) -> Void) { done(granted, authError) }

    func post(_ request: UNNotificationRequest, _ done: @escaping (Error?) -> Void) {
        posted.append(request)
        done(postError)
    }
}

final class FakeOpener: URLOpening {
    var opened: [URL] = []
    func open(_ url: URL) -> Bool {
        opened.append(url)
        return true
    }
}

final class FakeRunner: CommandRunning {
    var ran: [String] = []
    func run(_ command: String) { ran.append(command) }
}

final class FakeApp: AppLifecycle {
    var terminated = 0
    var exits: [Int32] = []
    var delays: [Double] = []
    func terminate() { terminated += 1 }
    func exit(_ code: Int32) { exits.append(code) }
    func after(_ seconds: Double, _ block: @escaping () -> Void) {
        delays.append(seconds)
        block()
    }
}

final class NotifierTests: XCTestCase {
    var center = FakeCenter()
    var opener = FakeOpener()
    var runner = FakeRunner()
    var app = FakeApp()
    var logged: [String] = []

    func notifier() -> Notifier {
        Notifier(center: center, opener: opener, runner: runner, app: app, log: { self.logged.append($0) })
    }

    func testOpt() {
        let a = Arguments(["claudia-notify", "post", "--title", "T", "--body"])
        XCTAssertTrue(a.isPost)
        XCTAssertEqual(a.opt("title"), "T")
        XCTAssertNil(a.opt("body"))
        XCTAssertNil(a.opt("subtitle"))
        XCTAssertFalse(Arguments(["claudia-notify"]).isPost)
        XCTAssertFalse(Arguments(["claudia-notify", "--title", "post"]).isPost)
    }

    func testContentDefaults() {
        let c = Arguments(["claudia-notify", "post"]).content()
        XCTAssertEqual(c.title, "claudia")
        XCTAssertEqual(c.subtitle, "")
        XCTAssertEqual(c.body, "")
        XCTAssertEqual(c.threadIdentifier, "")
        XCTAssertNotNil(c.sound)
        XCTAssertTrue(c.userInfo.isEmpty)
        XCTAssertTrue(c.attachments.isEmpty)
    }

    func testContentFull() throws {
        let img = FileManager.default.temporaryDirectory.appendingPathComponent("claudia-test-\(UUID()).png")
        try renderIcon(size: 32).write(to: img)
        defer { try? FileManager.default.removeItem(at: img) }
        let c = Arguments(["claudia-notify", "post", "--title", "T", "--subtitle", "S", "--body", "B",
                           "--thread", "G", "--exec", "echo hi", "--url", "https://x", "--sound", "none",
                           "--image", img.path]).content()
        XCTAssertEqual(c.title, "T")
        XCTAssertEqual(c.subtitle, "S")
        XCTAssertEqual(c.body, "B")
        XCTAssertEqual(c.threadIdentifier, "G")
        XCTAssertNil(c.sound)
        XCTAssertEqual(c.userInfo as? [String: String], ["exec": "echo hi", "url": "https://x"])
        XCTAssertEqual(c.attachments.map(\.identifier), ["image"])
    }

    func testUnusableImageIsSkipped() {
        let c = Arguments(["claudia-notify", "post", "--image", "/etc/hosts"]).content()
        XCTAssertTrue(c.attachments.isEmpty)
    }

    func testRequestID() {
        XCTAssertEqual(Arguments(["claudia-notify", "post", "--id", "setup"]).request().identifier, "setup")
        let a = Arguments(["claudia-notify", "post"]).request().identifier
        XCTAssertNotNil(UUID(uuidString: a))
    }

    func testPost() {
        notifier().launched(["claudia-notify", "post", "--title", "T", "--id", "x"])
        XCTAssertEqual(center.posted.map(\.identifier), ["x"])
        XCTAssertEqual(center.posted.first?.content.title, "T")
        XCTAssertEqual(app.exits, [0])
        XCTAssertTrue(logged.isEmpty)
        XCTAssertEqual(app.terminated, 0)
    }

    func testPostDenied() {
        center.granted = false
        center.authError = Failure(errorDescription: "denied")
        notifier().launched(["claudia-notify", "post"])
        XCTAssertTrue(center.posted.isEmpty)
        XCTAssertEqual(app.exits, [2])
        XCTAssertEqual(logged, ["claudia-notify: notifications not allowed denied\n"])
    }

    func testPostDeniedWithoutError() {
        center.granted = false
        notifier().launched(["claudia-notify", "post"])
        XCTAssertEqual(logged, ["claudia-notify: notifications not allowed \n"])
    }

    func testPostFails() {
        center.postError = Failure(errorDescription: "boom")
        notifier().launched(["claudia-notify", "post"])
        XCTAssertEqual(app.exits, [1])
        XCTAssertEqual(logged, ["claudia-notify: boom\n"])
    }

    func testLaunchedForClickQuitsLater() {
        notifier().launched(["claudia-notify"])
        XCTAssertEqual(app.delays, [10])
        XCTAssertEqual(app.terminated, 1)
        XCTAssertTrue(center.posted.isEmpty)
    }

    func testClickRunsAndOpens() {
        var done = 0
        notifier().clicked(action: UNNotificationDefaultActionIdentifier,
                           userInfo: ["exec": "tmux switch-client", "url": "https://example.com/a"], done: { done += 1 })
        XCTAssertEqual(runner.ran, ["tmux switch-client"])
        XCTAssertEqual(opener.opened, [URL(string: "https://example.com/a")!])
        XCTAssertEqual(done, 1)
        XCTAssertEqual(app.terminated, 1)
    }

    func testClickWithoutActions() {
        notifier().clicked(action: UNNotificationDefaultActionIdentifier, userInfo: ["url": ""], done: {})
        XCTAssertTrue(runner.ran.isEmpty)
        XCTAssertTrue(opener.opened.isEmpty)
        XCTAssertEqual(app.terminated, 1)
    }

    func testDismissDoesNothing() {
        var done = 0
        notifier().clicked(action: UNNotificationDismissActionIdentifier,
                           userInfo: ["exec": "x", "url": "https://x"], done: { done += 1 })
        XCTAssertTrue(runner.ran.isEmpty)
        XCTAssertTrue(opener.opened.isEmpty)
        XCTAssertEqual(done, 1)
        XCTAssertEqual(app.terminated, 1)
    }

    func testOptions() {
        XCTAssertEqual(Notifier.authorization, [.alert, .sound])
        XCTAssertEqual(Notifier.presentation, [.banner, .list, .sound])
    }

    func testDefaultRunnerIsShell() throws {
        let out = FileManager.default.temporaryDirectory.appendingPathComponent("claudia-test-\(UUID())")
        defer { try? FileManager.default.removeItem(at: out) }
        Notifier(center: center, opener: opener, app: app, log: { _ in })
            .clicked(action: UNNotificationDefaultActionIdentifier, userInfo: ["exec": "printf hi > '\(out.path)'"], done: {})
        XCTAssertEqual(try String(contentsOf: out, encoding: .utf8), "hi")
    }

    func testIcon() throws {
        let png = renderIcon(size: 64)
        XCTAssertEqual(Array(png.prefix(4)), [0x89, 0x50, 0x4e, 0x47])
        let rep = try XCTUnwrap(NSBitmapImageRep(data: png))
        XCTAssertEqual(rep.pixelsWide, 64)
        XCTAssertEqual(rep.colorAt(x: 1, y: 1)?.alphaComponent, 0)
        XCTAssertEqual(rep.colorAt(x: 32, y: 8)?.alphaComponent, 1)
    }
}
