// Writes claudia's app icon as a 1024px PNG to the given path, for build.sh.
import Foundation
import NotifierCore

try renderIcon().write(to: URL(fileURLWithPath: CommandLine.arguments[1]))
