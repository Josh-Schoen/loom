// Copyright 2017 by Teradata Corporation. All Rights Reserved.
// TERADATA CORPORATION CONFIDENTIAL AND TRADE SECRET

package teradata

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
)

func getOSProcessID () int {

	return os.Getpid ()

} // end getOSProcessID

func getOSProgramName (con *TeradataConnection) (sOutput string) {

	sOutput, err := os.Executable ()

	if sOutput == "" { sOutput = "-" }

	if err != nil && con.hasDebugLog () {
		debugLog (fmt.Sprintf ("getOSProgramName err=%v", err))
	}

	return

} // end getOSProgramName

func getOSProgramBaseName (con *TeradataConnection) string {

	return filepath.Base (getOSProgramName (con))

} // end getOSProgramBaseName

func getOSUserName () string {

	user, _ := user.Current ()
	return func () string { if user != nil { return user.Username } ; return "" } ()

} // end getOSUserName

func splitCommandLine (sInput string) (asOutput [] string) {

	sInput = strings.TrimSpace (sInput)

	bInsideToken := false
	bQuotedToken := false

	for _, r := range sInput {

		s := string (r)

		if bInsideToken && ! bQuotedToken && s == " " {
			bInsideToken = false

		} else if bInsideToken {
			if s == `"` { bQuotedToken = ! bQuotedToken } // toggle the quoted status
			asOutput [len (asOutput) - 1] += s // append to the last token

		} else if s != " " {
			bInsideToken = true
			bQuotedToken = s == `"`
			asOutput = append (asOutput, s) // begin a new token
		}
	} // end for

	return

} // end splitCommandLine

func parseCommandLine (sCommandLine string) (cmd *exec.Cmd, err error) {

	asTokens := splitCommandLine (sCommandLine)

	if len (asTokens) == 0 {
		err = errors.New ("Program name is missing from command line")
		return
	}

	// From the documentation for the exec.Command function:
	//
	// The returned Cmd's Args field is constructed from the command name followed by the elements of arg, so arg should not include the command name itself.
	// For example, Command("echo", "hello"). Args[0] is always name, not the possibly resolved Path.
	// On Windows, processes receive the whole command line as a single string and do their own parsing.
	// Command combines and quotes Args into a command line string with an algorithm compatible with applications using CommandLineToArgvW (which is the most common way).
	// Notable exceptions are msiexec.exe and cmd.exe (and thus, all batch files), which have a different unquoting algorithm.
	// In these or other similar cases, you can do the quoting yourself and provide the full command line in SysProcAttr.CmdLine, leaving Args empty.

	sProgramName := strings.Trim (asTokens [0], `"`) // strip any leading and trailing double-quote characters from program name
	asArguments  := asTokens [1 : ]

	// On Windows, exec.Command concatenates and quotes arguments in a manner that is incompatible with cmd.exe unquoting.
	// On Windows, we must avoid passing arguments to exec.Command and instead use the Windows-specific syscall.SysProcAttr.CmdLine field.
	// To keep our source code platform-independent, we use reflection via JSON to set the Windows-specific syscall.SysProcAttr.CmdLine field.

	if runtime.GOOS == "windows" {
		cmd = exec.Command (sProgramName)
		cmd.SysProcAttr = &syscall.SysProcAttr {}
		err = mapStringStringToStruct (map [string] string { "CmdLine" : sCommandLine }, cmd.SysProcAttr)
	} else {
		cmd = exec.Command (sProgramName, asArguments ...)
	}

	return

} // end parseCommandLine

func execCommandLine (sCommandLine string) (err error) {

	cmd, err := parseCommandLine (sCommandLine)
	if err != nil { return }

	err = cmd.Start () // does not wait for program to end
	return

} // end execCommandLine

func ensureWritableFile (sFileName string) error {

	f, err := os.OpenFile (sFileName, os.O_CREATE | os.O_WRONLY, 0666)

	if f != nil {
		f.Close ()
	}

	return err

} // end ensureWritableFile
