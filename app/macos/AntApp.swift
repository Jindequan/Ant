// Ant.app: the walking ant in the menu bar. It runs Ant's server in the
// background (or uses the one already running), and its panel shows what
// to use first, what needs you, and every account at a glance.
import AppKit
import SwiftUI

let fm = FileManager.default
let home = fm.homeDirectoryForCurrentUser
let antDir = home.appendingPathComponent(".config/ant")
let demo = ProcessInfo.processInfo.environment["ANT_DEMO"] == "1"
let stateFile = antDir.appendingPathComponent(demo ? "server-demo.json" : "server.json")
let launchAgent = home.appendingPathComponent("Library/LaunchAgents/local.ant.app.plist")
// the last answer, drawn at launch before Ant answers again (design 15)
let cacheFile = home.appendingPathComponent("Library/Caches/local.ant.app/\(demo ? "view-demo" : "view").json")
// drawing the panel as a picture: controls and scrolling can't be drawn
// offscreen, so their content is drawn without them
let picture = ProcessInfo.processInfo.environment["ANT_SNAPSHOT"] != nil

// MARK: - what Ant's API says

struct Window: Decodable, Hashable {
    var name: String
    var usedPercent: Double?
    var left: Double?
    var limit: Double?
    var unit: String?
    var resetsAt: String?
    var expires: Bool?
    var aside: Bool?
    var model: String?
    // what Ant's view says of it (the one use-first rule, on the server)
    var percentLeft: Double?
    var useFirst: String?
}

struct Balance: Decodable, Hashable {
    var label: String?
    var currency: String
    var amount: String
}

struct Group: Decodable, Hashable {
    var windows: [Window]?
    var balances: [Balance]?
}

struct Account: Decodable, Identifiable, Hashable {
    var id: String
    var provider: String
    var name: String
    var label: String?
    var plan: String?
    var modeName: String?
    var kind: String?
    var status: String
    var message: String?
    var windows: [Window]?
    var balances: [Balance]?
    var groups: [Group]?

    var title: String { (label?.isEmpty == false ? label! : name) }
    var subtitle: String { [plan, modeName].compactMap { $0 }.filter { !$0.isEmpty }.joined(separator: " · ") }
    // what a row speaks for: the account's own allowances, else its groups'
    var own: [Window] {
        let mine = (windows ?? []).filter { $0.aside != true }
        return mine.isEmpty ? (groups ?? []).flatMap { $0.windows ?? [] }.filter { $0.aside != true } : mine
    }
    var all: [Window] { ((windows ?? []) + (groups ?? []).flatMap { $0.windows ?? [] }).filter { $0.aside != true } }
}

// Glance is the top of the panel, as Ant's server says it (design 15):
// the panel draws it and keeps no rule of its own.
struct Glance: Decodable {
    struct UseFirst: Decodable { var account: String; var window: Window; var at: String }
    var useFirst: [UseFirst]
    var needsYou: [String]
    var held: [Balance]
}
struct Snapshot: Decodable { var accounts: [Account]?; var refreshedAt: String?; var refreshing: Bool?; var glance: Glance? }
struct ServerState: Decodable { var url: String; var pid: Int32 }

let isoFrac: ISO8601DateFormatter = { let f = ISO8601DateFormatter(); f.formatOptions = [.withInternetDateTime, .withFractionalSeconds]; return f }()
let isoPlain = ISO8601DateFormatter()
func date(_ s: String?) -> Date? { s.flatMap { isoFrac.date(from: $0) ?? isoPlain.date(from: $0) } }

func percentLeft(_ w: Window) -> Double? { w.percentLeft }

func until(_ d: Date) -> String {
    let m = max(0, Int(d.timeIntervalSinceNow / 60))
    if m < 60 { return "\(m)m" }
    let h = m / 60
    if h < 24 { return m % 60 == 0 ? "\(h)h" : "\(h)h \(m % 60)m" }
    return "\(h / 24)d"
}

func ago(_ d: Date) -> String {
    let s = max(0, Int(-d.timeIntervalSinceNow))
    if s < 60 { return "just now" }
    if s < 3600 { return "\(s / 60) min ago" }
    if s < 86400 { return "\(s / 3600) h ago" }
    return "\(s / 86400) d ago"
}

func short(_ n: Double) -> String {
    switch abs(n) {
    case 1_000_000...: return String(format: "%.1fM", n / 1_000_000).replacingOccurrences(of: ".0M", with: "M")
    case 10_000...: return String(format: "%.0fK", n / 1000)
    default: return n == n.rounded() ? String(format: "%.0f", n) : String(format: "%.1f", n)
    }
}

func leftText(_ w: Window) -> String {
    if let l = w.left { return "\(short(l)) \(w.unit ?? "")".trimmingCharacters(in: .whitespaces) }
    if let p = percentLeft(w) { return p == 0 ? "Used up" : "\(Int(p.rounded()))%" }
    if let m = w.limit { return "\(short(m)) \(w.unit ?? "")".trimmingCharacters(in: .whitespaces) }
    return "—"
}

func money(_ currency: String, _ amount: Double) -> String {
    if currency == "credits" { return "\(short(amount)) credits" }
    let f = NumberFormatter()
    f.numberStyle = .currency
    f.currencyCode = currency
    f.maximumFractionDigits = amount >= 1000 ? 0 : 2
    return f.string(from: NSNumber(value: amount)) ?? "\(amount) \(currency)"
}

func money(_ b: Balance) -> String { Double(b.amount).map { money(b.currency, $0) } ?? "\(b.amount) \(b.currency)" }

// One allowance to use before it's lost.
struct Urgent: Identifiable, Hashable {
    var account: Account
    var window: Window
    var at: Date
    var id: String { account.id + "|" + window.name + "|" + (window.model ?? "") }
}

// MARK: - the server

final class Server: @unchecked Sendable {
    var process: Process?

    // The login shell's environment, as Terminal has it: the proxy, PATH
    // tools, the keys some providers are found by.
    static func loginEnvironment() -> [String: String] {
        let shell = ProcessInfo.processInfo.environment["SHELL"] ?? "/bin/zsh"
        let p = Process()
        p.executableURL = URL(fileURLWithPath: shell)
        p.arguments = ["-lic", "env -0"]
        let out = Pipe()
        p.standardOutput = out
        p.standardError = FileHandle.nullDevice
        p.standardInput = FileHandle.nullDevice
        guard (try? p.run()) != nil else { return [:] }
        var data = Data()
        let reader = DispatchQueue(label: "env")
        let done = DispatchSemaphore(value: 0)
        reader.async { data = out.fileHandleForReading.readDataToEndOfFile(); done.signal() }
        if done.wait(timeout: .now() + 8) == .timedOut { p.terminate(); return [:] }
        var env: [String: String] = [:]
        for entry in data.split(separator: 0) {
            guard let s = String(data: Data(entry), encoding: .utf8), let eq = s.firstIndex(of: "=") else { continue }
            env[String(s[..<eq])] = String(s[s.index(after: eq)...])
        }
        for k in ["PWD", "OLDPWD", "SHLVL", "_", "TERM_SESSION_ID", "TERM_PROGRAM", "TERM_PROGRAM_VERSION"] { env.removeValue(forKey: k) }
        return env
    }

    func running() -> URL? {
        guard let data = try? Data(contentsOf: stateFile),
              let s = try? JSONDecoder().decode(ServerState.self, from: data),
              kill(s.pid, 0) == 0 else { return nil }
        return URL(string: s.url)
    }

    // start uses a running Ant, else starts the one inside the app, on Ant's
    // port or the next free one; it returns where Ant listens.
    func start() -> URL? {
        if let url = running() { return url }
        guard let exe = Bundle.main.url(forResource: "ant-server", withExtension: nil) else { return nil }
        try? fm.createDirectory(at: antDir, withIntermediateDirectories: true, attributes: [.posixPermissions: 0o700])
        let logs = home.appendingPathComponent("Library/Logs/Ant")
        try? fm.createDirectory(at: logs, withIntermediateDirectories: true)
        let log = logs.appendingPathComponent("server.log")
        if !fm.fileExists(atPath: log.path) { fm.createFile(atPath: log.path, contents: nil) }
        let p = Process()
        p.executableURL = exe
        p.arguments = ["-db", antDir.appendingPathComponent(demo ? "demo.db" : "ant.db").path,
                       "-addr", demo ? "127.0.0.1:17990" : "127.0.0.1:17890", "-next-free-port",
                       "-state-file", stateFile.path] + (demo ? ["-demo"] : [])
        var env = ProcessInfo.processInfo.environment
        for (k, v) in Server.loginEnvironment() { env[k] = v }
        p.environment = env
        if let h = try? FileHandle(forWritingTo: log) { h.seekToEndOfFile(); p.standardOutput = h; p.standardError = h }
        do { try p.run() } catch { return nil }
        process = p
        for _ in 0..<150 { // up to 15 s for the server to say where it listens
            if let url = running() { return url }
            Thread.sleep(forTimeInterval: 0.1)
        }
        return nil
    }

    func stop() {
        if let p = process, p.isRunning { p.terminate(); p.waitUntilExit() }
    }
}

// MARK: - the model

@MainActor
final class Model: ObservableObject {
    @Published var base: URL?
    @Published var accounts: [Account] = []
    @Published var glance: Glance?
    @Published var refreshedAt: Date?
    @Published var reading = false
    @Published var loadIssue: String?
    @Published var openAtLogin = fm.fileExists(atPath: launchAgent.path)
    let server = Server()
    var timer: Timer?

    var byID: [String: Account] { Dictionary(accounts.map { ($0.id, $0) }, uniquingKeysWith: { a, _ in a }) }
    var urgentNow: [Urgent] {
        let by = byID
        return (glance?.useFirst ?? []).compactMap { u in
            guard let a = by[u.account], let at = date(u.at) else { return nil }
            return Urgent(account: a, window: u.window, at: at)
        }
    }
    var needsYou: [Account] { let by = byID; return (glance?.needsYou ?? []).compactMap { by[$0] } }
    var ok: [Account] { accounts.filter { $0.status == "ok" } }
    // Every API account must stay visible even if a newer server status or
    // package kind has not been added to the panel's named sections yet.
    var otherOK: [Account] { ok.filter { !["subscription", "free", "api"].contains($0.kind ?? "api") } }
    var unclassified: [Account] {
        let covered = Set(ok.map(\.id) + needsYou.map(\.id))
        return accounts.filter { !covered.contains($0.id) }
    }
    // status says how old what's shown is, and whether Ant is reading again
    var status: String {
        switch (reading, refreshedAt) {
        case (true, let at?): return "Updated \(ago(at)) · reading…"
        case (true, nil): return "Reading your accounts…"
        case (false, let at?): return "Updated \(ago(at))"
        default: return " "
        }
    }
    var prepaid: String { (glance?.held ?? []).map(money).joined(separator: " · ") }

    // show draws an answer of Ant's: from the server, or the one kept on disk.
    func show(_ snap: Snapshot) {
        accounts = snap.accounts ?? []
        glance = snap.glance
        refreshedAt = date(snap.refreshedAt)
        reading = snap.refreshing ?? false
    }

    func keep(_ data: Data) throws {
        let dir = cacheFile.deletingLastPathComponent()
        try fm.createDirectory(at: dir, withIntermediateDirectories: true, attributes: [.posixPermissions: 0o700])
        try fm.setAttributes([.posixPermissions: 0o700], ofItemAtPath: dir.path)
        try data.write(to: cacheFile, options: .atomic)
        try fm.setAttributes([.posixPermissions: 0o600], ofItemAtPath: cacheFile.path)
    }

    func boot() {
        if let data = try? Data(contentsOf: cacheFile),
           let snap = try? JSONDecoder().decode(Snapshot.self, from: data),
           let at = date(snap.refreshedAt), at.timeIntervalSince1970 > 0 {
            show(snap)
            reading = true // until Ant answers
            if ProcessInfo.processInfo.environment["ANT_DEBUG"] == "1" { print("kept: accounts=\(accounts.count) use-first=\(urgentNow.count)"); fflush(stdout) }
        }
        let server = self.server
        Task.detached {
            let url = server.start()
            await MainActor.run {
                self.base = url
                if url == nil {
                    self.loadIssue = "Ant couldn't start. Check the app installation, then try again."
                    self.reading = false
                } else {
                    self.load()
                }
                self.timer = Timer.scheduledTimer(withTimeInterval: 60, repeats: true) { _ in Task { @MainActor in self.load() } }
            }
        }
    }

    func request(_ path: String, method: String = "GET") -> URLRequest? {
        guard let base else { return nil }
        var r = URLRequest(url: base.appendingPathComponent(path), timeoutInterval: method == "POST" ? 180 : 10)
        r.httpMethod = method
        r.setValue("1", forHTTPHeaderField: "X-Ant")
        return r
    }

    // load fetches Ant's answer behind what the panel shows; one at a time.
    func load() {
        guard !fetching, let r = request("api/quota") else { return }
        fetching = true
        Task {
            defer { fetching = false }
            do {
                let (data, response) = try await URLSession.shared.data(for: r)
                guard let http = response as? HTTPURLResponse, (200..<300).contains(http.statusCode) else {
                    throw URLError(.badServerResponse)
                }
                let snap = try JSONDecoder().decode(Snapshot.self, from: data)
                // A zero-time snapshot is Ant's startup placeholder, not an
                // empty result. Keep any saved rows until the first read ends.
                let completed = date(snap.refreshedAt).map { $0.timeIntervalSince1970 > 0 } ?? false
                if completed {
                    show(snap)
                    loadIssue = nil
                } else {
                    reading = true
                }
                if completed, snap.refreshing != true {
                    do { try keep(data) }
                    catch { loadIssue = "Couldn't save this reading for next time. Your current list is still available." }
                }
            } catch {
                loadIssue = accounts.isEmpty
                    ? "Couldn't load your accounts. Check Ant's connection, then try again."
                    : "Couldn't refresh your accounts. Showing the last saved list; try again."
                reading = false
                return
            }
            // Ant is still reading: look again soon, not in a minute
            if (reading || refreshedAt == nil) && soon < 90 {
                if !again {
                    soon += 1
                    again = true
                    DispatchQueue.main.asyncAfter(deadline: .now() + 2) { self.again = false; self.load() }
                }
            } else if !reading {
                soon = 0
            }
            if let path = ProcessInfo.processInfo.environment["ANT_SNAPSHOT"], !snapshotted {
                snapshotted = true // the panel as a picture, for checking its design
                DispatchQueue.main.asyncAfter(deadline: .now() + 2) { self.snapshot(to: path) }
            }
            if ProcessInfo.processInfo.environment["ANT_DEBUG"] == "1" {
                print("base=\(base?.absoluteString ?? "-") accounts=\(accounts.count) use-first=\(urgentNow.count) needs-you=\(needsYou.count) prepaid=\(prepaid)")
                for u in urgentNow.prefix(3) { print("  use first: \(u.account.title) · \(u.window.name) \(leftText(u.window)) · \(until(u.at))") }
                fflush(stdout)
            }
        }
    }

    func retry() {
        loadIssue = nil
        if base != nil { load(); return }
        let server = self.server
        Task.detached {
            let url = server.start()
            await MainActor.run {
                self.base = url
                if url == nil {
                    self.loadIssue = "Ant couldn't start. Check the app installation, then try again."
                } else {
                    self.load()
                }
            }
        }
    }

    var snapshotted = false
    var soon = 0
    var fetching = false
    var again = false // a look-again is scheduled
    func snapshot(to path: String) {
        let r = ImageRenderer(content: Panel(model: self).background(Color(nsColor: .windowBackgroundColor)).environment(\.colorScheme, ProcessInfo.processInfo.environment["ANT_SNAPSHOT_DARK"] == "1" ? .dark : .light))
        r.scale = 2
        if let img = r.nsImage, let tiff = img.tiffRepresentation, let rep = NSBitmapImageRep(data: tiff), let png = rep.representation(using: .png, properties: [:]) {
            try? png.write(to: URL(fileURLWithPath: path))
        }
    }

    func refresh() {
        guard let r = request("api/quota/refresh", method: "POST") else { return }
        reading = true
        Task { _ = try? await URLSession.shared.data(for: r); load() }
    }

    func open() {
        if let base { NSWorkspace.shared.open(base) }
    }

    func setOpenAtLogin(_ on: Bool) {
        if on {
            let plist: [String: Any] = ["Label": "local.ant.app", "ProgramArguments": ["/usr/bin/open", "-a", Bundle.main.bundlePath], "RunAtLoad": true]
            try? fm.createDirectory(at: launchAgent.deletingLastPathComponent(), withIntermediateDirectories: true)
            if let d = try? PropertyListSerialization.data(fromPropertyList: plist, format: .xml, options: 0) { try? d.write(to: launchAgent) }
        } else {
            try? fm.removeItem(at: launchAgent)
        }
        openAtLogin = fm.fileExists(atPath: launchAgent.path)
    }
}

// MARK: - the panel

func level(_ p: Double?) -> Color { guard let p else { return .secondary }; return p < 20 ? PanelPalette.danger : p < 50 ? PanelPalette.warning : PanelPalette.good }

// logos are the page's own (server/internal/webui/static/logos), copied
// into the app, so they draw at once, server or not
var logoCache: [String: NSImage] = [:]
func logo(_ provider: String) -> NSImage? {
    if let img = logoCache[provider] { return img }
    guard let url = Bundle.main.url(forResource: provider, withExtension: "png", subdirectory: "logos"), let img = NSImage(contentsOf: url) else { return nil }
    logoCache[provider] = img
    return img
}

struct Logo: View {
    var provider: String
    var name: String
    var size: CGFloat = 24
    var body: some View {
        ZStack {
            RoundedRectangle(cornerRadius: size * 0.28).fill(Color.white)
                .overlay(RoundedRectangle(cornerRadius: size * 0.28).stroke(Color.black.opacity(0.08), lineWidth: 0.5))
            if let img = logo(provider) {
                Image(nsImage: img).resizable().scaledToFit().padding(size * 0.1)
            } else {
                Text(String(name.prefix(1))).font(.system(size: size * 0.48, weight: .semibold)).foregroundStyle(Color.gray)
            }
        }
        .frame(width: size, height: size)
    }
}

struct Bar: View {
    var percent: Double?
    var body: some View {
        ZStack(alignment: .leading) {
            Capsule().fill(PanelPalette.track)
            if let p = percent { Capsule().fill(level(p)).frame(width: max(4, 64 * p / 100)) }
        }
        .frame(width: 64, height: 6)
    }
}

struct Chip: View {
    var icon: String
    var text: String
    var color: Color
    var body: some View {
        HStack(spacing: 4) {
            Image(systemName: icon).font(.system(size: 10, weight: .bold))
            Text(text).font(.system(size: 11.5, weight: .semibold)).lineLimit(1)
        }
        .padding(.horizontal, 8).padding(.vertical, 4)
        .background(Capsule().fill(color.opacity(0.14)))
        .foregroundStyle(color)
    }
}

struct SectionTitle: View {
    var icon: String
    var text: String
    var color: Color
    var body: some View {
        HStack(spacing: 5) {
            Image(systemName: icon).font(.system(size: 10, weight: .bold))
            Text(text).font(.system(size: 12, weight: .semibold))
        }
        .foregroundStyle(color)
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(.horizontal, 16).padding(.top, 12).padding(.bottom, 4)
    }
}

// a row's hover highlight (an object: the command line tools have no
// SwiftUI macro plugin for @State)
final class Hover: ObservableObject { @Published var on = false }

struct Row<Trailing: View>: View {
    @ObservedObject var model: Model
    var account: Account
    var detail: String
    var percent: Double?
    var showBar = true
    @ViewBuilder var trailing: () -> Trailing
    @StateObject private var hover = Hover()
    var body: some View {
        if picture { content } else {
            Button { model.open() } label: { content }
                .buttonStyle(.plain)
                .onHover { hover.on = $0 }
                .padding(.horizontal, 6)
        }
    }

    var content: some View {
            HStack(spacing: 10) {
                Logo(provider: account.provider, name: account.name)
                VStack(alignment: .leading, spacing: 1) {
                    Text(account.title).font(.system(size: 13, weight: .medium)).lineLimit(1)
                    if !detail.isEmpty { Text(detail).font(.system(size: 11)).foregroundStyle(PanelPalette.secondary).lineLimit(1) }
                }
                Spacer(minLength: 8)
                if showBar { Bar(percent: percent) }
                trailing()
            }
            .padding(.horizontal, 10).padding(.vertical, 6)
            .background(RoundedRectangle(cornerRadius: 8).fill(hover.on ? Color.primary.opacity(0.07) : .clear))
            .contentShape(Rectangle())
            .padding(.horizontal, picture ? 6 : 0)
    }
}

struct Panel: View {
    @ObservedObject var model: Model

    var body: some View {
        VStack(spacing: 0) {
            header
            Divider().opacity(0.6)
            scroll {
                VStack(spacing: 0) {
                    if model.base == nil && model.accounts.isEmpty {
                        emptyState
                    } else if model.accounts.isEmpty, model.loadIssue != nil {
                        emptyState
                    } else if model.accounts.isEmpty {
                        VStack(spacing: 8) {
                            if model.reading || model.refreshedAt == nil { ProgressView().controlSize(.small) }
                            Text(model.reading || model.refreshedAt == nil ? "Reading your accounts…" : "No accounts yet. Open Ant to add one.")
                                .font(.system(size: 12)).foregroundStyle(PanelPalette.secondary)
                        }
                        .padding(36)
                    } else {
                        if let issue = model.loadIssue {
                            HStack(spacing: 8) {
                                Image(systemName: "exclamationmark.circle.fill").foregroundStyle(PanelPalette.warning)
                                Text(issue).font(.system(size: 11.5)).foregroundStyle(.primary).fixedSize(horizontal: false, vertical: true)
                                Button("Try again") { model.retry() }.buttonStyle(.borderless).tint(PanelPalette.action)
                            }
                            .padding(.horizontal, 16).padding(.vertical, 8)
                        }
                        useFirst
                        needsYou
                        everyAccount
                        unclassifiedAccounts
                    }
                }
                .padding(.bottom, 10)
            }
            Divider().opacity(0.6)
            footer
        }
        // MenuBarExtra's window can otherwise size itself to the header and
        // footer, leaving its ScrollView with a few points of height. Give
        // the live panel a real viewport; on shorter displays the scroll
        // area can still shrink to the space the system provides.
        .frame(width: 370, height: picture ? nil : 420)
        .background(PanelPalette.surface)
        .tint(PanelPalette.action)
        .onAppear { model.load() } // fresh each time it opens
    }

    var emptyState: some View {
        VStack(spacing: 9) {
            if model.loadIssue == nil { ProgressView().controlSize(.small) }
            Text(model.loadIssue ?? "Ant is starting…")
                .font(.system(size: 12.5, weight: .medium)).foregroundStyle(.primary)
                .multilineTextAlignment(.center).fixedSize(horizontal: false, vertical: true)
            if model.loadIssue != nil {
                Button("Try again") { model.retry() }.buttonStyle(.borderedProminent).controlSize(.small)
            }
        }
        .padding(36)
    }

    @ViewBuilder func scroll<C: View>(@ViewBuilder _ c: () -> C) -> some View {
        // The live panel owns a fixed-height viewport above; this lets the
        // list fill the available middle space and scroll instead of allowing
        // MenuBarExtra to collapse it while header counts remain visible.
        if picture { c() } else { ScrollView { c() }.frame(maxWidth: .infinity, maxHeight: .infinity) }
    }

    // icon is a header or footer control, drawn plain in a picture
    @ViewBuilder func control(_ systemName: String, _ help: String, _ action: @escaping () -> Void) -> some View {
        let img = Image(systemName: systemName).font(.system(size: 13, weight: .semibold))
        if picture { img.foregroundStyle(PanelPalette.secondary) } else { Button(action: action) { img }.buttonStyle(.borderless).help(help) }
    }

    var header: some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack(spacing: 9) {
                if let img = Bundle.main.image(forResource: "AntTemplate") {
                    Image(nsImage: img).renderingMode(.template).resizable().frame(width: 24, height: 24)
                }
                VStack(alignment: .leading, spacing: 0) {
                    Text("Ant").font(.system(size: 15, weight: .semibold))
                    Text(model.status)
                        .font(.system(size: 11)).foregroundStyle(PanelPalette.secondary)
                }
                Spacer()
                control("arrow.clockwise", "Read every account again") { model.refresh() }
            }
            if model.base != nil || !model.accounts.isEmpty {
                HStack(spacing: 6) {
                    if !model.urgentNow.isEmpty { Chip(icon: "hourglass", text: "\(model.urgentNow.count) use first", color: PanelPalette.warning) }
                    if !model.needsYou.isEmpty { Chip(icon: "exclamationmark.triangle.fill", text: "\(model.needsYou.count) need you", color: PanelPalette.danger) }
                    if !model.prepaid.isEmpty { Chip(icon: "creditcard.fill", text: model.prepaid, color: PanelPalette.good) }
                    if model.urgentNow.isEmpty && model.needsYou.isEmpty { Chip(icon: "checkmark.circle.fill", text: "All good", color: PanelPalette.good) }
                }
            }
        }
        .padding(16)
    }

    @ViewBuilder var useFirst: some View {
        if !model.urgentNow.isEmpty {
            SectionTitle(icon: "hourglass", text: "Use first", color: PanelPalette.warning)
            ForEach(model.urgentNow.prefix(6)) { u in
                Row(model: model, account: u.account, detail: u.window.name, percent: percentLeft(u.window)) {
                    VStack(alignment: .trailing, spacing: 1) {
                        Text(leftText(u.window)).font(.system(size: 12.5, weight: .semibold)).monospacedDigit()
                        Text(until(u.at)).font(.system(size: 11.5, weight: .semibold)).foregroundStyle(PanelPalette.warning)
                    }
                    .frame(width: 88, alignment: .trailing)
                }
            }
            if model.urgentNow.count > 6 {
                Text("\(model.urgentNow.count - 6) more in Ant").font(.system(size: 11)).foregroundStyle(PanelPalette.secondary)
                    .frame(maxWidth: .infinity, alignment: .leading).padding(.horizontal, 16).padding(.top, 2)
            }
        }
    }

    @ViewBuilder var needsYou: some View {
        if !model.needsYou.isEmpty {
            SectionTitle(icon: "exclamationmark.triangle.fill", text: "Needs you", color: PanelPalette.danger)
            ForEach(model.needsYou) { a in
                Row(model: model, account: a, detail: a.status == "signed_out" ? "Signed out · sign in again" : (a.message ?? "Couldn't read"), percent: nil, showBar: false) {
                    Image(systemName: a.status == "signed_out" ? "key.slash.fill" : "wifi.exclamationmark")
                        .foregroundStyle(PanelPalette.danger).font(.system(size: 12, weight: .semibold))
                }
            }
        }
    }

    // every account, by kind, each with the one value that matters
    @ViewBuilder var everyAccount: some View {
        ForEach([("subscription", "Subscriptions", "repeat"), ("free", "Free", "gift.fill"), ("api", "Balances", "creditcard.fill")], id: \.0) { kind, title, icon in
            let list = model.ok.filter { ($0.kind ?? "api") == kind }
            if !list.isEmpty {
                SectionTitle(icon: icon, text: "\(title) · \(list.count)", color: PanelPalette.section)
                ForEach(list) { a in accountRow(a) }
            }
        }
        if !model.otherOK.isEmpty {
            SectionTitle(icon: "square.stack.3d.up.fill", text: "Other accounts · \(model.otherOK.count)", color: PanelPalette.section)
            ForEach(model.otherOK) { a in accountRow(a) }
        }
    }

    @ViewBuilder var unclassifiedAccounts: some View {
        if !model.unclassified.isEmpty {
            SectionTitle(icon: "questionmark.circle.fill", text: "Account status · \(model.unclassified.count)", color: PanelPalette.warning)
            ForEach(model.unclassified) { a in
                Row(model: model, account: a, detail: a.message ?? "This account needs a fresh read.", percent: nil, showBar: false) {
                    Image(systemName: "exclamationmark.circle.fill").foregroundStyle(PanelPalette.warning).font(.system(size: 12, weight: .semibold))
                }
            }
        }
    }

    @ViewBuilder func accountRow(_ a: Account) -> some View {
        let measured = a.own.filter { percentLeft($0) != nil }.min { (percentLeft($0) ?? 0) < (percentLeft($1) ?? 0) }
        let counted = a.own.first { $0.left != nil || $0.limit != nil }
        if let b = (a.balances ?? []).first, a.kind == "api" || (measured == nil && counted == nil) {
            Row(model: model, account: a, detail: a.subtitle, percent: nil, showBar: false) { value(money(b)) }
        } else if a.kind == "free" && a.own.count > 2 {
            Row(model: model, account: a, detail: a.subtitle, percent: nil, showBar: false) { value("\(a.own.count) allowances", quiet: true) }
        } else if let w = measured ?? counted {
            Row(model: model, account: a, detail: [a.subtitle, w.name].filter { !$0.isEmpty }.joined(separator: " · "), percent: percentLeft(w), showBar: percentLeft(w) != nil) {
                value(leftText(w))
            }
        } else {
            Row(model: model, account: a, detail: a.subtitle, percent: nil, showBar: false) { value("—", quiet: true) }
        }
    }

    // value is a row's number, in one column so the bars line up
    func value(_ text: String, quiet: Bool = false) -> some View {
        Text(text).font(.system(size: 12.5, weight: quiet ? .regular : .semibold)).monospacedDigit()
            .foregroundStyle(quiet ? PanelPalette.secondary : Color.primary).lineLimit(1)
            .frame(width: 88, alignment: .trailing)
    }

    var footer: some View {
        HStack(spacing: 10) {
            if picture {
                Label("Open Ant", systemImage: "arrow.up.forward.app").font(.system(size: 12, weight: .semibold)).foregroundStyle(.white)
                    .padding(.horizontal, 10).padding(.vertical, 5).background(RoundedRectangle(cornerRadius: 6).fill(PanelPalette.action))
                Spacer()
                Text("Open at login").font(.system(size: 11)).foregroundStyle(PanelPalette.secondary)
                Capsule().fill(model.openAtLogin ? Color.accentColor : Color.primary.opacity(0.15)).frame(width: 26, height: 15)
                    .overlay(Circle().fill(.white).padding(2), alignment: model.openAtLogin ? .trailing : .leading)
                Image(systemName: "power").font(.system(size: 12, weight: .semibold)).foregroundStyle(PanelPalette.secondary)
            } else {
                Button { model.open() } label: {
                    Label("Open Ant", systemImage: "arrow.up.forward.app")
                        .font(.system(size: 12, weight: .semibold)).foregroundStyle(.white)
                        .padding(.horizontal, 10).padding(.vertical, 5)
                        .background(RoundedRectangle(cornerRadius: 6).fill(PanelPalette.action))
                }
                .buttonStyle(.plain)
                Spacer()
                Toggle(isOn: Binding(get: { model.openAtLogin }, set: { model.setOpenAtLogin($0) })) {
                    Text("Open at login").font(.system(size: 11)).foregroundStyle(PanelPalette.secondary)
                }
                .toggleStyle(.switch).controlSize(.mini)
                Button { NSApp.terminate(nil) } label: { Image(systemName: "power").font(.system(size: 12, weight: .semibold)) }
                    .buttonStyle(.borderless).help("Quit Ant")
            }
        }
        .padding(.horizontal, 16).padding(.vertical, 10)
    }
}

// MARK: - the app

final class AppDelegate: NSObject, NSApplicationDelegate {
    var stops: [DispatchSourceSignal] = []
    func applicationDidFinishLaunching(_ note: Notification) {
        // a quit by signal (kill, Ctrl-C) stops the server too, as Quit does
        for sig in [SIGTERM, SIGINT] {
            signal(sig, SIG_IGN)
            let src = DispatchSource.makeSignalSource(signal: sig, queue: .main)
            src.setEventHandler { NSApp.terminate(nil) }
            src.resume()
            stops.append(src)
        }
    }
    func applicationWillTerminate(_ note: Notification) {
        MainActor.assumeIsolated { AntApp.model.server.stop() }
    }
}

@main
struct AntApp: App {
    @NSApplicationDelegateAdaptor(AppDelegate.self) var delegate
    @MainActor static let model: Model = { let m = Model(); m.boot(); return m }()
    @ObservedObject var model = AntApp.model

    var body: some Scene {
        MenuBarExtra {
            Panel(model: model)
        } label: {
            Image(nsImage: menuBarAnt()) // the ant alone
        }
        .menuBarExtraStyle(.window)
    }
}

func menuBarAnt() -> NSImage {
    let img = Bundle.main.image(forResource: "AntTemplate") ?? NSImage()
    img.isTemplate = true
    img.size = NSSize(width: 20, height: 20)
    return img
}
