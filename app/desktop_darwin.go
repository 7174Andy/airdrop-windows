package main

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func xmlText(s string) string {
	var b bytes.Buffer
	xml.EscapeText(&b, []byte(s))
	return b.String()
}

// The login item is a LaunchAgent; launchd runs it at the next login. Removing it
// doesn't stop the running app.
func loginItem() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents", "dev.airdrop-windows.app.plist")
}

func loginEnabled() bool {
	_, err := os.Stat(loginItem())
	return err == nil
}

func setLogin(on bool, exe string) error {
	p := loginItem()
	if !on {
		if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	}
	plist := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key><string>dev.airdrop-windows.app</string>
	<key>ProgramArguments</key><array><string>` + xmlText(exe) + `</string><string>--background</string></array>
	<key>RunAtLoad</key><true/>
</dict>
</plist>
`
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(plist), 0o644)
}

// The right-click entry is a Finder Quick Action: an Automator workflow in ~/Library/Services
// whose shell script runs `exe --send <files>`.
func contextMenuItem() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "Services", "Send with airdrop.workflow")
}

func contextMenuEnabled() bool {
	_, err := os.Stat(contextMenuItem())
	return err == nil
}

func setContextMenu(on bool, exe string) error {
	p := contextMenuItem()
	if !on {
		return os.RemoveAll(p)
	}
	// Backgrounded so the workflow finishes at once even when this launch becomes the app.
	script := shellQuote(exe) + ` --send "$@" >/dev/null 2>&1 &`
	if err := os.MkdirAll(filepath.Join(p, "Contents"), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(p, "Contents", "Info.plist"), []byte(quickActionInfo), 0o644); err != nil {
		return err
	}
	wflow := strings.Replace(quickActionWorkflow, "{{COMMAND}}", xmlText(script), 1)
	if err := os.WriteFile(filepath.Join(p, "Contents", "document.wflow"), []byte(wflow), 0o644); err != nil {
		return err
	}
	exec.Command("/System/Library/CoreServices/pbs", "-update").Run() // refresh the Services list now, not at next login
	return nil
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

const quickActionInfo = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>NSServices</key>
	<array>
		<dict>
			<key>NSMenuItem</key><dict><key>default</key><string>Send with airdrop</string></dict>
			<key>NSMessage</key><string>runWorkflowAsService</string>
			<key>NSRequiredContext</key><dict><key>NSApplicationIdentifier</key><string>com.apple.finder</string></dict>
			<key>NSSendFileTypes</key><array><string>public.item</string></array>
		</dict>
	</array>
</dict>
</plist>
`

// A one-action Automator workflow: Run Shell Script, files passed as arguments.
const quickActionWorkflow = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>AMApplicationBuild</key><string>523</string>
	<key>AMApplicationVersion</key><string>2.10</string>
	<key>AMDocumentVersion</key><string>2</string>
	<key>actions</key>
	<array>
		<dict>
			<key>action</key>
			<dict>
				<key>AMAccepts</key>
				<dict>
					<key>Container</key><string>List</string>
					<key>Optional</key><true/>
					<key>Types</key><array><string>com.apple.cocoa.string</string></array>
				</dict>
				<key>AMActionVersion</key><string>2.0.3</string>
				<key>AMApplication</key><array><string>Automator</string></array>
				<key>AMParameterProperties</key>
				<dict>
					<key>COMMAND_STRING</key><dict/>
					<key>CheckedForUserDefaultShell</key><dict/>
					<key>inputMethod</key><dict/>
					<key>shell</key><dict/>
					<key>source</key><dict/>
				</dict>
				<key>AMProvides</key>
				<dict>
					<key>Container</key><string>List</string>
					<key>Types</key><array><string>com.apple.cocoa.string</string></array>
				</dict>
				<key>ActionBundlePath</key><string>/System/Library/Automator/Run Shell Script.action</string>
				<key>ActionName</key><string>Run Shell Script</string>
				<key>ActionParameters</key>
				<dict>
					<key>COMMAND_STRING</key><string>{{COMMAND}}</string>
					<key>CheckedForUserDefaultShell</key><true/>
					<key>inputMethod</key><integer>1</integer>
					<key>shell</key><string>/bin/bash</string>
					<key>source</key><string></string>
				</dict>
				<key>BundleIdentifier</key><string>com.apple.RunShellScript</string>
				<key>CFBundleVersion</key><string>2.0.3</string>
				<key>CanShowSelectedItemsWhenRun</key><false/>
				<key>CanShowWhenRun</key><true/>
				<key>Category</key><array><string>AMCategoryUtilities</string></array>
				<key>Class Name</key><string>RunShellScriptAction</string>
				<key>InputUUID</key><string>4A6C2E61-7D3B-4C1E-9F0A-2B8D5E7C1A01</string>
				<key>Keywords</key><array><string>Shell</string><string>Script</string></array>
				<key>OutputUUID</key><string>4A6C2E61-7D3B-4C1E-9F0A-2B8D5E7C1A02</string>
				<key>UUID</key><string>4A6C2E61-7D3B-4C1E-9F0A-2B8D5E7C1A03</string>
				<key>UnlocalizedApplications</key><array><string>Automator</string></array>
				<key>isViewVisible</key><integer>1</integer>
			</dict>
			<key>isViewVisible</key><integer>1</integer>
		</dict>
	</array>
	<key>connectors</key><dict/>
	<key>workflowMetaData</key>
	<dict>
		<key>applicationBundleID</key><string>com.apple.finder</string>
		<key>inputTypeIdentifier</key><string>com.apple.Automator.fileSystemObject</string>
		<key>outputTypeIdentifier</key><string>com.apple.Automator.nothing</string>
		<key>presentationMode</key><integer>15</integer>
		<key>processesInput</key><false/>
		<key>serviceInputTypeIdentifier</key><string>com.apple.Automator.fileSystemObject</string>
		<key>serviceOutputTypeIdentifier</key><string>com.apple.Automator.nothing</string>
		<key>serviceProcessesInput</key><false/>
		<key>systemImageName</key><string>NSActionTemplate</string>
		<key>useAutomaticInputType</key><false/>
		<key>workflowTypeIdentifier</key><string>com.apple.Automator.servicesMenu</string>
	</dict>
</dict>
</plist>
`
