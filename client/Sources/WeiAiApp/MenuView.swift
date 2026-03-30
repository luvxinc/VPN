import SwiftUI

struct ConnectView: View {
    @ObservedObject var vpn: VPNManager
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

            Text("为爱鼓掌 VPN")
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
            TextField("用户名", text: $username)
                .textFieldStyle(.roundedBorder)
                .autocorrectionDisabled()

            SecureField("密码", text: $password)
                .textFieldStyle(.roundedBorder)

            Button(action: attemptConnect) {
                Text("连接")
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
            Text("连接中...").foregroundStyle(.secondary)
        }
        .frame(height: 36)
    }

    // MARK: - Update required

    private func updateRequiredView(downloadURL: String) -> some View {
        VStack(spacing: 12) {
            Image(systemName: "arrow.down.circle.fill")
                .font(.system(size: 36))
                .foregroundStyle(.orange)

            Text("需要更新客户端")
                .font(.headline)

            Text("当前版本过旧，请下载最新版本后重新安装。")
                .font(.caption)
                .foregroundStyle(.secondary)
                .multilineTextAlignment(.center)

            Button(action: { openDownload(url: downloadURL) }) {
                Label("下载最新版本", systemImage: "arrow.down.circle")
                    .frame(maxWidth: .infinity)
            }
            .buttonStyle(.borderedProminent)
            .controlSize(.large)
            .tint(.orange)

            Button("返回") {
                updateURL = nil
                errorMsg = nil
            }
            .buttonStyle(.borderless)
            .foregroundStyle(.secondary)
            .font(.caption)
        }
    }

    // MARK: - Device verification code form

    private var deviceCodeView: some View {
        VStack(spacing: 10) {
            Text("此设备未注册")
                .font(.headline)

            Text("请联系管理员获取 8 位验证码")
                .font(.caption)
                .foregroundStyle(.secondary)
                .multilineTextAlignment(.center)

            TextField("验证码（8位）", text: $verificationCode)
                .textFieldStyle(.roundedBorder)
                .autocorrectionDisabled()
                .textCase(.uppercase)
                .onChange(of: verificationCode) { value in
                    verificationCode = String(value.uppercased().prefix(8))
                }

            Button(action: attemptVerify) {
                Text("验证并连接")
                    .frame(maxWidth: .infinity)
            }
            .buttonStyle(.borderedProminent)
            .controlSize(.large)
            .tint(.red)
            .disabled(verificationCode.count != 8)

            Button("返回") {
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
                errorMsg = "验证码无效或已过期"
            case .updateRequired(let url):
                updateURL = url
                showDeviceCodeForm = false
            case .error(let msg):
                errorMsg = msg
            }
        }
    }

    private func openDownload(url: String) {
        guard let nsURL = URL(string: url) else { return }
        NSWorkspace.shared.open(nsURL)
    }
}
