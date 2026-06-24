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
import ServiceManagement

private enum Bridge {
    static let base = "http://127.0.0.1:8765"
    static let pollInterval: TimeInterval = 2
    static let requestTimeout: TimeInterval = 600 // stop triggers transcription
}

private enum State: String {
    case idle, recording, paused, processing
}

// Session kind reported by the bridge on /status (matches Go's session.Kind).
private let kindNote = "note"

// UI language for menus, prompts and labels. Defaults to the OS preference and
// is overridden by the bridge's resolved `uiLanguage` (which honors the
// ui_language config). English is the fallback for anything non-Portuguese.
private enum UILang { case pt, en }

private var gUILang: UILang =
    (Locale.preferredLanguages.first ?? "en").lowercased().hasPrefix("pt") ? .pt : .en

// tr picks the string for the current UI language — translations live inline at
// each call site, so there's no separate catalog to keep in sync.
private func tr(_ pt: String, _ en: String) -> String { gUILang == .pt ? pt : en }

// Claude mascot (pixel-art) menu-bar icon, drawn per state (colour-coded).
private enum ClaudeIcon {
    private static let cols = 16
    private static let sprite = [
        "..XXXXXXXXXXXX..",
        ".XXXXXXXXXXXXXX.",
        ".XXXXXXXXXXXXXX.",
        ".XXXXXXXXXXXXXX.",
        ".XXXXXXXXXXXXXX.",
        ".XXXXXXXXXXXXXX.",
        "XXXeXXXXXXXXeXXX",
        "XXXXeXXXXXXeXXXX",
        ".XXeXXXXXXXXeXX.",
        ".XXXXXXXXXXXXXX.",
        ".XXXXXXXXXXXXXX.",
        "..XX.XX..XX.XX..",
        "..XX.XX..XX.XX..",
    ]
    private static let orange = NSColor(red: 0.85, green: 0.46, blue: 0.34, alpha: 1)
    private static let gray = NSColor(white: 0.55, alpha: 1)
    private static let dark = NSColor(white: 0.12, alpha: 1)

    static func image(for state: State, online: Bool, asleep: Bool = false, height: CGFloat = 20) -> NSImage {
        let s = height / 20 // scale (base canvas is 22×20)
        let img = NSImage(size: NSSize(width: 22 * s, height: height), flipped: false) { _ in
            let area = NSRect(x: 1 * s, y: 1 * s, width: 20 * s, height: 16.25 * s) // square cells; room for the bubble
            if asleep {
                sleeping(in: area)
                return true
            }
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

    // appIcon renders the colored brand icon for the .app bundle: the headphone
    // mascot centered on a warm dark rounded square.
    static func appIcon(px: CGFloat) -> NSImage {
        let img = NSImage(size: NSSize(width: px, height: px), flipped: false) { _ in
            let pad = px * 0.05
            let bgRect = NSRect(x: pad, y: pad, width: px - 2 * pad, height: px - 2 * pad)
            let bg = NSBezierPath(roundedRect: bgRect, xRadius: px * 0.22, yRadius: px * 0.22)
            NSColor(red: 0.16, green: 0.14, blue: 0.13, alpha: 1).setFill()
            bg.fill()

            let w = px * 0.60
            let c = w / CGFloat(cols)
            let area = NSRect(x: (px - w) / 2, y: (px - 13 * c) / 2, width: w, height: 13 * c)
            creature(in: area, body: orange, eyes: true)
            headphones(in: area)
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

    // fillCells unites all matching cells into one path and fills once, so the
    // body renders solid (no seams between the pixel rects).
    private static func fillCells(in r: NSRect, where keep: (Character) -> Bool) {
        let p = NSBezierPath()
        for (y, line) in sprite.enumerated() {
            for (x, ch) in line.enumerated() where keep(ch) {
                p.appendRect(px(x, y, r))
            }
        }
        p.fill()
    }

    private static func creature(in r: NSRect, body: NSColor, eyes: Bool) {
        body.setFill()
        fillCells(in: r) { $0 == "X" }
        if eyes {
            dark.setFill()
            fillCells(in: r) { $0 == "e" }
        }
    }

    // Snooze: dimmed body, closed eyes (dashes instead of `> <`), and "zZz"
    // drifting up to the right.
    private static func sleeping(in r: NSRect) {
        gray.setFill()
        fillCells(in: r) { $0 == "X" }
        dark.setFill()
        let lids = NSBezierPath()
        for x in [3, 4] { lids.appendRect(px(x, 7, r)) }   // left closed eye
        for x in [11, 12] { lids.appendRect(px(x, 7, r)) } // right closed eye
        lids.fill()

        // "z z z" drifting up to the right over the head. White so it reads against
        // the gray head on both light and dark menu bars (like the recording fone).
        let c = cell(r)
        let zs: [(x: CGFloat, y: CGFloat, size: CGFloat)] = [
            (8.6, 6.0, 2.8), (10.5, 4.1, 3.6), (12.6, 1.9, 4.6),
        ]
        for z in zs {
            let attrs: [NSAttributedString.Key: Any] = [
                .font: NSFont.boldSystemFont(ofSize: c * z.size),
                .foregroundColor: NSColor.white,
            ]
            ("z" as NSString).draw(at: NSPoint(x: r.minX + c * z.x, y: r.minY + r.height - c * z.y), withAttributes: attrs)
        }
    }

    // White headphone cells: an arc band over the head connecting both ear cups.
    private static let headphoneCells: [(Int, Int)] = [
        (3, 0), (4, 0), (5, 0), (6, 0), (7, 0), (8, 0), (9, 0), (10, 0), (11, 0), (12, 0),
        (2, 1), (3, 1), (4, 1), (5, 1), (6, 1), (7, 1), (8, 1), (9, 1), (10, 1), (11, 1), (12, 1), (13, 1),
        (1, 2), (1, 3), (1, 4), (14, 2), (14, 3), (14, 4),
        (0, 5), (1, 5), (0, 6), (1, 6), (0, 7), (1, 7), (0, 8), (1, 8), (0, 9), (1, 9),
        (14, 5), (15, 5), (14, 6), (15, 6), (14, 7), (15, 7), (14, 8), (15, 8), (14, 9), (15, 9),
    ]

    private static func headphones(in r: NSRect) {
        // thin black outline (the 1-cell border around the white headphone)
        let white = Set(headphoneCells.map { "\($0.0),\($0.1)" })
        var seen = Set<String>()
        let outline = NSBezierPath()
        for (x, y) in headphoneCells {
            for dx in -1...1 {
                for dy in -1...1 where !(dx == 0 && dy == 0) {
                    let nx = x + dx, ny = y + dy, key = "\(nx),\(ny)"
                    if !white.contains(key), !seen.contains(key) {
                        seen.insert(key)
                        outline.appendRect(px(nx, ny, r))
                    }
                }
            }
        }
        dark.setFill()
        outline.fill()

        let band = NSBezierPath()
        for (x, y) in headphoneCells { band.appendRect(px(x, y, r)) }
        NSColor.white.setFill()
        band.fill()
    }

    private static func pauseBars(in r: NSRect) {
        let p = NSBezierPath()
        for y in 3...10 { for x in [6, 7, 9, 10] { p.appendRect(px(x, y, r)) } }
        dark.setFill()
        p.fill()
    }

    private static func bubble(in r: NSRect) {
        let c = cell(r)
        let bx = r.minX + c * 9.5, by = r.minY + r.height - c * 5.0
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
    private var asleep = false // snooze: bridge silenced; icon shows zZz
    private var kind = "" // "meeting" | "note" while a session is active
    private var sessionID: String?
    private var title = ""
    private var meetingProject = "" // shown when an auto-detected meeting has no title
    private var outputRoot = ""
    private var filesRoot = "" // recordings folder (holds meetings/ and notes/)
    private var detectedCode: String?
    private var detectedTitle = ""

    private var dismissedCode: String? // meeting the user declined to record
    private var lastProject = ""       // remembered across prompts
    private var promptShowing = false
    private var triedLaunch = false
    private var settingsController: SettingsWindowController?

    private var onboardingController: OnboardingWindowController?
    private static let onboardedKey = "didOnboard"

    func applicationDidFinishLaunching(_ notification: Notification) {
        NSApp.setActivationPolicy(.accessory)
        statusItem.button?.image = ClaudeIcon.image(for: .idle, online: false)
        statusItem.button?.imagePosition = .imageOnly
        rebuildMenu()
        timer = Timer.scheduledTimer(withTimeInterval: Bridge.pollInterval, repeats: true) { [weak self] _ in
            self?.poll()
        }
        poll()
        // First run: guide the user through the TCC permissions before they record.
        if !UserDefaults.standard.bool(forKey: Self.onboardedKey) {
            UserDefaults.standard.set(true, forKey: Self.onboardedKey)
            openOnboarding()
        }
    }

    // On quit, tell the bridge to finalize any recording and exit, so its capture
    // helper isn't orphaned (which would keep the macOS screen-capture indicator
    // lit). Synchronous so the bridge gets the request before this process dies.
    func applicationWillTerminate(_ notification: Notification) {
        guard let url = URL(string: Bridge.base + "/shutdown") else { return }
        var req = URLRequest(url: url)
        req.httpMethod = "POST"
        req.timeoutInterval = 2
        let sem = DispatchSemaphore(value: 0)
        URLSession.shared.dataTask(with: req) { _, _, _ in sem.signal() }.resume()
        _ = sem.wait(timeout: .now() + 2.5)
    }

    @objc private func openOnboarding() {
        if onboardingController == nil { onboardingController = OnboardingWindowController() }
        onboardingController?.show()
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
        asleep = obj["asleep"] as? Bool ?? false
        kind = obj["kind"] as? String ?? ""
        outputRoot = obj["outputRoot"] as? String ?? ""
        filesRoot = obj["filesRoot"] as? String ?? ""

        // The bridge resolves ui_language (incl. "auto") to "pt"/"en"; honor it
        // so a config override drives the menu UI too, not just the .md output.
        if let ui = obj["uiLanguage"] as? String {
            gUILang = ui == "pt" ? .pt : .en
        }

        if let meeting = obj["meeting"] as? [String: Any] {
            sessionID = meeting["ID"] as? String
            title = meeting["Title"] as? String ?? ""
            meetingProject = meeting["Project"] as? String ?? ""
        } else {
            sessionID = nil
            if state != .processing { title = ""; meetingProject = "" } // keep name while transcribing
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

    // Snooze only manifests while idle: an active recording always wins the UI
    // (so its Stop action and "recording" state stay visible and reachable).
    private var snoozing: Bool { asleep && state == .idle }

    private func updateIcon() {
        let button = statusItem.button
        button?.image = ClaudeIcon.image(for: state, online: online, asleep: snoozing)
        // While recording/transcribing, show the meeting name next to the icon —
        // but only when there's a real name, so there's no dangling separator.
        let name = recordingName()
        if online, state == .recording || state == .processing, !name.isEmpty {
            button?.title = " – " + name
            button?.imagePosition = .imageLeading
        } else {
            button?.title = ""
            button?.imagePosition = .imageOnly
        }
    }

    // recordingName is the label shown in the menu bar while active: the note
    // label, else the title, else the project — empty when none, never "untitled".
    private func recordingName() -> String {
        let raw = (state == .recording && kind == kindNote)
            ? tr("Nota de voz", "Voice note")
            : (title.isEmpty ? meetingProject : title)
        return raw.count > 30 ? String(raw.prefix(29)) + "…" : raw
    }

    // --- prompt on detection ------------------------------------------------

    private func maybePrompt() {
        guard online, !asleep, state == .idle, !promptShowing,
              let code = detectedCode, code != dismissedCode else { return }

        promptShowing = true
        NSApp.activate(ignoringOtherApps: true)
        let alert = NSAlert()
        alert.messageText = tr("Reunião detectada", "Meeting detected")
        alert.informativeText = detectedTitle.isEmpty
            ? tr("Começar a gravar esta reunião?", "Start recording this meeting?")
            : tr("Começar a gravar “\(detectedTitle)”?", "Start recording “\(detectedTitle)”?")
        alert.addButton(withTitle: tr("Gravar", "Record"))
        alert.addButton(withTitle: tr("Agora não", "Not now"))

        let projectField = textField(placeholder: tr("Projeto (opcional)", "Project (optional)"), value: lastProject)
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
        let title = input.title.isEmpty ? tr("Gravação manual", "Manual recording") : input.title
        startSession(title: title, project: input.project, platform: "manual")
    }

    // startNote begins a quick voice note: mic-only, no prompt, lands in notes/.
    @objc private func startNote() {
        request("POST", "/notes/start") { [weak self] data, ok in
            guard let self = self else { return }
            if !ok {
                let message = self.errorMessage(from: data) ?? tr("Falha ao iniciar a nota.", "Failed to start the note.")
                DispatchQueue.main.async { self.showError(message) }
            }
            self.poll()
        }
    }

    // startSession posts a start request and surfaces any error. failCode, when
    // set, marks a detected meeting as dismissed so a failure doesn't re-prompt.
    private func startSession(title: String, project: String, platform: String, failCode: String? = nil) {
        request("POST", "/sessions/start",
                body: ["title": title, "project": project, "platform": platform]) { [weak self] data, ok in
            guard let self = self else { return }
            if !ok {
                if let code = failCode { self.dismissedCode = code }
                let message = self.errorMessage(from: data) ?? tr("Falha ao iniciar a gravação.", "Failed to start recording.")
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

    private func showError(_ message: String) { showAlert(message, style: .warning) }
    private func showInfo(_ message: String) { showAlert(message, style: .informational) }

    private func showAlert(_ message: String, style: NSAlert.Style) {
        NSApp.activate(ignoringOtherApps: true)
        let alert = NSAlert()
        alert.alertStyle = style
        alert.messageText = "MeetMD"
        alert.informativeText = message
        alert.addButton(withTitle: "OK")
        alert.runModal()
    }

    @objc private func pause() { post("/sessions/\(sessionID ?? "")/pause") }
    @objc private func resume() { post("/sessions/\(sessionID ?? "")/resume") }

    // stop surfaces failures: a recording must never fail silently — if saving
    // errors, tell the user (the raw audio is kept under ~/.meetmd for recovery).
    @objc private func stop() {
        guard let id = sessionID, !id.isEmpty else { return }
        request("POST", "/sessions/\(id)/stop") { [weak self] data, ok in
            guard let self = self else { return }
            if !ok {
                let msg = self.errorMessage(from: data) ?? tr(
                    "Não foi possível salvar a gravação. O áudio bruto foi mantido em ~/.meetmd para recuperação.",
                    "Couldn't save the recording. The raw audio was kept under ~/.meetmd for recovery.")
                DispatchQueue.main.async { self.showError(msg) }
            }
            self.poll()
        }
    }

    private func post(_ path: String) {
        guard sessionID != nil else { return }
        request("POST", path) { [weak self] _, _ in self?.poll() }
    }

    @objc private func openFolder() {
        // Open the folder that holds both meetings/ and notes/ (falls back to the
        // meetings root if the bridge didn't report a common folder).
        let path = filesRoot.isEmpty ? outputRoot : filesRoot
        guard !path.isEmpty else { return }
        NSWorkspace.shared.open(URL(fileURLWithPath: path))
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
        alert.informativeText = tr("""
        Captura suas reuniões e gera a transcrição em Markdown, organizada por \
        projeto e pronta para o Claude processar.

        A transcrição é feita localmente (whisper.cpp) — o áudio não sai da sua \
        máquina. Reuniões do Google Meet no Safari são detectadas \
        automaticamente; ou grave manualmente pelo menu.

        Use também "Nova nota de voz" para ditar uma anotação rápida (só \
        microfone, sem permissão de tela) direto para a pasta de notas.
        """, """
        Captures your meetings and generates the transcript in Markdown, \
        organized by project and ready for Claude to process.

        Transcription runs locally (whisper.cpp) — the audio never leaves your \
        machine. Google Meet meetings in Safari are detected automatically; or \
        record manually from the menu.

        Use "New voice note" to dictate a quick note (mic only, no screen \
        permission) straight to your notes folder.
        """)
        alert.icon = ClaudeIcon.image(for: .recording, online: true, height: 76) // mascote gravando
        alert.addButton(withTitle: "OK")
        alert.runModal()
    }

    @objc private func quit() { NSApp.terminate(nil) }

    // promptStartInput asks for a title and project before a manual recording.
    // Returns nil if the user cancels.
    private func promptStartInput() -> (title: String, project: String)? {
        NSApp.activate(ignoringOtherApps: true)
        let alert = NSAlert()
        alert.messageText = tr("Iniciar gravação", "Start recording")
        alert.informativeText = tr("Título e projeto (opcionais).", "Title and project (optional).")
        alert.addButton(withTitle: tr("Iniciar", "Start"))
        alert.addButton(withTitle: tr("Cancelar", "Cancel"))
        // Draw the brand icon directly instead of falling back to
        // NSApp.applicationIconImage, which shows a generic placeholder until the
        // LaunchServices icon cache catches up with a freshly (re)built bundle.
        alert.icon = ClaudeIcon.appIcon(px: 64)

        let width: CGFloat = 240
        let titleField = textField(placeholder: tr("Título", "Title"), value: "")
        titleField.frame = NSRect(x: 0, y: 30, width: width, height: 24)
        let projectField = textField(placeholder: tr("Projeto", "Project"), value: lastProject)
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

    // --- snooze & login item ------------------------------------------------

    @objc private func toggleSleep() {
        asleep.toggle() // optimistic: flip now, the next poll() reconciles
        updateIcon()
        rebuildMenu()
        request("POST", asleep ? "/sleep" : "/wake") { [weak self] _, _ in self?.poll() }
    }

    // loginItemRegistered: enabled OR requiresApproval both mean "the user asked
    // for it" — requiresApproval just needs a one-time approval in System Settings.
    @available(macOS 13.0, *)
    private var loginItemRegistered: Bool {
        let s = SMAppService.mainApp.status
        return s == .enabled || s == .requiresApproval
    }

    // loginItemMenuItem reflects and toggles whether MeetMD launches at login,
    // using the system Login Items registration (macOS 13+ SMAppService).
    private func loginItemMenuItem() -> NSMenuItem {
        let mi = item(tr("Abrir no login", "Open at login"), #selector(toggleLoginItem), "", "power.dotted")
        if #available(macOS 13.0, *) {
            mi.state = loginItemRegistered ? .on : .off
        } else {
            mi.isEnabled = false
        }
        return mi
    }

    @objc private func toggleLoginItem() {
        guard #available(macOS 13.0, *) else { return }
        do {
            if loginItemRegistered {
                try SMAppService.mainApp.unregister()
            } else {
                try SMAppService.mainApp.register()
                if SMAppService.mainApp.status == .requiresApproval {
                    showInfo(tr("Aprove o MeetMD em Ajustes do Sistema ▸ Geral ▸ Itens de Início para ele abrir no login.",
                                "Approve MeetMD in System Settings ▸ General ▸ Login Items so it opens at login."))
                }
            }
        } catch {
            showError(tr("Não foi possível alterar o início no login: \(error.localizedDescription). O app precisa estar em /Aplicativos.",
                         "Couldn't change open-at-login: \(error.localizedDescription). The app must be in /Applications."))
        }
        rebuildMenu()
    }

    // --- menu ---------------------------------------------------------------

    private func rebuildMenu() {
        let menu = NSMenu()
        menu.addItem(disabled(headerText()))
        menu.addItem(.separator())

        if online, snoozing {
            // Snoozing (only while idle): do nothing but offer to wake.
            menu.addItem(item(tr("Acordar", "Wake up"), #selector(toggleSleep), "", "sun.max"))
        } else if online {
            switch state {
            case .idle:
                menu.addItem(item(tr("Iniciar gravação", "Start recording"), #selector(startManual), "r", "record.circle"))
                menu.addItem(item(tr("Nova nota de voz", "New voice note"), #selector(startNote), "n", "mic.circle"))
            case .recording where kind == kindNote:
                menu.addItem(item(tr("Parar e salvar nota", "Stop & save note"), #selector(stop), "s", "stop.circle"))
            case .recording:
                menu.addItem(item(tr("Pausar", "Pause"), #selector(pause), "p", "pause.circle"))
                menu.addItem(item(tr("Parar e salvar", "Stop & save"), #selector(stop), "s", "stop.circle"))
            case .paused:
                menu.addItem(item(tr("Retomar", "Resume"), #selector(resume), "p", "play.circle"))
                menu.addItem(item(tr("Parar e salvar", "Stop & save"), #selector(stop), "s", "stop.circle"))
            case .processing:
                break // transcrevendo — sem ações
            }
            menu.addItem(item(tr("Abrir pasta dos arquivos", "Open files folder"), #selector(openFolder), "f", "folder"))
            menu.addItem(item(tr("Configurações…", "Settings…"), #selector(openSettings), ",", "gearshape"))
            if state == .idle { // snooze only makes sense when there's nothing to record
                menu.addItem(item(tr("Dormir", "Snooze"), #selector(toggleSleep), "", "moon"))
            }
        }
        menu.addItem(.separator())
        menu.addItem(loginItemMenuItem())
        menu.addItem(item(tr("Permissões…", "Permissions…"), #selector(openOnboarding), "", "checkmark.shield"))
        menu.addItem(item(tr("Sobre o MeetMD", "About MeetMD"), #selector(openAbout), "", "info.circle"))
        menu.addItem(item(tr("Sair", "Quit"), #selector(quit), "q", "power"))
        statusItem.menu = menu
    }

    private func headerText() -> String {
        if !online { return tr("Bridge offline", "Bridge offline") }
        if snoozing { return tr("Dormindo (soneca)", "Snoozing") }
        switch state {
        case .recording where kind == kindNote: return tr("Gravando nota…", "Recording note…")
        case .recording: return tr("Gravando: ", "Recording: ") + displayTitle()
        case .paused: return tr("Pausado: ", "Paused: ") + displayTitle()
        case .processing: return tr("Processando…", "Processing…")
        case .idle: return tr("Pronto", "Ready")
        }
    }

    private func displayTitle() -> String {
        // Auto-detected ad-hoc Meets often have no title; fall back to the project,
        // which is the meaningful label the user configured for them.
        if !title.isEmpty { return title }
        if !meetingProject.isEmpty { return meetingProject }
        return tr("sem título", "untitled")
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
        candidates.append(selfDir.appendingPathComponent("meetmd-bridge").path) // bundled (.app)
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
    private let uiLanguagePopup = NSPopUpButton()
    private let autoDetectPopup = NSPopUpButton()
    private let micCheck = NSButton(checkboxWithTitle: tr("Incluir minha voz (microfone)", "Include my voice (microphone)"), target: nil, action: nil)
    private let deleteCheck = NSButton(checkboxWithTitle: tr("Apagar áudio bruto após transcrever", "Delete raw audio after transcribing"), target: nil, action: nil)
    private let hint = NSTextField(labelWithString: "")

    // (display title, config value) pairs.
    private let languages = [(tr("Automático", "Automatic"), "auto"), ("Português", "pt"), (tr("Espanhol", "Spanish"), "es"), (tr("Inglês", "English"), "en")]
    private let uiLanguages = [(tr("Automático (do sistema)", "Automatic (system)"), "auto"), ("Português", "pt"), (tr("Inglês", "English"), "en")]
    private let autoModes = [(tr("Perguntar antes de gravar", "Ask before recording"), "ask"), (tr("Gravar automaticamente", "Record automatically"), "auto"), (tr("Desligada", "Off"), "off")]

    init(base: String) {
        self.base = base
        let window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 470, height: 360),
                              styleMask: [.titled, .closable], backing: .buffered, defer: false)
        window.title = tr("MeetMD — Configurações", "MeetMD — Settings")
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
        uiLanguagePopup.addItems(withTitles: uiLanguages.map { $0.0 })
        autoDetectPopup.addItems(withTitles: autoModes.map { $0.0 })
        outputField.placeholderString = "/Users/…/recordings"
        projectField.placeholderString = tr("ex.: bora (opcional)", "e.g. bora (optional)")

        let choose = NSButton(title: tr("Escolher…", "Choose…"), target: self, action: #selector(chooseFolder))
        choose.bezelStyle = .rounded
        let outputRow = NSStackView(views: [outputField, choose])
        outputRow.orientation = .horizontal
        outputRow.spacing = 6

        let form = NSStackView(views: [
            labeled(tr("Pasta de gravações", "Recordings folder"), outputRow),
            labeled(tr("Idioma da interface", "Interface language"), uiLanguagePopup),
            labeled(tr("Idioma da transcrição", "Transcription language"), languagePopup),
            labeled(tr("Projeto padrão", "Default project"), projectField),
            labeled(tr("Detecção automática", "Auto-detect"), autoDetectPopup),
            labeled("", micCheck),
            labeled("", deleteCheck),
        ])
        form.orientation = .vertical
        form.alignment = .leading
        form.spacing = 10
        form.translatesAutoresizingMaskIntoConstraints = false

        hint.textColor = .systemRed
        hint.font = .systemFont(ofSize: 11)

        let save = NSButton(title: tr("Salvar", "Save"), target: self, action: #selector(saveSettings))
        save.bezelStyle = .rounded
        save.keyEquivalent = "\r"
        let cancel = NSButton(title: tr("Cancelar", "Cancel"), target: self, action: #selector(cancel))
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
                self?.hint.stringValue = tr("Bridge offline", "Bridge offline")
                return
            }
            self.outputField.stringValue = o["recordingsRoot"] as? String ?? ""
            self.projectField.stringValue = o["defaultProject"] as? String ?? ""
            self.select(self.languagePopup, self.languages, value: o["language"] as? String ?? "auto")
            self.select(self.uiLanguagePopup, self.uiLanguages, value: o["uiLanguage"] as? String ?? "auto")
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
            "recordingsRoot": outputField.stringValue,
            "language": value(languagePopup, languages),
            "uiLanguage": value(uiLanguagePopup, uiLanguages),
            "defaultProject": projectField.stringValue,
            "autoDetect": value(autoDetectPopup, autoModes),
            "captureMic": micCheck.state == .on,
            "deleteAudio": deleteCheck.state == .on,
        ]
        request("PUT", body) { [weak self] obj in
            if obj != nil {
                self?.window?.close()
            } else {
                self?.hint.stringValue = tr("Falha ao salvar (pasta inválida ou bridge offline)", "Failed to save (invalid folder or bridge offline)")
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

// MARK: - First-run onboarding

// OnboardingWindowController guides the user through the three TCC permissions
// MeetMD needs, with one-click deep links into the relevant System Settings panes.
final class OnboardingWindowController: NSWindowController {
    init() {
        let window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 480, height: 360),
                              styleMask: [.titled, .closable], backing: .buffered, defer: false)
        window.title = tr("Bem-vindo ao MeetMD", "Welcome to MeetMD")
        super.init(window: window)
        buildUI()
    }

    required init?(coder: NSCoder) { fatalError("not used") }

    func show() {
        NSApp.activate(ignoringOtherApps: true)
        window?.center()
        showWindow(nil)
        window?.makeKeyAndOrderFront(nil)
    }

    private func buildUI() {
        let intro = NSTextField(wrappingLabelWithString: tr(
            "Pro MeetMD gravar suas reuniões, conceda estas permissões (uma vez). Clique em “Abrir Ajustes” e ative o MeetMD em cada painel.",
            "For MeetMD to record your meetings, grant these permissions (once). Click “Open Settings” and enable MeetMD in each pane."))
        intro.font = .systemFont(ofSize: 12)

        let rows = NSStackView(views: [
            permissionRow(tr("Gravação de Tela", "Screen Recording"),
                          tr("captura a voz dos participantes", "captures the participants' audio"), pane: "Privacy_ScreenCapture"),
            permissionRow(tr("Microfone", "Microphone"),
                          tr("captura a sua voz", "captures your voice"), pane: "Privacy_Microphone"),
            permissionRow(tr("Automação ▸ Safari", "Automation ▸ Safari"),
                          tr("detecta reuniões do Google Meet abertas", "detects open Google Meet tabs"), pane: "Privacy_Automation"),
        ])
        rows.orientation = .vertical
        rows.alignment = .leading
        rows.spacing = 14

        let done = NSButton(title: tr("Concluir", "Done"), target: self, action: #selector(finish))
        done.bezelStyle = .rounded
        done.keyEquivalent = "\r"
        let spacer = NSView()
        spacer.setContentHuggingPriority(.defaultLow, for: .horizontal)
        let buttons = NSStackView(views: [spacer, done])
        buttons.orientation = .horizontal

        let content = NSStackView(views: [intro, rows, buttons])
        content.orientation = .vertical
        content.alignment = .leading
        content.spacing = 18
        content.edgeInsets = NSEdgeInsets(top: 20, left: 20, bottom: 20, right: 20)
        content.translatesAutoresizingMaskIntoConstraints = false
        window?.contentView = content
        NSLayoutConstraint.activate([
            buttons.widthAnchor.constraint(equalTo: content.widthAnchor, constant: -40),
            rows.widthAnchor.constraint(equalTo: content.widthAnchor, constant: -40),
            intro.widthAnchor.constraint(equalTo: content.widthAnchor, constant: -40),
        ])
    }

    private func permissionRow(_ name: String, _ why: String, pane: String) -> NSView {
        let title = NSTextField(labelWithString: name)
        title.font = .boldSystemFont(ofSize: 13)
        let desc = NSTextField(labelWithString: why)
        desc.font = .systemFont(ofSize: 11)
        desc.textColor = .secondaryLabelColor
        let text = NSStackView(views: [title, desc])
        text.orientation = .vertical
        text.alignment = .leading
        text.spacing = 1
        let open = NSButton(title: tr("Abrir Ajustes", "Open Settings"), target: self, action: #selector(openPane(_:)))
        open.bezelStyle = .rounded
        open.identifier = NSUserInterfaceItemIdentifier(pane)
        let spacer = NSView()
        spacer.setContentHuggingPriority(.defaultLow, for: .horizontal)
        let row = NSStackView(views: [text, spacer, open])
        row.orientation = .horizontal
        row.spacing = 10
        return row
    }

    @objc private func openPane(_ sender: NSButton) {
        guard let pane = sender.identifier?.rawValue,
              let url = URL(string: "x-apple.systempreferences:com.apple.preference.security?\(pane)") else { return }
        NSWorkspace.shared.open(url)
    }

    @objc private func finish() { window?.close() }
}

// Hidden dev/build helper: `MeetMD --render <state> <out.png> [px]` renders one
// icon state to a PNG. Used by build-app.sh to generate the .app icon and to
// preview states. States: idle | recording | paused | processing | offline | asleep.
func writePNG(_ img: NSImage, _ path: String) {
    if let tiff = img.tiffRepresentation, let rep = NSBitmapImageRep(data: tiff),
       let png = rep.representation(using: .png, properties: [:]) {
        try? png.write(to: URL(fileURLWithPath: path))
    }
}
// Bring up the AppKit graphics stack before any off-screen text/icon rendering.
if CommandLine.arguments.count >= 2, CommandLine.arguments[1].hasPrefix("--") {
    _ = NSApplication.shared
}
if CommandLine.arguments.count >= 4, CommandLine.arguments[1] == "--render" {
    let name = CommandLine.arguments[2]
    let px = CommandLine.arguments.count >= 5 ? CGFloat(Double(CommandLine.arguments[4]) ?? 20) : 20
    let img = ClaudeIcon.image(for: State(rawValue: name) ?? .idle, online: name != "offline", asleep: name == "asleep", height: px)
    writePNG(img, CommandLine.arguments[3])
    exit(0)
}
if CommandLine.arguments.count >= 4, CommandLine.arguments[1] == "--app-icon" {
    let px = CGFloat(Double(CommandLine.arguments[3]) ?? 1024)
    writePNG(ClaudeIcon.appIcon(px: px), CommandLine.arguments[2])
    exit(0)
}

let app = NSApplication.shared
let controller = AppController()
app.delegate = controller
app.run()
