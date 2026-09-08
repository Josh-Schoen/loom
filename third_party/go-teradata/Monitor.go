// Copyright 2021 by Teradata Corporation. All Rights Reserved.
// TERADATA CORPORATION CONFIDENTIAL AND TRADE SECRET

package teradata

import (
	"sync"
	"time"
)

// Mimics a Java monitor having a wait method with timeout parameter.
// Use similarly to how a Go sync.Cond is used.
type Monitor struct {

	L      sync.Mutex
	wake   chan struct {}

} // end type Monitor

func NewMonitor () *Monitor {

	return &Monitor {
		L      : sync.Mutex {},
		wake   : make (chan struct {}),
	}
} // end NewMonitor

// Equivalent to Java's Object.notify() method.
// The caller should hold mon.L prior to calling Signal.
// Signal wakes one goroutine waiting on mon, if there is any.
func (mon *Monitor) Signal () {

	select {
		case mon.wake <- struct {} {}:
		default: // non-blocking
	}
} // end method Signal

// Equivalent to Java's Object.wait(long timeout) method.
// The caller must hold mon.L prior to calling Wait.
// Wait atomically unlocks mon.L and suspends execution of the calling goroutine.
// After later resuming execution, Wait locks mon.L before returning.
func (mon *Monitor) Wait (timeout time.Duration) {

	mon.L.Unlock ()

	if timeout == 0 {
		<- mon.wake
	} else {
		select { // blocking
			case <- mon.wake:
			case <- time.After (timeout):
		}
	}

	mon.L.Lock ()

} // end method Wait
