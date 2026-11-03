import AppKit

/// claudia's app icon as PNG: a purple ✻ on One Dark.
public func renderIcon(size: Int = 1024) -> Data {
    let rep = NSBitmapImageRep(bitmapDataPlanes: nil, pixelsWide: size, pixelsHigh: size, bitsPerSample: 8,
                               samplesPerPixel: 4, hasAlpha: true, isPlanar: false, colorSpaceName: .deviceRGB,
                               bytesPerRow: 0, bitsPerPixel: 0)!
    let s = Double(size)
    NSGraphicsContext.saveGraphicsState()
    NSGraphicsContext.current = NSGraphicsContext(bitmapImageRep: rep)
    let inset = s * 0.1
    let rect = NSRect(x: inset, y: inset, width: s - 2 * inset, height: s - 2 * inset)
    NSColor(red: 0x28 / 255, green: 0x2c / 255, blue: 0x34 / 255, alpha: 1).setFill()
    NSBezierPath(roundedRect: rect, xRadius: rect.width * 0.225, yRadius: rect.width * 0.225).fill()
    let glyph = NSAttributedString(string: "✻", attributes: [
        .font: NSFont.systemFont(ofSize: s * 0.56, weight: .bold),
        .foregroundColor: NSColor(red: 0xc6 / 255, green: 0x78 / 255, blue: 0xdd / 255, alpha: 1),
    ])
    let g = glyph.size()
    glyph.draw(at: NSPoint(x: (s - g.width) / 2, y: (s - g.height) / 2))
    NSGraphicsContext.restoreGraphicsState()
    return rep.representation(using: .png, properties: [:])!
}
