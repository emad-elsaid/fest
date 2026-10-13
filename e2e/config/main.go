// Command config is the system configuration the end-to-end test applies inside
// a disposable Arch Linux VM. Every declaration here is asserted afterwards by
// e2e/verify.sh, so keep the two files in sync.
package main

import "github.com/emad-elsaid/fest"

func init() {
	// System configuration. The timezone and NTP settings differ from a stock
	// install, so the test proves fest changed them rather than finding them.
	fest.Timedate("Asia/Tokyo", true)
	fest.Locale("en_US.UTF-8")
	fest.Keyboard("us", "us", "pc105", "", "ctrl:nocaps")

	// cronie is disabled on a stock installation, so enabling it proves fest
	// did the work. The accompanying system/ and user/ trees next to this file
	// are picked up automatically from the working directory.
	fest.Package("tree", "jq", "cronie")
	fest.SystemService("cronie")

	// The stock vagrant user is not in the video group.
	fest.Group("video")
}

func main() {
	fest.Main()
}
