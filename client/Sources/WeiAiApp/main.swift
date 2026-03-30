import AppKit

// main.swift 在主线程执行，用 assumeIsolated 告知编译器
MainActor.assumeIsolated {
    let delegate = AppDelegate()
    NSApplication.shared.delegate = delegate
}
NSApp.run()
