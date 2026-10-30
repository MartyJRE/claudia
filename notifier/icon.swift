// Renders claudia's app icon: a purple ✻ on One Dark, as a 1024px PNG.
import AppKit

let size = 1024.0
let img = NSImage(size: NSSize(width: size, height: size))
img.lockFocus()
let inset = size * 0.1
let rect = NSRect(x: inset, y: inset, width: size - 2 * inset, height: size - 2 * inset)
NSColor(red: 0x28 / 255, green: 0x2c / 255, blue: 0x34 / 255, alpha: 1).setFill()
NSBezierPath(roundedRect: rect, xRadius: rect.width * 0.225, yRadius: rect.width * 0.225).fill()
let glyph = NSAttributedString(string: "✻", attributes: [
    .font: NSFont.systemFont(ofSize: size * 0.56, weight: .bold),
    .foregroundColor: NSColor(red: 0xc6 / 255, green: 0x78 / 255, blue: 0xdd / 255, alpha: 1),
])
let g = glyph.size()
glyph.draw(at: NSPoint(x: (size - g.width) / 2, y: (size - g.height) / 2))
img.unlockFocus()
let rep = NSBitmapImageRep(data: img.tiffRepresentation!)!
try! rep.representation(using: .png, properties: [:])!.write(to: URL(fileURLWithPath: CommandLine.arguments[1]))
