// Copyright 2017 by Teradata Corporation. All Rights Reserved.
// TERADATA CORPORATION CONFIDENTIAL AND TRADE SECRET

package teradata

import (
	"fmt"
	"time"
)

func formatDuration (dur time.Duration) string {

	return fmt.Sprintf ("%d ms", dur.Milliseconds ())

} // end formatDuration

func formatElapsed (timeStart time.Time) string {

	return formatDuration (time.Since (timeStart))

} // end formatElapsed

func formatTimeout (timeDeadline time.Time) string {

	if timeDeadline.IsZero () {
		return "reset"
	}

	dur := time.Until (timeDeadline)
	if dur >= 0 {
		return formatDuration (dur)
	}

	return "expired " + formatElapsed (timeDeadline) + " ago"

} // end formatTimeout

func formatTime (t time.Time) string {

	return t.Format ("2006-01-02.15:04:05.000")

} // end formatTime

func threadSleep (bTiming bool, dur time.Duration) {

	if bTiming {
		s := formatDuration (dur)
		timingLog ("Sleeping for " + s)
		defer func () { timingLog ("Finished sleeping for " + s) } ()
	}

	time.Sleep (dur)

} // end threadSleep

func currentTimeMillis () int {

	return int (time.Now ().UnixMilli ())

} // end currentTimeMillis

func currentTimeSeconds () int {

	return int (time.Now ().Unix ())

} // end currentTimeSeconds
