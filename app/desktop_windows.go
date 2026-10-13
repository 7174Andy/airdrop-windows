package main

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"

	"github.com/go-ole/go-ole"
	"github.com/go-ole/go-ole/oleutil"
	"golang.org/x/sys/windows/registry"
)

// The login item is a value under the per-user Run key, which is also what
// Task Manager's "Startup apps" lists and lets you disable.
const runKey = `Software\Microsoft\Windows\CurrentVersion\Run`

func loginEnabled() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	_, _, err = k.GetStringValue("airdrop")
	return err == nil
}

func setLogin(on bool, exe string) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	if on {
		return k.SetStringValue("airdrop", `"`+exe+`" --background`)
	}
	if err := k.DeleteValue("airdrop"); err != nil && !errors.Is(err, registry.ErrNotExist) {
		return err
	}
	return nil
}

// The right-click entry is a shortcut in the Send to folder; Explorer appends the
// selected files to its arguments, giving `exe --send <files>`.
func contextMenuItem() string {
	return filepath.Join(os.Getenv("APPDATA"), "Microsoft", "Windows", "SendTo", "airdrop.lnk")
}

func contextMenuEnabled() bool {
	_, err := os.Stat(contextMenuItem())
	return err == nil
}

func setContextMenu(on bool, exe string) error {
	p := contextMenuItem()
	if !on {
		if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	}
	runtime.LockOSThread() // COM is per thread
	defer runtime.UnlockOSThread()
	if err := ole.CoInitializeEx(0, ole.COINIT_APARTMENTTHREADED); err == nil {
		defer ole.CoUninitialize()
	}
	obj, err := oleutil.CreateObject("WScript.Shell")
	if err != nil {
		return err
	}
	defer obj.Release()
	shell, err := obj.QueryInterface(ole.IID_IDispatch)
	if err != nil {
		return err
	}
	defer shell.Release()
	v, err := oleutil.CallMethod(shell, "CreateShortcut", p)
	if err != nil {
		return err
	}
	lnk := v.ToIDispatch()
	defer lnk.Release()
	for prop, val := range map[string]string{"TargetPath": exe, "Arguments": "--send", "Description": "Send with airdrop"} {
		if _, err := oleutil.PutProperty(lnk, prop, val); err != nil {
			return err
		}
	}
	_, err = oleutil.CallMethod(lnk, "Save")
	return err
}
