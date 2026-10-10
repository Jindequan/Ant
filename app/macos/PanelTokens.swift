import AppKit
import SwiftUI

// The menu bar panel follows Ant's established fog, route-teal and review
// amber palette (design/09-WEB-CONSOLE.md). Each semantic color stays legible
// in both macOS appearances.
enum PanelPalette {
    static let surface = adaptive(light: 0xfafcfb, dark: 0x222927)
    static let secondary = adaptive(light: 0x56635f, dark: 0xb8c3c0)
    static let action = adaptive(light: 0x08766d, dark: 0x63c9b0)
    static let good = adaptive(light: 0x146b4a, dark: 0x6ed2a0)
    static let warning = adaptive(light: 0x815000, dark: 0xffd166)
    static let danger = adaptive(light: 0x9b3027, dark: 0xff9185)
    static let section = adaptive(light: 0x4f5d5b, dark: 0xb4c1be)
    static let track = adaptive(light: 0xd7dfdc, dark: 0x414b48)

    private static func adaptive(light: UInt32, dark: UInt32) -> Color {
        Color(nsColor: NSColor(name: nil) { appearance in
            let isDark = appearance.bestMatch(from: [.darkAqua, .aqua]) == .darkAqua
            let hex = isDark ? dark : light
            return NSColor(
                red: CGFloat((hex >> 16) & 0xff) / 255,
                green: CGFloat((hex >> 8) & 0xff) / 255,
                blue: CGFloat(hex & 0xff) / 255,
                alpha: 1
            )
        })
    }
}
