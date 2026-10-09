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

struct Snapshot: Decodable { var accounts: [Account]?; var refreshedAt: String?; var refreshing: Bool? }
struct Insight: Decodable { var window: String?; var model: String?; var outlook: String? }
struct ServerState: Decodable { var url: String; var pid: Int32 }

let isoFrac: ISO8601DateFormatter = { let f = ISO8601DateFormatter(); f.formatOptions = [.withInternetDateTime, .withFractionalSeconds]; return f }()
let isoPlain = ISO8601DateFormatter()
func date(_ s: String?) -> Date? { s.flatMap { isoFrac.date(from: $0) ?? isoPlain.date(from: $0) } }

func percentLeft(_ w: Window) -> Double? {
    if let u = w.usedPercent { return max(0, min(100, 100 - u)) }
    if let l = w.left, let m = w.limit, m > 0 { return max(0, min(100, l / m * 100)) }
    return nil
}

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

// One allowance to use before it's lost (the page's rule).
struct Urgent: Identifiable, Hashable {
    var account: Account
    var window: Window
    var at: Date
    var id: String { account.id + "|" + window.name + "|" + (window.model ?? "") }
}

func urgent(_ accounts: [Account], _ insights: [String: [Insight]]) -> [Urgent] {
    var out: [Urgent] = []
    for a in accounts where a.status == "ok" {
        for w in a.all {
            guard let at = date(w.resetsAt), at > Date() else { continue }
            if (insights[a.id] ?? []).contains(where: { $0.window == w.name && ($0.model ?? "") == (w.model ?? "") && $0.outlook == "runs_out" }) { continue }
            if w.expires == true {
                if at.timeIntervalSinceNow < 14 * 86400 { out.append(Urgent(account: a, window: w, at: at)) }
            } else if let p = percentLeft(w), p >= 30, at.timeIntervalSinceNow < 86400 {
                out.append(Urgent(account: a, window: w, at: at))
            }
        }
    }
    return out.sorted { $0.at < $1.at }
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
    @Published var urgentNow: [Urgent] = []
    @Published var refreshedAt: Date?
    @Published var reading = false
    @Published var openAtLogin = fm.fileExists(atPath: launchAgent.path)
    let server = Server()
    var timer: Timer?

    var needsYou: [Account] { accounts.filter { $0.status == "signed_out" || $0.status == "error" } }
    var ok: [Account] { accounts.filter { $0.status == "ok" } }
    var prepaid: String {
        var totals: [String: Double] = [:]
        for a in ok where a.kind == "api" {
            for b in a.balances ?? [] where b.currency != "credits" { totals[b.currency, default: 0] += Double(b.amount) ?? 0 }
        }
        return totals.sorted { $0.key < $1.key }.map { money($0.key, $0.value) }.joined(separator: " · ")
    }

    func boot() {
        let server = self.server
        Task.detached {
            let url = server.start()
            await MainActor.run {
                self.base = url
                self.load()
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

    func load() {
        guard let r = request("api/quota") else { return }
        Task {
            guard let (data, _) = try? await URLSession.shared.data(for: r),
                  let snap = try? JSONDecoder().decode(Snapshot.self, from: data) else { return }
            var insights: [String: [Insight]] = [:]
            if let ri = request("api/insights"), let (d, _) = try? await URLSession.shared.data(for: ri) {
                insights = (try? JSONDecoder().decode([String: [Insight]].self, from: d)) ?? [:]
            }
            accounts = snap.accounts ?? []
            urgentNow = urgent(accounts, insights)
            refreshedAt = date(snap.refreshedAt)
            reading = snap.refreshing ?? false
            // the first read is still under way: look again soon, not in a minute
            if (reading || refreshedAt == nil || accounts.isEmpty) && soon < 90 {
                soon += 1
                DispatchQueue.main.asyncAfter(deadline: .now() + 2) { self.load() }
            } else if !reading && !accounts.isEmpty {
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

    var snapshotted = false
    var soon = 0
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

extension Color {
    static let antGood = Color(red: 0.18, green: 0.62, blue: 0.40)
    static let antWarn = Color(red: 0.90, green: 0.56, blue: 0.08)
    static let antLow = Color(red: 0.86, green: 0.30, blue: 0.22)
}

func level(_ p: Double?) -> Color { guard let p else { return .secondary }; return p < 20 ? .antLow : p < 50 ? .antWarn : .antGood }

struct Logo: View {
    var base: URL?
    var provider: String
    var name: String
    var size: CGFloat = 24
    var body: some View {
        ZStack {
            RoundedRectangle(cornerRadius: size * 0.28).fill(Color.white)
                .overlay(RoundedRectangle(cornerRadius: size * 0.28).stroke(Color.black.opacity(0.08), lineWidth: 0.5))
            AsyncImage(url: base?.appendingPathComponent("logos/\(provider).png")) { img in
                img.resizable().scaledToFit().padding(size * 0.1)
            } placeholder: {
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
            Capsule().fill(Color.primary.opacity(0.09))
            if let p = percent { Capsule().fill(level(p)).frame(width: max(4, 52 * p / 100)) }
        }
        .frame(width: 52, height: 5)
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
        .background(Capsule().fill(color.opacity(0.15)))
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
            Text(text.uppercased()).font(.system(size: 10.5, weight: .bold)).kerning(0.6)
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
                Logo(base: model.base, provider: account.provider, name: account.name)
                VStack(alignment: .leading, spacing: 1) {
                    Text(account.title).font(.system(size: 13, weight: .medium)).lineLimit(1)
                    if !detail.isEmpty { Text(detail).font(.system(size: 11)).foregroundStyle(.secondary).lineLimit(1) }
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
                    if model.base == nil {
                        VStack(spacing: 8) {
                            ProgressView().controlSize(.small)
                            Text("Ant is starting…").font(.system(size: 12)).foregroundStyle(.secondary)
                        }
                        .padding(36)
                    } else if model.accounts.isEmpty {
                        VStack(spacing: 8) {
                            if model.reading || model.refreshedAt == nil { ProgressView().controlSize(.small) }
                            Text(model.reading || model.refreshedAt == nil ? "Reading your accounts…" : "No accounts yet. Open Ant to add one.")
                                .font(.system(size: 12)).foregroundStyle(.secondary)
                        }
                        .padding(36)
                    } else {
                        useFirst
                        needsYou
                        everyAccount
                    }
                }
                .padding(.bottom, 10)
            }
            Divider().opacity(0.6)
            footer
        }
        .frame(width: 370)
        .onAppear { model.load() } // fresh each time it opens
    }

    @ViewBuilder func scroll<C: View>(@ViewBuilder _ c: () -> C) -> some View {
        if picture { c() } else { ScrollView { c() }.frame(maxHeight: 480) }
    }

    // icon is a header or footer control, drawn plain in a picture
    @ViewBuilder func control(_ systemName: String, _ help: String, _ action: @escaping () -> Void) -> some View {
        let img = Image(systemName: systemName).font(.system(size: 13, weight: .semibold))
        if picture { img.foregroundStyle(.secondary) } else { Button(action: action) { img }.buttonStyle(.borderless).help(help) }
    }

    var header: some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack(spacing: 9) {
                if let img = Bundle.main.image(forResource: "AntTemplate") {
                    Image(nsImage: img).renderingMode(.template).resizable().frame(width: 24, height: 24)
                }
                VStack(alignment: .leading, spacing: 0) {
                    Text("Ant").font(.system(size: 15, weight: .semibold))
                    Text(model.reading ? "Reading your accounts…" : model.refreshedAt.map { "Updated \(ago($0))" } ?? " ")
                        .font(.system(size: 11)).foregroundStyle(.secondary)
                }
                Spacer()
                control("arrow.clockwise", "Read every account again") { model.refresh() }
                control("arrow.up.forward.app", "Open Ant") { model.open() }
            }
            if model.base != nil {
                HStack(spacing: 6) {
                    if !model.urgentNow.isEmpty { Chip(icon: "hourglass", text: "\(model.urgentNow.count) use first", color: .antWarn) }
                    if !model.needsYou.isEmpty { Chip(icon: "exclamationmark.triangle.fill", text: "\(model.needsYou.count) need you", color: .antLow) }
                    if !model.prepaid.isEmpty { Chip(icon: "creditcard.fill", text: model.prepaid, color: .antGood) }
                    if model.urgentNow.isEmpty && model.needsYou.isEmpty { Chip(icon: "checkmark.circle.fill", text: "All good", color: .antGood) }
                }
            }
        }
        .padding(16)
    }

    @ViewBuilder var useFirst: some View {
        if !model.urgentNow.isEmpty {
            SectionTitle(icon: "hourglass", text: "Use first", color: .antWarn)
            ForEach(model.urgentNow.prefix(6)) { u in
                Row(model: model, account: u.account, detail: u.window.name, percent: percentLeft(u.window)) {
                    VStack(alignment: .trailing, spacing: 1) {
                        Text(leftText(u.window)).font(.system(size: 12.5, weight: .semibold)).monospacedDigit()
                        Text(until(u.at)).font(.system(size: 10.5, weight: .semibold)).foregroundStyle(Color.antWarn)
                    }
                    .frame(width: 88, alignment: .trailing)
                }
            }
            if model.urgentNow.count > 6 {
                Text("\(model.urgentNow.count - 6) more in Ant").font(.system(size: 11)).foregroundStyle(.secondary)
                    .frame(maxWidth: .infinity, alignment: .leading).padding(.horizontal, 16).padding(.top, 2)
            }
        }
    }

    @ViewBuilder var needsYou: some View {
        if !model.needsYou.isEmpty {
            SectionTitle(icon: "exclamationmark.triangle.fill", text: "Needs you", color: .antLow)
            ForEach(model.needsYou) { a in
                Row(model: model, account: a, detail: a.status == "signed_out" ? "Signed out · sign in again" : (a.message ?? "Couldn't read"), percent: nil, showBar: false) {
                    Image(systemName: a.status == "signed_out" ? "key.slash.fill" : "wifi.exclamationmark")
                        .foregroundStyle(Color.antLow).font(.system(size: 12, weight: .semibold))
                }
            }
        }
    }

    // every account, by kind, each with the one value that matters
    @ViewBuilder var everyAccount: some View {
        ForEach([("subscription", "Subscriptions", "repeat"), ("free", "Free", "gift.fill"), ("api", "Balances", "creditcard.fill")], id: \.0) { kind, title, icon in
            let list = model.ok.filter { ($0.kind ?? "api") == kind }
            if !list.isEmpty {
                SectionTitle(icon: icon, text: "\(title) · \(list.count)", color: .secondary)
                ForEach(list) { a in accountRow(a) }
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
            .foregroundStyle(quiet ? Color.secondary : Color.primary).lineLimit(1)
            .frame(width: 88, alignment: .trailing)
    }

    var footer: some View {
        HStack(spacing: 10) {
            if picture {
                Label("Open Ant", systemImage: "arrow.up.forward.app").font(.system(size: 12, weight: .medium)).foregroundStyle(.white)
                    .padding(.horizontal, 10).padding(.vertical, 4).background(RoundedRectangle(cornerRadius: 6).fill(Color.accentColor))
                Spacer()
                Text("Open at login").font(.system(size: 11)).foregroundStyle(.secondary)
                Capsule().fill(model.openAtLogin ? Color.accentColor : Color.primary.opacity(0.15)).frame(width: 26, height: 15)
                    .overlay(Circle().fill(.white).padding(2), alignment: model.openAtLogin ? .trailing : .leading)
                Image(systemName: "power").font(.system(size: 12, weight: .semibold)).foregroundStyle(.secondary)
            } else {
                Button { model.open() } label: { Label("Open Ant", systemImage: "arrow.up.forward.app") }
                    .buttonStyle(.borderedProminent).controlSize(.small)
                Spacer()
                Toggle(isOn: Binding(get: { model.openAtLogin }, set: { model.setOpenAtLogin($0) })) {
                    Text("Open at login").font(.system(size: 11)).foregroundStyle(.secondary)
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
