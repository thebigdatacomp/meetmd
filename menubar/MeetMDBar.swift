// MeetMD menu-bar app: a macOS status-bar client for the local bridge.
//
// - Shows recording state in the top bar (🔴 gravando / ⏸ pausado / 🎙 pronto / ⚠︎ offline).
// - When the bridge detects a Meet (ask mode), pops up asking whether to record.
// - Menu: Iniciar · Pausar/Retomar · Parar · Abrir pasta dos arquivos · Sair.
//
// Build: swiftc -O MeetMDBar.swift -o meetmd-bar -framework Cocoa
// Run:   ./meetmd-bar   (lives in the menu bar; no Dock icon)

import Cocoa

private enum Bridge {
    static let base = "http://127.0.0.1:8765"
    static let pollInterval: TimeInterval = 2
    static let requestTimeout: TimeInterval = 600 // stop triggers transcription
}

private enum Glyph {
    static let recording = "🔴"
    static let paused = "⏸"
    static let idle = "🎙"
    static let offline = "⚠︎"
}

private enum State: String {
    case idle, recording, paused
}

final class AppController: NSObject, NSApplicationDelegate {
    private let statusItem = NSStatusBar.system.statusItem(withLength: NSStatusItem.variableLength)
    private var timer: Timer?

    private var online = false
    private var state: State = .idle
    private var sessionID: String?
    private var title = ""
    private var outputRoot = ""
    private var detectedCode: String?
    private var detectedTitle = ""

    private var dismissedCode: String? // meeting the user declined to record
    private var promptShowing = false
    private var triedLaunch = false

    func applicationDidFinishLaunching(_ notification: Notification) {
        NSApp.setActivationPolicy(.accessory)
        statusItem.button?.title = Glyph.offline
        rebuildMenu()
        timer = Timer.scheduledTimer(withTimeInterval: Bridge.pollInterval, repeats: true) { [weak self] _ in
            self?.poll()
        }
        poll()
    }

    // --- polling ------------------------------------------------------------

    private func poll() {
        request("GET", "/status") { [weak self] data, ok in
            guard let self = self else { return }
            self.online = ok
            if ok, let data = data,
               let obj = (try? JSONSerialization.jsonObject(with: data)) as? [String: Any] {
                self.apply(obj)
            } else {
                self.state = .idle
                self.ensureBridge()
            }
            DispatchQueue.main.async {
                self.updateIcon()
                self.rebuildMenu()
                self.maybePrompt()
            }
        }
    }

    private func apply(_ obj: [String: Any]) {
        state = State(rawValue: obj["state"] as? String ?? "idle") ?? .idle
        outputRoot = obj["outputRoot"] as? String ?? ""

        if let meeting = obj["meeting"] as? [String: Any] {
            sessionID = meeting["ID"] as? String
            title = meeting["Title"] as? String ?? ""
        } else {
            sessionID = nil
            title = ""
        }

        if let detected = obj["detected"] as? [String: Any] {
            detectedCode = detected["code"] as? String
            detectedTitle = detected["title"] as? String ?? ""
        } else {
            detectedCode = nil
            detectedTitle = ""
            dismissedCode = nil // meeting gone → allow prompting for the next one
        }
    }

    private func updateIcon() {
        let glyph: String
        switch (online, state) {
        case (false, _): glyph = Glyph.offline
        case (_, .recording): glyph = Glyph.recording
        case (_, .paused): glyph = Glyph.paused
        default: glyph = Glyph.idle
        }
        statusItem.button?.title = glyph
    }

    // --- prompt on detection ------------------------------------------------

    private func maybePrompt() {
        guard online, state == .idle, !promptShowing,
              let code = detectedCode, code != dismissedCode else { return }

        promptShowing = true
        NSApp.activate(ignoringOtherApps: true)
        let alert = NSAlert()
        alert.messageText = "Reunião detectada"
        alert.informativeText = detectedTitle.isEmpty
            ? "Começar a gravar esta reunião?"
            : "Começar a gravar “\(detectedTitle)”?"
        alert.addButton(withTitle: "Gravar")
        alert.addButton(withTitle: "Agora não")
        let response = alert.runModal()
        promptShowing = false

        if response == .alertFirstButtonReturn {
            startMeeting(title: detectedTitle)
        } else {
            dismissedCode = code
        }
    }

    // --- actions ------------------------------------------------------------

    @objc private func startManual() {
        request("POST", "/sessions/start", body: ["title": "Gravação manual", "platform": "manual"]) { [weak self] _, _ in
            self?.poll()
        }
    }

    private func startMeeting(title: String) {
        request("POST", "/sessions/start", body: ["title": title, "platform": "google-meet"]) { [weak self] _, _ in
            self?.poll()
        }
    }

    @objc private func pause() { post("/sessions/\(sessionID ?? "")/pause") }
    @objc private func resume() { post("/sessions/\(sessionID ?? "")/resume") }
    @objc private func stop() { post("/sessions/\(sessionID ?? "")/stop") }

    private func post(_ path: String) {
        guard sessionID != nil else { return }
        request("POST", path) { [weak self] _, _ in self?.poll() }
    }

    @objc private func openFolder() {
        guard !outputRoot.isEmpty else { return }
        NSWorkspace.shared.open(URL(fileURLWithPath: outputRoot))
    }

    @objc private func openPanel() {
        if let url = URL(string: Bridge.base) { NSWorkspace.shared.open(url) }
    }

    @objc private func quit() { NSApp.terminate(nil) }

    // --- menu ---------------------------------------------------------------

    private func rebuildMenu() {
        let menu = NSMenu()
        menu.addItem(disabled(headerText()))
        menu.addItem(.separator())

        if online {
            switch state {
            case .idle:
                menu.addItem(item("Iniciar gravação", #selector(startManual), "r"))
            case .recording:
                menu.addItem(item("Pausar", #selector(pause), "p"))
                menu.addItem(item("Parar e salvar", #selector(stop), "s"))
            case .paused:
                menu.addItem(item("Retomar", #selector(resume), "p"))
                menu.addItem(item("Parar e salvar", #selector(stop), "s"))
            }
            menu.addItem(item("Abrir pasta dos arquivos", #selector(openFolder), "f"))
            menu.addItem(item("Abrir painel…", #selector(openPanel), "o"))
        }
        menu.addItem(.separator())
        menu.addItem(item("Sair", #selector(quit), "q"))
        statusItem.menu = menu
    }

    private func headerText() -> String {
        if !online { return "Bridge offline" }
        switch state {
        case .recording: return "Gravando: " + displayTitle()
        case .paused: return "Pausado: " + displayTitle()
        case .idle: return "Pronto"
        }
    }

    private func displayTitle() -> String {
        title.isEmpty ? "sem título" : title
    }

    private func item(_ title: String, _ selector: Selector, _ key: String) -> NSMenuItem {
        let item = NSMenuItem(title: title, action: selector, keyEquivalent: key)
        item.target = self
        return item
    }

    private func disabled(_ title: String) -> NSMenuItem {
        let item = NSMenuItem(title: title, action: nil, keyEquivalent: "")
        item.isEnabled = false
        return item
    }

    // --- bridge launch ------------------------------------------------------

    private func ensureBridge() {
        guard !triedLaunch, let bin = findBridgeBinary() else { return }
        triedLaunch = true
        let proc = Process()
        proc.executableURL = bin
        proc.arguments = ["serve"]
        try? proc.run() // detached; outlives this app
    }

    private func findBridgeBinary() -> URL? {
        let fm = FileManager.default
        var candidates: [String] = []
        if let env = ProcessInfo.processInfo.environment["MEETMD_BIN"] { candidates.append(env) }
        let selfDir = URL(fileURLWithPath: CommandLine.arguments[0]).deletingLastPathComponent()
        candidates.append(selfDir.appendingPathComponent("meetmd").path)
        candidates.append("/usr/local/bin/meetmd")
        candidates.append("/opt/homebrew/bin/meetmd")
        candidates.append((NSHomeDirectory() as NSString).appendingPathComponent("go/bin/meetmd"))
        return candidates.first(where: { fm.isExecutableFile(atPath: $0) }).map { URL(fileURLWithPath: $0) }
    }

    // --- HTTP ---------------------------------------------------------------

    private func request(_ method: String, _ path: String, body: [String: Any]? = nil,
                         done: @escaping (Data?, Bool) -> Void) {
        guard let url = URL(string: Bridge.base + path) else { done(nil, false); return }
        var req = URLRequest(url: url)
        req.httpMethod = method
        req.timeoutInterval = Bridge.requestTimeout
        if let body = body {
            req.setValue("application/json", forHTTPHeaderField: "Content-Type")
            req.httpBody = try? JSONSerialization.data(withJSONObject: body)
        }
        URLSession.shared.dataTask(with: req) { data, resp, err in
            let code = (resp as? HTTPURLResponse)?.statusCode ?? 0
            done(data, err == nil && (200..<300).contains(code))
        }.resume()
    }
}

let app = NSApplication.shared
let controller = AppController()
app.delegate = controller
app.run()
