// Copyright 2020 by Teradata Corporation. All Rights Reserved.
// TERADATA CORPORATION CONFIDENTIAL AND TRADE SECRET

package teradata

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"strings"
)

var _ fastExportManager = &fastExportManagerCSV {} // implements fastExportManager
var _ fmt.Stringer      = &fastExportManagerCSV {} // implements fmt.Stringer

type fastExportManagerCSV struct {
	*fastExportManagerBase
	m_nStmtIndex           int
}

func (fxpCSVManager *fastExportManagerCSV) Value () fastExportManager { return fxpCSVManager }

func (fxpCSVManager *fastExportManagerCSV) String () string { // satisfies fmt.Stringer

	return fmt.Sprintf ("fxpCSVManager=%p key=%v %v", fxpCSVManager, fxpCSVManager.m_appRows.m_sNativeSQL, fxpCSVManager.m_appRows)

} // end method String

func newFastExportManagerCSV (fxpManager *fastExportManagerBase) (fastExportManager, error) {

	var err error

	fxpCSVManager := &fastExportManagerCSV {
		fastExportManagerBase : fxpManager,
		m_nStmtIndex          : 0,
	}

	if fxpManager.m_appRows.m_sFieldQuote     == "" { fxpManager.m_appRows.m_sFieldQuote     = fxpManager.m_appRows.m_stmt.m_con.m_params.M_sFieldQuote }
	if fxpManager.m_appRows.m_sFieldSeparator == "" { fxpManager.m_appRows.m_sFieldSeparator = fxpManager.m_appRows.m_stmt.m_con.m_params.M_sFieldSeparator }

	// These values can be set with the escape function, so check here to make sure they are not equal.
	if fxpManager.m_appRows.m_sFieldQuote == fxpManager.m_appRows.m_sFieldSeparator {

		err = fxpManager.m_appRows.m_stmt.m_con.makeDriverError (err, 550,
			fmt.Sprintf ("Different values must be specified for the field quote and field separator characters, but the same value %v was used",
				fxpManager.m_appRows.m_sFieldQuote))

		return fxpCSVManager, err
	}

	nStmt := 0

	for {
		nStmt ++
		if fxpCSVManager.m_preparedRows.m_uActivityType != 1 { // 1=SELECT

			msg := "Cannot FastExport because statement is not a SELECT"

			if fxpCSVManager.m_appRows.m_bTryFastExport {
				sWarning := fxpCSVManager.m_appCon.makeDriverWarning (534, msg)
				fxpCSVManager.m_asWarnings = append (fxpCSVManager.m_asWarnings, sWarning)

			} else {
				err = fxpCSVManager.m_appCon.makeDriverError (nil, 532, msg)
			}

			return fxpCSVManager, err
		}

		err = fxpCSVManager.isTeradataTypeSupported ()
		if err != nil { return fxpCSVManager, err }

		fxpCSVManager.m_aColumnMetadata = append (fxpCSVManager.m_aColumnMetadata, fxpCSVManager.m_preparedRows.m_aColumnMetadata)
		fxpCSVManager.m_asColumnNames = append (fxpCSVManager.m_asColumnNames, fxpCSVManager.m_preparedRows.m_asColumnNames)
		fxpCSVManager.m_atColumnScanTypes = append (fxpCSVManager.m_atColumnScanTypes, fxpCSVManager.m_preparedRows.m_atColumnScanTypes)

		err = fxpCSVManager.m_preparedRows.NextResultSet ()
		if err == nil {
			continue
		} else if errors.Is (err, io.EOF) {
			err = nil
			break
		} else {
			return fxpCSVManager, err
		}
	}

	return fxpCSVManager, err

} // end method newFastExportManagerCSV

func (fxpCSVManager *fastExportManagerCSV) isTeradataTypeSupported () (err error) {

	var sWarning string

	if fxpCSVManager.m_appCon.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter fastExportManagerCSV.isTeradataTypeSupported len(m_aColumnMetadata)=%v m_atColumnScanTypes=%v",
			len (fxpCSVManager.m_preparedRows.m_aColumnMetadata), fxpCSVManager.m_preparedRows.m_atColumnScanTypes))
		defer func () { traceLog (fmt.Sprintf ("< leave fastExportManagerCSV.isTeradataTypeSupported sWarning=%v", sWarning)) } ()
	}

	aColMetadata := fxpCSVManager.m_preparedRows.m_aColumnMetadata
	for i := range aColMetadata {
		switch aColMetadata [i].M_uCookedDataType {
			case T_BIGINT,
				 T_BYTEINT,
				 T_DECIMAL,
				 T_FLOAT,
				 T_INTEGER,
				 T_NUMBER,
				 T_SMALLINT,
				 T_CHAR,
				 T_VARCHAR,
				 T_LONG_VARCHAR,
				 T_DATE_ANSI,
				 T_DATE_INTEGER,
				 T_TIME,
				 T_TIME_WITH_TIME_ZONE,
				 T_TIMESTAMP,
				 T_TIMESTAMP_WITH_TIME_ZONE,
				 T_INTERVAL_YEAR,
				 T_INTERVAL_YEAR_TO_MONTH,
				 T_INTERVAL_MONTH,
				 T_INTERVAL_DAY,
				 T_INTERVAL_DAY_TO_HOUR,
				 T_INTERVAL_DAY_TO_MINUTE,
				 T_INTERVAL_DAY_TO_SECOND,
				 T_INTERVAL_HOUR,
				 T_INTERVAL_HOUR_TO_MINUTE,
				 T_INTERVAL_HOUR_TO_SECOND,
				 T_INTERVAL_MINUTE,
				 T_INTERVAL_MINUTE_TO_SECOND,
				 T_INTERVAL_SECOND,
				 T_PERIOD_DATE,
				 T_PERIOD_TIME,
				 T_PERIOD_TIME_WITH_TIME_ZONE,
				 T_PERIOD_TIMESTAMP,
				 T_PERIOD_TIMESTAMP_WITH_TIME_ZONE:
				continue

			case T_BYTE,
				 T_VARBYTE,
				 T_LONG_VARBYTE:
				msg := fmt.Sprintf ("Cannot FastExport because a data type of %v (%v) in column %v of the selected table %v.%v is not supported when exporting to a csv file",
					aColMetadata [i].M_sTypeName,
					aColMetadata [i].M_uCookedDataType,
					i + 1,
					aColMetadata [i].M_sDatabaseName,
					aColMetadata [i].M_sObjectName)

				err = fxpCSVManager.m_appCon.makeDriverError (nil, 533, msg)
				return


			default:
				msg := fmt.Sprintf ("Cannot FastExport because a data type of %v (%v) in column %v of the selected table %v.%v is not supported",
					aColMetadata [i].M_sTypeName,
					aColMetadata [i].M_uCookedDataType,
					i + 1,
					aColMetadata [i].M_sDatabaseName,
					aColMetadata [i].M_sObjectName)

				if fxpCSVManager.m_appRows.m_bTryFastExport {
					sWarning = fxpCSVManager.m_appCon.makeDriverWarning (535, msg)
					fxpCSVManager.m_asWarnings = append (fxpCSVManager.m_asWarnings, sWarning)

				} else {
					err = fxpCSVManager.m_appCon.makeDriverError (nil, 533, msg)
				}

				return
		}
	}
	return

} // end method isTeradataTypeSupported

func (fxpCSVManager *fastExportManagerCSV) connectFastExport (ctx context.Context) (err error) {

	if fxpCSVManager.m_appCon.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter connectFastExport m_bConnected=%v m_controlSession=%v m_fastexportCon=%v",
			fxpCSVManager.m_bConnected, fxpCSVManager.m_controlSession, fxpCSVManager.m_fastexportCon))
		defer func () { traceLog (fmt.Sprintf ("< leave connectFastExport m_bConnected=%v m_controlSession=%v m_fastexportCon=%v",
			fxpCSVManager.m_bConnected, fxpCSVManager.m_controlSession, fxpCSVManager.m_fastexportCon)) } ()
	}

	if ! fxpCSVManager.m_bConnected {
		defer func () {
			if err != nil {
				err = chainErrors (err, fxpCSVManager.closeFastExport (ctx))
			}
		} ()

		requestedSessions := fxpCSVManager.m_appRows.m_uSessions

		fxpCSVManager.m_bConnected = true // to force close of resources created below

		mapDelegatedParams := fxpCSVManager.m_appCon.m_params.getDelegatedConParams ()

		sControlParams, err := mapStringStringAsJSON (mapStringStringAddAll (mapDelegatedParams, map [string] string {
			"tmode"            : "TERA",
			"connect_function" : "1", // 1=allocate LSN
		}))
		if err != nil { return err }

		controlParams, err := ParseConParams (sControlParams)
		if err != nil { return err }

		fxpCSVManager.m_controlSession, err = newTeradataConnection (controlParams)
		if err != nil { return err }

		fxpCSVManager.m_sBeginFastExport = "BEGIN FASTEXPORT"
		if fxpCSVManager.m_controlSession.m_config.M_uFastExportNoSpoolSupport >= 1 {
			fxpCSVManager.m_sBeginFastExport += " WITH NO SPOOL"
		}

		sControlParamLSN := format10Uint32 (fxpCSVManager.m_controlSession.m_uLogonSequenceNumber)

		permittedSessions, err := fxpCSVManager.getSessionsCount (requestedSessions, ctx)
		if err != nil { return err }

		sFastExportParams, err := mapStringStringAsJSON (mapStringStringAddAll (mapDelegatedParams, map [string] string {
			"partition"             : "export",
			"connect_function"      : "2", // 2=associate LSN
			"logon_sequence_number" : sControlParamLSN,
		}))
		if err != nil { return err }

		fastexportParams, err := ParseConParams (sFastExportParams)
		if err != nil { return err }

		fxpCSVManager.m_fastexportCon = make ([] *TeradataConnection, permittedSessions)
		for i := 0 ; i < len (fxpCSVManager.m_fastexportCon); i++ {
			fxpCSVManager.m_fastexportCon [i], err = newTeradataConnection (fastexportParams)
			if err != nil { return err }
			fxpCSVManager.m_fastexportCon [i].m_mapRequestToManager = fxpCSVManager.m_appCon.m_mapRequestToManager
		}
		fxpCSVManager.m_fastexportRows     = make ([] *TeradataRows, permittedSessions)
		fxpCSVManager.m_fastexportRowsSkip = make ([] bool         , permittedSessions)
	}

	return

} // end method connectFastExport

func (fxpCSVManager *fastExportManagerCSV) closeFastExport (ctx context.Context) (err error) {

	if fxpCSVManager.m_appCon.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter closeFastExport m_bConnected=%v m_controlSession=%v m_fastexportCon=%v",
			fxpCSVManager.m_bConnected, fxpCSVManager.m_controlSession, fxpCSVManager.m_fastexportCon))
		defer func () { traceLog (fmt.Sprintf ("< leave closeFastExport m_bConnected=%v m_controlSession=%v m_fastexportCon=%v",
			fxpCSVManager.m_bConnected, fxpCSVManager.m_controlSession, fxpCSVManager.m_fastexportCon)) } ()
	}

	if fxpCSVManager.m_bConnected {
		defer func () {
			fxpCSVManager.m_bConnected = false
			fxpCSVManager.m_fastexportCon = nil
			fxpCSVManager.m_controlSession = nil
		} ()

		err = fxpCSVManager.endFastExport (ctx)

		if fxpCSVManager.m_controlRows != nil && fxpCSVManager.m_fastexportCon != nil {

			sKey := strings.TrimSpace (fxpCSVManager.m_controlRows.m_sNativeSQL)

			for i := 0 ; i < len (fxpCSVManager.m_fastexportCon); i++ {
				if fxpCSVManager.m_fastexportCon [i] != nil {
					if fxpCSVManager.m_fastexportCon [i].m_mapRequestToManager != nil {
						delete (fxpCSVManager.m_fastexportCon [i].m_mapRequestToManager, sKey)
					}
					err = chainErrors (err, fxpCSVManager.m_fastexportCon [i].Close ())
				}
			}
		}

		if fxpCSVManager.m_controlSession != nil {
			err = chainErrors (err, fxpCSVManager.m_controlSession.Close ())
		}
	}

	return

} // end method closeFastExport

func (fxpCSVManager *fastExportManagerCSV) executeSelect (aBindValues [] driver.NamedValue, ctx context.Context) (err error) {

	if fxpCSVManager.m_appCon.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter executeSelect len(m_asWarnings)=%v %v",
			len (fxpCSVManager.m_asWarnings), fxpCSVManager))
		defer func () { traceLog (fmt.Sprintf ("< leave executeSelect len(m_asWarnings)=%v %v",
			len (fxpCSVManager.m_asWarnings), fxpCSVManager)) } ()
	}

	err = fxpCSVManager.connectFastExport (ctx)
	if err != nil { return }

	err = fxpCSVManager.beginFastExport (aBindValues, ctx)
	if err != nil { return }

	err = fxpCSVManager.writeExportedData (ctx)
	if err != nil { return }

	return

} // end method executeSelect

func (fxpCSVManager *fastExportManagerCSV) writeExportedData (ctx context.Context) (err error) { // Change name to exportData

	if fxpCSVManager.m_appCon.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter writeExportedData %v", fxpCSVManager))
		defer func () { traceLog (fmt.Sprintf ("< leave writeExportedData %v err=%v", fxpCSVManager, err)) } ()
	}

	for {

		err = func () (err error) {
			fxpCSVManager.initStatementBlock ()
			fxpCSVManager.initBindMetadata ()

			sExportPathName := fxpCSVManager.m_controlRows.getExportFilePathName (fxpCSVManager.m_sExportPathName, fxpCSVManager.m_nColumnMetadataIndex)

			fxpCSVManager.m_fileExporter, err = fxpCSVManager.m_controlRows.createFileExporter (sExportPathName,  fxpCSVManager.m_asColumnNames [fxpCSVManager.m_nColumnMetadataIndex], fxpCSVManager.m_appRows.m_sFieldSeparator, fxpCSVManager.m_appRows.m_sFieldQuote)
			if err != nil {
				err = chainErrors (err, fxpCSVManager.closeFastExport (ctx))
				return
			}

			defer func () {
				fxpCSVManager.m_fileExporter.close ()
				fxpCSVManager.m_fileExporter = nil
				fxpCSVManager.m_appRows.m_fileExporter = nil
			} ()

			for fxpCSVManager.m_uBlkNumberReceive <= fxpCSVManager.m_uBlkCount && ! fxpCSVManager.finishedNoSpoolMode () {

				err = fxpCSVManager.requestResults (ctx)
				if err != nil { return }

				err = fxpCSVManager.receiveResults (ctx)
				if err != nil { return }
			}

			if fxpCSVManager.finishedNoSpoolMode () {
				if fxpCSVManager.m_appCon.hasDebugLog () { debugLog ("writeExportedData finished NoSpool mode, loop condition EOF") }
				return io.EOF
			}

			fxpCSVManager.m_controlRows.m_fastexportManager = fxpCSVManager
			err = fxpCSVManager.m_controlRows.NextResultSet ()
			if err != nil { return }

			return
		} ()

		if errors.Is (err, io.EOF) {
			if fxpCSVManager.m_appCon.hasDebugLog () { debugLog ("writeExportedData exiting loop due to EOF") }
			return nil
		}

		if err != nil {
			if fxpCSVManager.m_appCon.hasDebugLog () { debugLog (fmt.Sprintf ("writeExportedData exiting loop due to err=%v", err)) }
			return
		}
	} // end for loop

	return

} // end method writeExportedData

func (fxpCSVManager *fastExportManagerCSV) receiveResults (ctx context.Context) (err error) {

	fastexportConsToBeUsed := fxpCSVManager.fastexportConnectionCount ()

	if fxpCSVManager.m_appCon.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter receiveResults m_uStmtNumber=%v fastexportConsToBeUsed=%v m_fastexportRowsSkip=%v",
			fxpCSVManager.m_uStmtNumber, fastexportConsToBeUsed, fxpCSVManager.m_fastexportRowsSkip))
		defer func () { traceLog (fmt.Sprintf ("< leave receiveResults next m_uBlkNumberReceive=%v out of m_uBlkCount=%v m_fastexportRowsSkip=%v",
			fxpCSVManager.m_uBlkNumberReceive, fxpCSVManager.m_uBlkCount, fxpCSVManager.m_fastexportRowsSkip)) } ()
	}

	for i := 0 ; fxpCSVManager.m_uBlkNumberReceive <= fxpCSVManager.m_uBlkCount && i < fastexportConsToBeUsed ; i++ {

		if ! fxpCSVManager.m_fastexportRowsSkip [i] {

			if fxpCSVManager.m_appCon.hasDebugLog () {
				debugLog (fmt.Sprintf ("receiveResults i=%v m_uStmtNumber=%v m_uBlkNumberReceive=%v m_uBlkCount=%v len(m_aColumnMetadata)=%v m_aBindMetadata=%v",
					i, fxpCSVManager.m_uStmtNumber, fxpCSVManager.m_uBlkNumberReceive, fxpCSVManager.m_uBlkCount, len (fxpCSVManager.m_controlRows.m_aColumnMetadata), fxpCSVManager.m_aBindMetadata))
			}

			var fxpRows driver.Rows
			fxpRows, err = fxpCSVManager.m_fastexportCon [i].QueryContext (ctx, fmt.Sprintf ("{fn teradata_provide(request_scope_receive_only)}{fn teradata_rpo(E)}{fn teradata_write_csv(%v)}{fn teradata_set_file_exporter(%v)}", fxpCSVManager.m_appRows.m_sExportPathName, fxpCSVManager.m_appRows.m_sNativeSQL), fxpCSVManager.m_aBindMetadata)
			if err != nil {
				if fxpCSVManager.m_noSpoolEnabled {
					errCode := GetErrorCode (err)
					if errCode == 9112 { // No more FastExport blocks on the session.
						fxpCSVManager.m_fastexportRowsSkip [i] = true
						fxpCSVManager.m_uBlkNumberReceive++
						err = nil
						continue
					}
				}
				return
			}

			fxpCSVManager.m_fastexportRows [i] = fxpRows.(*TeradataRows)
			fxpCSVManager.m_appRows.m_uActivityCount += fxpCSVManager.m_fastexportRows [i].m_uActivityCount
			fxpCSVManager.m_uBlkNumberReceive++
		}
	}

	return

} // end method receiveResults

func (fxpCSVManager *fastExportManagerCSV) nextExportResultSet (ctx context.Context) (err error) {

	fastexportConsToBeUsed := fxpCSVManager.fastexportConnectionCount ()

	if fxpCSVManager.m_appCon.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter nextExportResultSet fastexportConsToBeUsed=%v", fastexportConsToBeUsed))
		defer func () { traceLog (fmt.Sprintf ("< leave nextExportResultSet fastexportConsToBeUsed=%v err=%v", fastexportConsToBeUsed, err)) } ()
	}

	// Call copyColMDToControlRows here, because after m_nColumnMetadataIndex gets incremented, the copy won't happen.
	fxpCSVManager.copyColMDToControlRows ()

	fxpCSVManager.m_nColumnMetadataIndex++

	if fxpCSVManager.finishedNoSpoolMode () {
		if fxpCSVManager.m_appCon.hasDebugLog () { debugLog ("nextExportResultSet finished NoSpool mode, returning EOF") }
		return io.EOF
	}

	for i := 0 ; i < fastexportConsToBeUsed ; i++ {
		err = fxpCSVManager.m_fastexportRows [i].consumeCurrentResultSet ()
		if err != nil { return }
	}

	err = fxpCSVManager.receiveResults (ctx)
	if err != nil { return }

	for i := 0 ; i < fastexportConsToBeUsed ; i++ {
		err = fxpCSVManager.m_fastexportRows [i].consumeCurrentResultSet ()
		if err != nil { return }
	}

	return

} // end method nextExportResultSet
