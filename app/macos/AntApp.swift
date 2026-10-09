// Ant.app: the walking ant in the menu bar. It runs Ant's server in the
// background (the one inside the app, or one already running), shows what
// to use before it's lost, and opens the page. No window, no Dock icon.
import AppKit

let port = ProcessInfo.processInfo.environment["ANT_PORT"] ?? "8080"
let base = URL(string: "http://127.0.0.1:\(port)")!
let home = FileManager.default.homeDirectoryForCurrentUser
let antDir = home.appendingPathComponent(".config/ant")
let demo = ProcessInfo.processInfo.environment["ANT_DEMO"] == "1"
let launchAgent = home.appendingPathComponent("Library/LaunchAgents/local.ant.app.plist")

// MARK: what the page's API says

struct Window: Decodable {
    var name: String
    var usedPercent: Double?
    var left: Double?
    var limit: Double?
    var unit: String?
    var resetsAt: String?
    var expires: Bool?
    var aside: Bool?
    var model: String?
}

struct Group: Decodable { var windows: [Window]? }

struct Account: Decodable {
    var id: String
    var provider: String
    var name: String
    var label: String?
    var status: String
    var windows: [Window]?
    var groups: [Group]?
}

struct Snapshot: Decodable { var accounts: [Account]? }

struct Insight: Decodable {
    var window: String?
    var model: String?
    var outlook: String?
}

// One allowance to use before it's lost.
struct Urgent {
    var account: Account
    var window: Window
    var at: Date
}

let iso: ISO8601DateFormatter = {
    let f = ISO8601DateFormatter()
    f.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
    return f
}()

func date(_ s: String?) -> Date? {
    guard let s else { return nil }
    return iso.date(from: s) ?? ISO8601DateFormatter().date(from: s)
}

func percentLeft(_ w: Window) -> Double? {
    if let u = w.usedPercent { return max(0, min(100, 100 - u)) }
    if let l = w.left, let m = w.limit, m > 0 { return max(0, min(100, l / m * 100)) }
    return nil
}

// The page's rule: a window resetting within a day with at least 30% left,
// or a grant ending within 14 days, unless the recent pace uses it up first.
func urgent(_ accounts: [Account], _ insights: [String: [Insight]]) -> [Urgent] {
    var out: [Urgent] = []
    let now = Date()
    for a in accounts where a.status == "ok" {
        let ws = (a.windows ?? []) + (a.groups ?? []).flatMap { $0.windows ?? [] }
        for w in ws where w.aside != true {
            guard let at = date(w.resetsAt), at > now else { continue }
            let runsOut = (insights[a.id] ?? []).contains { $0.window == w.name && ($0.model ?? "") == (w.model ?? "") && $0.outlook == "runs_out" }
            if runsOut { continue }
            if w.expires == true {
                if at.timeIntervalSince(now) < 14 * 86400 { out.append(Urgent(account: a, window: w, at: at)) }
            } else if let p = percentLeft(w), p >= 30, at.timeIntervalSince(now) < 86400 {
                out.append(Urgent(account: a, window: w, at: at))
            }
        }
    }
    return out.sorted { $0.at < $1.at }
}

func until(_ d: Date) -> String {
    let m = max(0, Int(d.timeIntervalSinceNow / 60))
    if m < 60 { return "\(m)m" }
    let h = m / 60
    if h < 24 { return m % 60 == 0 ? "\(h)h" : "\(h)h \(m % 60)m" }
    return "\(h / 24)d"
}

func leftText(_ w: Window) -> String {
    if let l = w.left {
        let n = l >= 10000 ? String(format: "%.0fK", l / 1000) : String(format: "%.0f", l)
        return "\(n) \(w.unit ?? "") left".replacingOccurrences(of: "  ", with: " ")
    }
    if let p = percentLeft(w) { return p == 0 ? "used up" : "\(Int(p.rounded()))% left" }
    return ""
}

// MARK: the app

final class AppDelegate: NSObject, NSApplicationDelegate, NSMenuDelegate {
    var item: NSStatusItem!
    var server: Process?
    var quitting = false
    var restarts = 0
    var urgentNow: [Urgent] = []
    var reachable = false
    var logos: [String: NSImage] = [:]
    var startTries = 0

    func applicationDidFinishLaunching(_ note: Notification) {
        item = NSStatusBar.system.statusItem(withLength: NSStatusItem.variableLength)
        if let img = Bundle.main.image(forResource: "AntTemplate") {
            img.isTemplate = true
            img.size = NSSize(width: 20, height: 20)
            item.button?.image = img
        }
        item.button?.imagePosition = .imageLeft
        item.button?.toolTip = "Ant"
        let menu = NSMenu()
        menu.delegate = self
        item.menu = menu
        startServer()
        poll()
        Timer.scheduledTimer(withTimeInterval: 60, repeats: true) { [weak self] _ in self?.poll() }
    }

    func applicationWillTerminate(_ note: Notification) {
        quitting = true
        if let s = server, s.isRunning {
            s.terminate()
            s.waitUntilExit()
        }
    }

    // MARK: the server

    func answers() -> Bool {
        var req = URLRequest(url: base.appendingPathComponent("api/quota"), timeoutInterval: 2)
        req.setValue("1", forHTTPHeaderField: "X-Ant")
        let done = DispatchSemaphore(value: 0)
        var ok = false
        URLSession.shared.dataTask(with: req) { _, res, _ in
            ok = (res as? HTTPURLResponse)?.statusCode == 200
            done.signal()
        }.resume()
        _ = done.wait(timeout: .now() + 3)
        return ok
    }

    func startServer() {
        if answers() { return } // `make run`, or another copy of the app, is already serving
        guard let exe = Bundle.main.url(forResource: "ant-server", withExtension: nil) else { return }
        try? FileManager.default.createDirectory(at: antDir, withIntermediateDirectories: true, attributes: [.posixPermissions: 0o700])
        let db = antDir.appendingPathComponent(demo ? "demo.db" : "ant.db").path
        let logs = home.appendingPathComponent("Library/Logs/Ant")
        try? FileManager.default.createDirectory(at: logs, withIntermediateDirectories: true)
        let log = logs.appendingPathComponent("server.log")
        if !FileManager.default.fileExists(atPath: log.path) { FileManager.default.createFile(atPath: log.path, contents: nil) }
        let p = Process()
        p.executableURL = exe
        p.arguments = ["-db", db, "-addr", "127.0.0.1:\(port)"] + (demo ? ["-demo"] : [])
        if let h = try? FileHandle(forWritingTo: log) {
            h.seekToEndOfFile()
            p.standardOutput = h
            p.standardError = h
        }
        p.terminationHandler = { [weak self] _ in
            DispatchQueue.main.async {
                guard let self, !self.quitting, self.restarts < 5 else { return }
                self.restarts += 1
                DispatchQueue.main.asyncAfter(deadline: .now() + 3) { self.startServer() }
            }
        }
        do { try p.run(); server = p } catch { NSLog("Ant: couldn't start the server: \(error)") }
    }

    // MARK: reading

    func get<T: Decodable>(_ path: String, _ type: T.Type, _ done: @escaping (T?) -> Void) {
        var req = URLRequest(url: base.appendingPathComponent(path), timeoutInterval: 10)
        req.setValue("1", forHTTPHeaderField: "X-Ant")
        URLSession.shared.dataTask(with: req) { data, res, _ in
            let ok = (res as? HTTPURLResponse)?.statusCode == 200
            let v = ok ? data.flatMap { try? JSONDecoder().decode(T.self, from: $0) } : nil
            DispatchQueue.main.async { done(v) }
        }.resume()
    }

    func poll() {
        get("api/quota", Snapshot.self) { [weak self] snap in
            guard let self else { return }
            self.reachable = snap != nil
            guard let accounts = snap?.accounts else {
                self.show()
                // the server is starting: look again soon, not in a minute
                if self.startTries < 40 {
                    self.startTries += 1
                    DispatchQueue.main.asyncAfter(deadline: .now() + 1.5) { self.poll() }
                }
                return
            }
            self.startTries = 0
            self.get("api/insights", [String: [Insight]].self) { ins in
                self.urgentNow = urgent(accounts, ins ?? [:])
                for u in self.urgentNow.prefix(5) { self.loadLogo(u.account.provider) }
                self.show()
            }
        }
    }

    func loadLogo(_ provider: String) {
        guard logos[provider] == nil else { return }
        let url = base.appendingPathComponent("logos/\(provider).png")
        URLSession.shared.dataTask(with: url) { data, res, _ in
            guard (res as? HTTPURLResponse)?.statusCode == 200, let data, let img = NSImage(data: data) else { return }
            img.size = NSSize(width: 16, height: 16)
            DispatchQueue.main.async { self.logos[provider] = img }
        }.resume()
    }

    // the count beside the ant: allowances to use before they're lost
    func show() {
        item.button?.title = urgentNow.isEmpty ? "" : " \(urgentNow.count)"
        item.button?.appearsDisabled = !reachable
        if ProcessInfo.processInfo.environment["ANT_DEBUG"] == "1" { // what the menu shows, for checking
            print("reachable=\(reachable) count=\(urgentNow.count)")
            for u in urgentNow.prefix(5) { print("  " + line(u).string + (logos[u.account.provider] == nil ? "" : "  [logo]")) }
            fflush(stdout)
        }
    }

    // MARK: the menu

    func menuNeedsUpdate(_ menu: NSMenu) {
        menu.removeAllItems()
        if !reachable {
            menu.addItem(disabled("Ant is starting…"))
        } else if urgentNow.isEmpty {
            menu.addItem(disabled("Nothing is about to be lost"))
        } else {
            menu.addItem(heading("Use first"))
            for u in urgentNow.prefix(5) {
                let mi = NSMenuItem(title: "", action: #selector(openPage), keyEquivalent: "")
                mi.target = self
                mi.image = logos[u.account.provider]
                mi.attributedTitle = line(u)
                menu.addItem(mi)
            }
            if urgentNow.count > 5 { menu.addItem(disabled("\(urgentNow.count - 5) more on the page")) }
        }
        menu.addItem(.separator())
        menu.addItem(action("Open Ant", #selector(openPage), "o"))
        menu.addItem(action("Read every account again", #selector(refresh), "r"))
        menu.addItem(.separator())
        let login = action("Open at login", #selector(toggleLogin), "")
        login.state = FileManager.default.fileExists(atPath: launchAgent.path) ? .on : .off
        menu.addItem(login)
        menu.addItem(.separator())
        menu.addItem(action("Quit Ant", #selector(quit), "q"))
    }

    func line(_ u: Urgent) -> NSAttributedString {
        let s = NSMutableAttributedString()
        let name = u.account.label?.isEmpty == false ? u.account.label! : u.account.name
        s.append(NSAttributedString(string: "\(name)  ", attributes: [.font: NSFont.menuFont(ofSize: 0)]))
        s.append(NSAttributedString(string: u.window.name, attributes: [.font: NSFont.menuFont(ofSize: 0), .foregroundColor: NSColor.secondaryLabelColor]))
        s.append(NSAttributedString(string: "   \(leftText(u.window))", attributes: [.font: NSFont.monospacedDigitSystemFont(ofSize: NSFont.systemFontSize, weight: .semibold)]))
        s.append(NSAttributedString(string: " · \(u.window.expires == true ? "ends" : "resets") in \(until(u.at))",
                                    attributes: [.font: NSFont.menuFont(ofSize: 0), .foregroundColor: NSColor.systemOrange]))
        return s
    }

    func heading(_ t: String) -> NSMenuItem {
        if #available(macOS 14.0, *) { return NSMenuItem.sectionHeader(title: t) }
        return disabled(t)
    }

    func disabled(_ t: String) -> NSMenuItem {
        let mi = NSMenuItem(title: t, action: nil, keyEquivalent: "")
        mi.isEnabled = false
        return mi
    }

    func action(_ t: String, _ sel: Selector, _ key: String) -> NSMenuItem {
        let mi = NSMenuItem(title: t, action: sel, keyEquivalent: key)
        mi.target = self
        return mi
    }

    @objc func openPage() { NSWorkspace.shared.open(base) }

    @objc func refresh() {
        var req = URLRequest(url: base.appendingPathComponent("api/quota/refresh"), timeoutInterval: 120)
        req.httpMethod = "POST"
        req.setValue("1", forHTTPHeaderField: "X-Ant")
        URLSession.shared.dataTask(with: req) { [weak self] _, _, _ in DispatchQueue.main.async { self?.poll() } }.resume()
    }

    // Open at login: a LaunchAgent that opens this app when you log in.
    @objc func toggleLogin() {
        let fm = FileManager.default
        if fm.fileExists(atPath: launchAgent.path) {
            try? fm.removeItem(at: launchAgent)
            return
        }
        let plist: [String: Any] = [
            "Label": "local.ant.app",
            "ProgramArguments": ["/usr/bin/open", "-a", Bundle.main.bundlePath],
            "RunAtLoad": true,
        ]
        try? fm.createDirectory(at: launchAgent.deletingLastPathComponent(), withIntermediateDirectories: true)
        if let data = try? PropertyListSerialization.data(fromPropertyList: plist, format: .xml, options: 0) {
            try? data.write(to: launchAgent)
        }
    }

    @objc func quit() { NSApp.terminate(nil) }
}

let app = NSApplication.shared
let delegate = AppDelegate()
app.delegate = delegate
// a quit by signal (kill, Ctrl-C) stops the server too, as Quit does
var stops: [DispatchSourceSignal] = []
for sig in [SIGTERM, SIGINT] {
    signal(sig, SIG_IGN)
    let src = DispatchSource.makeSignalSource(signal: sig, queue: .main)
    src.setEventHandler { NSApp.terminate(nil) }
    src.resume()
    stops.append(src)
}
app.setActivationPolicy(.accessory) // the menu bar only
app.run()
