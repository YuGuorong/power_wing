//go:build windows

// Package voice uses Windows System.Speech.Recognition via a PowerShell
// subprocess to listen for spoken commands and relay them to the manager.
package voice

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/yuguorong/power_wing/internal/manager"
)

const psScript = `
Add-Type -AssemblyName System.Speech
$engine = New-Object System.Speech.Recognition.SpeechRecognitionEngine
$choices = New-Object System.Speech.Recognition.Choices
$cmds = @(
    "output on", "output off",
    "voltage up", "voltage down",
    "current up", "current down",
    "next page", "previous page",
    "port one on",  "port one off",
    "port two on",  "port two off",
    "port three on","port three off",
    "port four on", "port four off"
)
foreach ($c in $cmds) { $choices.Add($c) }
$gb = New-Object System.Speech.Recognition.GrammarBuilder
$gb.Append($choices)
$grammar = New-Object System.Speech.Recognition.Grammar($gb)
$engine.LoadGrammar($grammar)
$engine.SetInputToDefaultAudioDevice()
Register-ObjectEvent $engine SpeechRecognized -Action {
    $text = $Event.SourceEventArgs.Result.Text
    Write-Output $text
    [Console]::Out.Flush()
} | Out-Null
$engine.RecognizeAsync([System.Speech.Recognition.RecognizeMode]::Multiple)
try { while ($true) { Start-Sleep -Milliseconds 200 } }
finally { $engine.RecognizeAsyncStop() }
`

// Listen launches the PowerShell speech engine and translates recognised
// phrases into manager commands on the active device.
func Listen(ctx context.Context, mgr *manager.Manager) {
	// Write script to a temp file.
	tmp := filepath.Join(os.TempDir(), "powerwing_voice.ps1")
	if err := os.WriteFile(tmp, []byte(psScript), 0600); err != nil {
		log.Printf("[voice] cannot write ps script: %v", err)
		return
	}
	defer os.Remove(tmp)

	cmd := exec.CommandContext(ctx, "powershell", "-NoProfile", "-ExecutionPolicy", "Bypass",
		"-File", tmp)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		log.Printf("[voice] stdout pipe: %v", err)
		return
	}
	if err := cmd.Start(); err != nil {
		log.Printf("[voice] start ps: %v", err)
		return
	}
	log.Println("[voice] Windows speech recognition active")

	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		phrase := strings.ToLower(strings.TrimSpace(scanner.Text()))
		if phrase == "" {
			continue
		}
		log.Printf("[voice] heard: %q", phrase)
		dispatchVoiceCmd(phrase, mgr)
	}
	_ = cmd.Wait()
}

// dispatchVoiceCmd translates a recognised phrase to a device command.
func dispatchVoiceCmd(phrase string, mgr *manager.Manager) {
	// Find first connected power supply or hub.
	devs := mgr.Devices()

	psuID, hubID := "", ""
	for _, d := range devs {
		if d.Connected && d.Type == "power_supply" && psuID == "" {
			psuID = d.ID
		}
		if d.Connected && d.Type == "usb_hub" && hubID == "" {
			hubID = d.ID
		}
	}

	send := func(id, cmd string, params map[string]interface{}) {
		if id == "" {
			return
		}
		if err := mgr.SendCmd(manager.CmdRequest{DeviceID: id, Command: cmd, Params: params}); err != nil {
			log.Printf("[voice] cmd error: %v", err)
		}
	}

	switch phrase {
	case "output on":
		send(psuID, "set_outp", map[string]interface{}{"on": true})
	case "output off":
		send(psuID, "set_outp", map[string]interface{}{"on": false})
	case "voltage up":
		// +0.1 V step – best-effort; manager will read current set-point
		log.Println("[voice] voltage up – use UI step buttons for precise control")
	case "voltage down":
		log.Println("[voice] voltage down – use UI step buttons for precise control")
	case "current up":
		log.Println("[voice] current up – use UI step buttons for precise control")
	case "current down":
		log.Println("[voice] current down – use UI step buttons for precise control")
	default:
		// USB port commands: "port one on" etc.
		portMap := map[string]int{"one": 1, "two": 2, "three": 3, "four": 4}
		for word, num := range portMap {
			if strings.Contains(phrase, fmt.Sprintf("port %s on", word)) {
				send(hubID, "set_usb_port", map[string]interface{}{"port": num, "on": true})
				return
			}
			if strings.Contains(phrase, fmt.Sprintf("port %s off", word)) {
				send(hubID, "set_usb_port", map[string]interface{}{"port": num, "on": false})
				return
			}
		}
	}
}
