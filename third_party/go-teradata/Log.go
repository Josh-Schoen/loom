// Copyright 2021 by Teradata Corporation. All Rights Reserved.
// TERADATA CORPORATION CONFIDENTIAL AND TRADE SECRET

package teradata

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"runtime"
	"time"
	"github.com/Teradata-TIO/go-teradata/goteragss"
)

const nNamePrefixLength = len ("goroutine ")

func GoroutineName () string {

	abyStack := make ([] byte, 64)
	runtime.Stack (abyStack, false)

	abyId       := abyStack [nNamePrefixLength : ]
	nIdLength   := bytes.IndexByte (abyId, ' ')

	nNameLength := nNamePrefixLength + nIdLength
	abyName     := abyStack [ : nNameLength] // trim off the rest of the stack trace after the goroutine name

	return string (abyName)

} // end GoroutineName

func debugLogHexDump (s string, aby [] byte) {

	logMsg ("GOSQL", "DEBUG", s)
	fmt.Print (hex.Dump (aby))

} // end debugLogHexDump

func dumpLogHexDump (s string, aby [] byte) {

	logMsg ("GOSQL", "DUMP", s)
	fmt.Print (hex.Dump (aby))

} // end dumpLogHexDump

func logMsg (sProduct string, sCategory string, s string) {

	_, sFile, nLine, _ := runtime.Caller (2)

	fmt.Printf ("%v [%v] %v-%v %v:%v %v\n", formatTime (time.Now ()), GoroutineName (), sProduct, sCategory, filepath.Base (sFile), nLine, s)

} // end func logMsg

func traceLog  (s string) { logMsg ("GOSQL", "TRACE" , s) }
func debugLog  (s string) { logMsg ("GOSQL", "DEBUG" , s) }
func dumpLog   (s string) { logMsg ("GOSQL", "DUMP"  , s) }
func timingLog (s string) { logMsg ("GOSQL", "TIMING", s) }

func (con *TeradataConnection) hasTraceLog  () bool { return con.m_params.hasTraceLog  () }
func (con *TeradataConnection) hasDebugLog  () bool { return con.m_params.hasDebugLog  () }
func (con *TeradataConnection) hasDumpLog   () bool { return con.m_params.hasDumpLog   () }
func (con *TeradataConnection) hasTimingLog () bool { return con.m_params.hasTimingLog () }

var _ goteragss.TdgssLogger = &TeradataConnection {} // implements goteragss.TdgssLogger

func (con *TeradataConnection) HasTraceLog  () bool { return con.m_params.hasTraceLog  () }
func (con *TeradataConnection) HasTimingLog () bool { return con.m_params.hasTimingLog () }
func (con *TeradataConnection) HasDebugLog  () bool { return con.m_params.hasDebugLog  () }
func (con *TeradataConnection) TraceLog  (s string) { logMsg ("TERAGSS", "TRACE" , s) }
func (con *TeradataConnection) TimingLog (s string) { logMsg ("TERAGSS", "TIMING", s) }
func (con *TeradataConnection) DebugLog  (s string) { logMsg ("TERAGSS", "DEBUG" , s) }
