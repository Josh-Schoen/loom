// Copyright 2021-2022 by Teradata Corporation. All Rights Reserved.
// TERADATA CORPORATION CONFIDENTIAL AND TRADE SECRET

package teradata

import (
	"context"
	"database/sql/driver"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"reflect"
	"strings"
)

const DEFAULT_FASTEXPORT_MAX_SESSION_COUNT = 8

type fastExportManagerBase struct {

	m_appRows                   *TeradataRows // parent rows and link to parent connection
	m_appCon                    *TeradataConnection // parent connection
	m_preparedRows              *TeradataRows // prepared rows needed to hold column (parameter) metadata
	m_nColumnMetadataIndex      int // an index into the array of column metadata
	m_aColumnMetadata           [][] columnMetadata
	m_asColumnNames             [][] string
	m_atColumnScanTypes         [][] reflect.Type
	m_controlSession            *TeradataConnection // DBC/SQL partition TERA connection needed to begin and end FastExport
	m_controlRows               *TeradataRows // DBC/SQL partition rows needed for FastExport
	m_fastexportCon             [] *TeradataConnection // array of FastExport partition connections needed to receive data
	m_fastexportRows            [] *TeradataRows // array of FastExport partition rows needed to receive data
	m_fastexportRowsSkip        [] bool // array of bool to indicate whether to skip FastExport partition rows needed to receive data
	m_aBindMetadata             [] driver.NamedValue // the metadata of the rows selected
	m_nFastExportRowsIndex      int // an index into the array of FastExport partition rows
	m_asWarnings                [] string // any warning in GoSQL FastExport
	m_bWarningsAtNew            bool // warnings while creating an instance of fastExportManager
	m_sBeginFastExport          string // the BEGIN FASTEXPORT statement to be used with FastExport
	m_sSelectStmt               string // the SELECT statement to be used with FastExport
	m_bConnected                bool // indicates whether connections needed to FastExport were created
	m_bFastexporting            bool // indicates whether FastExport has begun
	m_noSpoolEnabled            bool // "WITH NO SPOOL" is not guaranteed. The DBS decides when the SELECT statement is executed.
	m_uStmtNumber               uint32 // statement number of current SELECT
	m_uBlkNumberSend            uint64 // block number sent
	m_uBlkNumberReceive         uint64 // block number received
	m_uBlkCount                 uint64 // block count for current SELECT
	m_fileExporter              fileExporter // Used to export file names
	m_sExportPathName           string // File Exporter Path Name
	m_nFileExporterType         int // File Exporter Type

} // end type fastExportManager

// Note: This pointer-receiver method will not work with a value inside an interface, which we do not expect or intend.

func (fxpManager *fastExportManagerBase) String () string { // satisfies fmt.Stringer

	return fmt.Sprintf ("fxpManager=%p key=%v %v", fxpManager, fxpManager.m_appRows.m_sNativeSQL, fxpManager.m_appRows)

} // end method String

func (fxpManager *fastExportManagerBase) Value () fastExportManager {

	return fxpManager

} // end method Value

func newFastExportManager (appRows *TeradataRows, ctx context.Context) (fastExportManager, error) {

	var err error
	var sWarning string

	fxpManager := &fastExportManagerBase {
		m_appRows           : appRows,
		m_appCon            : appRows.m_stmt.m_con,
	}

	if fxpManager.m_appCon.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter newFastExportManager %v", fxpManager.m_appRows))
		defer func () { traceLog (fmt.Sprintf ("< leave newFastExportManager %v sWarning=%v", fxpManager, sWarning)) } ()
	}

	fxpManager.m_preparedRows, err = fxpManager.prepareInsert (ctx)
	if err != nil { return fxpManager, err }

	fxpManager.m_aColumnMetadata   = make ([][] columnMetadata, 0)
	fxpManager.m_asColumnNames     = make ([][] string        , 0)
	fxpManager.m_atColumnScanTypes = make ([][] reflect.Type  , 0)

	fxpManager.m_sSelectStmt = fxpManager.m_appRows.m_sNativeSQL

	if fxpManager.m_appRows.m_sExportPathName != "" && fxpManager.m_appRows.m_nFileExporterType == CSV_FILE_TYPE {
		fxpManager.m_sExportPathName = fxpManager.m_appRows.m_sExportPathName
		return newFastExportManagerCSV (fxpManager)
	}

	for {
		if fxpManager.m_preparedRows.m_uActivityType != 1 { // 1=SELECT
			msg := "Cannot FastExport because statement is not a SELECT"
			if fxpManager.m_appRows.m_bTryFastExport {
				sWarning = fxpManager.m_appCon.makeDriverWarning (534, msg)
				fxpManager.m_asWarnings = append (fxpManager.m_asWarnings, sWarning)
			} else {
				err = fxpManager.m_appCon.makeDriverError (nil, 532, msg)
			}
			return fxpManager, err
		}

		err = fxpManager.isTeradataTypeSupported ()
		if err != nil { return fxpManager, err }

		fxpManager.m_aColumnMetadata = append (fxpManager.m_aColumnMetadata, fxpManager.m_preparedRows.m_aColumnMetadata)
		fxpManager.m_asColumnNames = append (fxpManager.m_asColumnNames, fxpManager.m_preparedRows.m_asColumnNames)
		fxpManager.m_atColumnScanTypes = append (fxpManager.m_atColumnScanTypes, fxpManager.m_preparedRows.m_atColumnScanTypes)

		err = fxpManager.m_preparedRows.NextResultSet ()
		if err == nil {
			continue
		} else if errors.Is (err, io.EOF) {
			err = nil
			break
		} else {
			return fxpManager, err
		}
	}

	return fxpManager, err

} // end func newFastExportManager

func (fxpManager *fastExportManagerBase) prepareInsert (ctx context.Context) (rows *TeradataRows, err error) {

	sQuery := "{fn teradata_rpo(S)}" + fxpManager.m_appRows.m_sNativeSQL // 'S'=PrepareParameterizedSQL

	if fxpManager.m_appCon.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter prepareInsert sQuery=%v", sQuery))
		defer func () { traceLog (fmt.Sprintf ("< leave prepareInsert sQuery=%v %v", sQuery, rows)) } ()
	}

	stmt, err := newTeradataStatement (fxpManager.m_appCon, ctx, sQuery)
	if err != nil { return }

	rows, err = newTeradataRows (stmt, ctx, nil, true)
	if err != nil { return }

	err = rows.Close ()
	if err != nil { return }

	return

} // end method prepareInsert

func (fxpManager *fastExportManagerBase) isTeradataTypeSupported () (err error) {

	var sWarning string

	if fxpManager.m_appCon.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter isTeradataTypeSupported len(m_aColumnMetadata)=%v m_atColumnScanTypes=%v",
			len (fxpManager.m_preparedRows.m_aColumnMetadata), fxpManager.m_preparedRows.m_atColumnScanTypes))
		defer func () { traceLog (fmt.Sprintf ("< leave isTeradataTypeSupported sWarning=%v", sWarning)) } ()
	}

	aColMetadata := fxpManager.m_preparedRows.m_aColumnMetadata
	for i := range aColMetadata {
		switch aColMetadata [i].M_uCookedDataType {
			case T_BIGINT,
				 T_BYTEINT,
				 T_DECIMAL,
				 T_FLOAT,
				 T_INTEGER,
				 T_NUMBER,
				 T_SMALLINT,
				 T_BYTE,
				 T_VARBYTE,
				 T_LONG_VARBYTE,
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

			default:
				msg := fmt.Sprintf ("Cannot FastExport because a data type of %v (%v) in column %v of the selected table %v.%v is not supported",
					aColMetadata [i].M_sTypeName,
					aColMetadata [i].M_uCookedDataType,
					i + 1,
					aColMetadata [i].M_sDatabaseName,
					aColMetadata [i].M_sObjectName)

				if fxpManager.m_appRows.m_bTryFastExport {
					sWarning = fxpManager.m_appCon.makeDriverWarning (535, msg)
					fxpManager.m_asWarnings = append (fxpManager.m_asWarnings, sWarning)

				} else {
					err = fxpManager.m_appCon.makeDriverError (nil, 533, msg)
				}

				return
		}
	}
	return

} // end method isTeradataTypeSupported

func (fxpManager *fastExportManagerBase) getSessionsCount (requestedSessions uint32, ctx context.Context) (permittedSessions uint32, err error) {

	var sWarning string
	ampCnt := fxpManager.m_controlSession.m_config.M_uAMPCount

	if fxpManager.m_appCon.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter getSessionsCount ampCnt=%v requestedSessions=%v", ampCnt, requestedSessions))
		defer func () { traceLog (fmt.Sprintf ("< leave getSessionsCount permittedSessions=%v sWarning=%v", permittedSessions, sWarning)) } ()
	}

	result, err := executeHelpSession (fxpManager.m_appCon, ctx)
	if err != nil { err = chainErrors (err, fxpManager.closeFastExport (ctx)); return 0, err }

	sCurrentDatabase    := getCurrentDatabaseFromHelpSessionResult (result)
	queryBandArray      := getQueryBandFromHelpSessionResult (result)
	bUnicodePassThrough := getUnicodePassThroughFromHelpSessionResult (result)

	if bUnicodePassThrough {
		sSetUnicodePassThrough := "{fn teradata_rpo(E)}SET SESSION CHARACTER SET UNICODE PASS THROUGH ON"
		_, err = fxpManager.m_controlSession.ExecContext (ctx, sSetUnicodePassThrough, nil)
		if err != nil { err = chainErrors (err, fxpManager.closeFastExport (ctx)); return 0, err }
	}

	if len (sCurrentDatabase) > 0 {
		sDatabaseCommand := fmt.Sprintf ("{fn teradata_rpo(E)}DATABASE \"%v\"", strings.ReplaceAll (sCurrentDatabase, "\"", "\"\""))
		_, err = fxpManager.m_controlSession.ExecContext (ctx, sDatabaseCommand, nil)
		if err != nil { err = chainErrors (err, fxpManager.closeFastExport (ctx)); return 0, err }
	}

	bUseCheckWorkload := fxpManager.m_appCon.m_config.M_uCheckWorkloadSupport >= 1 && (fxpManager.m_appRows.m_bIsGoverned || fxpManager.m_appCon.m_config.M_uTDWMFailFastSupport >= 1)
	if bUseCheckWorkload {

		sSetQueryBand := "{fn teradata_rpo(E)}SET QUERY_BAND='UtilityName=JDBCE;' FOR SESSION"

		_, err = fxpManager.m_controlSession.ExecContext (ctx, sSetQueryBand, nil)
		if err != nil { err = chainErrors (err, fxpManager.closeFastExport (ctx)); return 0, err }

		if len (queryBandArray [QUERY_BAND_SESSION_INDEX]) > 0 {
			updateQueryband := fmt.Sprintf ("{fn teradata_rpo(E)}SET QUERY_BAND='%v' UPDATE FOR SESSION", queryBandArray [QUERY_BAND_SESSION_INDEX])
			_, err = fxpManager.m_controlSession.ExecContext (ctx, updateQueryband, nil)
			if err != nil { err = chainErrors (err, fxpManager.closeFastExport (ctx)); return 0, err}
		}

		_, err = fxpManager.m_controlSession.ExecContext (ctx, "{fn teradata_rpo(E)}CHECK WORKLOAD FOR " + fxpManager.m_sBeginFastExport, nil)
		if err != nil { err = chainErrors (err, fxpManager.closeFastExport (ctx)); return 0, err }

		_, err = fxpManager.m_controlSession.ExecContext (ctx, "{fn teradata_rpo(E)}CHECK WORKLOAD FOR " + fxpManager.m_sSelectStmt, nil)
		if err != nil { err = chainErrors (err, fxpManager.closeFastExport (ctx)); return 0, err }

		checkWorkloadEnd := "{fn teradata_values_off}CHECK WORKLOAD END"
		if ! fxpManager.m_appRows.m_bIsGoverned {
			checkWorkloadEnd = "{fn teradata_failfast}" + checkWorkloadEnd // "TERADATA_FAILFAST" is only supported in TD 14.10 or higher
		}

		var rows driver.Rows
		rows, err = fxpManager.m_controlSession.QueryContext (ctx, checkWorkloadEnd, nil)
		if err != nil { err = chainErrors (err, fxpManager.closeFastExport (ctx)); return 0, err }

		asColumnNames := rows.Columns ()
		dest := make ([] driver.Value, len (asColumnNames))
		rows.Next (dest)

		if len (asColumnNames) >= 2 && asColumnNames [1] != "" {
			workloadEnabled := strings.TrimSpace (dest [0].(string))

			if strings.EqualFold (workloadEnabled, "Y") {
				maxSessions := uint32 (dest [1].(int32))

				if maxSessions > 0 {
					if requestedSessions == 0 {
						permittedSessions = maxSessions
					} else {
						permittedSessions = minUint32 (requestedSessions, maxSessions)
					}

					if permittedSessions < requestedSessions {
						sWarning = fxpManager.m_controlSession.makeDriverWarning (569,
							fmt.Sprintf ("Requested SESSIONS=%v was overridden by a Workload Management rule as a session count of %v for statement %v",
								requestedSessions,
								permittedSessions,
								fxpManager.m_sBeginFastExport))

						fxpManager.m_asWarnings = append (fxpManager.m_asWarnings, sWarning)
					}

					if fxpManager.m_appCon.hasTraceLog () {
						traceLog (fmt.Sprintf("Permitted FastExport sessions count: %v", permittedSessions))
					}

					return

				} else {
					if fxpManager.m_appCon.hasTraceLog () {
						traceLog ("FastExport received no valid SESSIONS count from " + checkWorkloadEnd + ", thus assuming default SESSIONS count.")
					}
				}
			} else {
				if fxpManager.m_appCon.hasTraceLog () {
					traceLog (fmt.Sprintf ("FastExport was told to assume default SESSIONS count by %v.", checkWorkloadEnd))
				}
			}
		}
	} // end if bUseCheckWorkload

	// At this point, CHECK WORKLOAD is assumed to be unsupported, disabled, or to have returned an invalid sessions value.
	// Thus, the database system AMP count is used to determine or verify the sessions value.
	if (requestedSessions == 0) {
		permittedSessions = minUint32 (DEFAULT_FASTEXPORT_MAX_SESSION_COUNT, uint32 (ampCnt))

	} else {
		permittedSessions = minUint32 (requestedSessions, uint32 (ampCnt))

		if (permittedSessions < requestedSessions) {
			sWarning = fxpManager.m_controlSession.makeDriverWarning (511,
				fmt.Sprintf ("Requested SESSIONS=%v was overridden by the Teradata Database AMP limit as a session count of %v for statement %v",
					requestedSessions,
					permittedSessions,
					fxpManager.m_sSelectStmt))

			fxpManager.m_asWarnings = append (fxpManager.m_asWarnings, sWarning)
		}
	}

	return

} // end method getSessionsCount

func (fxpManager *fastExportManagerBase) connectFastExport (ctx context.Context) (err error) {

	if fxpManager.m_appCon.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter connectFastExport m_bConnected=%v m_controlSession=%v m_fastexportCon=%v",
			fxpManager.m_bConnected, fxpManager.m_controlSession, fxpManager.m_fastexportCon))
		defer func () { traceLog (fmt.Sprintf ("< leave connectFastExport m_bConnected=%v m_controlSession=%v m_fastexportCon=%v",
			fxpManager.m_bConnected, fxpManager.m_controlSession, fxpManager.m_fastexportCon)) } ()
	}

	if ! fxpManager.m_bConnected {
		defer func () {
			if err != nil {
				err = chainErrors (err, fxpManager.closeFastExport (ctx))
			}
		} ()

		requestedSessions := fxpManager.m_appRows.m_uSessions

		fxpManager.m_bConnected = true // to force close of resources created below

		mapDelegatedParams := fxpManager.m_appCon.m_params.getDelegatedConParams ()

		sControlParams, err := mapStringStringAsJSON (mapStringStringAddAll (mapDelegatedParams, map [string] string {
			"application_esn"  : format10Uint32 (fxpManager.m_appCon.m_uSessionNumber),
			"tmode"            : "TERA",
			"connect_function" : "1", // 1=allocate LSN
		}))
		if err != nil { return err }

		controlParams, err := ParseConParams (sControlParams)
		if err != nil { return err }

		fxpManager.m_controlSession, err = newTeradataConnection (controlParams)
		if err != nil { return err }

		uDatabaseLSN, err := fxpManager.m_controlSession.queryDatabaseLSN (ctx)
		if err != nil { return err }

		fxpManager.m_sBeginFastExport = "BEGIN FASTEXPORT"
		if fxpManager.m_controlSession.m_config.M_uFastExportNoSpoolSupport >= 1 {
			fxpManager.m_sBeginFastExport += " WITH NO SPOOL"
		}

		sControlParamLSN := format10Uint32 (fxpManager.m_controlSession.m_uLogonSequenceNumber)

		permittedSessions, err := fxpManager.getSessionsCount (requestedSessions, ctx)
		if err != nil { return err }

		sFastExportParams, err := mapStringStringAsJSON (mapStringStringAddAll (mapDelegatedParams, map [string] string {
			"application_esn"       : format10Uint32 (fxpManager.m_appCon.m_uSessionNumber),
			"control_esn"           : format10Uint32 (fxpManager.m_controlSession.m_uSessionNumber),
			"database_lsn"          : format10Uint32 (uDatabaseLSN),
			"partition"             : "export",
			"connect_function"      : "2", // 2=associate LSN
			"logon_sequence_number" : sControlParamLSN,
		}))
		if err != nil { return err }

		fastexportParams, err := ParseConParams (sFastExportParams)
		if err != nil { return err }

		fxpManager.m_fastexportCon = make ([] *TeradataConnection, permittedSessions)
		for i := 0 ; i < len (fxpManager.m_fastexportCon); i++ {
			fxpManager.m_fastexportCon [i], err = newTeradataConnection (fastexportParams)
			if err != nil { return err }
		}
		fxpManager.m_fastexportRows     = make ([] *TeradataRows, permittedSessions)
		fxpManager.m_fastexportRowsSkip = make ([] bool         , permittedSessions)
	}

	return

} // end method connectFastExport

func (fxpManager *fastExportManagerBase) closeFastExport (ctx context.Context) (err error) {

	if fxpManager.m_appCon.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter closeFastExport m_bConnected=%v m_controlSession=%v m_fastexportCon=%v",
			fxpManager.m_bConnected, fxpManager.m_controlSession, fxpManager.m_fastexportCon))
		defer func () { traceLog (fmt.Sprintf ("< leave closeFastExport m_bConnected=%v m_controlSession=%v m_fastexportCon=%v",
			fxpManager.m_bConnected, fxpManager.m_controlSession, fxpManager.m_fastexportCon)) } ()
	}

	if fxpManager.m_bConnected {
		defer func () {
			fxpManager.m_bConnected = false
			fxpManager.m_fastexportCon = nil
			fxpManager.m_controlSession = nil
		} ()

		err = fxpManager.endFastExport (ctx)

		if fxpManager.m_fastexportCon != nil {
			for i := 0 ; i < len (fxpManager.m_fastexportCon); i++ {
				if fxpManager.m_fastexportCon [i] != nil {
					err = chainErrors (err, fxpManager.m_fastexportCon [i].Close ())
				}
			}
		}

		if fxpManager.m_controlSession != nil {
			err = chainErrors (err, fxpManager.m_controlSession.Close ())
		}
	}

	return

} // end method closeFastExport

func (fxpManager *fastExportManagerBase) beginFastExport (aBindValues [] driver.NamedValue, ctx context.Context) (err error) {

	if fxpManager.m_appCon.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter beginFastExport m_bConnected=%v m_bFastexporting=%v m_sBeginFastExport=%v",
			fxpManager.m_bConnected, fxpManager.m_bFastexporting, fxpManager.m_sBeginFastExport))
		defer func () { traceLog (fmt.Sprintf ("< leave beginFastExport m_bConnected=%v m_bFastexporting=%v",
			fxpManager.m_bConnected, fxpManager.m_bFastexporting)) } ()
	}

	if fxpManager.m_bConnected && ! fxpManager.m_bFastexporting {
		_, err = fxpManager.m_controlSession.ExecContext (ctx, "{fn teradata_rpo(E)}" + fxpManager.m_sBeginFastExport, nil)
		if err != nil { return }

		request := "{fn teradata_rpo(E)}{fn teradata_lobselect(S)}" + fxpManager.m_sSelectStmt
		if fxpManager.m_appRows.m_bFakeResultSets {
			request = "{fn teradata_fake_result_sets}"+request
		}

		var ctlRows driver.Rows
		ctlRows, err = fxpManager.m_controlSession.QueryContext (ctx, request, aBindValues)
		if err != nil { return }

		defer ctlRows.Close ()

		fxpManager.m_controlRows = ctlRows.(*TeradataRows)
		fxpManager.m_bFastexporting = true
	}

	return

} // end method beginFastExport

func (fxpManager *fastExportManagerBase) endFastExport (ctx context.Context) (err error) {

	if fxpManager.m_appCon.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter endFastExport m_bConnected=%v m_bFastexporting=%v len(m_asWarnings)=%v",
			fxpManager.m_bConnected, fxpManager.m_bFastexporting, len (fxpManager.m_asWarnings)))
		defer func () { traceLog (fmt.Sprintf ("< leave endFastExport m_bConnected=%v m_bFastexporting=%v len(m_asWarnings)=%v",
			fxpManager.m_bConnected, fxpManager.m_bFastexporting, len (fxpManager.m_asWarnings))) } ()
	}

	if fxpManager.m_bConnected && fxpManager.m_bFastexporting {
		defer func () {
			fxpManager.m_bFastexporting = false
		} ()

		_, err := fxpManager.m_controlSession.ExecContext (ctx, "{fn teradata_rpo(E)}END FASTEXPORT", nil)
		if err != nil { return err }
	}

	return

} // end method endFastExport

func (fxpManager *fastExportManagerBase) executeSelect (aBindValues [] driver.NamedValue, ctx context.Context) (err error) {

	if fxpManager.m_appCon.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter executeSelect len(m_asWarnings)=%v %v",
			len (fxpManager.m_asWarnings), fxpManager))
		defer func () { traceLog (fmt.Sprintf ("< leave executeSelect len(m_asWarnings)=%v %v",
			len (fxpManager.m_asWarnings), fxpManager)) } ()
	}

	err = fxpManager.connectFastExport (ctx)
	if err != nil { return }

	err = fxpManager.beginFastExport (aBindValues, ctx)
	if err != nil { return }

	fxpManager.initStatementBlock ()
	fxpManager.initBindMetadata ()

	err = fxpManager.requestResults (ctx)
	if err != nil { return }

	err = fxpManager.receiveResults (ctx)
	if err != nil { return }

	err = fxpManager.requestResults (ctx)
	if err != nil { return }

	return

} // end method executeSelect

func (fxpManager *fastExportManagerBase) initStatementBlock () {

	if fxpManager.m_appCon.hasTraceLog () {
		traceLog ("> enter initStatementBlock")
		defer func () { traceLog (fmt.Sprintf ("< leave initStatementBlock m_nFastExportRowsIndex=%v m_noSpoolEnabled=%v m_uStmtNumber=%v m_uBlkNumberSend=%v, m_uBlkNumberReceive=%v m_uBlkCount=%v",
			fxpManager.m_nFastExportRowsIndex, fxpManager.m_noSpoolEnabled, fxpManager.m_uStmtNumber, fxpManager.m_uBlkNumberSend, fxpManager.m_uBlkNumberReceive, fxpManager.m_uBlkCount)) } ()
	}

	fxpManager.m_nFastExportRowsIndex = 0
	// FastExport WITH NO SPOOL is only permitted on a single-statement SELECT with no ORDER BY clause.
	fxpManager.m_noSpoolEnabled = fxpManager.m_controlRows.m_uActivityType == 196
	for i := 0 ; i < len (fxpManager.m_fastexportCon) ; i++ {
		fxpManager.m_fastexportCon [i].m_nFastExportReqMsgLen = func () (int) { if fxpManager.m_noSpoolEnabled { return 4 } ; return 8 } ()
	}

	fxpManager.m_uStmtNumber       = fxpManager.m_controlRows.m_uStatementNumber
	fxpManager.m_uBlkNumberSend    = 1
	fxpManager.m_uBlkNumberReceive = 1

	if fxpManager.m_noSpoolEnabled {
		fxpManager.m_uBlkCount = math.MaxInt64 - 1
	} else {
		fxpManager.m_uBlkCount = fxpManager.m_controlRows.m_uActivityCount
	}

} // end initStatementBlock

func (fxpManager *fastExportManagerBase) initBindMetadata () {

	if fxpManager.m_appCon.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter initBindMetadata m_controlRows=%v", fxpManager.m_controlRows))
		defer func () { traceLog (fmt.Sprintf ("< leave initBindMetadata len(m_aColumnMetadata)=%v m_aBindMetadata=%v",
			len (fxpManager.m_controlRows.m_aColumnMetadata), fxpManager.m_aBindMetadata)) } ()
	}

	uaRawDataType := make ([] uint16, len (fxpManager.m_controlRows.m_aColumnMetadata))
	uaByteCount   := make ([] uint64, len (fxpManager.m_controlRows.m_aColumnMetadata))
	uaScale       := make ([] uint8 , len (fxpManager.m_controlRows.m_aColumnMetadata))

	for i := 0 ; i < len (fxpManager.m_controlRows.m_aColumnMetadata) ; i++ {
		uaRawDataType [i] = fxpManager.m_controlRows.m_aColumnMetadata [i].M_uRawDataType
		uaByteCount   [i] = fxpManager.m_controlRows.m_aColumnMetadata [i].M_uByteCount
		uaScale       [i] = fxpManager.m_controlRows.m_aColumnMetadata [i].M_uScale
	}

	fxpManager.m_aBindMetadata = nil
	fxpManager.m_aBindMetadata = append (fxpManager.m_aBindMetadata, driver.NamedValue{Ordinal:1, Value:uaRawDataType})
	fxpManager.m_aBindMetadata = append (fxpManager.m_aBindMetadata, driver.NamedValue{Ordinal:2, Value:uaByteCount})
	fxpManager.m_aBindMetadata = append (fxpManager.m_aBindMetadata, driver.NamedValue{Ordinal:3, Value:uaScale})

} // end initBindMetadata

func (fxpManager *fastExportManagerBase) fastexportConnectionCount () int {

	if int (fxpManager.m_uBlkCount) < len (fxpManager.m_fastexportCon) {
		return int (fxpManager.m_uBlkCount)
	}

	return len (fxpManager.m_fastexportCon)

} // end fastexportConnectionCount

func (fxpManager *fastExportManagerBase) finishedNoSpoolMode () (bOutput bool) {

	fastexportConsToBeUsed := fxpManager.fastexportConnectionCount ()

	if fxpManager.m_appCon.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter finishedNoSpoolMode fastexportConsToBeUsed=%v m_fastexportRowsSkip=%v", fastexportConsToBeUsed, fxpManager.m_fastexportRowsSkip))
		defer func () { traceLog (fmt.Sprintf ("< leave finishedNoSpoolMode fastexportConsToBeUsed=%v m_fastexportRowsSkip=%v", fastexportConsToBeUsed, fxpManager.m_fastexportRowsSkip)) } ()
	}

	nDoneCount := 0
	for i := 0 ; i < fastexportConsToBeUsed ; i++ {
		if fxpManager.m_fastexportRowsSkip [i] {
			nDoneCount++
		}
	}

	bOutput = nDoneCount == fastexportConsToBeUsed
	return

} // end method finishedNoSpoolMode

func (fxpManager *fastExportManagerBase) requestResults (ctx context.Context) (err error) {

	fastexportConsToBeUsed := fxpManager.fastexportConnectionCount ()

	if fxpManager.m_appCon.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter requestResults m_noSpoolEnabled=%v m_uStmtNumber=%v fastexportConsToBeUsed=%v m_fastexportRowsSkip=%v",
			fxpManager.m_noSpoolEnabled, fxpManager.m_uStmtNumber, fastexportConsToBeUsed, fxpManager.m_fastexportRowsSkip))
		defer func () { traceLog (fmt.Sprintf ("< leave requestResults next m_uBlkNumberSend=%v out of m_uBlkCount=%v m_fastexportRowsSkip=%v",
			fxpManager.m_uBlkNumberSend, fxpManager.m_uBlkCount, fxpManager.m_fastexportRowsSkip)) } ()
	}

	found := false
	for i := 0 ; fxpManager.m_uBlkNumberSend <= fxpManager.m_uBlkCount && i < fastexportConsToBeUsed ; i++ {

		if ! fxpManager.m_fastexportRowsSkip [i] {

			var abyOutput [] byte
			if fxpManager.m_noSpoolEnabled {
				abyOutput = make ([] byte, 4)
				binary.BigEndian.PutUint32 (abyOutput, uint32 (fxpManager.m_uStmtNumber))
			} else {
				abyOutput = make ([] byte, 8)
				binary.BigEndian.PutUint64 (abyOutput, (uint64 (fxpManager.m_uStmtNumber) << 32) + fxpManager.m_uBlkNumberSend)
			}
			stmtBlk := ""
			for i := 0 ; i < len (abyOutput) ; i++ {
				stmtBlk += fmt.Sprintf ("\\u%04x", string (abyOutput [i]))
			}

			if fxpManager.m_appCon.hasDebugLog () {
				debugLog (fmt.Sprintf ("requestResults i=%v m_uStmtNumber=%v m_uBlkNumberSend=%v m_uBlkCount=%v stmtBlk=%v",
					i, fxpManager.m_uStmtNumber, fxpManager.m_uBlkNumberSend, fxpManager.m_uBlkCount, stmtBlk))
			}

			_, err = fxpManager.m_fastexportCon [i].QueryContext (ctx, "{fn teradata_provide(request_scope_send_only)}{fn teradata_rpo(E)}" + string (abyOutput), nil)
			if err != nil { return }

			fxpManager.m_uBlkNumberSend++
			if !found {
				found = true
				fxpManager.m_nFastExportRowsIndex = i
			}
		}
	}

	return

} // end requestResults

func (fxpManager *fastExportManagerBase) receiveResults (ctx context.Context) (err error) {

	fastexportConsToBeUsed := fxpManager.fastexportConnectionCount ()

	if fxpManager.m_appCon.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter receiveResults m_uStmtNumber=%v fastexportConsToBeUsed=%v m_fastexportRowsSkip=%v",
			fxpManager.m_uStmtNumber, fastexportConsToBeUsed, fxpManager.m_fastexportRowsSkip))
		defer func () { traceLog (fmt.Sprintf ("< leave receiveResults next m_uBlkNumberReceive=%v out of m_uBlkCount=%v m_fastexportRowsSkip=%v",
			fxpManager.m_uBlkNumberReceive, fxpManager.m_uBlkCount, fxpManager.m_fastexportRowsSkip)) } ()
	}

	for i := 0 ; fxpManager.m_uBlkNumberReceive <= fxpManager.m_uBlkCount && i < fastexportConsToBeUsed ; i++ {

		if ! fxpManager.m_fastexportRowsSkip [i] {

			if fxpManager.m_appCon.hasDebugLog () {
				debugLog (fmt.Sprintf ("receiveResults i=%v m_uStmtNumber=%v m_uBlkNumberReceive=%v m_uBlkCount=%v len(m_aColumnMetadata)=%v m_aBindMetadata=%v",
					i, fxpManager.m_uStmtNumber, fxpManager.m_uBlkNumberReceive, fxpManager.m_uBlkCount, len (fxpManager.m_controlRows.m_aColumnMetadata), fxpManager.m_aBindMetadata))
			}

			var fxpRows driver.Rows
			fxpRows, err = fxpManager.m_fastexportCon [i].QueryContext (ctx, "{fn teradata_provide(request_scope_receive_only)}{fn teradata_rpo(E)}", fxpManager.m_aBindMetadata)
			if err != nil {
				if fxpManager.m_noSpoolEnabled {
					errCode := GetErrorCode (err)
					if errCode == 9112 { // No more FastExport blocks on the session.
						fxpManager.m_fastexportRowsSkip [i] = true
						fxpManager.m_uBlkNumberReceive++
						err = nil
						continue
					}
				}
				return
			}

			fxpManager.m_fastexportRows [i] = fxpRows.(*TeradataRows)
			fxpManager.m_appRows.m_uActivityCount += fxpManager.m_fastexportRows [i].m_uActivityCount
			fxpManager.m_uBlkNumberReceive++
		}
	}

	return

} // end receiveResults

func (fxpManager *fastExportManagerBase) getNextFastExportRows () (rows *TeradataRows) {

	fastexportConsToBeUsed := fxpManager.fastexportConnectionCount ()

	if fxpManager.m_appCon.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter getNextFastExportRows m_nFastExportRowsIndex=%v fastexportConsToBeUsed=%v m_fastexportRowsSkip=%v",
			fxpManager.m_nFastExportRowsIndex, fastexportConsToBeUsed, fxpManager.m_fastexportRowsSkip))
		defer func () { traceLog (fmt.Sprintf ("< leave getNextFastExportRows m_nFastExportRowsIndex=%v rows %v nil",
			fxpManager.m_nFastExportRowsIndex, func ()(string) { if rows == nil { return "==" } ; return "!=" } ())) } ()
	}

	for i := 0 ; i < fastexportConsToBeUsed ; i++ {
		fxpManager.m_nFastExportRowsIndex++
		if fxpManager.m_nFastExportRowsIndex >= fastexportConsToBeUsed {
			fxpManager.m_nFastExportRowsIndex = 0
		}
		if ! fxpManager.m_fastexportRowsSkip [fxpManager.m_nFastExportRowsIndex] {
			rows = fxpManager.m_fastexportRows [fxpManager.m_nFastExportRowsIndex]
			if rows != nil {
				return
			}
		}
	}

	return

} // end method getNextFastExportRows

func (fxpManager *fastExportManagerBase) copyColMDToControlRows () {

	if fxpManager.m_nColumnMetadataIndex < len (fxpManager.m_aColumnMetadata) {

		fxpManager.m_controlRows.m_aColumnMetadata   = fxpManager.m_aColumnMetadata   [fxpManager.m_nColumnMetadataIndex]
		fxpManager.m_controlRows.m_asColumnNames     = fxpManager.m_asColumnNames     [fxpManager.m_nColumnMetadataIndex]
		fxpManager.m_controlRows.m_atColumnScanTypes = fxpManager.m_atColumnScanTypes [fxpManager.m_nColumnMetadataIndex]
	}
} // end method copyColMDToControlRows

func (fxpManager *fastExportManagerBase) nextExportResultSet (ctx context.Context) (err error) {

	fastexportConsToBeUsed := fxpManager.fastexportConnectionCount ()

	if fxpManager.m_appCon.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter nextExportResultSet fastexportConsToBeUsed=%v", fastexportConsToBeUsed))
		defer func () { traceLog (fmt.Sprintf ("< leave nextExportResultSet m_nFastExportRowsIndex=%v err=%v", fastexportConsToBeUsed, err)) } ()
	}

	fxpManager.m_nColumnMetadataIndex++

	for i := 0 ; i < fastexportConsToBeUsed ; i++ {
		err = fxpManager.m_fastexportRows [i].consumeCurrentResultSet ()
		if err != nil { return }
	}

	err = fxpManager.receiveResults (ctx)
	if err != nil { return }

	for i := 0 ; i < fastexportConsToBeUsed ; i++ {
		err = fxpManager.m_fastexportRows [i].consumeCurrentResultSet ()
		if err != nil { return }
	}

	return

} // end method nextExportResultSet ()

func (mgr *fastExportManagerBase) getAppCon              () *TeradataConnection      { return mgr.m_appCon }
func (mgr *fastExportManagerBase) getAppRows             () *TeradataRows            { return mgr.m_appRows }
func (mgr *fastExportManagerBase) getBlkCount            () uint64                   { return mgr.m_uBlkCount }
func (mgr *fastExportManagerBase) getBlkNumberReceive    () uint64                   { return mgr.m_uBlkNumberReceive }
func (mgr *fastExportManagerBase) getControlRows         () *TeradataRows            { return mgr.m_controlRows }
func (mgr *fastExportManagerBase) getControlSession      () *TeradataConnection      { return mgr.m_controlSession }
func (mgr *fastExportManagerBase) getFastExportRows      () *TeradataRows            { return mgr.m_fastexportRows [mgr.m_nFastExportRowsIndex] }
func (mgr *fastExportManagerBase) getFastExportRowsIndex () int                      { return mgr.m_nFastExportRowsIndex }
func (mgr *fastExportManagerBase) getFileExporter        () fileExporter             { return mgr.m_fileExporter }
func (mgr *fastExportManagerBase) getFileExporterType    () int                      { return mgr.m_nFileExporterType }
func (mgr *fastExportManagerBase) getStatementNumber     () uint32                   { return mgr.m_uStmtNumber }
func (mgr *fastExportManagerBase) getWarnings            () [] string                { return mgr.m_asWarnings }
func (mgr *fastExportManagerBase) hadWarningsAtNew       () bool                     { return mgr.m_bWarningsAtNew }
func (mgr *fastExportManagerBase) isConnected            () bool                     { return mgr.m_bConnected }
func (mgr *fastExportManagerBase) setFileExporter        (fileExp fileExporter)      { mgr.m_fileExporter = fileExp }
func (mgr *fastExportManagerBase) setFileExportPath      (fileExpPathName string)    { mgr.m_sExportPathName = fileExpPathName }
func (mgr *fastExportManagerBase) setWarnings            (asWarnings [] string)      { mgr.m_asWarnings = asWarnings }
func (mgr *fastExportManagerBase) setWarningsAtNew       (b bool)                    { mgr.m_bWarningsAtNew = b }
func (mgr *fastExportManagerBase) writeExportedData      (ctx context.Context) error { return nil }
