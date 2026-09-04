package cmd

import (
	"io"

	"github.com/mdp/qrterminal/v3"
)

// printTerminalQR renders content as a compact half-block QR code so a phone
// on the same network can open a tunnel URL straight from the terminal.
func printTerminalQR(w io.Writer, content string) {
	qrterminal.GenerateWithConfig(content, qrterminal.Config{
		Level:      qrterminal.L,
		Writer:     w,
		HalfBlocks: true,
		QuietZone:  2,
	})
}
