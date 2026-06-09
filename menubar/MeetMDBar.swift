// MeetMD menu-bar app: a macOS status-bar client for the local bridge.
//
// - Shows state via the Claude mascot icon (orange idle, +headset recording,
//   bars paused, speech bubble processing, gray offline).
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

private enum State: String {
    case idle, recording, paused, processing
}

// Claude mascot (pixel-art) menu-bar icon, drawn per state (colour-coded).
private enum ClaudeIcon {
    private static let cols = 16
    private static let sprite = [
        "....XXXXXXXX....",
        "..XXXXXXXXXXXX..",
        ".XXXXXXXXXXXXXX.",
        ".XXXXXXXXXXXXXX.",
        "XXXXXXXXXXXXXXXX",
        "XXXXXXXXXXXXXXXX",
        "XXXeXXXXXXXXeXXX",
        "XXXXeXXXXXXeXXXX",
        "XXXeXXXXXXXXeXXX",
        "XXXXXXXXXXXXXXXX",
        "XXXXXXXXXXXXXXXX",
        ".XXXXXXXXXXXXXX.",
        ".XXXXXXXXXXXXXX.",
        "XXX.XX.XXXX.XX.X",
        "XX..XX..XX..XX..",
    ]
    private static let orange = NSColor(red: 0.85, green: 0.46, blue: 0.34, alpha: 1)
    private static let gray = NSColor(white: 0.55, alpha: 1)
    private static let blue = NSColor(red: 0.18, green: 0.37, blue: 0.55, alpha: 1)
    private static let dark = NSColor(white: 0.12, alpha: 1)

    static func image(for state: State, online: Bool) -> NSImage {
        let img = NSImage(size: NSSize(width: 22, height: 20), flipped: false) { _ in
            let area = NSRect(x: 1, y: 1, width: 20, height: 18) // top room for the bubble
            let body = online ? orange : gray
            let showEyes = !(online && state == .paused) // paused: bars only, no eyes
            creature(in: area, body: body, eyes: showEyes)
            if online {
                switch state {
                case .recording: headphones(in: area)
                case .paused: pauseBars(in: area)
                case .processing: bubble(in: area)
                case .idle: break
                }
            }
            return true
        }
        img.isTemplate = false
        return img
    }

    private static func cell(_ r: NSRect) -> CGFloat { r.width / CGFloat(cols) }

    private static func px(_ x: Int, _ y: Int, _ r: NSRect) -> NSRect {
        let c = cell(r)
        return NSRect(x: r.minX + CGFloat(x) * c, y: r.minY + r.height - CGFloat(y + 1) * c, width: c, height: c)
    }

    private static func creature(in r: NSRect, body: NSColor, eyes: Bool) {
        for (y, line) in sprite.enumerated() {
            for (x, ch) in line.enumerated() {
                if ch == "X" {
                    body.setFill(); px(x, y, r).fill()
                } else if ch == "e", eyes {
                    dark.setFill(); px(x, y, r).fill()
                }
            }
        }
    }

    private static func headphones(in r: NSRect) {
        NSColor.white.setFill()
        for x in 4...11 { px(x, 0, r).fill() }
        for x in 2...13 { px(x, 1, r).fill() }
        blue.setFill()
        for y in 5...10 {
            px(0, y, r).fill(); px(1, y, r).fill(); px(14, y, r).fill(); px(15, y, r).fill()
        }
        for y in 9...11 { px(0, y, r).fill(); px(15, y, r).fill() }
    }

    private static func pauseBars(in r: NSRect) {
        dark.setFill()
        for y in 5...12 { px(6, y, r).fill(); px(7, y, r).fill(); px(9, y, r).fill(); px(10, y, r).fill() }
    }

    private static func bubble(in r: NSRect) {
        let c = cell(r)
        let bx = r.minX + c * 9.5, by = r.minY + r.height - c * 5.5
        let box = NSRect(x: bx, y: by, width: c * 6.5, height: c * 4)
        let path = NSBezierPath(roundedRect: box, xRadius: c * 1.2, yRadius: c * 1.2)
        let tail = NSBezierPath()
        tail.move(to: NSPoint(x: bx + c * 1.0, y: by + c * 0.4))
        tail.line(to: NSPoint(x: bx - c * 0.4, y: by - c * 1.2))
        tail.line(to: NSPoint(x: bx + c * 2.2, y: by + c * 0.4))
        tail.close()
        NSColor.white.setFill(); path.fill(); tail.fill()
        dark.setStroke(); path.lineWidth = c * 0.6; path.stroke(); tail.lineWidth = c * 0.6; tail.stroke()
        dark.setFill()
        for i in 0..<3 {
            NSRect(x: box.minX + c * 1.2 + CGFloat(i) * c * 1.6, y: box.midY - c * 0.5, width: c, height: c).fill()
        }
    }
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
    private var lastProject = ""       // remembered across prompts
    private var promptShowing = false
    private var triedLaunch = false
    private var settingsController: SettingsWindowController?

    func applicationDidFinishLaunching(_ notification: Notification) {
        NSApp.setActivationPolicy(.accessory)
        statusItem.button?.image = ClaudeIcon.image(for: .idle, online: false)
        statusItem.button?.imagePosition = .imageOnly
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
        let button = statusItem.button
        button?.title = ""
        button?.image = ClaudeIcon.image(for: state, online: online)
        button?.imagePosition = .imageOnly
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

        let projectField = textField(placeholder: "Projeto (opcional)", value: lastProject)
        alert.accessoryView = projectField
        alert.window.initialFirstResponder = projectField

        let response = alert.runModal()
        promptShowing = false

        if response == .alertFirstButtonReturn {
            lastProject = projectField.stringValue
            startSession(title: detectedTitle, project: projectField.stringValue,
                         platform: "google-meet", failCode: code)
        } else {
            dismissedCode = code
        }
    }

    // --- actions ------------------------------------------------------------

    @objc private func startManual() {
        guard let input = promptStartInput() else { return }
        let title = input.title.isEmpty ? "Gravação manual" : input.title
        startSession(title: title, project: input.project, platform: "manual")
    }

    // startSession posts a start request and surfaces any error. failCode, when
    // set, marks a detected meeting as dismissed so a failure doesn't re-prompt.
    private func startSession(title: String, project: String, platform: String, failCode: String? = nil) {
        request("POST", "/sessions/start",
                body: ["title": title, "project": project, "platform": platform]) { [weak self] data, ok in
            guard let self = self else { return }
            if !ok {
                if let code = failCode { self.dismissedCode = code }
                let message = self.errorMessage(from: data) ?? "Falha ao iniciar a gravação."
                DispatchQueue.main.async { self.showError(message) }
            }
            self.poll()
        }
    }

    private func errorMessage(from data: Data?) -> String? {
        guard let data = data,
              let obj = (try? JSONSerialization.jsonObject(with: data)) as? [String: Any] else { return nil }
        return obj["message"] as? String
    }

    private func showError(_ message: String) {
        NSApp.activate(ignoringOtherApps: true)
        let alert = NSAlert()
        alert.alertStyle = .warning
        alert.messageText = "MeetMD"
        alert.informativeText = message
        alert.addButton(withTitle: "OK")
        alert.runModal()
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

    @objc private func openSettings() {
        if settingsController == nil {
            settingsController = SettingsWindowController(base: Bridge.base)
        }
        settingsController?.show()
    }

    @objc private func openAbout() {
        NSApp.activate(ignoringOtherApps: true)
        let alert = NSAlert()
        alert.messageText = "MeetMD"
        alert.informativeText = """
        Captura suas reuniões e gera a transcrição em Markdown, organizada por \
        projeto e pronta para o Claude processar.

        A transcrição é feita localmente (whisper.cpp) — o áudio não sai da sua \
        máquina. Reuniões do Google Meet no Safari são detectadas \
        automaticamente; ou grave manualmente pelo menu.
        """
        alert.icon = NSImage(systemSymbolName: "mic.circle.fill", accessibilityDescription: "MeetMD")
        alert.addButton(withTitle: "OK")
        alert.runModal()
    }

    @objc private func quit() { NSApp.terminate(nil) }

    // promptStartInput asks for a title and project before a manual recording.
    // Returns nil if the user cancels.
    private func promptStartInput() -> (title: String, project: String)? {
        NSApp.activate(ignoringOtherApps: true)
        let alert = NSAlert()
        alert.messageText = "Iniciar gravação"
        alert.informativeText = "Título e projeto (opcionais)."
        alert.addButton(withTitle: "Iniciar")
        alert.addButton(withTitle: "Cancelar")

        let width: CGFloat = 240
        let titleField = textField(placeholder: "Título", value: "")
        titleField.frame = NSRect(x: 0, y: 30, width: width, height: 24)
        let projectField = textField(placeholder: "Projeto", value: lastProject)
        projectField.frame = NSRect(x: 0, y: 0, width: width, height: 24)

        let container = NSView(frame: NSRect(x: 0, y: 0, width: width, height: 54))
        container.addSubview(titleField)
        container.addSubview(projectField)
        alert.accessoryView = container
        alert.window.initialFirstResponder = titleField

        guard alert.runModal() == .alertFirstButtonReturn else { return nil }
        lastProject = projectField.stringValue
        return (titleField.stringValue, projectField.stringValue)
    }

    private func textField(placeholder: String, value: String) -> NSTextField {
        let field = NSTextField(frame: NSRect(x: 0, y: 0, width: 240, height: 24))
        field.placeholderString = placeholder
        field.stringValue = value
        return field
    }

    // --- menu ---------------------------------------------------------------

    private func rebuildMenu() {
        let menu = NSMenu()
        menu.addItem(disabled(headerText()))
        menu.addItem(.separator())

        if online {
            switch state {
            case .idle:
                menu.addItem(item("Iniciar gravação", #selector(startManual), "r", "record.circle"))
            case .recording:
                menu.addItem(item("Pausar", #selector(pause), "p", "pause.circle"))
                menu.addItem(item("Parar e salvar", #selector(stop), "s", "stop.circle"))
            case .paused:
                menu.addItem(item("Retomar", #selector(resume), "p", "play.circle"))
                menu.addItem(item("Parar e salvar", #selector(stop), "s", "stop.circle"))
            case .processing:
                break // transcrevendo — sem ações
            }
            menu.addItem(item("Abrir pasta dos arquivos", #selector(openFolder), "f", "folder"))
            menu.addItem(item("Configurações…", #selector(openSettings), ",", "gearshape"))
        }
        menu.addItem(.separator())
        menu.addItem(item("Sobre o MeetMD", #selector(openAbout), "", "info.circle"))
        menu.addItem(item("Sair", #selector(quit), "q", "power"))
        statusItem.menu = menu
    }

    private func headerText() -> String {
        if !online { return "Bridge offline" }
        switch state {
        case .recording: return "Gravando: " + displayTitle()
        case .paused: return "Pausado: " + displayTitle()
        case .processing: return "Processando…"
        case .idle: return "Pronto"
        }
    }

    private func displayTitle() -> String {
        title.isEmpty ? "sem título" : title
    }

    private func item(_ title: String, _ selector: Selector, _ key: String, _ symbol: String) -> NSMenuItem {
        let item = NSMenuItem(title: title, action: selector, keyEquivalent: key)
        item.target = self
        item.image = NSImage(systemSymbolName: symbol, accessibilityDescription: title)
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

// MARK: - Settings window

// A native settings window backed by the bridge's GET/PUT /settings. Only
// user-facing options are shown; internal paths (model, helper, VAD) are hidden.
final class SettingsWindowController: NSWindowController {
    private let base: String

    private let outputField = NSTextField()
    private let projectField = NSTextField()
    private let languagePopup = NSPopUpButton()
    private let autoDetectPopup = NSPopUpButton()
    private let micCheck = NSButton(checkboxWithTitle: "Incluir minha voz (microfone)", target: nil, action: nil)
    private let deleteCheck = NSButton(checkboxWithTitle: "Apagar áudio bruto após transcrever", target: nil, action: nil)
    private let hint = NSTextField(labelWithString: "")

    // (display title, config value) pairs.
    private let languages = [("Automático", "auto"), ("Português", "pt"), ("Espanhol", "es"), ("Inglês", "en")]
    private let autoModes = [("Perguntar antes de gravar", "ask"), ("Gravar automaticamente", "auto"), ("Desligada", "off")]

    init(base: String) {
        self.base = base
        let window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 470, height: 360),
                              styleMask: [.titled, .closable], backing: .buffered, defer: false)
        window.title = "MeetMD — Configurações"
        super.init(window: window)
        buildUI()
    }

    required init?(coder: NSCoder) { fatalError("not used") }

    func show() {
        load()
        NSApp.activate(ignoringOtherApps: true)
        window?.center()
        showWindow(nil)
        window?.makeKeyAndOrderFront(nil)
    }

    private func buildUI() {
        languagePopup.addItems(withTitles: languages.map { $0.0 })
        autoDetectPopup.addItems(withTitles: autoModes.map { $0.0 })
        outputField.placeholderString = "/Users/…/meetings"
        projectField.placeholderString = "ex.: bora (opcional)"

        let choose = NSButton(title: "Escolher…", target: self, action: #selector(chooseFolder))
        choose.bezelStyle = .rounded
        let outputRow = NSStackView(views: [outputField, choose])
        outputRow.orientation = .horizontal
        outputRow.spacing = 6

        let form = NSStackView(views: [
            labeled("Pasta de saída", outputRow),
            labeled("Idioma", languagePopup),
            labeled("Projeto padrão", projectField),
            labeled("Detecção automática", autoDetectPopup),
            labeled("", micCheck),
            labeled("", deleteCheck),
        ])
        form.orientation = .vertical
        form.alignment = .leading
        form.spacing = 10
        form.translatesAutoresizingMaskIntoConstraints = false

        hint.textColor = .systemRed
        hint.font = .systemFont(ofSize: 11)

        let save = NSButton(title: "Salvar", target: self, action: #selector(saveSettings))
        save.bezelStyle = .rounded
        save.keyEquivalent = "\r"
        let cancel = NSButton(title: "Cancelar", target: self, action: #selector(cancel))
        cancel.bezelStyle = .rounded
        let spacer = NSView()
        spacer.setContentHuggingPriority(.defaultLow, for: .horizontal)
        let buttons = NSStackView(views: [hint, spacer, cancel, save])
        buttons.orientation = .horizontal
        buttons.spacing = 8
        buttons.translatesAutoresizingMaskIntoConstraints = false

        let content = NSView()
        content.addSubview(form)
        content.addSubview(buttons)
        window?.contentView = content

        NSLayoutConstraint.activate([
            form.topAnchor.constraint(equalTo: content.topAnchor, constant: 18),
            form.leadingAnchor.constraint(equalTo: content.leadingAnchor, constant: 18),
            form.trailingAnchor.constraint(equalTo: content.trailingAnchor, constant: -18),
            outputField.widthAnchor.constraint(greaterThanOrEqualToConstant: 250),
            buttons.topAnchor.constraint(equalTo: form.bottomAnchor, constant: 18),
            buttons.leadingAnchor.constraint(equalTo: content.leadingAnchor, constant: 18),
            buttons.trailingAnchor.constraint(equalTo: content.trailingAnchor, constant: -18),
        ])
    }

    private func labeled(_ title: String, _ control: NSView) -> NSView {
        let label = NSTextField(labelWithString: title)
        label.alignment = .right
        label.widthAnchor.constraint(equalToConstant: 140).isActive = true
        let row = NSStackView(views: [label, control])
        row.orientation = .horizontal
        row.spacing = 8
        return row
    }

    // --- load / save --------------------------------------------------------

    private func load() {
        request("GET", nil) { [weak self] obj in
            guard let self = self, let o = obj else {
                self?.hint.stringValue = "Bridge offline"
                return
            }
            self.outputField.stringValue = o["outputRoot"] as? String ?? ""
            self.projectField.stringValue = o["defaultProject"] as? String ?? ""
            self.select(self.languagePopup, self.languages, value: o["language"] as? String ?? "auto")
            self.select(self.autoDetectPopup, self.autoModes, value: o["autoDetect"] as? String ?? "ask")
            self.micCheck.state = (o["captureMic"] as? Bool ?? true) ? .on : .off
            self.deleteCheck.state = (o["deleteAudio"] as? Bool ?? true) ? .on : .off
            self.hint.stringValue = ""
        }
    }

    @objc private func chooseFolder() {
        let panel = NSOpenPanel()
        panel.canChooseDirectories = true
        panel.canChooseFiles = false
        panel.canCreateDirectories = true
        if panel.runModal() == .OK, let url = panel.url {
            outputField.stringValue = url.path
        }
    }

    @objc private func saveSettings() {
        let body: [String: Any] = [
            "outputRoot": outputField.stringValue,
            "language": value(languagePopup, languages),
            "defaultProject": projectField.stringValue,
            "autoDetect": value(autoDetectPopup, autoModes),
            "captureMic": micCheck.state == .on,
            "deleteAudio": deleteCheck.state == .on,
        ]
        request("PUT", body) { [weak self] obj in
            if obj != nil {
                self?.window?.close()
            } else {
                self?.hint.stringValue = "Falha ao salvar (pasta inválida ou bridge offline)"
            }
        }
    }

    @objc private func cancel() { window?.close() }

    // --- helpers ------------------------------------------------------------

    private func select(_ popup: NSPopUpButton, _ items: [(String, String)], value: String) {
        if let i = items.firstIndex(where: { $0.1 == value }) { popup.selectItem(at: i) }
    }

    private func value(_ popup: NSPopUpButton, _ items: [(String, String)]) -> String {
        let i = popup.indexOfSelectedItem
        return (i >= 0 && i < items.count) ? items[i].1 : items.first?.1 ?? ""
    }

    private func request(_ method: String, _ body: [String: Any]?, done: @escaping ([String: Any]?) -> Void) {
        guard let url = URL(string: base + "/settings") else { done(nil); return }
        var req = URLRequest(url: url)
        req.httpMethod = method
        req.timeoutInterval = 10
        if let body = body {
            req.setValue("application/json", forHTTPHeaderField: "Content-Type")
            req.httpBody = try? JSONSerialization.data(withJSONObject: body)
        }
        URLSession.shared.dataTask(with: req) { data, resp, err in
            let ok = (resp as? HTTPURLResponse).map { (200..<300).contains($0.statusCode) } ?? false
            let obj = data.flatMap { try? JSONSerialization.jsonObject(with: $0) } as? [String: Any]
            DispatchQueue.main.async { done(err == nil && ok ? obj : nil) }
        }.resume()
    }
}

let app = NSApplication.shared
let controller = AppController()
app.delegate = controller
app.run()
