import SwiftUI

struct ConnectView: View {
    @ObservedObject var vpn: VPNManager
    @ObservedObject private var updater = UpdateService.shared
    let onConnected: () -> Void

    @State private var username: String = AuthService.shared.savedUsername ?? ""
    @State private var password: String = AuthService.shared.savedPassword ?? ""
    @State private var verificationCode = ""
    @State private var showDeviceCodeForm = false
    @State private var updateURL: String?
    @State private var errorMsg: String?

    var body: some View {
        VStack(spacing: 0) {
            Spacer()

            // Logo
            Image(systemName: "heart.fill")
                .font(.system(size: 48))
                .foregroundStyle(.red)
                .padding(.bottom, 12)

            Text(L("app.name"))
                .font(.title2.bold())
                .padding(.bottom, 24)

            if vpn.isConnecting {
                connectingView
            } else if let url = updateURL {
                updateRequiredView(downloadURL: url)
            } else if showDeviceCodeForm {
                deviceCodeView
            } else {
                loginView
            }

            Spacer()

            // Version / author footer
            VStack(spacing: 2) {
                Text("v\(AppVersion.current) · \(AppVersion.releaseDate)")
                Text("© \(AppVersion.author)")
            }
            .font(.system(size: 10))
            .foregroundStyle(.tertiary)
            .padding(.bottom, 12)
        }
        .frame(width: 280)
        .padding(.horizontal, 24)
        .padding(.top, 20)
    }

    // MARK: - Login form

    private var loginView: some View {
        VStack(spacing: 10) {
            TextField(L("login.username"), text: $username)
                .textFieldStyle(.roundedBorder)
                .autocorrectionDisabled()

            SecureField(L("login.password"), text: $password)
                .textFieldStyle(.roundedBorder)

            Button(action: attemptConnect) {
                Text(L("login.connect"))
                    .frame(maxWidth: .infinity)
            }
            .buttonStyle(.borderedProminent)
            .controlSize(.large)
            .tint(.red)
            .disabled(username.isEmpty || password.isEmpty)
            .padding(.top, 4)

            errorLabel
        }
    }

    // MARK: - Connecting spinner

    private var connectingView: some View {
        HStack(spacing: 8) {
            ProgressView().scaleEffect(0.8)
            Text(L("login.connecting")).foregroundStyle(.secondary)
        }
        .frame(height: 36)
    }

    // MARK: - Update required

    private func updateRequiredView(downloadURL: String) -> some View {
        VStack(spacing: 14) {
            switch updater.state {
            case .idle:
                Image(systemName: "arrow.down.circle.fill")
                    .font(.system(size: 36))
                    .foregroundStyle(.orange)

                Text(L("update.title"))
                    .font(.headline)

                Text(L("update.message"))
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .multilineTextAlignment(.center)

                Button(action: { updater.start(downloadURL: downloadURL) }) {
                    Label(L("update.action"), systemImage: "arrow.down.circle")
                        .frame(maxWidth: .infinity)
                }
                .buttonStyle(.borderedProminent)
                .controlSize(.large)
                .tint(.orange)

            case .downloading(let p):
                Image(systemName: "arrow.down.circle")
                    .font(.system(size: 28))
                    .foregroundStyle(.orange)

                ProgressView(value: p)
                    .progressViewStyle(.linear)
                    .frame(width: 200)

                Text(String(format: L("update.progress"), Int(p * 100)))
                    .font(.caption)
                    .foregroundStyle(.secondary)

            case .installing:
                ProgressView()
                Text(L("update.installing"))
                    .font(.caption)
                    .foregroundStyle(.secondary)

            case .failed(let msg):
                Image(systemName: "exclamationmark.triangle.fill")
                    .font(.system(size: 28))
                    .foregroundStyle(.red)

                Text(msg)
                    .font(.caption)
                    .foregroundStyle(.red)
                    .multilineTextAlignment(.center)

                Button(action: { updater.start(downloadURL: downloadURL) }) {
                    Text(L("update.retry")).frame(maxWidth: .infinity)
                }
                .buttonStyle(.borderedProminent)
                .controlSize(.regular)
                .tint(.orange)
            }
        }
        .animation(.easeInOut(duration: 0.2), value: updater.state)
    }

    // MARK: - Device verification code form

    private var deviceCodeView: some View {
        VStack(spacing: 10) {
            Text(L("deviceCode.title"))
                .font(.headline)

            Text(L("deviceCode.message"))
                .font(.caption)
                .foregroundStyle(.secondary)
                .multilineTextAlignment(.center)

            TextField(L("deviceCode.placeholder"), text: $verificationCode)
                .textFieldStyle(.roundedBorder)
                .autocorrectionDisabled()
                .textCase(.uppercase)
                .onChange(of: verificationCode) { value in
                    verificationCode = String(value.uppercased().prefix(8))
                }

            Button(action: attemptVerify) {
                Text(L("deviceCode.verify"))
                    .frame(maxWidth: .infinity)
            }
            .buttonStyle(.borderedProminent)
            .controlSize(.large)
            .tint(.red)
            .disabled(verificationCode.count != 8)

            Button(L("deviceCode.back")) {
                showDeviceCodeForm = false
                verificationCode = ""
                errorMsg = nil
            }
            .buttonStyle(.borderless)
            .foregroundStyle(.secondary)
            .font(.caption)

            errorLabel
        }
    }

    // MARK: - Error label

    private var errorLabel: some View {
        Group {
            if let msg = errorMsg {
                Text(msg)
                    .font(.caption)
                    .foregroundStyle(.red)
                    .multilineTextAlignment(.center)
                    .fixedSize(horizontal: false, vertical: true)
            } else {
                Color.clear.frame(height: 1)
            }
        }
        .padding(.top, 6)
    }

    // MARK: - Actions

    private func attemptConnect() {
        errorMsg = nil
        updateURL = nil
        vpn.connect(username: username, password: password) { result in
            switch result {
            case .success:
                onConnected()
            case .deviceNotRegistered:
                showDeviceCodeForm = true
                errorMsg = nil
            case .updateRequired(let url):
                updateURL = url
            case .quotaExceeded(let resetsAt):
                errorMsg = quotaErrorMessage(resetsAt: resetsAt)
            case .error(let msg):
                errorMsg = msg
            }
        }
    }

    private func attemptVerify() {
        errorMsg = nil
        vpn.verifyDevice(username: username, password: password, code: verificationCode) { result in
            switch result {
            case .success:
                onConnected()
            case .deviceNotRegistered:
                errorMsg = L("deviceCode.invalid")
            case .updateRequired(let url):
                updateURL = url
                showDeviceCodeForm = false
            case .quotaExceeded(let resetsAt):
                errorMsg = quotaErrorMessage(resetsAt: resetsAt)
                showDeviceCodeForm = false
            case .error(let msg):
                errorMsg = msg
            }
        }
    }

    private func quotaErrorMessage(resetsAt: Date?) -> String {
        guard let d = resetsAt else { return L("error.quotaExceeded") }
        let f = RelativeDateTimeFormatter()
        f.unitsStyle = .full
        return L("error.quotaExceeded") + "\n" + L("quota.resetsIn") + " " + f.localizedString(for: d, relativeTo: Date())
    }
}
